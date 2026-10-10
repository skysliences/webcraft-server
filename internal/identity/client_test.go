package identity

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	api "mcraft/backend/pkg/plugin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func issuedClientGrant(t *testing.T, s *Service, token, server string) (string, map[string]any) {
	t.Helper()
	w := websiteRequest(t, s, "POST", "/api/auth/server-proof", token, map[string]string{"serverId": server})
	if w.Code != 200 {
		t.Fatalf("issue client grant %d %s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a client grant created or transferred an account cookie")
	}
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	proof, _ := data["proof"].(string)
	if proof == "" {
		t.Fatal("client grant response omitted the proof")
	}
	return proof, data
}
func consumeClientGrant(t *testing.T, s *Service, proof, server string) *httptest.ResponseRecorder {
	t.Helper()
	return websiteRequest(t, s, "POST", "/api/token/client", "", map[string]string{"proof": proof, "client_id": server})
}
func decodeClientLease(t *testing.T, s *Service, w *httptest.ResponseRecorder, server string) (*Claims, string) {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("consume client grant %d %s", w.Code, w.Body.String())
	}
	var data struct {
		Proof     string `json:"proof"`
		ExpiresIn int    `json:"expiresIn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.ExpiresIn != 600 {
		t.Fatal("invalid short game lease response")
	}
	c, err := Verify(s.key.Public().(ed25519.PublicKey), data.Proof, s.config.Issuer, server, time.Now())
	if err != nil || c.Purpose != "" || c.GrantID != "" {
		t.Fatal("consumed grant did not become a normal game identity lease")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("game lease endpoint leaked or created a core website cookie")
	}
	return c, data.Proof
}

func TestClientGrantAudienceSingleUseAndSeparateWebsitePresence(t *testing.T) {
	s, store, token, ordinaryProof := presenceFixture(t)
	grant, data := issuedClientGrant(t, s, token, "server-a")
	if data["serverId"] != "server-a" || data["issuer"] != s.config.Issuer || data["callbackApiOrigin"] != "http://127.0.0.1:8081" || data["expiresIn"] != float64(60) {
		t.Fatalf("grant was not routed to the registered private API: %v", data)
	}
	claims, err := Verify(s.key.Public().(ed25519.PublicKey), grant, s.config.Issuer, "server-a", time.Now())
	if err != nil || claims.Purpose != "client_join" || len(claims.GrantID) != 64 || claims.Expires-claims.IssuedAt != 60 || claims.Subject != store.Current(Hash(token)).ID || claims.Session != Hash(token) {
		t.Fatal("one-minute grant lost its audience, account, purpose or session binding")
	}
	assertWebsite(t, s, 0, 0)
	for _, c := range []struct{ proof, server string }{{grant, "server-b"}, {grant + "forged", "server-a"}, {ordinaryProof, "server-a"}} {
		if w := consumeClientGrant(t, s, c.proof, c.server); w.Code != 401 {
			t.Fatal("forged, wrong-audience or ordinary proof consumed a client grant")
		}
	}
	if w := websiteRequest(t, s, "POST", "/api/token/status", "", map[string]string{"proof": grant, "client_id": "server-a"}); w.Code != 401 {
		t.Fatal("client_join grant bypassed single use by calling token status")
	}
	c, lease := decodeClientLease(t, s, consumeClientGrant(t, s, grant, "server-a"), "server-a")
	if c.Subject != claims.Subject || c.Session != claims.Session {
		t.Fatal("consumption changed the fixed account identity")
	}
	if w := consumeClientGrant(t, s, grant, "server-a"); w.Code != 401 {
		t.Fatal("one-use grant accepted replay")
	}
	if w := websiteRequest(t, s, "POST", "/api/token/status", "", map[string]string{"proof": lease, "client_id": "server-a"}); w.Code != 200 {
		t.Fatal("game lease could not refresh after a successful consumption")
	}
	assertWebsite(t, s, 0, 0)
	if w := websiteHeartbeat(t, s, token, strings.Repeat("a", 32), true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 1, 1)
}
func TestClientGrantExpirationLogoutDisableAndRestart(t *testing.T) {
	for _, kind := range []string{"proof_expired", "grant_expired", "logout", "disabled", "restart"} {
		t.Run(kind, func(t *testing.T) {
			s, store, _ := consoleFixture(t, "")
			token := strings.Repeat("c", 64)
			grant, _ := issuedClientGrant(t, s, token, "testserver")
			claims, err := Verify(s.key.Public().(ed25519.PublicKey), grant, s.config.Issuer, "testserver", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "proof_expired":
				claims.IssuedAt = time.Now().Add(-time.Minute - 2*time.Second).Unix()
				claims.Expires = time.Now().Add(-time.Second).Unix()
				grant, err = Sign(s.key, *claims)
				if err != nil {
					t.Fatal(err)
				}
			case "grant_expired":
				s.mu.Lock()
				g := s.clientGrants[Hash(claims.GrantID)]
				g.Expires = time.Now().Add(-time.Second)
				s.clientGrants[Hash(claims.GrantID)] = g
				s.mu.Unlock()
			case "logout":
				if w := websiteRequest(t, s, "POST", "/api/auth/logout", token, map[string]any{}); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "disabled":
				if _, err := store.SetAccountDisabled(testUserID, true); err != nil {
					t.Fatal(err)
				}
			case "restart":
				config := s.config
				s.Close()
				s, err = New(config, store)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
			}
			if w := consumeClientGrant(t, s, grant, "testserver"); w.Code != 401 {
				t.Fatalf("%s grant accepted: %d %s", kind, w.Code, w.Body.String())
			}
			assertWebsite(t, s, 0, 0)
		})
	}
}
func TestClientGrantConcurrentReplayConsumesOnce(t *testing.T) {
	s, _, token, _ := presenceFixture(t)
	grant, _ := issuedClientGrant(t, s, token, "server-a")
	var wg sync.WaitGroup
	results := make(chan int, 16)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- consumeClientGrant(t, s, grant, "server-a").Code
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for code := range results {
		if code == 200 {
			accepted++
		} else if code != 401 {
			t.Fatalf("concurrent consume returned %d", code)
		}
	}
	if accepted != 1 {
		t.Fatalf("grant replay accepted %d times", accepted)
	}
}

func TestClientAuthorizationPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "client-authorization-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.AccountServerAuthorizing, api.AccountRegistering, api.AccountLoggingIn} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			mode, _ := os.ReadFile(filepath.Join(c.DataDir, "mode"))
			if string(mode) == "cancel" {
				e.Cancel("client.authorization.protected")
			}
			e.Data["actor"] = "forged"
			e.Data["target"] = "forged"
			e.Data["serverId"] = "forged"
			if e.Identity != nil {
				e.Identity.ID = "forged"
			}
			return nil
		})
	}
	for _, name := range []string{api.AccountServerAuthorizing, api.AccountServerAuthorized, api.AccountServerAuthorizationConsumed, api.AccountRegistered, api.AccountLoggedIn} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			e.Cancel("monitor.cannot.cancel")
			e.Data["serverId"] = "monitor.forged"
			return err
		})
	}
	if err := p.Serve(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func clientAuthorizationPlugin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "client-authorization")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	from, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.OpenFile(filepath.Join(path, "plugin"), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(to, from)
	to.Close()
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"client-authorization","name":"Client authorization fixture","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestClientAuthorizationPluginProcess$","client-authorization-fixture"]}`
	if err := os.WriteFile(filepath.Join(path, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func clientAuthorizationEvents(t *testing.T, dir string) []api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "client-authorization", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}
func TestClientGrantPluginCancellationReadOnlyAndSuccessEvents(t *testing.T) {
	s, _, dir := consoleFixture(t, clientAuthorizationPlugin(t))
	token := strings.Repeat("c", 64)
	mode := filepath.Join(dir, "plugins", "client-authorization", "mode")
	if err := os.WriteFile(mode, []byte("cancel"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := websiteRequest(t, s, "POST", "/api/auth/server-proof", token, map[string]string{"serverId": "testserver"}); w.Code != 403 {
		t.Fatal("plugin cancellation did not block client grant")
	}
	if len(s.clientGrants) != 0 {
		t.Fatal("cancelled authorization created a grant")
	}
	for _, event := range clientAuthorizationEvents(t, dir) {
		if event.Name != api.AccountServerAuthorizing {
			t.Fatal("cancelled grant emitted a success event")
		}
	}
	if err := os.WriteFile(mode, nil, 0600); err != nil {
		t.Fatal(err)
	}
	grant, _ := issuedClientGrant(t, s, token, "testserver")
	decodeClientLease(t, s, consumeClientGrant(t, s, grant, "testserver"), "testserver")
	if w := consumeClientGrant(t, s, grant, "testserver"); w.Code != 401 {
		t.Fatal("replayed grant did not fail")
	}
	events := clientAuthorizationEvents(t, dir)
	if len(events) != 4 || events[1].Name != api.AccountServerAuthorizing || events[2].Name != api.AccountServerAuthorized || events[3].Name != api.AccountServerAuthorizationConsumed {
		t.Fatalf("grant success journal %v", events)
	}
	for _, e := range events {
		if e.Identity == nil || e.Identity.ID != testUserID || e.String("actor") != testUserID || e.String("target") != "testserver" || e.String("serverId") != "testserver" || e.String("reason") != "client_server_join" {
			t.Fatal("plugin modified immutable authorization context")
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), token) || strings.Contains(string(raw), Hash(token)) || strings.Contains(string(raw), grant) {
			t.Fatal("credentials entered a plugin event")
		}
	}
	assertWebsite(t, s, 0, 0)
}
func TestClientGrantCommitRejectsConcurrentRevocation(t *testing.T) {
	token := strings.Repeat("d", 64)
	store := &presenceRevokingValidationStore{consoleStore: newConsoleStore(), target: Hash(token)}
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, Issuer: "http://localhost", PluginDir: clientAuthorizationPlugin(t), Clients: []Client{{ID: "testserver", Name: "独立游戏服务器", FrontendURL: "http://localhost:8080", CallbackURL: "http://localhost:8080/api/auth/callback"}}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	grant, _ := issuedClientGrant(t, s, token, "testserver")
	store.validationMu.Lock()
	store.armed = true
	store.validationMu.Unlock()
	if w := consumeClientGrant(t, s, grant, "testserver"); w.Code != 401 {
		t.Fatalf("revoked during validation must reject the grant commit: %d %s", w.Code, w.Body.String())
	}
	if len(s.clientGrants) != 1 {
		t.Fatal("failed grant commit consumed its one-time token")
	}
	for _, e := range clientAuthorizationEvents(t, dir) {
		if e.Name == api.AccountServerAuthorizationConsumed {
			t.Fatal("revoked grant emitted a consumed success event")
		}
	}
}

type clientActivityStore struct{ *consoleStore }

func (s *clientActivityStore) Register(username, password, previous string) (*Profile, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, token := strings.Repeat("e", 32), Random()
	s.accounts[id] = ManagedAccount{ID: id, Username: username, CreatedAt: time.Now().Unix()}
	s.sessions[Hash(token)] = id
	return &Profile{ID: id, Username: username}, token, nil
}
func (s *clientActivityStore) Login(username, password, previous string) (*Profile, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.Username == username {
			token := Random()
			s.sessions[Hash(token)] = a.ID
			return &Profile{ID: a.ID, Username: a.Username, Name: a.Name}, token, nil
		}
	}
	return nil, "", os.ErrNotExist
}
func TestWebsiteAuthenticationReadsAndSuccessDoNotCreateOrRenewActivity(t *testing.T) {
	dir := t.TempDir()
	store := &clientActivityStore{newConsoleStore()}
	s, err := New(Config{DataDir: dir, Issuer: "http://localhost", AdminUsers: []string{"sky"}, PluginDir: clientAuthorizationPlugin(t)}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	input := map[string]string{"username": "newplayer", "password": "fixture-password"}
	registered := websiteRequest(t, s, "POST", "/api/auth/register", "", input)
	if registered.Code != 201 || len(registered.Result().Cookies()) != 1 {
		t.Fatalf("fixture registration %d %s", registered.Code, registered.Body.String())
	}
	cookie := registered.Result().Cookies()[0]
	if cookie.Name != Cookie || !cookie.HttpOnly || len(cookie.Value) != 64 {
		t.Fatal("registration did not provide a normal core account cookie")
	}
	for _, path := range []string{"/api/auth/me", "/api/launcher"} {
		if w := websiteRequest(t, s, "GET", path, cookie.Value, nil); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	for _, path := range []string{"/api/admin/me", "/api/admin/online", "/api/admin/stats", "/api/admin/users", "/api/admin/servers"} {
		if w := websiteRequest(t, s, "GET", path, strings.Repeat("a", 64), nil); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if w := websiteRequest(t, s, "POST", "/api/auth/login", "", input); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 0, 0)
	// A registered user whose name is not yet configured is still a logged-in
	// visitor; only private-server grants require choosing the player name.
	nonce := strings.Repeat("b", 32)
	if w := websiteHeartbeat(t, s, cookie.Value, nonce, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 1, 1)
	s.presence.mu.Lock()
	e := s.presence.entries[nonce]
	e.seen = time.Now().Add(-presenceTTL - time.Second)
	s.presence.entries[nonce] = e
	s.presence.mu.Unlock()
	websiteRequest(t, s, "GET", "/api/auth/me", cookie.Value, nil)
	websiteRequest(t, s, "POST", "/api/auth/login", cookie.Value, input)
	assertWebsite(t, s, 0, 0)
	mode := filepath.Join(dir, "plugins", "client-authorization", "mode")
	if err := os.WriteFile(mode, []byte("cancel"), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(clientAuthorizationEvents(t, dir))
	for _, action := range []string{"register", "login"} {
		w := websiteRequest(t, s, "POST", "/api/auth/"+action, cookie.Value, input)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Fatal("cancelled account action created a login cookie")
		}
	}
	if len(clientAuthorizationEvents(t, dir)) != before {
		t.Fatal("cancelled account action emitted a success event")
	}
	assertWebsite(t, s, 0, 0)
}
func TestClientGrantInvalidUserServerAndEventCatalog(t *testing.T) {
	s, store, token, _ := presenceFixture(t)
	if w := websiteRequest(t, s, "POST", "/api/auth/server-proof", "", map[string]string{"serverId": "server-a"}); w.Code != 401 {
		t.Fatal("anonymous visitor received a join grant")
	}
	if w := websiteRequest(t, s, "POST", "/api/auth/server-proof", token, map[string]string{"serverId": "unknown"}); w.Code != 404 {
		t.Fatal("unregistered private server received a grant")
	}
	store.mu.Lock()
	p := store.users[Hash(token)]
	p.Name = ""
	store.users[Hash(token)] = p
	store.mu.Unlock()
	if w := websiteRequest(t, s, "POST", "/api/auth/server-proof", token, map[string]string{"serverId": "server-a"}); w.Code != 401 {
		t.Fatal("an unnamed account received a game join grant")
	}
	wanted := map[string]bool{api.AccountServerAuthorizing: true, api.AccountServerAuthorized: false, api.AccountServerAuthorizationConsumed: false}
	for _, spec := range api.Events() {
		if cancellable, ok := wanted[spec.Name]; ok {
			if spec.Cancellable != cancellable || len(spec.Mutable) != 0 {
				t.Fatalf("client grant catalog exposes mutable authorization: %v", spec)
			}
			delete(wanted, spec.Name)
		}
	}
	if len(wanted) != 0 {
		t.Fatal("client grant SDK events missing from the API catalog")
	}
}
