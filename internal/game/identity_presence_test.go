package game

import (
	"mcraft/backend/internal/identity"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type websiteRequestCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *websiteRequestCounter) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.counts[r.URL.Path]++
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
func (c *websiteRequestCounter) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[path]
}

func websiteCore(t *testing.T, clients []identity.Client) (*coreFixture, *websiteRequestCounter) {
	t.Helper()
	h := federationListener(t)
	dir := t.TempDir()
	store, err := OpenIdentityStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	a := store.(*identityStore)
	a.accounts.hashCost = bcrypt.MinCost
	user, token, err := store.Register("core_player", "website-fixture-password", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetName(user.ID, "网页验收玩家"); err != nil {
		t.Fatal(err)
	}
	core, err := identity.New(identity.Config{DataDir: dir, Issuer: "http://" + h.Listener.Addr().String(), Clients: clients, AdminUsers: []string{"core_player"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	counter := &websiteRequestCounter{counts: map[string]int{}}
	h.Config.Handler = counter.handler(core.Handler())
	h.Start()
	t.Cleanup(func() { h.Close(); core.Close() })
	return &coreFixture{Service: core, HTTP: h, Store: a, Cookie: &http.Cookie{Name: identity.Cookie, Value: token}, ID: user.ID}, counter
}
func websiteConnections(t *testing.T, c *coreFixture) int {
	t.Helper()
	data := federationJSON(t, federationRequest(t, "GET", c.HTTP.URL+"/api/admin/online", c.Cookie, nil), 200)
	connections := 0
	for _, raw := range data["users"].([]any) {
		user := raw.(map[string]any)
		if user["id"] != c.ID {
			t.Fatal("website activity associated a forged account")
		}
		for _, value := range user["locations"].([]any) {
			location := value.(map[string]any)
			if location["location"] != "website" || location["serverId"] != nil || location["serverName"] != nil {
				t.Fatal("private game server data entered the account console")
			}
			connections += int(location["connections"].(float64))
		}
	}
	return connections
}

func websiteGameClients(h1, h2 *httptest.Server) []identity.Client {
	return []identity.Client{
		{ID: "private-one", Name: "One", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h1.Listener.Addr().String() + "/api/auth/callback"},
		{ID: "private-two", Name: "Two", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h2.Listener.Addr().String() + "/api/auth/callback"},
	}
}

func TestPrivateGameServersAuthenticateWithoutReportingWebsiteActivity(t *testing.T) {
	h1, h2 := federationListener(t), federationListener(t)
	c, requests := websiteCore(t, websiteGameClients(h1, h2))
	g1, g2 := federationGame(t, h1, c, "private-one", ""), federationGame(t, h2, c, "private-two", "")
	cookie1, cookie2 := federationLogin(t, g1, c), federationLogin(t, g2, c)
	if got := websiteConnections(t, c); got != 0 {
		t.Fatalf("login leases counted as website activity: %d", got)
	}
	w1, welcome1 := authConnect(t, g1.HTTP.URL, DefaultRoom, "", "", cookie1)
	w2, welcome2 := authConnect(t, g2.HTTP.URL, DefaultRoom, "", "", cookie2)
	defer w1.Close()
	defer w2.Close()
	for _, welcome := range []map[string]any{welcome1, welcome2} {
		if welcome["you"].(map[string]any)["name"] != "网页验收玩家" {
			t.Fatal("private game lost the central account profile")
		}
	}
	for _, g := range []*gameFixture{g1, g2} {
		g.App.mu.Lock()
		for peer := range g.App.rooms[DefaultRoom].Peers {
			if peer.sid != c.ID {
				g.App.mu.Unlock()
				t.Fatal("private game lost the fixed central account ID")
			}
		}
		g.App.mu.Unlock()
	}
	deadline := time.Now().Add(3 * time.Second)
	for requests.count("/api/token/status") < 2 {
		if time.Now().After(deadline) {
			t.Fatal("removing game presence also removed identity lease refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.count("/api/presence") != 0 || requests.count("/api/auth/heartbeat") != 0 || websiteConnections(t, c) != 0 {
		t.Fatal("a game join or lease refresh reported website activity")
	}
	nonce := strings.Repeat("1", 32)
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/heartbeat", c.Cookie, map[string]any{"connectionId": nonce, "online": true}), 200)
	if got := websiteConnections(t, c); got != 1 {
		t.Fatalf("one active client webpage should count once while two games run, got %d", got)
	}
	if err := g2.App.Close(); err != nil {
		t.Fatal(err)
	}
	if got := websiteConnections(t, c); got != 1 {
		t.Fatal("game shutdown incorrectly cleared active website presence")
	}
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/heartbeat", c.Cookie, map[string]any{"connectionId": nonce, "online": false}), 200)
	if got := websiteConnections(t, c); got != 0 {
		t.Fatal("a still-running private game kept a departed website user online")
	}
	if requests.count("/api/presence") != 0 {
		t.Fatal("game disconnect or shutdown still sent a presence report")
	}
	if g1.App.accounts.session(identity.Hash(cookie1.Value)) == nil {
		t.Fatal("departing website activity revoked the independent private game lease")
	}
}
