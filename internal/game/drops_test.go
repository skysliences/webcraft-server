package game

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dropTestWorld(s *Server) (*Room, *Peer) {
	r := newRoom("0123456789abcdef", "掉落测试", "survival", 0)
	for x := 7; x <= 16; x++ {
		for z := 7; z <= 16; z++ {
			for y := 59; y <= 66; y++ {
				r.Edits[blockKey(x, y, z)] = 0
			}
			r.Edits[blockKey(x, 59, z)] = s.rules.byName["Stone"]
		}
	}
	p := dropTestPlayer(r, 1, 10.5, 60, 10.5)
	r.Peers[p] = true
	return r, p
}
func dropTestPlayer(r *Room, id uint16, x, y, z float64) *Peer {
	return &Peer{id: id, room: r, send: make(chan packet, 4096), rd: 2, known: map[int]bool{0: true}, knownDrops: map[uint32]bool{}, state: &Player{X: x, Y: y, Z: z, Health: 20, Inv: &Inventory{Slots: make(Stacks, 46)}}}
}
func stackCount(p *Peer, id int) int {
	count := 0
	for _, v := range p.state.Inv.Slots[:36] {
		if v != nil && v.ID == id {
			count += v.Count
		}
	}
	return count
}
func testDrop(t *testing.T, s *Server, r *Room, item, count int, x, y, z float64) *ItemDrop {
	t.Helper()
	drops, err := s.prepareDrops(r, nil, Stacks{stack(item, count, 0)}, x, y, z, "test")
	if err != nil || len(drops) != 1 {
		t.Fatalf("prepare: %+v %v", drops, err)
	}
	s.publishDrops(r, nil, drops, "test")
	return drops[0]
}
func TestMiningDropsInsteadOfInventoryAndFalls(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	log := s.rules.byName["Oak Log"]
	for i := 0; i < 36; i++ {
		p.state.Inv.Slots[i] = stack(s.rules.byName["Oak Planks"], 64, 0)
	}
	key := blockKey(12, 61, 10)
	r.Edits[key] = log
	s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1})
	p.mining.At = time.Now().Add(-time.Minute)
	s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 61, Z: 10, F: 1})
	if r.Edits[key] != 0 || len(r.Drops) != 1 || stackCount(p, log) != 0 {
		t.Fatal("mining should succeed with a full inventory and create a ground item")
	}
	var d *ItemDrop
	for _, drop := range r.Drops {
		d = drop
	}
	if d.Stack.ID != log || d.Stack.Count != 1 || !p.knownDrops[d.ID] {
		t.Fatal("drop not replicated")
	}
	spawned := false
	for len(p.send) > 0 {
		pkt := <-p.send
		if len(pkt.body) > 0 && pkt.body[0] == 7 {
			spawned = true
			if len(pkt.body) != 23 || binary.LittleEndian.Uint16(pkt.body[1:]) != 1 || binary.LittleEndian.Uint32(pkt.body[3:]) != d.ID || pkt.body[7] != 1 || int(binary.LittleEndian.Uint16(pkt.body[20:])) != log || pkt.body[22] != 1 {
				t.Fatal("invalid item entity spawn frame")
			}
		}
	}
	if !spawned {
		t.Fatal("no entity spawn frame")
	}
	for i := 0; i < 30; i++ {
		s.dropsTick(r)
	}
	if d.Y < 59.998 || d.Y > 60.01 || len(r.Drops) != 1 {
		t.Fatalf("drop did not land on the solid floor: %f", d.Y)
	}
	p.state.X, p.state.Y, p.state.Z = d.X, 60, d.Z
	s.dropsTick(r)
	if len(r.Drops) != 1 {
		t.Fatal("full inventory ate drop")
	}
	p.state.Inv.Slots[0] = nil
	d.retryAt = 0
	s.dropsTick(r)
	if len(r.Drops) != 0 || stackCount(p, log) != 1 {
		t.Fatal("nearby item not picked up after freeing capacity")
	}
}
func TestDropPartialPickupAndSingleWinner(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	log, plank := s.rules.byName["Oak Log"], s.rules.byName["Oak Planks"]
	p.state.X = 12.5
	for i := 0; i < 36; i++ {
		p.state.Inv.Slots[i] = stack(plank, 64, 0)
	}
	p.state.Inv.Slots[0] = stack(log, 63, 0)
	d := testDrop(t, s, r, log, 3, 12.5, 60, 10.5)
	d.PickupDelayMS = 0
	s.pickupDrop(r, d)
	if stackCount(p, log) != 64 || r.Drops[d.ID].Stack.Count != 2 {
		t.Fatal("partial pickup must leave remainder on the ground")
	}
	// The client animates the collected quantity and keeps the remainder visible.
	var notice []byte
	for len(p.send) > 0 {
		pkt := <-p.send
		if len(pkt.body) > 0 && pkt.body[0] == 10 {
			notice = pkt.body
		}
	}
	if len(notice) != 30 || notice[7] != 6 || binary.LittleEndian.Uint16(notice[8:]) != 1 || notice[17] != 5 || notice[27] != 6 || binary.LittleEndian.Uint16(notice[28:]) != 2 {
		t.Fatal("partial pickup animation must not collect the remaining stack")
	}
	second := dropTestPlayer(r, 2, 12.8, 60, 10.5)
	r.Peers[second] = true
	s.syncDrops(r)
	d.retryAt = 0
	s.pickupDrop(r, d)
	if len(r.Drops) != 0 || stackCount(second, log) != 2 {
		t.Fatal("remainder not available to another player")
	}
	for i := range p.state.Inv.Slots {
		p.state.Inv.Slots[i] = nil
	}
	p.state.X = 12.5
	second.state.X = 12.6
	d = testDrop(t, s, r, log, 5, 12.5, 60, 10.5)
	d.PickupDelayMS = 0
	s.pickupDrop(r, d)
	if len(r.Drops) != 0 || stackCount(p, log) != 5 || stackCount(second, log) != 2 {
		t.Fatal("two players received the same drop")
	}
	s.dropsTick(r)
	if stackCount(p, log) != 5 || stackCount(second, log) != 2 {
		t.Fatal("removed entity picked up twice")
	}
}

