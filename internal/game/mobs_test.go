package game

import (
	"encoding/binary"
	"encoding/json"
	"github.com/gorilla/websocket"
	api "mcraft/backend/pkg/plugin"
	"os"
	"testing"
	"time"
)

func mobTestWorld(s *Server) (*Room, *Peer) {
	r, p := dropTestWorld(s)
	for x := 7; x <= 40; x++ {
		for z := 7; z <= 20; z++ {
			r.Edits[blockKey(x, 59, z)] = 2
			for y := 60; y < 128; y++ {
				r.Edits[blockKey(x, y, z)] = 0
			}
		}
	}
	return r, p
}
func TestWorldTimeCreativeSurvivalAndOffline(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mode := range []string{"creative", "survival"} {
		r, p := dropTestWorld(s)
		r.Mode = mode
		r.Time = 23998
		s.tick = 1
		s.worldTick(r)
		if r.Time != 24000 {
			t.Fatal("active world must pass midnight")
		}
		delete(r.Peers, p)
		s.worldTick(r)
		if r.Time != 24000 {
			t.Fatal("empty world must pause")
		}
		if !s.setWorldTime(r, nil, 18000, "test") || r.Time != 18000 {
			t.Fatal("time command failed")
		}
	}
}
func TestMobSpawnConditionsTorchRadiusAndWalls(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := mobTestWorld(s)
	if !s.canNaturalSpawn(r, "cow", 12, 60, 10) || s.canNaturalSpawn(r, "zombie", 12, 60, 10) {
		t.Fatal("daytime spawn rules")
	}
	r.Time = 18000
	if s.canNaturalSpawn(r, "cow", 12, 60, 10) || !s.canNaturalSpawn(r, "zombie", 12, 60, 10) {
		t.Fatal("night spawn rules")
	}
	r.Edits[blockKey(10, 60, 10)] = 82
	for d := 0; d <= 14; d++ {
		if got := s.blockLight(r, 10+d, 60, 10); got != max(0, 14-d) {
			t.Fatalf("light at %d: %d", d, got)
		}
	}
	if s.canNaturalSpawn(r, "zombie", 12, 60, 10) || !s.canNaturalSpawn(r, "zombie", 25, 60, 10) {
		t.Fatal("torch must only protect local area")
	}
	for y := 45; y <= 80; y++ {
		for z := 0; z <= 30; z++ {
			r.Edits[blockKey(11, y, z)] = 1
		}
	}
	if s.blockLight(r, 12, 60, 10) != 0 {
		t.Fatal("torch must not shine through an opaque wall")
	}
	delete(r.Edits, blockKey(10, 60, 10))
	r.Edits[blockKey(10, 60, 10)] = 0
	if s.blockLight(r, 10, 60, 10) != 0 {
		t.Fatal("removed torch left light")
	}
}
func TestAllMobsCombatWallsLootAndVisibility(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	q := dropTestPlayer(r, 2, 13, 60, 10)
	r.Peers[q] = true
	for typ := range mobSpecs {
		m, err := s.spawnMob(r, p, typ, 12.5, 60, 10.5, "test")
		if err != nil || !p.knownMobs[m.ID] || !q.knownMobs[m.ID] || m.ID&mobNamespace == 0 {
			t.Fatalf("%s spawn: %v", typ, err)
		}
		var frame []byte
		for len(q.send) > 0 {
			pkt := <-q.send
			if len(pkt.body) > 0 && pkt.body[0] == 7 {
				frame = pkt.body
			}
		}
		if len(frame) != 23 || binary.LittleEndian.Uint32(frame[3:]) != m.ID || frame[7] != mobSpecs[typ].Kind {
			t.Fatal("spawn protocol")
		}
		p.attackAt = time.Time{}
		r.Edits[blockKey(11, 61, 10)] = 1
		s.attackMob(p, m.ID)
		if m.Health != mobSpecs[typ].Health {
			t.Fatal("attack crossed a wall")
		}
		r.Edits[blockKey(11, 61, 10)] = 0
		p.state.Inv.Slots[0] = stack(s.rules.byName["Diamond Sword"], 1, 0)
		s.attackMob(p, m.ID)
		if m.Health >= mobSpecs[typ].Health {
			t.Fatal("weapon did not damage mob")
		}
		remaining := m.Health
		s.attackMob(p, m.ID)
		if m.Health != remaining {
			t.Fatal("attack cooldown bypass")
		}
		if m.Health > 0 && !s.damageMob(r, p, m, m.Health, "test") {
			t.Fatalf("%s lethal damage was rejected", typ)
		}
		if r.Mobs[m.ID] != nil || p.knownMobs[m.ID] || q.knownMobs[m.ID] {
			t.Fatal("dead mob not removed on both clients")
		}
	}
	if len(r.Drops) < 8 || stackCount(p, s.rules.byName["Raw Beef"]) != 0 {
		t.Fatal("mob loot must stay on ground")
	}
	m, _ := s.spawnMob(r, p, "cow", 12.5, 60, 10.5, "test")
	p.state.X = 200
	p.state.Z = 200
	s.syncMobsFor(p)
	if p.knownMobs[m.ID] || !q.knownMobs[m.ID] {
		t.Fatal("entity interest visibility")
	}
}
func TestMobAIChaseAttackSunlightAndCreativeImmunity(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	r.Time = 18000
	m, _ := s.spawnMob(r, nil, "zombie", 12, 60, 10.5, "test")
	p.state.X = 13.4
	for i := 1; i <= 20; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
	}
	if p.state.Health >= 20 || m.Flags&4 == 0 {
		t.Fatal("night monster must chase and attack")
	}
	r.Mode = "creative"
	p.state.Health = 20
	for i := 21; i <= 40; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
	}
	if p.state.Health != 20 {
		t.Fatal("creative player must be immune to monsters")
	}
	r.Time = 6000
	old := m.Health
	for i := 41; i <= 70; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
	}
	if m.Health >= old {
		t.Fatal("zombie must burn in direct daylight")
	}
	r.Edits[blockKey(int(m.X), 65, int(m.Z))] = 1
	m.burnMS = 0
	// Keep the creature under the shelter rather than wandering out.
	m.walkMS = 500
	s.tick = 80
	s.mobsTick(r)
	if m.Flags&8 != 0 {
		t.Fatal("roof must protect zombie from sunlight")
	}
}

