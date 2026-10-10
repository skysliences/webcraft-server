package identity

import (
	"encoding/json"
	"errors"
	"io"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAdminID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testProtectedID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const testUserID = "cccccccccccccccccccccccccccccccc"

type consoleStore struct {
	mu       sync.Mutex
	accounts map[string]ManagedAccount
	sessions map[string]string
	failSave bool
}

func newConsoleStore() *consoleStore {
	now := time.Now().Unix()
	s := &consoleStore{accounts: map[string]ManagedAccount{}, sessions: map[string]string{}}
	for _, a := range []ManagedAccount{
		{ID: testAdminID, Username: "sky", Name: "核心管理员", CreatedAt: now - 100, RegistrationIP: "192.0.2.1", LastLoginAt: now - 5, LastLoginIP: "192.0.2.2"},
		{ID: testProtectedID, Username: "keeper", Name: "备份管理员", CreatedAt: now - 50},
		{ID: testUserID, Username: "player", Name: "测试玩家", CreatedAt: now, RegistrationIP: "2001:db8::3", LastLoginAt: now, LastLoginIP: "192.0.2.4"},
	} {
		s.accounts[a.ID] = a
	}
	s.sessions[Hash(strings.Repeat("a", 64))] = testAdminID
	s.sessions[Hash(strings.Repeat("b", 64))] = testProtectedID
	s.sessions[Hash(strings.Repeat("c", 64))] = testUserID
	s.sessions[Hash(strings.Repeat("d", 64))] = testUserID
	return s
}
func (s *consoleStore) Current(hash string) *Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[s.sessions[hash]]
	if !ok || a.Disabled {
		return nil
	}
	return &Profile{ID: a.ID, Username: a.Username, Name: a.Name}
}
func (s *consoleStore) Register(string, string, string) (*Profile, string, error) {
	return nil, "", errors.New("not implemented")
}
func (s *consoleStore) Login(string, string, string) (*Profile, string, error) {
	return nil, "", errors.New("not implemented")
}
func (s *consoleStore) SetName(string, string) (*Profile, error) {
	return nil, errors.New("not implemented")
}
func (s *consoleStore) Logout(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hash)
	return nil
}
func (s *consoleStore) Allow(*http.Request) bool { return true }
func (s *consoleStore) Close() error             { return nil }
func (s *consoleStore) accountLocked(id string) *ManagedAccount {
	a, ok := s.accounts[id]
	if !ok {
		return nil
	}
	a.SessionCount = 0
	for _, uid := range s.sessions {
		if uid == id {
			a.SessionCount++
		}
	}
	return &a
}
func (s *consoleStore) Account(id string) *ManagedAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(id)
}
func (s *consoleStore) ListAccounts() []ManagedAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []ManagedAccount{}
	for id := range s.accounts {
		rows = append(rows, *s.accountLocked(id))
	}
	return rows
}
func (s *consoleStore) SetAccountDisabled(id string, disabled bool) (*ManagedAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return nil, errors.New("fixture save failure")
	}
	a := s.accounts[id]
	a.Disabled = disabled
	s.accounts[id] = a
	if disabled {
		for hash, uid := range s.sessions {
			if uid == id {
				delete(s.sessions, hash)
			}
		}
	}
	return s.accountLocked(id), nil
}
func (s *consoleStore) RevokeAccountSessions(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return 0, errors.New("fixture save failure")
	}
	count := 0
	for hash, uid := range s.sessions {
		if uid == id {
			delete(s.sessions, hash)
			count++
		}
	}
	return count, nil
}