func TestDropEscapesPlacedBlock(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	p.state.X = 15
	d := testDrop(t, s, r, s.rules.byName["Oak Log"], 1, 10.5, 60, 10.5)
	r.Edits[blockKey(10, 60, 10)] = s.rules.byName["Stone"]
	s.dropsTick(r)
	if d.Y < 60.998 || s.dropBlocked(r, d.X, d.Y, d.Z) || len(r.Drops) != 1 {
		t.Fatal("placing a block trapped or destroyed the item")
	}
}

func TestDropMultiplayerWebSocketReplication(t *testing.T) {
	s, httpServer := testApp(t, t.TempDir())
	s.mu.Lock()
	r, fake := dropTestWorld(s)
	delete(r.Peers, fake)
	ownerSID, guestSID := "DropOwnerPlayer123", "DropGuestPlayer123"
	for _, sid := range []string{ownerSID, guestSID} {
		r.Players[sid] = &Player{X: 10.5, Y: 60, Z: 10.5, Health: 20, Food: 20, Inv: &Inventory{Slots: make(Stacks, 46)}}
		s.accounts.legacy[sid] = true
	}
	log := s.rules.byName["Oak Log"]
	r.Edits[blockKey(12, 61, 10)] = log
	s.rooms[r.ID] = r
	s.mu.Unlock()
	a, _ := connect(t, httpServer.URL, r.ID, ownerSID, "")
	b, _ := connect(t, httpServer.URL, r.ID, guestSID, "")
	send(t, a, map[string]any{"t": "dig", "s": 0, "x": 12, "y": 61, "z": 10, "f": 1})
	if err := a.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	a.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := a.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == "pong" {
			break
		}
	}
	s.mu.Lock()
	var miner, picker *Peer
	for peer := range r.Peers {
		if peer.sid == ownerSID {
			miner = peer
		} else if peer.sid == guestSID {
			picker = peer
		}
	}
	if miner == nil || picker == nil || miner.mining == nil {
		s.mu.Unlock()
		t.Fatal("mining request was not accepted")
	}
	miner.mining.At = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	send(t, a, map[string]any{"t": "dig", "s": 2, "x": 12, "y": 61, "z": 10, "f": 1})
	spawnA, spawnB := receiveBinary(t, a, 7), receiveBinary(t, b, 7)
	if string(spawnA) != string(spawnB) || len(spawnA) != 23 {
		t.Fatal("nearby players did not receive the same item entity")
	}
	id := binary.LittleEndian.Uint32(spawnA[3:])
	receive(t, a, "sinv")
	s.mu.Lock()
	d := r.Drops[id]
	if d == nil || stackCount(miner, log) != 0 || stackCount(picker, log) != 0 {
		s.mu.Unlock()
		t.Fatal("mining credited a player before pickup")
	}
	picker.state.X, picker.state.Y, picker.state.Z = d.X, d.Y, d.Z
	d.PickupDelayMS = 0
	s.pickupDrop(r, d)
	if len(r.Drops) != 0 || stackCount(picker, log) != 1 || stackCount(miner, log) != 0 {
		s.mu.Unlock()
		t.Fatal("drop was not awarded exactly once to the nearby player")
	}
	s.mu.Unlock()
	receive(t, b, "sinv")
	for _, conn := range []*websocket.Conn{a, b} {
		notice := receiveBinary(t, conn, 10)
		gone := receiveBinary(t, conn, 9)
		if binary.LittleEndian.Uint32(notice[3:]) != id || notice[7] != 5 || binary.LittleEndian.Uint16(notice[8:]) != picker.id || binary.LittleEndian.Uint32(gone[3:]) != id {
			t.Fatal("pickup and entity removal were not replicated")
		}
	}
}

