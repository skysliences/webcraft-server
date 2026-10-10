package game

import (
	"bytes"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"mcraft/backend/internal/identity"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserGameRequest(t *testing.T, g *gameFixture, method, path, token, origin string, data any) *http.Response {
	t.Helper()
	var body io.Reader
	if data != nil {
		raw, _ := json.Marshal(data)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, g.HTTP.URL+path, body)
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func browserGrant(t *testing.T, c *coreFixture, id string) string {
	t.Helper()
	data := federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/server-proof", c.Cookie, map[string]string{"serverId": id}), 200)
	return data["proof"].(string)
}
func browserLogin(t *testing.T, c *coreFixture, g *gameFixture, want int) map[string]any {
	t.Helper()
	res := browserGameRequest(t, g, "POST", "/api/auth/client", "", "http://127.0.0.1:8080", map[string]string{"proof": browserGrant(t, c, g.Config.IdentityServerID)})
	if len(res.Cookies()) != 0 {
		t.Fatal("browser client issued a third-party cookie")
	}
	return federationJSON(t, res, want)
}
func TestBrowserClientPrivateServerTokensAndWebsocket(t *testing.T) {
	h1, h2 := federationListener(t), federationListener(t)
	c, requests := websiteCore(t, websiteGameClients(h1, h2))
	g1, g2 := federationGame(t, h1, c, "private-one", identityPlugin(t)), federationGame(t, h2, c, "private-two", "")
	first, second := browserLogin(t, c, g1, 200), browserLogin(t, c, g2, 200)
	token1, token2 := first["clientToken"].(string), second["clientToken"].(string)
	if !validClientToken(token1) || !validClientToken(token2) || token1 == token2 {
		t.Fatal("invalid isolated tokens")
	}
	for i, g := range []*gameFixture{g1, g2} {
		token := []string{token1, token2}[i]
		data := federationJSON(t, browserGameRequest(t, g, "GET", "/api/auth/me", token, "http://127.0.0.1:8080", nil), 200)
		if data["user"].(map[string]any)["id"] != c.ID {
			t.Fatal("fixed global ID changed")
		}
		restored, err := newFederatedAccounts(g.Config.DataDir, g.App.identity)
		if err != nil || restored.session(identity.Hash(token)) == nil {
			t.Fatal("opaque client session was not preserved")
		}
	}
	if federationJSON(t, browserGameRequest(t, g2, "GET", "/api/auth/me", token1, "http://127.0.0.1:8080", nil), 200)["user"] != nil {
		t.Fatal("another private server accepted a token")
	}
	public := browserGameRequest(t, g1, "GET", "/api/server", "", "https://unknown.example", nil)
	if public.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("public server discovery blocked")
	}
	federationJSON(t, public, 200)
	federationJSON(t, browserGameRequest(t, g1, "GET", "/api/auth/me", token1, "https://unknown.example", nil), 403)
	proof := browserGrant(t, c, "private-one")
	federationJSON(t, browserGameRequest(t, g2, "POST", "/api/auth/client", "", "", map[string]string{"proof": proof}), 401)
	federationJSON(t, browserGameRequest(t, g1, "POST", "/api/auth/client", "", "", map[string]string{"proof": proof}), 200)
	federationJSON(t, browserGameRequest(t, g1, "POST", "/api/auth/client", "", "", map[string]string{"proof": proof}), 401)
	dialer := websocket.Dialer{Subprotocols: []string{"mcraft-client", "auth." + token1}, HandshakeTimeout: time.Second}
	conn, res, err := dialer.Dial("ws"+strings.TrimPrefix(g1.HTTP.URL, "http")+"/ws/"+DefaultRoom, http.Header{"Origin": []string{"http://127.0.0.1:8080"}})
	if err != nil {
		t.Fatal("cookie-free WebSocket authentication failed", err)
	}
	defer conn.Close()
	if conn.Subprotocol() != "mcraft-client" || strings.Contains(res.Header.Get("Sec-WebSocket-Protocol"), token1) {
		t.Fatal("secret websocket subprotocol was echoed")
	}
	if websiteConnections(t, c) != 0 || requests.count("/api/presence") != 0 || requests.count("/api/auth/heartbeat") != 0 {
		t.Fatal("game authentication counted as website activity")
	}
	federationJSON(t, federationRequest(t, "POST", c.HTTP.URL+"/api/auth/logout", c.Cookie, map[string]string{}), 200)
	deadline := time.Now().Add(3 * time.Second)
	for g1.App.accounts.session(identity.Hash(token1)) != nil || g2.App.accounts.session(identity.Hash(token2)) != nil {
		if time.Now().After(deadline) {
			t.Fatal("core logout failed to revoke bearer sessions")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestBrowserClientPluginGateCancellationAndSaveRollback(t *testing.T) {
	h := federationListener(t)
	c := federationCore(t, []identity.Client{{ID: "gate", Name: "Gate", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://" + h.Listener.Addr().String() + "/api/auth/callback"}}, "")
	g := federationGame(t, h, c, "gate", identityPlugin(t))
	mode := filepath.Join(g.Config.DataDir, "plugins", "identity-test", "mode")
	for _, v := range []string{"identity_cancel", "cancel", "invalid_bool", "invalid_fields"} {
		os.WriteFile(mode, []byte(v), 0600)
		want := 403
		if strings.HasPrefix(v, "invalid") {
			want = 422
		}
		browserLogin(t, c, g, want)
		if identityCount(t, g.Config.DataDir, api.PlayerIdentified) != 0 || identityCount(t, g.Config.DataDir, api.PlayerAuthenticated) != 0 {
			t.Fatal("failed action reported success")
		}
	}
	os.WriteFile(mode, []byte(""), 0600)
	tmp := g.App.accounts.file + ".tmp"
	os.Mkdir(tmp, 0700)
	browserLogin(t, c, g, 503)
	os.Remove(tmp)
	if identityCount(t, g.Config.DataDir, api.PlayerIdentified) != 0 {
		t.Fatal("failed persistence reported success")
	}
	os.WriteFile(mode, []byte("gate"), 0600)
	data := browserLogin(t, c, g, 200)
	token := data["clientToken"].(string)
	if data["access"].(map[string]any)["authenticated"] != false {
		t.Fatal("plugin verification was bypassed")
	}
	federationJSON(t, browserGameRequest(t, g, "POST", "/api/rooms", token, "", map[string]string{"name": "forbidden"}), 401)
	result := federationJSON(t, browserGameRequest(t, g, "POST", "/api/auth/server-access", token, "", map[string]any{"action": "register", "values": map[string]string{"invite": "fixture-code"}}), 200)
	if result["user"].(map[string]any)["id"] != c.ID || result["access"].(map[string]any)["authenticated"] != true {
		t.Fatal("plugin verification changed fixed identity")
	}
	for _, e := range identityEventLog(t, g.Config.DataDir) {
		if e.Identity == nil || e.Identity.ID != c.ID || e.String("actor") != c.ID {
			t.Fatal("immutable identity changed")
		}
		raw, _ := json.Marshal(e)
		if bytes.Contains(raw, []byte(token)) {
			t.Fatal("game token leaked into event")
		}
	}
}
