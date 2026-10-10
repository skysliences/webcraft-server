package game

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

var testEggTypes = []string{"zombie", "skeleton", "creeper", "spider", "enderman", "cow", "pig", "sheep", "chicken", "wolf", "villager"}

func eggTestPlayer(s *Server) (*Room, *Peer) {
	r, p := dropTestWorld(s)
	p.sid = "5ab5ef00c7708e582a40d01669f0af7c"
	p.name = "生成蛋测试"
	p.state.Inv.Slots[0] = customStack(420, 2)
	return r, p
}

func TestSpawnEggAllTypesConsumptionAndReplication(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, typ := range testEggTypes {
		for _, mode := range []string{"survival", "creative"} {
			r, p := eggTestPlayer(s)
			p.ext().GameMode = mode
			p.state.Inv.Slots[0] = customStack(420+i, 2)
			before := copyStack(p.state.Inv.Slots[0])
			if item := s.rules.byID[420+i]; item.SpawnEgg != typ || s.rules.max(item.ID) != 64 {
				t.Fatalf("egg %d mapping or stacking incorrect: %+v", 420+i, item)
			}
			other := dropTestPlayer(r, 2, 11.5, 60, 10.5)
			r.Peers[other] = true
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
			wantCount := 1
			if mode == "creative" {
				wantCount = 2
			}
			if len(r.Mobs) != 1 || p.state.Inv.Slots[0].Count != wantCount || !same(p.state.Inv.Slots[0], before) {
				t.Fatalf("%s %s spawn/consumption/metadata incorrect", typ, mode)
			}
			for _, mob := range r.Mobs {
				if mob.Type != typ || !p.knownMobs[mob.ID] || !other.knownMobs[mob.ID] {
					t.Fatalf("%s not replicated to both players", typ)
				}
				seen := false
				for len(other.send) > 0 {
					packet := <-other.send
					if len(packet.body) == 23 && packet.body[0] == 7 && binary.LittleEndian.Uint32(packet.body[3:]) == mob.ID && packet.body[7] == mobSpecs[typ].Kind {
						seen = true
					}
				}
				if !seen {
					t.Fatal("observer missing correctly typed entity packet")
				}
			}
		}
	}
	// The last survival item disappears; creative keeps the single item.
	_, p := eggTestPlayer(s)
	p.state.Inv.Slots[0].Count = 1
	if !s.useSpawnEgg(p, Message{X: 12, Y: 59, Z: 10, F: 1}, nil) || p.state.Inv.Slots[0] != nil {
		t.Fatal("last egg was not consumed")
	}
}

func TestSpawnEggRejectsInvalidPermissionPositionCapacityAndStaleAction(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, reason := range []string{"locked", "adventure", "spectator", "dead", "far", "face", "solid", "no-floor", "capacity", "missing", "invalid-meta", "stale", "slot"} {
		r, p := eggTestPlayer(s)
		m := Message{X: 12, Y: 59, Z: 10, F: 1}
		expected := itemSnapshot(p.state.Inv.Slots[0])
		s.rooms[r.ID] = r
		switch reason {
		case "locked":
			r.Lock = 1
		case "adventure", "spectator":
			p.ext().GameMode = reason
		case "dead":
			p.state.Dead = true
		case "far":
			m.X = 40
		case "face":
			m.F = 6
		case "solid":
			r.Edits[blockKey(12, 60, 10)] = 1
		case "no-floor":
			r.Edits[blockKey(12, 59, 10)] = 0
		case "capacity":
			for i := uint32(1); i <= maxWorldMobs; i++ {
				r.Mobs[mobNamespace|i] = &Mob{ID: mobNamespace | i}
			}
		case "missing":
			p.state.Inv.Slots[0] = nil
		case "invalid-meta":
			p.state.Inv.Slots[0].Meta.CustomData["bad-key"] = true
		case "stale":
			p.state.Inv.Slots[0].Meta.Lore[0] = "moved or changed"
		case "slot":
			p.state.Inv.Selected = 1
		}
		before := copyStack(p.state.Inv.Slots[0])
		mobCount := len(r.Mobs)
		s.applyAction(api.Action{Type: "use_spawn_egg", WorldID: r.ID, PlayerID: p.id, Slot: 0, Expected: expected, X: float64(m.X), Y: float64(m.Y), Z: float64(m.Z), Face: m.F})
		if !reflect.DeepEqual(p.state.Inv.Slots[0], before) || len(r.Mobs) != mobCount {
			t.Fatalf("rejected %s action committed", reason)
		}
	}
}

func TestSpawnEggPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "spawn-eggs-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.InventoryChanging} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") != "spawn_egg" {
				return nil
			}
			if stack := e.Item("stack"); stack != nil && stack.Meta != nil && stack.Meta.CustomData["test:cancel"] == e.Name {
				e.Cancel("spawn_eggs.protected")
			}
			e.Data["type"], e.Data["x"], e.Data["count"] = "ender_dragon", 99999, 64
			e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
	}
	for _, name := range []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.InventoryChanging, api.EntitySpawned, api.InventoryChanged, api.ItemSpawnEggUsed} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			if e.String("reason") != "spawn_egg" {
				return nil
			}
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			f.Close()
			e.Cancel("monitor ignored")
			e.Data["type"] = "ender_dragon"
			c.GiveItem(e, "Stone", 64)
			return err
		})
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func spawnEggPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "egg-test")
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"egg-test","name":"egg-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestSpawnEggPluginProcess$","spawn-eggs-fixture"]}`
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func readSpawnEggEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "egg-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, &e)
	}
	return events
}

func TestSpawnEggProtectionMonitorMetadataAndSDK(t *testing.T) {
	s := spawnEggPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.InventoryChanging} {
		r, p := eggTestPlayer(s)
		p.state.Inv.Slots[0].Meta.CustomData["test:cancel"] = name
		start := len(readSpawnEggEvents(t, s))
		if s.useSpawnEgg(p, Message{X: 12, Y: 59, Z: 10, F: 1}, nil) || p.state.Inv.Slots[0].Count != 2 || len(r.Mobs) != 0 || r.NextMobID != 1 {
			t.Fatalf("%s cancellation consumed an egg or spawned", name)
		}
		for _, e := range readSpawnEggEvents(t, s)[start:] {
			if e.Name == api.ItemSpawnEggUsed || e.Name == api.EntitySpawned || e.Name == api.InventoryChanged {
				t.Fatalf("cancelled %s operation emitted success %s", name, e.Name)
			}
		}
	}
	r, p := eggTestPlayer(s)
	s.rooms[r.ID] = r
	start := len(readSpawnEggEvents(t, s))
	ctx := &api.Context{}
	ctx.UseSpawnEgg(s.event(api.CommandExecuting, r, p, nil), 0, 12, 59, 10, 1)
	if len(ctx.Actions) != 1 {
		t.Fatal("SDK did not enqueue a snapshot action")
	}
	s.pluginSource = "plugin:egg-test"
	s.applyAction(ctx.Actions[0])
	s.drainActions()
	if len(r.Mobs) != 1 || p.state.Inv.Slots[0].Count != 1 || stackCount(p, s.rules.byName["Stone"]) != 0 {
		t.Fatal("SDK or Monitor committed incorrect state")
	}
	want := []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.InventoryChanging, api.EntitySpawned, api.InventoryChanged, api.ItemSpawnEggUsed}
	events := readSpawnEggEvents(t, s)[start:]
	if len(events) != len(want) {
		t.Fatalf("unexpected event sequence: %v", events)
	}
	for i, e := range events {
		if e.Name != want[i] || e.String("actor") != p.sid || e.String("type") != "zombie" || e.Number("x") != 12.5 || e.Data["target"] == nil || e.Player == nil || e.Player.AccountID != p.sid || e.Source != "plugin:egg-test" || e.Item("stack").Meta.Lore[0] != "第一行" || e.Item("before").Count != 2 || e.Item("after").Count != 1 {
			t.Fatalf("event %d lacks readonly metadata or attribution: %+v", i, e)
		}
		if i >= 3 && (e.Player.Item(0).Count != 1 || e.Number("entityId") == 0) {
			t.Fatal("success emitted before both commits")
		}
	}
	// Reusing the old action cannot consume the changed remainder.
	s.applyAction(ctx.Actions[0])
	if p.state.Inv.Slots[0].Count != 1 || len(r.Mobs) != 1 {
		t.Fatal("stale SDK snapshot consumed remaining egg")
	}
	for _, spec := range api.Events() {
		if spec.Name == api.ItemSpawnEggUsing && (!spec.Cancellable || len(spec.Mutable) != 0) || spec.Name == api.ItemSpawnEggUsed && spec.Cancellable {
			t.Fatal("invalid spawn egg event specification", spec)
		}
	}
}

func TestSpawnEggRemainderAndEntityPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := eggTestPlayer(s)
	r.Players[p.sid] = p.state
	defer delete(r.Peers, p)
	if !s.useSpawnEgg(p, Message{X: 12, Y: 59, Z: 10, F: 1}, nil) {
		t.Fatal("spawn failed")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	got := loader.rooms[r.ID]
	if len(got.Mobs) != 1 || !reflect.DeepEqual(got.Players[p.sid].Inv.Slots[0], p.state.Inv.Slots[0]) {
		t.Fatal("restart lost spawned entity or full remaining item metadata")
	}
}