func TestPreDropSaveMigrationBackup(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			dir := t.TempDir()
			os.MkdirAll(filepath.Join(dir, "rooms"), 0700)
			old, _ := json.Marshal(diskRoom{Version: version, Name: "原来的世界", Mode: "creative", Edits: [][2]int{{133131, 5}}})
			file := filepath.Join(dir, "rooms", DefaultRoom+".json")
			os.WriteFile(file, old, 0600)
			s, _ := testApp(t, dir)
			s.mu.Lock()
			defer s.mu.Unlock()
			r := s.rooms[DefaultRoom]
			r.Dirty = true
			if err := s.saveRoom(r); err != nil {
				t.Fatal(err)
			}
			backup := file + ".pre-drops-v" + string(rune('0'+version)) + ".bak"
			got, err := os.ReadFile(backup)
			if err != nil || string(got) != string(old) || r.saveVersion < 6 || r.Edits[fromWireKey(133131)] != 5 {
				t.Fatal("migration must preserve an exact pre-upgrade save")
			}
			r.Name, r.Dirty = "修改后的世界", true
			if err := s.saveRoom(r); err != nil {
				t.Fatal(err)
			}
			got, _ = os.ReadFile(backup)
			if string(got) != string(old) {
				t.Fatal("later saves overwrote the original backup")
			}
		})
	}
}
func TestDropDelayDeadDistanceVisibilityAndExpiry(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	log := s.rules.byName["Oak Log"]
	d := testDrop(t, s, r, log, 1, 10.5, 60, 10.5)
	s.pickupDrop(r, d)
	if len(r.Drops) != 1 {
		t.Fatal("pickup delay ignored")
	}
	d.PickupDelayMS = 0
	p.state.Dead = true
	s.pickupDrop(r, d)
	if len(r.Drops) != 1 {
		t.Fatal("dead player picked up item")
	}
	p.state.Dead = false
	p.state.X = 15
	s.pickupDrop(r, d)
	if len(r.Drops) != 1 {
		t.Fatal("distant player picked up item")
	}
	delete(p.known, 0)
	s.syncDropsFor(p)
	if p.knownDrops[d.ID] {
		t.Fatal("entity remained visible outside loaded chunks")
	}
	p.known[0] = true
	s.syncDropsFor(p)
	if !p.knownDrops[d.ID] {
		t.Fatal("entering view did not receive existing drop")
	}
	d.AgeMS = dropLifetimeMS - 100
	s.dropsTick(r)
	if len(r.Drops) != 0 || p.knownDrops[d.ID] {
		t.Fatal("expiry not synchronized")
	}
	d = testDrop(t, s, r, log, 1, 10.5, 60, 10.5)
	d.PickupDelayMS = 0
	d.X = 12.25
	p.state.X = 10.9
	r.Edits[blockKey(11, 60, 10)] = s.rules.byName["Stone"]
	if s.pickupReachable(p, d) {
		t.Fatal("pickup should not go through a wall")
	}
}
func TestDropPersistRestoreAndCorruptProtection(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	r, p := dropTestWorld(s)
	p.state.X = 15
	d := testDrop(t, s, r, s.rules.byName["Oak Log"], 3, 10.5, 60, 10.5)
	d.AgeMS = 1234
	d.PickupDelayMS = 200
	nextID := r.NextDropID
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	delete(r.Peers,p)
 if err:=s.Close();err!=nil {t.Fatal(err)}
 restored, err := New(Config{DataDir: s.config.DataDir, EngineFile: "../../../public/engine.wasm"})
	if err != nil {
		t.Fatal(err)
	}
	saved := restored.rooms[r.ID]
	if len(saved.Drops) != 1 || saved.Drops[d.ID].AgeMS != 1234 || saved.Drops[d.ID].PickupDelayMS != 200 || saved.NextDropID != nextID {
		t.Fatal("drop state did not survive restart")
	}
	restored.Close()
	raw, _ := os.ReadFile(s.roomFile(r.ID))
	var data map[string]any
	json.Unmarshal(raw, &data)
	if data["version"].(float64) < 6 {
		t.Fatal("new drop save must be versioned")
	}
	data["drops"].([]any)[0].(map[string]any)["stack"] = stack(d.Stack.ID, -1, 0)
	bad, _ := json.Marshal(data)
	os.WriteFile(s.roomFile(r.ID), bad, 0600)
	if _, err = New(Config{DataDir: s.config.DataDir, EngineFile: "../../../public/engine.wasm"}); err == nil {
		t.Fatal("invalid drop was silently discarded")
	}
	after, _ := os.ReadFile(s.roomFile(r.ID))
	if string(after) != string(bad) {
		t.Fatal("invalid save was overwritten")
	}
}
func TestDropPluginSpawnPickupAndDespawn(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	log := s.rules.byName["Oak Log"]
	prepared, err := s.prepareDrops(r, p, Stacks{stack(log, 1, 0)}, 10.5, 60, 10.5, "cancel_spawn")
	if err != nil || len(prepared) != 0 {
		t.Fatal("cancelled item spawn still produced an entity")
	}
	prepared, err = s.prepareDrops(r, p, Stacks{stack(log, 1, 0)}, 10.5, 60, 10.5, "modify_drop")
	if err != nil || len(prepared) != 1 || prepared[0].Stack.Count != 2 {
		t.Fatal("plugin count modification ignored")
	}
	s.publishDrops(r, p, prepared, "test")
	d := prepared[0]
	d.PickupDelayMS = 0
	s.pickupDrop(r, d)
	if len(r.Drops) != 1 || stackCount(p, log) != 0 {
		t.Fatal("cancelled pickup mutated inventory/entity")
	}
	d.AgeMS = dropLifetimeMS - 100
	s.dropsTick(r)
	if len(r.Drops) != 1 || d.AgeMS >= dropLifetimeMS {
		t.Fatal("cancelled despawn removed item or made save invalid")
	}
}
