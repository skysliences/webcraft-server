package identity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type presenceTestStore struct {
	mu    sync.Mutex
	users map[string]Profile
}

func (m *presenceTestStore) Current(h string) *Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[h]
	if !ok {
		return nil
	}
	return &u
}
func (m *presenceTestStore) Register(a, b, c string) (*Profile, string, error) { return nil, "", nil }
func (m *presenceTestStore) Login(a, b, c string) (*Profile, string, error)    { return nil, "", nil }
func (m *presenceTestStore) SetName(a, b string) (*Profile, error)             { return nil, nil }
func (m *presenceTestStore) Logout(h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, h)
	return nil
}
func (m *presenceTestStore) Allow(r *http.Request) bool { return true }
func (m *presenceTestStore) Close() error               { return nil }
func presenceFixture(t *testing.T) (*Service, *presenceTestStore, string, string) {
	t.Helper()
	token := Random()
	user := Profile{ID: strings.Repeat("a", 32), Username: "alice", Name: "玩家甲"}
	store := &presenceTestStore{users: map[string]Profile{Hash(token): user}}
	s, err := New(Config{DataDir: t.TempDir(), Issuer: "http://127.0.0.1:8082", Clients: []Client{
		{ID: "server-a", Name: "游戏服甲", FrontendURL: "http://127.0.0.1:8080", CallbackURL: "http://127.0.0.1:8081/api/auth/callback"},
		{ID: "server-b", Name: "游戏服乙", FrontendURL: "http://127.0.0.1:8180", CallbackURL: "http://127.0.0.1:8181/api/auth/callback"}}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, store, token, presenceProof(t, s, user, "server-a", Hash(token), time.Now().Add(10*time.Minute))
}
func presenceProof(t *testing.T, s *Service, user Profile, server, hash string, expires time.Time) string {
	t.Helper()
	proof, err := Sign(s.key, Claims{Issuer: s.config.Issuer, Audience: server, Subject: user.ID, IssuedAt: expires.Add(-10 * time.Minute).Unix(), Expires: expires.Unix(), Session: hash, Profile: user})
	if err != nil {
		t.Fatal(err)
	}
	return proof
}
func websiteRequest(t *testing.T, s *Service, method, path, token string, input any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if input != nil {
		raw, _ = json.Marshal(input)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", s.config.Issuer)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: Cookie, Value: token})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func websiteHeartbeat(t *testing.T, s *Service, token, nonce string, online bool) *httptest.ResponseRecorder {
	t.Helper()
	return websiteRequest(t, s, "POST", "/api/auth/heartbeat", token, map[string]any{"connectionId": nonce, "online": online})
}
func assertWebsite(t *testing.T, s *Service, accounts, connections int) {
	t.Helper()
	rows := s.adminPresence()
	if len(rows) != accounts {
		t.Fatalf("online accounts: got %v want %d", rows, accounts)
	}
	total := 0
	for _, row := range rows {
		if row.Location != "website" || row.ServerID != "" || row.ServerName != "" {
			t.Fatalf("private game server data entered website presence: %v", row)
		}
		total += row.Connections
	}
	if total != connections {
		t.Fatalf("website connections: got %d want %d", total, connections)
	}
}
func TestWebsitePresenceOnlyActivePagesAndMultipleTabs(t *testing.T) {
	s, store, token, proof := presenceFixture(t)
	if w := websiteRequest(t, s, "GET", "/api/auth/me", token, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 0, 0)
	if w := websiteRequest(t, s, "POST", "/api/token/status", "", map[string]any{"proof": proof, "client_id": "server-a"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 0, 0)
	tabA, tabB, resumed := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)
	for _, nonce := range []string{tabA, tabB, tabB} {
		if w := websiteHeartbeat(t, s, token, nonce, true); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	assertWebsite(t, s, 1, 2)
	if w := websiteHeartbeat(t, s, token, tabA, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 1, 1)
	if w := websiteHeartbeat(t, s, token, tabA, false); w.Code != 200 {
		t.Fatal("repeat departure was not idempotent")
	}
	if w := websiteHeartbeat(t, s, token, tabA, true); w.Code != 409 {
		t.Fatal("late request resurrected a departed page")
	}
	assertWebsite(t, s, 1, 1)
	if w := websiteHeartbeat(t, s, token, resumed, true); w.Code != 200 {
		t.Fatal("visible resume with a new connection failed")
	}
	assertWebsite(t, s, 1, 2)
	if w := websiteHeartbeat(t, s, token, tabA, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 1, 2)
	if w := websiteRequest(t, s, "POST", "/api/auth/logout", token, map[string]any{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if store.Current(Hash(token)) != nil {
		t.Fatal("logout did not revoke the cookie")
	}
	assertWebsite(t, s, 0, 0)
	if w := websiteHeartbeat(t, s, token, resumed, true); w.Code != 401 {
		t.Fatal("revoked cookie created website presence")
	}
}
func TestWebsitePresenceCookieOwnershipAndValidation(t *testing.T) {
	s, store, token, _ := presenceFixture(t)
	nonce := strings.Repeat("4", 32)
	cases := []struct {
		token string
		body  any
		code  int
	}{
		{"", map[string]any{"connectionId": nonce, "online": true}, 401},
		{strings.Repeat("f", 64), map[string]any{"connectionId": nonce, "online": true}, 401},
		{token, map[string]any{}, 400},
		{token, map[string]any{"connectionId": nonce}, 400},
		{token, map[string]any{"connectionId": "bad nonce", "online": true}, 400},
		{token, map[string]any{"connectionId": strings.Repeat("A", 32), "online": true}, 400},
		{token, map[string]any{"connectionId": nonce, "online": "true"}, 400},
		{token, map[string]any{"connectionId": nonce, "online": true, "serverId": "private"}, 400},
	}
	for _, c := range cases {
		if w := websiteRequest(t, s, "POST", "/api/auth/heartbeat", c.token, c.body); w.Code != c.code {
			t.Fatalf("invalid heartbeat got%d want%d: %s", w.Code, c.code, w.Body.String())
		}
		assertWebsite(t, s, 0, 0)
	}
	if w := websiteHeartbeat(t, s, token, nonce, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	sameAccountToken, otherToken := Random(), Random()
	store.mu.Lock()
	store.users[Hash(sameAccountToken)] = store.users[Hash(token)]
	store.users[Hash(otherToken)] = Profile{ID: strings.Repeat("b", 32), Username: "bob", Name: "玩家乙"}
	store.mu.Unlock()
	for _, wrong := range []string{sameAccountToken, otherToken} {
		for _, online := range []bool{true, false} {
			if w := websiteHeartbeat(t, s, wrong, nonce, online); w.Code != 409 {
				t.Fatal("another session changed an owned tab")
			}
		}
	}
	assertWebsite(t, s, 1, 1)
	raw, _ := json.Marshal(s.adminPresence())
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), Hash(token)) || strings.Contains(string(raw), "server-a") {
		t.Fatal("public website presence contains credentials or server data")
	}
	second := strings.Repeat("5", 32)
	if w := websiteHeartbeat(t, s, sameAccountToken, second, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 1, 2)
	websiteRequest(t, s, "POST", "/api/auth/logout", token, map[string]any{})
	assertWebsite(t, s, 1, 1)
	if w := websiteHeartbeat(t, s, sameAccountToken, second, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertWebsite(t, s, 0, 0)
	r := httptest.NewRequest("POST", "/api/auth/heartbeat", strings.NewReader(`{"connectionId":"`+second+`","online":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.invalid")
	r.AddCookie(&http.Cookie{Name: Cookie, Value: sameAccountToken})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin cookie heartbeat accepted")
	}
	assertWebsite(t, s, 0, 0)
}
func TestWebsitePresenceExpiryAndBoundedDepartures(t *testing.T) {
	s, _, token, _ := presenceFixture(t)
	nonce := strings.Repeat("6", 32)
	websiteHeartbeat(t, s, token, nonce, true)
	s.presence.mu.Lock()
	e := s.presence.entries[nonce]
	e.seen = time.Now().Add(-presenceTTL - time.Second)
	s.presence.entries[nonce] = e
	s.presence.mu.Unlock()
	if w := websiteRequest(t, s, "GET", "/api/auth/me", token, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Reading an authentication lease must not renew an inactive page, even
	// when another tab or a backend continues to check the same cookie.
	assertWebsite(t, s, 0, 0)
	s.expirePresence("core_revoked")
	if len(s.presence.entries) != 0 {
		t.Fatal("expired activity not removed")
	}
	// After a missed heartbeat, the still-visible page may refresh its nonce.
	if w := websiteHeartbeat(t, s, token, nonce, true); w.Code != 200 {
		t.Fatal("expired page could not send fresh activity")
	}
	websiteHeartbeat(t, s, token, nonce, false)
	if w := websiteHeartbeat(t, s, token, nonce, true); w.Code != 409 {
		t.Fatal("departed page was not terminal")
	}
	s.presence.mu.Lock()
	for i := len(s.presence.departed); i < presenceLimit; i++ {
		s.presence.departed[fmt.Sprintf("%032x", i)] = presenceDeparture{seen: time.Now()}
	}
	s.presence.mu.Unlock()
	if w := websiteHeartbeat(t, s, token, strings.Repeat("7", 32), true); w.Code != 429 {
		t.Fatal("unbounded tab/departure state")
	}
	if w := websiteHeartbeat(t, s, token, nonce, false); w.Code != 200 {
		t.Fatal("repeated departure at capacity not idempotent")
	}
	s.presence.mu.Lock()
	for key, d := range s.presence.departed {
		d.seen = time.Now().Add(-presenceTTL - time.Second)
		s.presence.departed[key] = d
	}
	s.presence.mu.Unlock()
	s.expirePresence("core_revoked")
	if len(s.presence.departed) != 0 {
		t.Fatal("departures accumulated after their TTL")
	}
	if w := websiteHeartbeat(t, s, token, strings.Repeat("7", 32), true); w.Code != 200 {
		t.Fatal("capacity did not recover after expiry")
	}
}
func TestWebsitePresenceCoreRestartRequiresFreshHeartbeat(t *testing.T) {
	s, store, token, _ := presenceFixture(t)
	nonce := strings.Repeat("8", 32)
	if w := websiteHeartbeat(t, s, token, nonce, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	config := s.config
	s.Close()
	restarted, err := New(config, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	assertWebsite(t, restarted, 0, 0)
	if w := websiteHeartbeat(t, restarted, token, nonce, true); w.Code != 200 {
		t.Fatal("preserved cookie did not restore fresh website activity")
	}
	assertWebsite(t, restarted, 1, 1)
}
func TestGamePresenceEndpointRetiredWithoutCounting(t *testing.T) {
	s, _, token, proof := presenceFixture(t)
	for _, value := range []string{proof, "forged"} {
		w := websiteRequest(t, s, "POST", "/api/presence", token, map[string]any{"client_id": "server-a", "updates": []any{map[string]any{"proof": value, "connectionId": strings.Repeat("9", 32), "online": true}}})
		if w.Code != 410 {
			t.Fatal("legacy game presence report was not retired")
		}
		assertWebsite(t, s, 0, 0)
	}
}
func TestTrustedProxyRegistrationIP(t *testing.T) {
	s := &Service{config: Config{TrustedProxies: []string{"127.0.0.1", "10.0.0.0/8"}}}
	if err := s.validateTrustedProxies(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ peer, forward, real, want string }{
		{"203.0.113.1:42", "198.51.100.2", "", "203.0.113.1"},
		{"127.0.0.1:42", "198.51.100.2, 10.0.0.8", "", "198.51.100.2"},
		{"127.0.0.1:42", "203.0.113.9, 198.51.100.2", "", "198.51.100.2"},
		{"127.0.0.1:42", "bad, 10.0.0.8", "", "127.0.0.1"},
		{"127.0.0.1:42", "", "2001:db8::12", "2001:db8::12"},
		{"[2001:db8::1]:42", "198.51.100.2", "", "2001:db8::1"},
	} {
		r := httptest.NewRequest("POST", "/api/auth/register", nil)
		r.RemoteAddr = c.peer
		r.Header.Set("X-Forwarded-For", c.forward)
		r.Header.Set("X-Real-IP", c.real)
		if got := s.clientIP(r); got != c.want {
			t.Fatalf("%v got %q", c, got)
		}
	}
	s.config.TrustedProxies = []string{"not-a-network"}
	if s.validateTrustedProxies() == nil {
		t.Fatal("invalid proxy config accepted")
	}
}

type rateIPStore struct {
	*presenceTestStore
	peer string
}

func (s *rateIPStore) Allow(r *http.Request) bool {
	s.peer = r.RemoteAddr
	return true
}

func TestCoreAuthenticationLimiterUsesTrustedClientIP(t *testing.T) {
	store := &rateIPStore{presenceTestStore: &presenceTestStore{}}
	s := &Service{store: store, config: Config{TrustedProxies: []string{"127.0.0.1"}}}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.8")
	if !s.allowAccountAttempt(r) || store.peer != "203.0.113.8:0" || r.RemoteAddr != "127.0.0.1:1234" {
		t.Fatal("trusted proxy users share a rate bucket or the original request was modified")
	}
	r.RemoteAddr = "198.51.100.9:1234"
	s.allowAccountAttempt(r)
	if store.peer != "198.51.100.9:0" {
		t.Fatal("untrusted forwarding header selected the authentication rate bucket")
	}
}