func consoleFixture(t *testing.T, pluginDir string) (*Service, *consoleStore, string) {
	t.Helper()
	store := newConsoleStore()
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, Issuer: "http://localhost", AdminUsers: []string{"sky", testProtectedID}, PluginDir: pluginDir, Clients: []Client{{ID: "testserver", Name: "独立游戏服务器", OwnerID: testUserID, FrontendURL: "http://localhost:8080", CallbackURL: "http://localhost:8080/api/auth/callback"}}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, store, dir
}
func consoleRequest(s *Service, method, path, token, csrf, origin, raw string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(raw))
	r.RemoteAddr = "192.0.2.9:4321"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: Cookie, Value: token})
	}
	if csrf != "" {
		r.Header.Set("X-Mcraft-CSRF", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func consoleCSRF(t *testing.T, s *Service, token string) string {
	t.Helper()
	w := consoleRequest(s, "GET", "/api/admin/me", token, "", "", "")
	if w.Code != 200 {
		t.Fatalf("admin me: %d %s", w.Code, w.Body.String())
	}
	var me struct{ CSRFToken string }
	if json.Unmarshal(w.Body.Bytes(), &me) != nil || len(me.CSRFToken) != 64 {
		t.Fatal("missing session-bound CSRF")
	}
	return me.CSRFToken
}

func TestCoreAdminPermissionsPrivacyPaginationAndDurableFixedID(t *testing.T) {
	s, _, dir := consoleFixture(t, "")
	for _, path := range []string{"/api/admin/me", "/api/admin/users", "/api/admin/users/" + testUserID, "/api/admin/stats", "/api/admin/online", "/api/admin/servers"} {
		for _, test := range []struct {
			token string
			code  int
		}{{"", 401}, {strings.Repeat("c", 64), 403}} {
			w := consoleRequest(s, "GET", path, test.token, "", "", "")
			if w.Code != test.code || strings.Contains(w.Body.String(), "registrationIp") {
				t.Fatalf("private %s returned %d %s", path, w.Code, w.Body.String())
			}
		}
	}
	token := strings.Repeat("a", 64)
	w := consoleRequest(s, "GET", "/api/admin/users?page=1&pageSize=1", token, "", "", "")
	var page struct {
		Users                        []adminAccount
		Total, Page, Pages, PageSize int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 3 || page.Pages != 3 || len(page.Users) != 1 || page.Users[0].ID != testUserID || page.Users[0].RegistrationIP != "2001:db8::3" {
		t.Fatalf("bad page %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"q=" + testUserID, "q=PLAYER", "q=%E6%B5%8B%E8%AF%95%E7%8E%A9%E5%AE%B6"} {
		w = consoleRequest(s, "GET", "/api/admin/users?"+query, token, "", "", "")
		if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 1 || page.Users[0].ID != testUserID {
			t.Fatalf("search %s: %s", query, w.Body.String())
		}
	}
	w = consoleRequest(s, "GET", "/api/admin/users?page=999&pageSize=999", token, "", "", "")
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Page != 1 || page.PageSize != 100 {
		t.Fatal("unbounded page")
	}
	for _, path := range []string{"/api/admin/users", "/api/admin/users/" + testUserID, "/api/admin/servers"} {
		w = consoleRequest(s, "GET", path, token, "", "", "")
		for _, secret := range []string{"password", "passwordHash", "legacySid", "sessionHash", strings.Repeat("a", 64), Hash(strings.Repeat("a", 64))} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("%s exposed %s", path, secret)
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "admins.json"))
	if err != nil || strings.Contains(string(raw), "sky") || !strings.Contains(string(raw), testAdminID) {
		t.Fatalf("administrators not stored by stable ID: %s %v", raw, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "admins.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("administrators file is not private")
	}
	// Omitting bootstrap config after restart preserves the resolved permission.
	store := newConsoleStore()
	account := store.accounts[testAdminID]
	account.Username = "renamedaccount"
	store.accounts[testAdminID] = account
	restarted, err := New(Config{DataDir: dir, Issuer: "http://localhost"}, store)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if consoleRequest(restarted, "GET", "/api/admin/me", token, "", "", "").Code != 200 {
		t.Fatal("stable administrator ID was lost after rename/restart")
	}
}

func TestCoreAdminMutationCSRFAndAdministratorProtection(t *testing.T) {
	s, store, _ := consoleFixture(t, "")
	token, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	csrf := consoleCSRF(t, s, token)
	consoleCSRF(t, s, other)
	path := "/api/admin/users/" + testUserID + "/status"
	for _, test := range []struct{ token, csrf, origin string }{{token, "", "http://localhost"}, {token, csrf, ""}, {token, csrf, "https://attacker.example"}, {other, csrf, "http://localhost"}} {
		if w := consoleRequest(s, "POST", path, test.token, test.csrf, test.origin, `{"disabled":true}`); w.Code != 403 {
			t.Fatalf("CSRF accepted %d %s", w.Code, w.Body.String())
		}
	}
	if store.Account(testUserID).Disabled {
		t.Fatal("CSRF mutated account")
	}
	for _, id := range []string{testAdminID, testProtectedID} {
		for _, suffix := range []string{"/status", "/sessions/revoke"} {
			raw := `{}`
			if suffix == "/status" {
				raw = `{"disabled":true}`
			}
			w := consoleRequest(s, "POST", "/api/admin/users/"+id+suffix, token, csrf, "http://localhost", raw)
			if w.Code != 409 {
				t.Fatalf("protected admin changed %d %s", w.Code, w.Body.String())
			}
		}
	}
	for _, raw := range []string{`{}`, `{"disabled":"true"}`, `{"disabled":true,"target":"forged"}`} {
		if w := consoleRequest(s, "POST", path, token, csrf, "http://localhost", raw); w.Code != 400 {
			t.Fatalf("invalid management payload accepted %d %s", w.Code, w.Body.String())
		}
	}
}

func TestIdentityAdminPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "identity-admin-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.AccountAdminStatusChanging, api.AccountAdminSessionsRevoking} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			mode, _ := os.ReadFile(filepath.Join(c.DataDir, "mode"))
			if string(mode) == "cancel" {
				e.Cancel("admin.fixture.protected")
			}
			e.Data["target"] = "forged"
			e.Data["disabled"] = false
			if e.Identity != nil {
				e.Identity.ID = "forged"
			}
			return nil
		})
	}
	for _, name := range []string{api.AccountAdminStatusChanging, api.AccountAdminStatusChanged, api.AccountAdminSessionsRevoking, api.AccountAdminSessionsRevoked, api.AccountPresenceUpdated, api.AccountPresenceOffline} {
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
			e.Data["target"] = "monitor.forged"
			return err
		})
	}
	if err := p.Serve(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func consolePlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "admin-test")
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
	manifest := `{"id":"admin-test","name":"Admin fixture","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestIdentityAdminPluginProcess$","identity-admin-fixture"]}`
	if err := os.WriteFile(filepath.Join(path, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func consoleEvents(t *testing.T, dir string) []api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "admin-test", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid event fixture")
		}
		events = append(events, e)
	}
	return events
}
func successfulAdminEvents(events []api.Event) int {
	n := 0
	for _, e := range events {
		if e.Name == api.AccountAdminStatusChanged || e.Name == api.AccountAdminSessionsRevoked {
			n++
		}
	}
	return n
}