func TestMobFallsAfterFloorRemovalAndRespawnClearsVisibility(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	m, err := s.spawnMob(r, nil, "pig", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	for y := 56; y <= 59; y++ {
		r.Edits[blockKey(12, y, 10)] = 0
	}
	r.Edits[blockKey(12, 55, 10)] = 1
	m.walkMS = 500
	s.tick = 1
	s.mobsTick(r)
	if m.Y != 56 {
		t.Fatal("mob stayed suspended after its floor was removed")
	}
	p.state.Dead = true
	s.survivalMessage(p, Message{T: "respawn"})
	if p.knownMobs[m.ID] {
		t.Fatal("respawn retained visibility of a distant entity after the client reset")
	}
	p.state.X, p.state.Y, p.state.Z = 10.5, 60, 10.5
	p.known[0] = true
	s.syncMobsFor(p)
	if !p.knownMobs[m.ID] {
		t.Fatal("existing entity did not respawn on returning to its chunk")
	}
}
func TestMobPersistenceAndVersion4Backup(t *testing.T) {
	dir := t.TempDir()
	s, _ := testApp(t, dir)
	s.mu.Lock()
	r, p := mobTestWorld(s)
	m, err := s.spawnMob(r, p, "sheep", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	r.Time = 24567
	old, _ := json.Marshal(diskRoom{Version: 4, Name: r.Name, Mode: r.Mode, Time: r.Time, Edits: [][2]int{{int(blockKey(10, 59, 10)), 2}}})
	os.WriteFile(s.roomFile(r.ID), old, 0600)
	r.saveVersion = 4
	if err = s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	backup, err := os.ReadFile(s.roomFile(r.ID) + ".pre-mobs-v4.bak")
	if err != nil || string(backup) != string(old) {
		t.Fatal("old save must remain recoverable")
	}
	delete(r.Peers, p)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, _ := testApp(t, dir)
	restored.mu.Lock()
	defer restored.mu.Unlock()
	got := restored.rooms[r.ID]
	if got.Time != 24567 || got.Mobs[m.ID].Type != "sheep" || got.Mobs[m.ID].Health != 8 {
		t.Fatal("lost mob/time on restart")
	}
	raw, _ := os.ReadFile(s.roomFile(r.ID))
	var disk diskRoom
	json.Unmarshal(raw, &disk)
	disk.Mobs[0].Type = "invalid"
	bad, _ := json.Marshal(disk)
	os.WriteFile(s.roomFile(r.ID), bad, 0600)
	if app, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm"}); err == nil {
		app.Close()
		t.Fatal("corrupt mob save must be rejected")
	}
	unchanged, _ := os.ReadFile(s.roomFile(r.ID))
	if string(unchanged) != string(bad) {
		t.Fatal("corrupt save overwritten")
	}
}
func TestMobPluginAndOwnerCommands(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	s.rooms[r.ID] = r
	defer delete(s.rooms, r.ID)
	s.executeCommand(p, "/time set night")
	if r.Time != 6000 {
		t.Fatal("guest changed world time")
	}
	s.executeCommand(p, "/summon cow 12.5 60 10.5")
	if len(r.Mobs) != 0 {
		t.Fatal("guest summoned entity")
	}
	p.owner = true
	s.executeCommand(p, "/time set midnight")
	s.executeCommand(p, "/summon cow 12.5 60 10.5")
	if r.Time != 18000 || len(r.Mobs) != 1 {
		t.Fatal("owner commands")
	}
	s.applyAction(api.Action{Type: "spawn_mob", WorldID: r.ID, EntityType: "pig", X: 14, Y: 60, Z: 10})
	s.applyAction(api.Action{Type: "set_time", WorldID: r.ID, Ticks: 1000})
	if len(r.Mobs) != 2 || r.Time != 1000 {
		t.Fatal("plugin actions")
	}
	for len(p.send) > 0 {
		pkt := <-p.send
		if pkt.kind == websocket.BinaryMessage && pkt.body[0] == 7 && len(pkt.body) < 23 {
			t.Fatal("malformed entity frame")
		}
	}
}

func TestMobPluginCancellationAndModifiedDamage(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	if s.setWorldTime(r, p, 18000, "protected") || r.Time != 6000 {
		t.Fatal("time cancellation")
	}
	if _, err := s.spawnMob(r, p, "cow", 12.5, 60, 10.5, "protected"); err == nil || len(r.Mobs) != 0 {
		t.Fatal("spawn cancellation")
	}
	m, _ := s.spawnMob(r, p, "cow", 12.5, 60, 10.5, "test")
	if s.damageMob(r, p, m, 3, "immune") || m.Health != 10 {
		t.Fatal("damage cancellation")
	}
	s.damageMob(r, p, m, 8, "modify")
	if m.Health != 8 {
		t.Fatal("modified damage")
	}
	if s.damageMob(r, p, m, 100, "protected") || m.Health != 8 || len(r.Drops) != 0 {
		t.Fatal("death cancellation must not create loot")
	}
	if s.removeMob(r, m, "protected") || r.Mobs[m.ID] == nil {
		t.Fatal("despawn cancellation")
	}
}

func TestActualClientAttackMessageKillsMob(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	m, err := s.spawnMob(r, nil, "chicken", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	p.state.Inv.Slots[0] = stack(276, 1, 0)
	packet, _ := json.Marshal(map[string]any{"t": "attack", "e": m.ID, "s": "strong", "sp": 1})
	msg, err := parseMessage(packet)
	if err != nil {
		t.Fatalf("actual browser attack packet rejected: %v", err)
	}
	s.message(p, msg)
	if r.Mobs[m.ID] != nil || len(r.Drops) != 2 || p.state.Inv.Slots[0].Damage != 1 {
		t.Fatal("attack must kill, drop loot and wear the sword on server")
	}
	if _, err := parseMessage([]byte(`{"t":"dig","s":"strong"}`)); err == nil {
		t.Fatal("non-attack numeric stages must still be validated")
	}
}

func TestMobRealtimeMultiplayerJoinAndClock(t *testing.T) {
	s, server := testApp(t, t.TempDir())
	id, key := postRoom(t, server.URL, "creative")
	owner, w1 := connect(t, server.URL, id, "MobClockOwner123", key)
	defer owner.Close()
	guest, w2 := connect(t, server.URL, id, "MobClockGuest123", "")
	defer guest.Close()
	if w2["time"].(float64) < w1["time"].(float64) {
		t.Fatal("join times inconsistent")
	}
	s.mu.Lock()
	r := s.rooms[id]
	for x := 510; x <= 515; x++ {
		for z := 508; z <= 515; z++ {
			r.Edits[blockKey(x, 80, z)] = 1
			for y := 81; y <= 87; y++ {
				r.Edits[blockKey(x, y, z)] = 0
			}
		}
	}
	for p := range r.Peers {
		p.state.X, p.state.Y, p.state.Z = 512.5, 81, 514.5
	}
	m, err := s.spawnMob(r, nil, "pig", 512.5, 81, 511.5, "test")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*websocket.Conn{owner, guest} {
		frame := receiveBinary(t, c, 7)
		if binary.LittleEndian.Uint32(frame[3:]) != m.ID || frame[7] != 10 {
			t.Fatal("two clients saw different entity")
		}
		movement := receiveBinary(t, c, 8)
		if binary.LittleEndian.Uint32(movement[3:]) != m.ID {
			t.Fatal("missing authoritative movement")
		}
	}
	time1 := receive(t, owner, "time")
	time2 := receive(t, guest, "time")
	if time1["time"] != time2["time"] || time1["time"].(float64) <= w1["time"].(float64) {
		t.Fatal("clock must advance and match across clients")
	}
	late, _ := connect(t, server.URL, id, "MobClockLate123", "")
	defer late.Close()
	frame := receiveBinary(t, late, 7)
	if binary.LittleEndian.Uint32(frame[3:]) != m.ID {
		t.Fatal("late join lost existing entity")
	}
	s.mu.Lock()
	mx, my, mz := m.X, m.Y, m.Z
	s.mu.Unlock()
	if err := owner.WriteMessage(websocket.BinaryMessage, position(mx, my, mz+2)); err != nil {
		t.Fatal(err)
	}
	send(t, owner, map[string]any{"t": "attack", "e": m.ID, "s": "strong", "sp": 1})
	for _, c := range []*websocket.Conn{owner, guest, late} {
		gone := receiveBinary(t, c, 9)
		if binary.LittleEndian.Uint32(gone[3:]) != m.ID {
			t.Fatal("browser attack kill not synchronized to all clients")
		}
	}
}
