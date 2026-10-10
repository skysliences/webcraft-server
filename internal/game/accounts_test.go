package game

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func authCall(t *testing.T, base, path string, cookie *http.Cookie, body any) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	method := "GET"
	var reader io.Reader
	if body != nil {
		method = "POST"
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, base+"/api/auth/"+path, reader)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data, res.Cookies()
}
func registerTestUser(t *testing.T, base, username, legacy string) (string, *http.Cookie) {
	t.Helper()
	status, data, cookies := authCall(t, base, "register", nil, map[string]string{"username": username, "password": "account-test-password", "legacySid": legacy})
	if status != 201 || len(cookies) != 1 {
		t.Fatalf("registration: %d", status)
	}
	return data["user"].(map[string]any)["id"].(string), cookies[0]
}
func setTestName(t *testing.T, base string, cookie *http.Cookie, name string) {
	t.Helper()
	status, _, _ := authCall(t, base, "profile", cookie, map[string]string{"name": name})
	if status != 200 {
		t.Fatalf("name: %d", status)
	}
}
func authConnect(t *testing.T, base, id, sid, key string, cookie *http.Cookie) (*websocket.Conn, map[string]any) {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/ws/"+id, http.Header{"Origin": []string{"http://127.0.0.1:8080"}, "Cookie": []string{cookie.String()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	send(t, c, map[string]any{"t": "hello", "v": 4, "sid": sid, "key": key, "rd": 2})
	welcome := receive(t, c, "welcome")
	receive(t, c, "synced")
	return c, welcome
}
func TestAccountsRegistrationNameGateAndImpersonation(t *testing.T) {
	s, h := testApp(t, t.TempDir())
	res, err := http.Post(h.URL+"/api/rooms", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("anonymous room creation accepted")
	}
	userID, cookie := registerTestUser(t, h.URL, "alice", "")
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatal("insecure session cookie")
	}
	_, reject, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+DefaultRoom, http.Header{"Cookie": []string{cookie.String()}})
	if err == nil || reject == nil || reject.StatusCode != 409 {
		t.Fatal("unnamed account joined world")
	}
	reject.Body.Close()
	setTestName(t, h.URL, cookie, "方块工程师")
	status, _, _ := authCall(t, h.URL, "register", nil, map[string]string{"username": "ALICE", "password": "another-test-password"})
	if status != 409 {
		t.Fatal("duplicate username accepted")
	}
	status, _, _ = authCall(t, h.URL, "login", nil, map[string]string{"username": "alice", "password": "incorrect-password"})
	if status != 401 {
		t.Fatal("incorrect password accepted")
	}
	req, _ := http.NewRequest("POST", h.URL+"/api/rooms", strings.NewReader(`{"mode":"survival"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var room map[string]string
	json.NewDecoder(res.Body).Decode(&room)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal("authenticated creator rejected")
	}
	a, wel := authConnect(t, h.URL, room["id"], "ForgedClientIdentity", "", cookie)
	if wel["owner"] != true || wel["you"].(map[string]any)["name"] != "方块工程师" {
		t.Fatal("account name or ownership not applied")
	}
	s.mu.Lock()
	r := s.rooms[room["id"]]
	r.Players[userID].Inv.Slots[0].Count = 3
	s.mu.Unlock()
	guestID, guestCookie := registerTestUser(t, h.URL, "bob", userID)
	if guestID == userID {
		t.Fatal("authenticated account was claimed by a supplied legacy SID")
	}
	status, _, _ = authCall(t, h.URL, "profile", guestCookie, map[string]string{"name": "方块工程师"})
	if status != 409 {
		t.Fatal("duplicate player name accepted")
	}
	setTestName(t, h.URL, guestCookie, "玩家二号")
	_, guest := authConnect(t, h.URL, room["id"], userID, room["key"], guestCookie)
	if guest["owner"] != false || guest["st"].(map[string]any)["inv"].(map[string]any)["s"].([]any)[0].([]any)[1] != float64(8) {
		t.Fatal("forged SID or owner key hijacked another account")
	}
	setTestName(t, h.URL, cookie, "改名后玩家")
	if receive(t, a, "who")["p"].(map[string]any)["name"] != "改名后玩家" {
		t.Fatal("name change was not broadcast")
	}
	s.mu.Lock()
	same := r.Players[userID].Inv.Slots[0].Count == 3 && r.OwnerAccount == userID
	s.mu.Unlock()
	if !same {
		t.Fatal("renaming changed inventory or owner identity")
	}
	raw, _ := os.ReadFile(s.accounts.file)
	if bytes.Contains(raw, []byte("account-test-password")) || bytes.Contains(raw, []byte(cookie.Value)) {
		t.Fatal("plaintext password or session token persisted")
	}
}
func TestAccountsSessionPersistenceLogoutAndExpiry(t *testing.T) {
	dir := t.TempDir()
	s, h := testApp(t, dir)
	id, cookie := registerTestUser(t, h.URL, "persist", "")
	setTestName(t, h.URL, cookie, "存档玩家")
	if err:=s.Close();err!=nil {t.Fatal(err)}
 restored, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm", AllowedOrigins: []string{"http://127.0.0.1:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	rh := httptest.NewServer(restored.Handler())
	t.Cleanup(func() { rh.Close(); restored.Close() })
 s,h=restored,rh
	status, data, _ := authCall(t, rh.URL, "me", cookie, nil)
	if status != 200 || data["user"].(map[string]any)["id"] != id || data["user"].(map[string]any)["name"] != "存档玩家" {
		t.Fatal("session or profile not restored")
	}
	c, _ := authConnect(t, h.URL, DefaultRoom, "SpoofedPlayer123", "", cookie)
	status, _, cleared := authCall(t, h.URL, "logout", cookie, map[string]any{})
	if status != 200 || len(cleared) != 1 || cleared[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	receive(t, c, "authrequired")
	_, data, _ = authCall(t, h.URL, "me", cookie, nil)
	if data["user"] != nil {
		t.Fatal("logged-out session replay accepted")
	}
	_, reject, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+DefaultRoom, http.Header{"Cookie": []string{cookie.String()}})
	if err == nil || reject == nil || reject.StatusCode != 401 {
		t.Fatal("logged-out session joined game")
	}
	reject.Body.Close()
	status, _, fresh := authCall(t, h.URL, "login", nil, map[string]string{"username": "PERSIST", "password": "account-test-password"})
	if status != 200 || fresh[0].Value == cookie.Value {
		t.Fatal("login did not rotate session token")
	}
	c, _ = authConnect(t, h.URL, DefaultRoom, "AnotherSpoofedID", "", fresh[0])
	s.accounts.mu.Lock()
	hash := hashKey(fresh[0].Value)
	session := s.accounts.sessions[hash]
	session.Expires = time.Now().Add(-time.Second).Unix()
	s.accounts.sessions[hash] = session
	s.accounts.mu.Unlock()
	receive(t, c, "authrequired") // Idle connections also expire without sending a packet.
}
func TestAccountsConcurrentNamesAndSaveFailure(t *testing.T) {
	s, h := testApp(t, t.TempDir())
	idA, cookieA := registerTestUser(t, h.URL, "name_a", "")
	idB, _ := registerTestUser(t, h.URL, "name_b", "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, id := range []string{idA, idB} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, err := s.accounts.setName(id, []string{"Builder", "builder"}[i])
			results <- err
		}(i, id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if e, ok := err.(*accountError); ok && e.status == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("simultaneous duplicate names were not serialized")
	}
	setTestName(t, h.URL, cookieA, "原来的名字")
	before, _ := os.ReadFile(s.accounts.file)
	os.Remove(s.accounts.file)
	os.Mkdir(s.accounts.file, 0700)
	status, _, _ := authCall(t, h.URL, "profile", cookieA, map[string]string{"name": "不应保存的名字"})
	if status != 503 || s.accounts.session(hashKey(cookieA.Value)).Name != "原来的名字" {
		t.Fatal("failed save committed profile")
	}
	status, _, _ = authCall(t, h.URL, "register", nil, map[string]string{"username": "retry_name", "password": "account-test-password"})
	if status != 503 {
		t.Fatal("registration claimed success after failed persistence")
	}
	os.Remove(s.accounts.file)
	os.WriteFile(s.accounts.file, before, 0600)
	status, _, _ = authCall(t, h.URL, "register", nil, map[string]string{"username": "retry_name", "password": "account-test-password"})
	if status != 201 {
		t.Fatal("failed registration was not rolled back for retry")
	}
}
func TestAccountsLegacyProgressAndMigrationBackup(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/rooms", 0700)
	legacySID := "LegacyAccountPlayer123"
	old := []byte(`{"version":3,"mode":"survival","name":"旧生存世界","players":{"LegacyAccountPlayer123":{"x":500,"y":90,"z":501,"health":20,"food":20,"inv":{"s":[[6,17,0]],"sel":0}}}}`)
	// Existing inventories use 46 slots; preserve an actual valid old inventory.
	var disk diskRoom
	json.Unmarshal(old, &disk)
	slots := make(Stacks, 46)
	slots[0] = stack(6, 17, 0)
	disk.Players[legacySID].Inv.Slots = slots
	old, _ = json.Marshal(disk)
	file := dir + "/rooms/" + DefaultRoom + ".json"
	os.WriteFile(file, old, 0600)
	s, h := testApp(t, dir)
	id, cookie := registerTestUser(t, h.URL, "legacy_user", legacySID)
	if id != legacySID {
		t.Fatal("legacy bearer progress was not adopted")
	}
	setTestName(t, h.URL, cookie, "老玩家的新名字")
	_, welcome := authConnect(t, h.URL, DefaultRoom, "IgnoredClientSID", "", cookie)
	st := welcome["st"].(map[string]any)
	if st["x"] != float64(500) || st["inv"].(map[string]any)["s"].([]any)[0].([]any)[1] != float64(17) {
		t.Fatal("registration lost old inventory or position")
	}
	s.mu.Lock()
	err := s.saveRoom(s.rooms[DefaultRoom])
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(file + ".pre-accounts-v3.bak")
	if err != nil || !bytes.Equal(backup, old) {
		t.Fatal("original save was not backed up before account migration")
	}
}
func TestAccountsValidationRateLimitAndCorruptProtection(t *testing.T) {
	s, h := testApp(t, t.TempDir())
	for _, body := range []map[string]string{{"username": "a", "password": "account-test-password"}, {"username": "valid", "password": "short"}, {"username": "valid", "password": strings.Repeat("a", 73)}} {
		status, _, _ := authCall(t, h.URL, "register", nil, body)
		if status != 422 {
			t.Fatal("invalid credentials accepted")
		}
	}
	_, cookie := registerTestUser(t, h.URL, "rate_test", "")
	for _, name := range []string{"a", "<script>", "a b", strings.Repeat("方", 21)} {
		status, _, _ := authCall(t, h.URL, "profile", cookie, map[string]string{"name": name})
		if status != 422 {
			t.Fatal("invalid player name accepted")
		}
	}
	for i := 0; i < 26; i++ {
		authCall(t, h.URL, "login", nil, map[string]string{"username": "rate_test", "password": "wrong"})
	}
	status, _, _ := authCall(t, h.URL, "login", nil, map[string]string{"username": "rate_test", "password": "account-test-password"})
	if status != 429 {
		t.Fatal("login flood not throttled")
	}
	req, _ := http.NewRequest("POST", h.URL+"/api/auth/logout", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://other.example")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross-origin credentialed action accepted")
	}
	old := []byte(`{"version":1,"users":[null]}`)
	os.WriteFile(s.accounts.file, old, 0600)
	if _, err := New(Config{DataDir: s.config.DataDir, EngineFile: "../../../public/engine.wasm"}); err == nil {
		t.Fatal("corrupt account store silently ignored")
	}
	after, _ := os.ReadFile(s.accounts.file)
	if !bytes.Equal(old, after) {
		t.Fatal("corrupt account store was overwritten")
	}
}

func TestAccountsLoginClaimsLegacyProgressOnce(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/rooms", 0700)
	legacy := "OldBearerIdentity123"
	disk := diskRoom{Version: 3, Mode: "creative", Name: "旧进度", Players: map[string]*Player{legacy: {X: 500, Y: 90, Z: 501, Health: 20, Food: 20}}}
	raw, _ := json.Marshal(disk)
	os.WriteFile(dir+"/rooms/"+DefaultRoom+".json", raw, 0600)
	s, h := testApp(t, dir)
	id, cookie := registerTestUser(t, h.URL, "existing_user", "")
	setTestName(t, h.URL, cookie, "登录接入老进度")
	_, welcome := authConnect(t, h.URL, DefaultRoom, legacy, "", cookie)
	if welcome["st"].(map[string]any)["x"] != float64(500) {
		t.Fatal("existing account did not adopt old browser progress")
	}
	other, otherCookie := registerTestUser(t, h.URL, "other_user", legacy)
	if other == legacy {
		t.Fatal("claimed legacy SID was reused as a new account ID")
	}
	setTestName(t, h.URL, otherCookie, "另一个玩家")
	_, _ = authConnect(t, h.URL, DefaultRoom, legacy, "", otherCookie)
	s.mu.Lock()
	retained := s.rooms[DefaultRoom].Players[id] != nil && s.rooms[DefaultRoom].Players[legacy] == nil
	s.mu.Unlock()
	if !retained {
		t.Fatal("legacy progress was duplicated or reassigned")
	}
	if got, _ := s.accounts.claimLegacy(other, legacy); got != "" {
		t.Fatal("second account claimed old bearer identity")
	}
}