func TestCoreAdminCancellationReadOnlyMonitorAndCommitEvents(t *testing.T) {
	s, store, dir := consoleFixture(t, consolePlugin(t))
	token := strings.Repeat("a", 64)
	csrf := consoleCSRF(t, s, token)
	if w := websiteHeartbeat(t, s, strings.Repeat("c", 64), strings.Repeat("e", 32), true); w.Code != 200 {
		t.Fatalf("website activity setup %d %s", w.Code, w.Body.String())
	}
	modePath := filepath.Join(dir, "plugins", "admin-test", "mode")
	if err := os.WriteFile(modePath, []byte("cancel"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/status", "/sessions/revoke"} {
		raw := `{}`
		if suffix == "/status" {
			raw = `{"disabled":true}`
		}
		w := consoleRequest(s, "POST", "/api/admin/users/"+testUserID+suffix, token, csrf, "http://localhost", raw)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "admin.fixture.protected") {
			t.Fatalf("cancellation %d %s", w.Code, w.Body.String())
		}
	}
	if store.Account(testUserID).Disabled || store.Account(testUserID).SessionCount != 2 || successfulAdminEvents(consoleEvents(t, dir)) != 0 {
		t.Fatal("cancelled mutation committed or reported success")
	}
	os.WriteFile(modePath, nil, 0600)
	w := consoleRequest(s, "POST", "/api/admin/users/"+testUserID+"/status", token, csrf, "http://localhost", `{"disabled":true}`)
	if w.Code != 200 || !store.Account(testUserID).Disabled || store.Account(testUserID).SessionCount != 0 {
		t.Fatalf("monitor or readonly mutation altered commit %d %s", w.Code, w.Body.String())
	}
	for _, p := range s.adminPresence() {
		if p.AccountID == testUserID {
			t.Fatal("disabled user's presence survived")
		}
	}
	events := consoleEvents(t, dir)
	if successfulAdminEvents(events) != 1 {
		t.Fatal("missing successful committed event")
	}
	for _, e := range events {
		if !strings.HasPrefix(e.Name, "account.admin.") {
			continue
		}
		if e.String("target") != testUserID || e.String("actor") != testAdminID || e.Identity == nil || e.Identity.ID != testAdminID {
			t.Fatal("plugin changed read-only account identity")
		}
		if e.Name == api.AccountAdminStatusChanged {
			before := e.Data["before"].(map[string]any)
			after := e.Data["after"].(map[string]any)
			if before["disabled"] != false || before["sessionCount"] != float64(2) || after["disabled"] != true || after["sessionCount"] != float64(0) || e.String("reason") != "admin_disable" {
				t.Fatal("completion missing accurate account snapshots")
			}
		}
	}
	w = consoleRequest(s, "POST", "/api/admin/users/"+testUserID+"/status", token, csrf, "http://localhost", `{"disabled":false}`)
	if w.Code != 200 || store.Account(testUserID).Disabled || store.Current(Hash(strings.Repeat("c", 64))) != nil {
		t.Fatal("enabling account revived an old session")
	}
	store.mu.Lock()
	store.sessions[Hash(strings.Repeat("e", 64))] = testUserID
	store.mu.Unlock()
	w = consoleRequest(s, "POST", "/api/admin/users/"+testUserID+"/sessions/revoke", token, csrf, "http://localhost", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revoked":1`) || store.Current(Hash(strings.Repeat("e", 64))) != nil {
		t.Fatalf("revocation failed %d %s", w.Code, w.Body.String())
	}
	if successfulAdminEvents(consoleEvents(t, dir)) != 3 {
		t.Fatal("incorrect success event count")
	}
	audit, err := os.ReadFile(filepath.Join(dir, "admin-audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), `"outcome":"cancelled"`) || !strings.Contains(string(audit), `"outcome":"committed"`) || !strings.Contains(string(audit), `"ip":"192.0.2.9"`) {
		t.Fatalf("missing durable audit %v %s", err, audit)
	}
	for _, secret := range []string{"password", "csrf", "sessionHash", token, Hash(token)} {
		if strings.Contains(string(audit), secret) {
			t.Fatal("audit contains credentials")
		}
	}
}

func TestCoreAdminSaveAndAuditFailureNeverReportSuccess(t *testing.T) {
	s, store, dir := consoleFixture(t, consolePlugin(t))
	token := strings.Repeat("a", 64)
	csrf := consoleCSRF(t, s, token)
	store.failSave = true
	for _, suffix := range []string{"/status", "/sessions/revoke"} {
		raw := `{}`
		if suffix == "/status" {
			raw = `{"disabled":true}`
		}
		w := consoleRequest(s, "POST", "/api/admin/users/"+testUserID+suffix, token, csrf, "http://localhost", raw)
		if w.Code != 503 {
			t.Fatalf("save failure %d %s", w.Code, w.Body.String())
		}
	}
	if store.Account(testUserID).Disabled || store.Account(testUserID).SessionCount != 2 || successfulAdminEvents(consoleEvents(t, dir)) != 0 {
		t.Fatal("save failed but mutation or success occurred")
	}
	store.failSave = false
	if err := os.Remove(filepath.Join(dir, "admin-audit.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "admin-audit.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	w := consoleRequest(s, "POST", "/api/admin/users/"+testUserID+"/status", token, csrf, "http://localhost", `{"disabled":true}`)
	if w.Code != 503 || store.Account(testUserID).Disabled || successfulAdminEvents(consoleEvents(t, dir)) != 0 {
		t.Fatal("mutation occurred without durable attempt audit")
	}
}

func TestCoreAdminConcurrentReadsAndChanges(t *testing.T) {
	s, _, _ := consoleFixture(t, "")
	token := strings.Repeat("a", 64)
	csrf := consoleCSRF(t, s, token)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path, method, raw := "/api/admin/users", "GET", ""
			if n%4 == 0 {
				path, method, raw = "/api/admin/users/"+testUserID+"/status", "POST", `{"disabled":true}`
			} else if n%4 == 1 {
				path, method, raw = "/api/admin/users/"+testUserID+"/status", "POST", `{"disabled":false}`
			} else if n%4 == 2 {
				path = "/api/admin/stats"
			}
			if w := consoleRequest(s, method, path, token, csrf, "http://localhost", raw); w.Code != 200 {
				t.Errorf("concurrent management returned %d %s", w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
}

func TestCoreAdminAssetsAndReadonlyEventCatalog(t *testing.T) {
	s, _, _ := consoleFixture(t, "")
	for _, path := range []string{"/admin", "/admin/", "/admin/admin.js", "/admin/admin.css"} {
		w := consoleRequest(s, "GET", path, "", "", "", "")
		if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("missing independent console asset %s", path)
		}
	}
	if consoleRequest(s, "GET", "/admin/not-a-route", "", "", "", "").Code != 404 {
		t.Fatal("unknown admin route serves a page")
	}
	before := map[string]bool{api.AccountAdminStatusChanging: true, api.AccountAdminStatusChanged: false, api.AccountAdminSessionsRevoking: true, api.AccountAdminSessionsRevoked: false, api.AccountPresenceUpdated: false, api.AccountPresenceOffline: false}
	for _, spec := range api.Events() {
		if cancel, exists := before[spec.Name]; exists {
			if spec.Cancellable != cancel || len(spec.Mutable) != 0 {
				t.Fatalf("wrong admin/presence catalog %v", spec)
			}
			delete(before, spec.Name)
		}
	}
	if len(before) != 0 {
		t.Fatal("missing admin/presence event specs")
	}
}
