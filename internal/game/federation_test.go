package game

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"mcraft/backend/internal/identity"
	api "mcraft/backend/pkg/plugin"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "identity-fixture" {
		return
	}
	p := api.New()
	mode := func(c *api.Context) string {
		raw, _ := os.ReadFile(filepath.Join(c.DataDir, "mode"))
		return string(raw)
	}
	p.On(api.PlayerIdentifying, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if mode(c) == "identity_cancel" {
			e.Cancel("identity.protected")
		}
		e.Data["actor"] = "forged"
		e.Identity.ID = "forged"
		return nil
	})
	p.On(api.PlayerAuthenticating, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		switch mode(c) {
		case "cancel":
			e.Cancel("access.protected")
		case "invalid_bool":
			e.Data["authenticated"] = "yes"
		case "invalid_fields":
			e.Data["fields"] = []api.AuthField{{Name: "password", Label: "Secret", Type: "html"}}
		case "gate":
			e.Data["authenticated"] = false
			e.Data["prompt"] = "测试本服注册"
			e.Data["fields"] = []api.AuthField{{Name: "invite", Label: "邀请码", Type: "text"}}
			if e.String("action") == "register" {
				v, _ := e.Data["values"].(map[string]any)
				e.Data["authenticated"] = v["invite"] == "fixture-code"
			}
		}
		return nil
	})
	p.On(api.AccountProfileChanging, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		switch e.String("name") {
		case "cancel":
			e.Cancel("profile.protected")
		case "invalid":
			e.Data["name"] = " "
		case "rename":
			e.Data["name"] = "核心插件名字"
		}
		e.Data["id"] = "forged"
		return nil
	})
	p.On(api.AccountRegistering, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("username") == "blocked" {
			e.Cancel("account.protected")
		}
		return nil
	})
	p.On(api.AccountLoggingIn, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.String("username") == "blocked" {
			e.Cancel("login.protected")
		}
		return nil
	})
	for _, spec := range api.Events() {
		if strings.HasPrefix(spec.Name, "account.") || strings.HasPrefix(spec.Name, "player.identity") || strings.HasPrefix(spec.Name, "player.auth") || spec.Name == api.PlayerIdentified || spec.Name == api.PlayerLoggedOut || spec.Name == api.PlayerLoggingOut {
			p.On(spec.Name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
				raw, err := json.Marshal(e)
				if err != nil {
					return err
				}
				f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = f.Write(append(raw, '\n'))
				e.Data["authenticated"] = false
				e.Data["name"] = "monitor ignored"
				e.Cancel("monitor ignored")
				return err
			})
		}
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func identityPlugin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "identity-test")
	os.MkdirAll(dir, 0700)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"identity-test","name":"identity-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestIdentityPluginProcess$","identity-fixture"]}`
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600)
	return root
}
func federationListener(t *testing.T) *httptest.Server {
	t.Helper()
	h := httptest.NewUnstartedServer(nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.Listener.Close()
	h.Listener = listener
	return h
}
func federationRequest(t *testing.T, method, target string, cookie *http.Cookie, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func federationJSON(t *testing.T, res *http.Response, want int) map[string]any {
	t.Helper()
	defer res.Body.Close()
	var data map[string]any
	if json.NewDecoder(res.Body).Decode(&data) != nil || res.StatusCode != want {
		t.Fatalf("JSON response status: %d, want %d", res.StatusCode, want)
	}
	return data
}

type coreFixture struct {
	Service *identity.Service
	HTTP    *httptest.Server
	Store   *identityStore
	Cookie  *http.Cookie
	ID      string
}

func federationCore(t *testing.T, clients []identity.Client, pluginDir string) *coreFixture {
	t.Helper()
	h := federationListener(t)
	dir := t.TempDir()
	store, err := OpenIdentityStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	a := store.(*identityStore)
	a.accounts.hashCost = bcrypt.MinCost
	core, err := identity.New(identity.Config{DataDir: dir, Issuer: "http://" + h.Listener.Addr().String(), Clients: clients, PluginDir: pluginDir}, store)
	if err != nil {
		t.Fatal(err)
	}
	h.Config.Handler = core.Handler()
	h.Start()
	t.Cleanup(func() { h.Close(); core.Close() })
	res := federationRequest(t, "POST", h.URL+"/api/auth/register", nil, map[string]string{"username": "core_player", "password": "core-fixture-password"})
	cookie := res.Cookies()[0]
	data := federationJSON(t, res, 201)
	id := data["user"].(map[string]any)["id"].(string)
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/profile", cookie, map[string]string{"name": "核心测试玩家"}), 200)
	return &coreFixture{core, h, a, cookie, id}
}

type gameFixture struct {
	App    *Server
	HTTP   *httptest.Server
	Config Config
}

func federationGame(t *testing.T, h *httptest.Server, core *coreFixture, id, plugins string) *gameFixture {
	t.Helper()
	cfg := Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", IdentityIssuer: core.HTTP.URL, IdentityServerID: id, IdentityCallback: "http://" + h.Listener.Addr().String() + "/api/auth/callback", GamePublicURL: "http://127.0.0.1:8080", AllowedOrigins: []string{"http://127.0.0.1:8080"}, PluginDir: plugins, PluginTimeout: time.Second, IdentityCheckInterval: 50 * time.Millisecond}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.Config.Handler = app.Handler()
	h.Start()
	t.Cleanup(func() { h.Close(); app.Close() })
	return &gameFixture{app, h, cfg}
}
func federationFlow(t *testing.T, g *gameFixture, c *coreFixture) (*http.Response, *http.Cookie) {
	t.Helper()
	start := federationRequest(t, "GET", g.HTTP.URL+"/api/auth/start?return_to="+url.QueryEscape("http://127.0.0.1:8080/#create"), nil, nil)
	if start.StatusCode != 302 {
		t.Fatal("start rejected")
	}
	oauth := start.Cookies()[0]
	authorizeURL := start.Header.Get("Location")
	start.Body.Close()
	authorized := federationRequest(t, "GET", authorizeURL, c.Cookie, nil)
	if authorized.StatusCode != 302 {
		t.Fatal("core did not authorize")
	}
	callback := authorized.Header.Get("Location")
	authorized.Body.Close()
	return federationRequest(t, "GET", callback, oauth, nil), oauth
}
func federationLogin(t *testing.T, g *gameFixture, c *coreFixture) *http.Cookie {
	t.Helper()
	res, _ := federationFlow(t, g, c)
	defer res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "http://127.0.0.1:8080/#create" {
		t.Fatalf("game callback: %d", res.StatusCode)
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == g.App.identity.cookie {
			return cookie
		}
	}
	t.Fatal("no game session")
	return nil
}
func identityEventLog(t *testing.T, dir string) []api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "identity-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid event log")
		}
		out = append(out, e)
	}
	return out
}
func identityCount(t *testing.T, dir, name string) int {
	n := 0
	for _, e := range identityEventLog(t, dir) {
		if e.Name == name {
			n++
		}
	}
	return n
}
func TestFederationSharedIdentityRenameRevocationAndPersistence(t *testing.T) {
	h1, h2 := federationListener(t), federationListener(t)
	clients := []identity.Client{{ID: "one", Name: "One", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h1.Listener.Addr().String() + "/api/auth/callback"}, {ID: "two", Name: "Two", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h2.Listener.Addr().String() + "/api/auth/callback"}}
	c := federationCore(t, clients, "")
	g1, g2 := federationGame(t, h1, c, "one", identityPlugin(t)), federationGame(t, h2, c, "two", "")
	cookie1, cookie2 := federationLogin(t, g1, c), federationLogin(t, g2, c)
	if cookie1.Name == cookie2.Name || !cookie1.HttpOnly || cookie1.SameSite != http.SameSiteLaxMode {
		t.Fatal("game cookie isolation failed")
	}
	for i, g := range []*gameFixture{g1, g2} {
		cookie := []*http.Cookie{cookie1, cookie2}[i]
		data := federationJSON(t, federationRequest(t, "GET", g.HTTP.URL+"/api/auth/me", cookie, nil), 200)
		if data["user"].(map[string]any)["id"] != c.ID || data["access"].(map[string]any)["authenticated"] != true {
			t.Fatal("global identity mismatch")
		}
		federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/auth/login", nil, map[string]string{"username": "core_player", "password": "core-fixture-password"}), 409)
		federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/rooms", cookie, map[string]string{"name": "核心账号的世界", "mode": "creative"}), 201)
		g.App.mu.Lock()
		owned := false
		for _, r := range g.App.rooms {
			if r.OwnerAccount == c.ID {
				owned = true
			}
		}
		g.App.mu.Unlock()
		if !owned {
			t.Fatal("world owner did not use core ID")
		}
	}
	restored, err := newFederatedAccounts(g1.Config.DataDir, g1.App.identity)
	if err != nil || restored.session(identity.Hash(cookie1.Value)).ID != c.ID {
		t.Fatal("signed lease restore failed")
	}
	raw, _ := os.ReadFile(restored.file)
	if bytes.Contains(raw, []byte("password")) {
		t.Fatal("game server persisted credentials")
	}
	g1.App.mu.Lock()
	backup, backupErr := g1.App.backup("test-console", "manual")
	if backupErr == nil {
		backupErr = g1.App.restoreBackup(backup, "test-console")
	}
	g1.App.mu.Unlock()
	if backupErr != nil {
		t.Fatal("central game backup/restore:", backupErr)
	}
	if g1.App.accounts.session(identity.Hash(cookie1.Value)) == nil {
		t.Fatal("central backup lost the verified session")
	}
	for _, e := range identityEventLog(t, g1.Config.DataDir) {
		if e.Identity == nil || e.Identity.ID != c.ID || e.String("actor") != c.ID {
			t.Fatal("plugin modified immutable identity")
		}
	}
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/profile", c.Cookie, map[string]string{"name": "改名后的玩家"}), 200)
	deadline := time.Now().Add(3 * time.Second)
	for {
		u := g1.App.accounts.session(identity.Hash(cookie1.Value))
		v := g2.App.accounts.session(identity.Hash(cookie2.Value))
		if u != nil && v != nil && u.Name == "改名后的玩家" && v.Name == u.Name && u.ID == c.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core rename did not propagate")
		}
		time.Sleep(20 * time.Millisecond)
	}
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/logout", c.Cookie, map[string]string{}), 200)
	deadline = time.Now().Add(3 * time.Second)
	for {
		if g1.App.accounts.session(identity.Hash(cookie1.Value)) == nil && g2.App.accounts.session(identity.Hash(cookie2.Value)) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core logout did not revoke game leases")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if identityCount(t, g1.Config.DataDir, api.PlayerIdentityUpdated) != 1 || identityCount(t, g1.Config.DataDir, api.PlayerIdentityExpired) != 1 {
		t.Fatal("identity lifecycle events missing")
	}
}
func TestFederationGateCancellationValidationAndSaveRollback(t *testing.T) {
	h := federationListener(t)
	c := federationCore(t, []identity.Client{{ID: "gate", Name: "Gate", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h.Listener.Addr().String() + "/api/auth/callback"}}, "")
	g := federationGame(t, h, c, "gate", identityPlugin(t))
	modeFile := filepath.Join(g.Config.DataDir, "plugins", "identity-test", "mode")
	for _, mode := range []string{"identity_cancel", "cancel", "invalid_bool", "invalid_fields"} {
		os.WriteFile(modeFile, []byte(mode), 0600)
		res, _ := federationFlow(t, g, c)
		want := 403
		if strings.HasPrefix(mode, "invalid") {
			want = 422
		}
		federationJSON(t, res, want)
		if identityCount(t, g.Config.DataDir, api.PlayerIdentified) != 0 || identityCount(t, g.Config.DataDir, api.PlayerAuthenticated) != 0 {
			t.Fatal("failure emitted success")
		}
	}
	os.WriteFile(modeFile, []byte("gate"), 0600)
	cookie := federationLogin(t, g, c)
	hash := identity.Hash(cookie.Value)
	if g.App.accounts.session(hash) != nil || g.App.accounts.sessionProfile(hash, false).ID != c.ID {
		t.Fatal("pending gate granted gameplay or lost ID")
	}
	federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/rooms", cookie, map[string]string{"mode": "creative"}), 401)
	federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/auth/server-access", cookie, map[string]any{"action": "register", "values": map[string]string{"id": "forged"}}), 422)
	tmpFile := g.App.accounts.file + ".tmp"
	os.Mkdir(tmpFile, 0700)
	federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/auth/server-access", cookie, map[string]any{"action": "register", "values": map[string]string{"invite": "fixture-code"}}), 503)
	if g.App.accounts.session(hash) != nil || identityCount(t, g.Config.DataDir, api.PlayerAuthenticated) != 0 {
		t.Fatal("failed save granted access")
	}
	os.Remove(tmpFile)
	data := federationJSON(t, federationRequest(t, "POST", g.HTTP.URL+"/api/auth/server-access", cookie, map[string]any{"action": "register", "values": map[string]string{"invite": "fixture-code"}}), 200)
	if data["user"].(map[string]any)["id"] != c.ID || g.App.accounts.session(hash) == nil || identityCount(t, g.Config.DataDir, api.PlayerAuthenticated) != 1 {
		t.Fatal("valid gate did not commit exactly once")
	}
	for _, e := range identityEventLog(t, g.Config.DataDir) {
		if e.Name == api.PlayerAuthenticated && e.Data["values"] != nil {
			t.Fatal("completed event leaked credentials")
		}
	}
	os.WriteFile(modeFile, []byte(""), 0600)
	before := identityCount(t, g.Config.DataDir, api.PlayerIdentified)
	os.Mkdir(tmpFile, 0700)
	res, _ := federationFlow(t, g, c)
	federationJSON(t, res, 503)
	os.Remove(tmpFile)
	if identityCount(t, g.Config.DataDir, api.PlayerIdentified) != before {
		t.Fatal("failed identity save emitted success")
	}
}
func TestFederationPKCEStateReplayAndAudience(t *testing.T) {
	h := federationListener(t)
	c := federationCore(t, []identity.Client{{ID: "security", Name: "Security", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h.Listener.Addr().String() + "/api/auth/callback"}}, "")
	g := federationGame(t, h, c, "security", "")
	federationJSON(t, federationRequest(t, "GET", g.HTTP.URL+"/api/auth/start?return_to=https://evil.example/", nil, nil), 400)
	federationJSON(t, federationRequest(t, "GET", g.HTTP.URL+"/api/auth/callback?state="+identity.Random()+"&code=forged", nil, nil), 403)
	start := federationRequest(t, "GET", g.HTTP.URL+"/api/auth/start", nil, nil)
	oauth := start.Cookies()[0]
	authURL := start.Header.Get("Location")
	start.Body.Close()
	q, _ := url.Parse(authURL)
	wrong := *q
	params := wrong.Query()
	params.Set("redirect_uri", "http://127.0.0.1:8089/api/auth/callback")
	wrong.RawQuery = params.Encode()
	federationJSON(t, federationRequest(t, "GET", wrong.String(), c.Cookie, nil), 400)
	res := federationRequest(t, "GET", authURL, c.Cookie, nil)
	callback := res.Header.Get("Location")
	res.Body.Close()
	u, _ := url.Parse(callback)
	code := u.Query().Get("code")
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/token", nil, map[string]string{"code": code, "client_id": "security", "redirect_uri": g.Config.IdentityCallback, "code_verifier": identity.Random()}), 401)
	res = federationRequest(t, "GET", callback, oauth, nil)
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatal("legitimate PKCE flow failed")
	}
	federationJSON(t, federationRequest(t, "GET", callback, oauth, nil), 401)
	g.App.accounts.mu.Lock()
	var proof string
	for _, session := range g.App.accounts.sessions {
		proof = session.Proof
	}
	g.App.accounts.mu.Unlock()
	if _, err := identity.Verify(g.App.identity.key, proof, c.HTTP.URL, "another-server", time.Now()); err == nil {
		t.Fatal("wrong audience accepted")
	}
	parts := strings.Split(proof, ".")
	parts[0] = strings.ReplaceAll(parts[0], "a", "b")
	if parts[0] == strings.Split(proof, ".")[0] {
		parts[0] = "A" + parts[0][1:]
	}
	if _, err := g.App.identity.verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered identity accepted")
	}
	attackerKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	claims, _ := g.App.identity.verify(proof)
	forged, _ := identity.Sign(attackerKey, claims)
	if _, err := g.App.identity.verify(forged); err == nil {
		t.Fatal("untrusted issuer key accepted")
	}
	g.App.accounts.mu.Lock()
	for hash, session := range g.App.accounts.sessions {
		session.Proof = forged
		g.App.accounts.sessions[hash] = session
	}
	g.App.accounts.saveLocked()
	g.App.accounts.mu.Unlock()
	restored, err := newFederatedAccounts(g.Config.DataDir, g.App.identity)
	if err != nil || len(restored.sessions) != 0 {
		t.Fatal("unverified saved session restored")
	}
}

func TestFederationLocalWorldReturnRouteAndLegacyRoutes(t *testing.T) {
	h := federationListener(t)
	c := federationCore(t, []identity.Client{{ID: "local-routes", Name: "Local routes", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h.Listener.Addr().String() + "/api/auth/callback"}}, "")
	g := federationGame(t, h, c, "local-routes", "")
	const front = "http://127.0.0.1:8080"
	for _, route := range []string{
		"/local/0123456789abcdef", "/local/0123456789abcdef/",
		front + "/local/0123456789abcdef?resume=1#saved-position",
		"/r/legacy_World-09", "/play/legacy_World-09/",
		"/#singleplayer", front + "/#servers",
	} {
		t.Run(route, func(t *testing.T) {
			start := federationRequest(t, "GET", g.HTTP.URL+"/api/auth/start?return_to="+url.QueryEscape(route), nil, nil)
			if start.StatusCode != http.StatusFound {
				start.Body.Close()
				t.Fatalf("valid return route rejected: %d", start.StatusCode)
			}
			cookies := start.Cookies()
			if len(cookies) != 1 || cookies[0].Name != g.App.identity.cookie+"_oauth" {
				start.Body.Close()
				t.Fatal("missing OAuth state cookie")
			}
			authorizeURL := start.Header.Get("Location")
			start.Body.Close()
			authorized := federationRequest(t, "GET", authorizeURL, c.Cookie, nil)
			if authorized.StatusCode != http.StatusFound {
				authorized.Body.Close()
				t.Fatalf("core rejected legitimate identity: %d", authorized.StatusCode)
			}
			callbackURL := authorized.Header.Get("Location")
			authorized.Body.Close()
			callback := federationRequest(t, "GET", callbackURL, cookies[0], nil)
			defer callback.Body.Close()
			want := route
			if strings.HasPrefix(route, "/") {
				want = front + route
			}
			if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != want {
				t.Fatalf("SSO must preserve return route: status=%d location=%q want=%q", callback.StatusCode, callback.Header.Get("Location"), want)
			}
			var session *http.Cookie
			for _, cookie := range callback.Cookies() {
				if cookie.Name == g.App.identity.cookie && cookie.MaxAge > 0 {
					session = cookie
				}
			}
			if session == nil {
				t.Fatal("SSO local route did not issue a game session")
			}
			me := federationJSON(t, federationRequest(t, "GET", g.HTTP.URL+"/api/auth/me", session, nil), http.StatusOK)
			if me["user"].(map[string]any)["id"] != c.ID {
				t.Fatal("local route changed the core identity ID")
			}
		})
	}
	for _, route := range []string{
		"https://evil.example/local/0123456789abcdef", "//evil.example/local/0123456789abcdef",
		"https://127.0.0.1:8080/local/0123456789abcdef", "http://localhost:8080/local/0123456789abcdef",
		"http://127.0.0.1:8081/local/0123456789abcdef", "http://attacker@127.0.0.1:8080/local/0123456789abcdef",
		"/local/0123456789abcde", "/local/0123456789abcdefg", "/local/0123456789abcdeF",
		"/local/0123456789abcde_", "/local/0123456789abcde-", "/local/",
		"/local/0123456789abcdef/extra", "/local/0123456789abcdef//", "/local//0123456789abcdef",
		"/local/0123456789abcdef/../api/auth/me", "/local/0123456789abcdef%2Fextra", "/api/auth/me",
	} {
		t.Run("reject:"+route, func(t *testing.T) {
			g.App.identity.mu.Lock()
			before := len(g.App.identity.flows)
			g.App.identity.mu.Unlock()
			response := federationRequest(t, "GET", g.HTTP.URL+"/api/auth/start?return_to="+url.QueryEscape(route), nil, nil)
			if len(response.Cookies()) != 0 || response.Header.Get("Location") != "" {
				response.Body.Close()
				t.Fatal("rejected return route created a state cookie or redirect")
			}
			federationJSON(t, response, http.StatusBadRequest)
			g.App.identity.mu.Lock()
			after := len(g.App.identity.flows)
			g.App.identity.mu.Unlock()
			if after != before {
				t.Fatal("rejected route created an identity flow")
			}
		})
	}
}

func TestCoreAccountEventsAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	old, err := newAccounts(dir, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	old.hashCost = bcrypt.MinCost
	u, token, err := old.register("legacy_user", "legacy-test-password", "", "")
	if err != nil {
		t.Fatal(err)
	}
	old.setName(u.ID, "旧玩家")
	source := filepath.Join(dir, "accounts.json")
	coreDir := t.TempDir()
	store, err := OpenIdentityStore(coreDir, source)
	if err != nil {
		t.Fatal(err)
	}
	a := store.(*identityStore)
	a.accounts.hashCost = bcrypt.MinCost
	if a.Current(identity.Hash(token)).ID != u.ID {
		t.Fatal("migration changed ID/session")
	}
	backups, _ := filepath.Glob(source + ".pre-core-*.bak")
	if len(backups) != 1 {
		t.Fatal("migration did not back up original")
	}
	h := federationListener(t)
	app, err := identity.New(identity.Config{DataDir: coreDir, Issuer: "http://" + h.Listener.Addr().String(), PluginDir: identityPlugin(t), AllowLegacyCookie: true}, store)
	if err != nil {
		t.Fatal(err)
	}
	h.Config.Handler = app.Handler()
	h.Start()
	t.Cleanup(func() { h.Close(); app.Close() })
	cookie := &http.Cookie{Name: accountCookie, Value: token}
	data := federationJSON(t, federationRequest(t, "GET", h.URL+"/api/auth/me", cookie, nil), 200)
	if data["user"].(map[string]any)["id"] != u.ID {
		t.Fatal("legacy SSO adoption failed")
	}
	for _, name := range []string{"cancel", "invalid"} {
		want := 403
		if name == "invalid" {
			want = 422
		}
		federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/profile", cookie, map[string]string{"name": name}), want)
	}
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/profile", cookie, map[string]string{"name": "rename"}), 200)
	if a.Current(identity.Hash(token)).Name != "核心插件名字" || a.Current(identity.Hash(token)).ID != u.ID {
		t.Fatal("core mutable profile validation failed")
	}
	os.Mkdir(a.accounts.file+".tmp", 0700)
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/profile", cookie, map[string]string{"name": "保存失败"}), 503)
	os.Remove(a.accounts.file + ".tmp")
	if identityCount(t, coreDir, api.AccountProfileChanged) != 1 {
		t.Fatal("failed/cancelled core profile emitted success")
	}
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/register", nil, map[string]string{"username": "blocked", "password": "test-password"}), 403)
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/login", nil, map[string]string{"username": "legacy_user", "password": "incorrect"}), 401)
	if identityCount(t, coreDir, api.AccountRegistered) != 0 || identityCount(t, coreDir, api.AccountLoggedIn) != 0 {
		t.Fatal("failed core auth emitted success")
	}
	registration := map[string]string{"name": "独立游戏服", "frontendUrl": "http://localhost:8580", "callbackUrl": "http://localhost:8581/api/auth/callback", "id": "attacker-chosen", "ownerId": "forged"}
	registered := federationJSON(t, federationRequest(t, "POST", h.URL+"/api/servers", cookie, registration), 201)["server"].(map[string]any)
	if registered["ownerId"] != u.ID || registered["id"] == "attacker-chosen" {
		t.Fatal("client registration trusted caller identity")
	}
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/servers", cookie, registration), 409)
	registration["callbackUrl"] = "http://localhost:8681/api/auth/callback"
	os.Mkdir(filepath.Join(coreDir, "servers.json.tmp"), 0700)
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/servers", cookie, registration), 503)
	os.Remove(filepath.Join(coreDir, "servers.json.tmp"))
	if identityCount(t, coreDir, api.AccountServerRegistered) != 1 {
		t.Fatal("failed registry commit emitted success")
	}
	res := federationRequest(t, "POST", h.URL+"/api/auth/register", nil, map[string]string{"username": "another_core_user", "password": "test-core-password"})
	newCookie := res.Cookies()[0]
	newID := federationJSON(t, res, 201)["user"].(map[string]any)["id"]
	login := federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/login", newCookie, map[string]string{"username": "another_core_user", "password": "test-core-password"}), 200)
	if login["user"].(map[string]any)["id"] != newID || identityCount(t, coreDir, api.AccountRegistered) != 1 || identityCount(t, coreDir, api.AccountLoggedIn) != 1 {
		t.Fatal("core login changed registered ID or omitted completion")
	}
	federationJSON(t, federationRequest(t, "POST", h.URL+"/api/auth/logout", cookie, map[string]string{}), 200)
	if a.Current(identity.Hash(token)) != nil || identityCount(t, coreDir, api.AccountLoggedOut) != 1 {
		t.Fatal("legacy session not revoked")
	}
}
