package game

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type testAccountInfo struct {
	ID     string
	Cookie *http.Cookie
}

var testAccounts sync.Map

func accountFixture(t *testing.T, base, label string) testAccountInfo {
	t.Helper()
	if value, ok := testAccounts.Load(base + " " + label); ok {
		return value.(testAccountInfo)
	}
	res, err := http.Post(base+"/api/auth/register", "application/json", strings.NewReader(`{"username":"`+label+`","password":"test-password-2026","legacySid":"`+label+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("account fixture: %d %s", res.StatusCode, raw)
	}
	var body struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	json.NewDecoder(res.Body).Decode(&body)
	info := testAccountInfo{ID: body.User.ID, Cookie: res.Cookies()[0]}
	req, _ := http.NewRequest("POST", base+"/api/auth/profile", strings.NewReader(`{"name":"`+label[:min(len(label), 20)]+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(info.Cookie)
	profile, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Body.Close()
	if profile.StatusCode != 200 {
		t.Fatalf("profile fixture: %d", profile.StatusCode)
	}
	testAccounts.Store(base+" "+label, info)
	return info
}
func fixtureID(base, label string) string {
	info, _ := testAccounts.Load(base + " " + label)
	return info.(testAccountInfo).ID
}

func findTestEngineFile() string {
	candidates := []string{
		"../../../webcraft-client/src/client/engine.wasm",
		"../../../public/engine.wasm",
		"../../public/engine.wasm",
		"../public/engine.wasm",
		"../../../../public/engine.wasm",
		"../../../../WebCraft/public/engine.wasm",
		"../../WebCraft/public/engine.wasm",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "../../../public/engine.wasm"
}

func testApp(t *testing.T, dir string) (*Server, *httptest.Server) {
	t.Helper()
	spawning := DefaultMobSpawningConfig()
	spawning.GenerateChunkAnimals = false // Isolated mechanics tests seed exact worlds.
	app, err := New(Config{MobSpawning: &spawning, DataDir: dir, EngineFile: findTestEngineFile(), AllowedOrigins: []string{"http://127.0.0.1:8080"}, MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	app.accounts.hashCost = bcrypt.MinCost
	httpServer := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app, httpServer
}
func postRoom(t *testing.T, base, mode string) (string, string) {
	t.Helper()
	info := accountFixture(t, base, "Creator"+randomHex(6))
	req, _ := http.NewRequest("POST", base+"/api/rooms", strings.NewReader(`{"name":"测试世界","mode":"`+mode+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(info.Cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		data, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d: %s", res.StatusCode, data)
	}
	var result map[string]string
	json.NewDecoder(res.Body).Decode(&result)
	testAccounts.Store(base+" key:"+result["key"], info)
	return result["id"], result["key"]
}
func connect(t *testing.T, base, id, sid, key string) (*websocket.Conn, map[string]any) {
	t.Helper()
	var info testAccountInfo
	if value, ok := testAccounts.Load(base + " key:" + key); key != "" && ok {
		info = value.(testAccountInfo)
		testAccounts.Store(base+" "+sid, info)
	} else {
		info = accountFixture(t, base, sid)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/ws/"+id, http.Header{"Origin": []string{"http://127.0.0.1:8080"}, "Cookie": []string{info.Cookie.String()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	send(t, conn, map[string]any{"t": "hello", "v": 4, "sid": sid, "key": key, "rd": 3})
	welcome := receive(t, conn, "welcome")
	receive(t, conn, "synced")
	return conn, welcome
}
func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}
func receive(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		kind, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && m["t"] == typ {
			return m
		}
	}
}
func receiveBinary(t *testing.T, c *websocket.Conn, typ byte) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		kind, data, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage && len(data) > 0 && data[0] == typ {
			return data
		}
	}
}
func editPacket(key uint64, b byte) []byte {
	return append(binary.LittleEndian.AppendUint32([]byte{2}, toWireKey(key)), b)
}
func position(x, y, z float64) []byte {
	out := []byte{1}
	for _, v := range []float64{x, y, z} {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
	}
	out = append(out, 0, 0, 0, 0, 0)
	return out
}
func TestRESTValidationAndLegacySave(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "rooms"), 0700)
	legacy := `{"version":1,"name":"旧世界","edits":[[133131,5]],"players":{"LegacyPlayer123":{"x":500,"y":83,"z":501,"hotbar":[2,3,1,4,5,6,10,11,7],"slot":2}}}`
	os.WriteFile(filepath.Join(dir, "rooms", DefaultRoom+".json"), []byte(legacy), 0600)
	app, httpServer := testApp(t, dir)
	if app.rooms[DefaultRoom].Name != "旧世界" || app.rooms[DefaultRoom].Edits[fromWireKey(133131)] != 5 {
		t.Fatal("legacy save not preserved")
	}
	for _, target := range []string{"/api/rooms/aaaaaaaaaaaaaaaa", "/engine.wasm"} {
		res, err := http.Get(httpServer.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal(target, res.StatusCode)
		}
	}
	validator := accountFixture(t, httpServer.URL, "ValidatorPlayer123")
	for _, body := range []string{`{"mode":"unknown"}`, `{"mode":"creative","hardcore":true}`, `{"lock":1}`, `{} {}`, strings.Repeat("a", 9000)} {
		req, _ := http.NewRequest("POST", httpServer.URL+"/api/rooms", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(validator.Cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode < 400 {
			t.Fatalf("invalid body accepted: %s", body[:min(60, len(body))])
		}
	}
	req, _ := http.NewRequest("OPTIONS", httpServer.URL+"/api/rooms", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8080" {
		t.Fatal("CORS preflight")
	}
	req, _ = http.NewRequest("POST", httpServer.URL+"/api/rooms", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://unknown.example")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("unknown origin accepted")
	}
	id, key := postRoom(t, httpServer.URL, "creative")
	res, _ = http.Get(httpServer.URL + "/api/rooms/" + id)
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if bytes.Contains(data, []byte(key)) || bytes.Contains(data, []byte("ownerHash")) {
		t.Fatal("public API leaked ownership")
	}
	req, _ = http.NewRequest("GET", httpServer.URL+"/api/rooms/"+id+"/stats", nil)
	req.Header.Set("X-Owner-Key", key)
	ownerFixture, _ := testAccounts.Load(httpServer.URL + " key:" + key)
	req.AddCookie(ownerFixture.(testAccountInfo).Cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("owner stats unavailable")
	}
	_, welcome := connect(t, httpServer.URL, DefaultRoom, "LegacyPlayer123", "")
	state := welcome["st"].(map[string]any)
	if state["x"] != float64(500) || state["slot"] != float64(2) {
		t.Fatal("legacy player state changed")
	}
}
func TestCreativeMultiplayerAndRestart(t *testing.T) {
	dir := t.TempDir()
	app, httpServer := testApp(t, dir)
	id, key := postRoom(t, httpServer.URL, "creative")
	owner, welcome := connect(t, httpServer.URL, id, "OwnerPlayer123", key)
	if welcome["owner"] != true {
		t.Fatal("owner denied")
	}
	guest, _ := connect(t, httpServer.URL, id, "GuestPlayer123", "")
	send(t, guest, map[string]any{"t": "rename", "name": "越权"})
	receive(t, guest, "sys")
	send(t, owner, map[string]any{"t": "rename", "name": "重命名世界"})
	receive(t, owner, "renamed")
	send(t, owner, map[string]any{"t": "lock", "mode": 2})
	receive(t, guest, "room")
	receive(t, guest, "room")
	keyBlock := blockKey(510, 95, 510)
	guest.WriteMessage(websocket.BinaryMessage, editPacket(keyBlock, 5))
	deny := receiveBinary(t, guest, 4)
	if deny[5] != 255 {
		t.Fatal("locked edit did not revert base")
	}
	owner.WriteMessage(websocket.BinaryMessage, editPacket(keyBlock, 5))
	relay := receiveBinary(t, guest, 5)
	if binary.LittleEndian.Uint32(relay[7:]) != toWireKey(keyBlock) || relay[11] != 5 {
		t.Fatal("edit not relayed")
	}
	send(t, guest, map[string]any{"t": "chat", "m": "联机测试"})
	if receive(t, owner, "chat")["m"] != "联机测试" {
		t.Fatal("chat not relayed")
	}
	owner.WriteMessage(websocket.BinaryMessage, position(511, 96, 511))
	send(t, owner, map[string]any{"t": "inv", "hotbar": []int{2, 3, 1, 4, 5, 6, 10, 11, 7}, "slot": 4})
	send(t, owner, map[string]any{"t": "undoBuilders"})
	receive(t, owner, "undoBuilders")
	app.mu.Lock()
	if app.rooms[id].Edits[keyBlock] != 5 {
		t.Fatal("edit not stored")
	}
	if err := app.saveRoom(app.rooms[id]); err != nil {
		t.Fatal(err)
	}
	app.mu.Unlock()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm", AllowedOrigins: []string{"http://127.0.0.1:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r := restored.rooms[id]
	if r.Name != "重命名世界" || r.Lock != 2 || r.Edits[keyBlock] != 5 || r.Players[fixtureID(httpServer.URL, "OwnerPlayer123")].Slot != 4 {
		t.Fatal("restart lost world or player data")
	}
	oldURL := httpServer.URL
	nextHTTP := httptest.NewServer(restored.Handler())
	t.Cleanup(nextHTTP.Close)
	testAccounts.Range(func(k, v any) bool {
		key := k.(string)
		if strings.HasPrefix(key, oldURL+" ") {
			testAccounts.Store(nextHTTP.URL+strings.TrimPrefix(key, oldURL), v)
		}
		return true
	})
	app, httpServer = restored, nextHTTP
	owner, _ = connect(t, httpServer.URL, id, "OwnerPlayer123", key)
	fresh, _ := connect(t, httpServer.URL, id, "FreshPlayer123", "")
	_ = fresh
	// A key with high bits must not alias a valid world coordinate.
	owner.WriteMessage(websocket.BinaryMessage, append(binary.LittleEndian.AppendUint32([]byte{2}, toWireKey(keyBlock)|1<<31), 7))
	send(t, owner, map[string]any{"t": "undoBuilders"})
	receive(t, owner, "undoBuilders")
	app.mu.Lock()
	if len(app.rooms[id].Edits) != 1 {
		t.Fatal("invalid coordinate persisted")
	}
	app.mu.Unlock()
}
func TestSurvivalMiningCraftingAndHealth(t *testing.T) {
	app, httpServer := testApp(t, t.TempDir())
	id, key := postRoom(t, httpServer.URL, "survival")
	c, welcome := connect(t, httpServer.URL, id, "SurvivalPlayer123", key)
	sv := welcome["sv"].(map[string]any)
	if sv["gen"] != float64(1) {
		t.Fatal("wrong survival terrain")
	}
	app.mu.Lock()
	r := app.rooms[id]
	p := (*Peer)(nil)
	for peer := range r.Peers {
		p = peer
	}
	x, y, z := int(p.state.X)+2, int(p.state.Y)+1, int(p.state.Z)
	target := blockKey(x, y, z)
	r.Edits[target] = app.rules.byName["Oak Log"]
	app.mu.Unlock()
	msg := map[string]any{"t": "dig", "s": 0, "x": x, "y": y, "z": z, "f": 1}
	send(t, c, msg)
	msg["s"] = 2
	send(t, c, msg)
	receive(t, c, "sys") // Reject instant completion.
	app.mu.Lock()
	p.mining.At = time.Now().Add(-time.Minute)
	app.mu.Unlock()
	send(t, c, msg)
	receiveBinary(t, c, 7)
	receive(t, c, "sinv")
	app.mu.Lock()
	if len(r.Drops) != 1 {
		t.Fatal("harvest should create a ground drop")
	}
	var drop *ItemDrop
	for _, d := range r.Drops {
		drop = d
	}
	if stackCount(p, app.rules.byName["Oak Log"]) != 0 {
		t.Fatal("harvest entered inventory before pickup")
	}
	p.state.X, p.state.Y, p.state.Z = drop.X, drop.Y-.125, drop.Z
	drop.PickupDelayMS = 0
	app.pickupDrop(r, drop)
	app.mu.Unlock()
	receive(t, c, "sinv")
	app.mu.Lock()
	if r.Edits[target] != 0 {
		t.Fatal("mining failed")
	}
	logSlot := -1
	for i, v := range p.state.Inv.Slots[:36] {
		if v != nil && v.ID == app.rules.byName["Oak Log"] {
			logSlot = i
		}
	}
	app.mu.Unlock()
	if logSlot < 0 {
		t.Fatal("harvest missing")
	}
	click := func(slot, button, mode, q int) {
		send(t, c, map[string]any{"t": "click", "w": 0, "s": slot, "b": button, "m": mode, "q": q})
		for {
			if receive(t, c, "cursor")["q"] == float64(q) {
				break
			}
		}
	}
	click(logSlot, 0, 0, 1)
	click(41, 0, 0, 2)
	click(45, 0, 0, 3)
	click(logSlot, 0, 0, 4)
	app.mu.Lock()
	if p.state.Inv.Slots[41] != nil || p.state.Inv.Slots[logSlot].ID != app.rules.byName["Oak Planks"] || p.state.Inv.Slots[logSlot].Count != 4 {
		t.Fatal("log -> planks craft incorrect")
	}
	app.mu.Unlock()
	// Client-provided inventory cannot mint resources in survival.
	send(t, c, map[string]any{"t": "inv", "hotbar": []int{64, 64, 64, 64, 64, 64, 64, 64, 64}, "slot": 0})
	send(t, c, map[string]any{"t": "sel", "i": logSlot})
	receive(t, c, "sinv")
	app.mu.Lock()
	if p.state.Inv.Slots[logSlot].Count != 4 {
		t.Fatal("client overwrote survival inventory")
	}
	app.mu.Unlock()
	// A crafting table and furnace use persistent server inventory, not client stacks.
	app.mu.Lock()
	coal, rawIron := app.rules.byName["Coal"], app.rules.byName["Raw Iron"]
	furnace := &Container{Kind: "furnace", Slots: Stacks{stack(rawIron, 1, 0), stack(coal, 1, 0), nil}}
	for i := 0; i < 101; i++ {
		app.furnaceTick(r, furnace)
	}
	if furnace.Slots[0] != nil || furnace.Slots[2].ID != app.rules.byName["Iron Ingot"] {
		t.Fatal("smelting did not consume input and fuel")
	}
	r.ext().Rules["keepInventory"] = true
	app.damage(p, 20, "测试死亡")
	app.mu.Unlock()
	receive(t, c, "dead")
	send(t, c, map[string]any{"t": "respawn"})
	receive(t, c, "place")
	receive(t, c, "synced")
	app.mu.Lock()
	if p.state.Dead || p.state.Health != 20 || p.state.Inv.Slots[logSlot].Count != 4 {
		t.Fatal("respawn lost inventory or health")
	}
	r.Containers[blockKey(x, y, z+1)] = furnace
	if err := app.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	app.mu.Unlock()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := New(Config{DataDir: app.config.DataDir, EngineFile: "../../../public/engine.wasm"})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	saved := restored.rooms[id]
	if saved.Players[fixtureID(httpServer.URL, "SurvivalPlayer123")].Inv.Slots[logSlot].Count != 4 || saved.Edits[target] != 0 || saved.Containers[blockKey(x, y, z+1)].Slots[2].ID != app.rules.byName["Iron Ingot"] {
		t.Fatal("restart lost survival inventory, terrain or container")
	}
}
func TestRecipesAndContainerConservation(t *testing.T) {
	app, _ := testApp(t, t.TempDir())
	app.mu.Lock()
	defer app.mu.Unlock()
	r := newRoom("aaaaaaaaaaaaaaaa", "test", "survival", 0)
	p := &Peer{room: r, send: make(chan packet, 256), state: &Player{Inv: &Inventory{Slots: make(Stacks, 46)}}}
	r.Peers[p] = true
	plank := app.rules.byName["Oak Planks"]
	for i := 41; i < 45; i++ {
		p.state.Inv.Slots[i] = stack(plank, 1, 0)
	}
	app.refreshCraft(p)
	if p.state.Inv.Slots[45].ID != app.rules.byName["Crafting Table"] {
		t.Fatal("2x2 recipe failed")
	}
	app.click(p, Message{S: 45, M: 1})
	if p.state.Inv.Slots[41] != nil || p.state.Inv.Slots[0].ID != app.rules.byName["Crafting Table"] {
		t.Fatal("shift craft duplicated input")
	}
	p.state.Cursor = stack(plank, 3, 0)
	app.closeWindow(p)
	if p.state.Cursor != nil || p.state.Inv.Slots[1].Count != 3 {
		t.Fatal("close lost cursor")
	}
	chestKey := blockKey(10, 90, 10)
	app.openWindow(p, chestKey, "chest", 1)
	app.click(p, Message{W: p.window.ID, S: 1, M: 1})
	if p.state.Inv.Slots[1] != nil || r.Containers[chestKey].Slots[0].Count != 3 {
		t.Fatal("chest transfer failed")
	}
	app.closeWindow(p)
	if r.Containers[chestKey].Slots[0].Count != 3 {
		t.Fatal("container contents lost")
	}
}

func TestSaveFailureRetriesAndCorruptSaveIsProtected(t *testing.T) {
	app, httpServer := testApp(t, t.TempDir())
	app.mu.Lock()
	r := app.rooms[DefaultRoom]
	r.Name = "保存重试"
	r.Dirty = true
	file := app.roomFile(DefaultRoom)
	// A directory at the target file blocks atomic rename in this disposable store.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err := app.saveRoom(r); err == nil || !r.Dirty || r.SaveError == "" {
		t.Fatal("save failure was not retained")
	}
	app.mu.Unlock()
	res, err := http.Get(httpServer.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("failed persistence did not affect health")
	}
	app.mu.Lock()
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := app.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	if r.Dirty || r.SaveError != "" {
		t.Fatal("retry did not recover")
	}
	app.mu.Unlock()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "rooms"), 0700)
	corruptFile := filepath.Join(dir, "rooms", DefaultRoom+".json")
	original := []byte(`{broken save`)
	os.WriteFile(corruptFile, original, 0600)
	damaged, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm", AllowedOrigins: []string{"http://127.0.0.1:8080"}})
	if err == nil {
		damaged.Close()
		t.Fatal("corrupt save was silently replaced")
	}
	after, _ := os.ReadFile(corruptFile)
	if !bytes.Equal(original, after) {
		t.Fatal("corrupt save was overwritten")
	}
}
