package game

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var redstoneTestItems = []int{320, 355, 356, 357, 358, 359, 360, 361}
var redstoneTestBlocks = []int{108, 109, 166, 167, 168, 169, 174, 175}

func redstoneTestPeer(s *Server, item int) (*Room, *Peer) {
	r, p := dropTestWorld(s)
	p.sid = "5ab5ef00c7708e582a40d01669f0af7c"
	p.state.Inv.Slots[0] = customStack(item, 2)
	p.state.Yaw = -3
	return r, p
}

func TestRedstoneComponentPlacementModesMetadataAndReplication(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range append(append([]int{}, redstoneTestItems...), redstoneTestBlocks...) {
		i := index % len(redstoneTestBlocks)
		for _, mode := range []string{"survival", "creative"} {
			r, p := redstoneTestPeer(s, item)
			p.ext().GameMode = mode
			observer := dropTestPlayer(r, 2, 11.5, 60, 10.5)
			observer.epoch = 42
			r.Peers[observer] = true
			before := copyStack(p.state.Inv.Slots[0])
			s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
			want := 1
			if mode == "creative" {
				want = 2
			}
			k := blockKey(12, 60, 10)
			if s.block(r, 12, 60, 10) != redstoneTestBlocks[i] || p.state.Inv.Slots[0].Count != want || !same(before, p.state.Inv.Slots[0]) || r.Ext == nil || r.Ext.Blocks[k] == nil || r.Ext.Blocks[k].Redstone == nil {
				t.Fatalf("%s item%d block/count/metadata/state incorrect", mode, item)
			}
			state := redstoneSnapshot(r, k)
			if state.Facing < 0 || state.Facing > 5 {
				t.Fatal("negative yaw gave invalid facing")
			}
			if i == 1 && s.redstoneWirePacket(r, k)["power"] != s.directPowerSource(r, 12, 60, 10, 0, 1, 0) {
				t.Fatal("redstone torch display differs from authoritative output")
			}
			seen := false
			for len(observer.send) > 0 {
				packet := <-observer.send
				var msg struct {
					T      string                      `json:"t"`
					Epoch  uint32                      `json:"epoch"`
					Blocks []struct{ X, Y, Z, ID int } `json:"blocks"`
				}
				if json.Unmarshal(packet.body, &msg) == nil && msg.T == "rs" && len(msg.Blocks) == 1 && msg.Blocks[0].ID == redstoneTestBlocks[i] {
					if msg.Epoch != 42 || msg.Blocks[0].X != 12 || msg.Blocks[0].Y != 60 || msg.Blocks[0].Z != 10 {
						t.Fatal("rs packet lacks coordinates/epoch")
					}
					seen = true
				}
			}
			if !seen {
				t.Fatalf("item%d did not replicate component state", item)
			}
		}
	}
	// Plant replacement uses its floor even when clicked from a side.
	r, p := redstoneTestPeer(s, 355)
	r.Edits[blockKey(12, 60, 10)] = 64
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 2})
	if s.block(r, 12, 60, 10) != 109 || redstoneSnapshot(r, blockKey(12, 60, 10)).SupportFace != 1 {
		t.Fatal("redstone torch cannot replace a plant")
	}
}

func TestRedstoneComponentInvalidRequestsAndStaleSDK(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, why := range []string{"lock", "dead", "adventure", "spectator", "far", "face", "occupied", "unsupported", "ceiling", "stale"} {
		r, p := redstoneTestPeer(s, 356)
		s.rooms[r.ID] = r
		m := Message{X: 12, Y: 59, Z: 10, F: 1}
		expected := itemSnapshot(s.heldItem(p))
		switch why {
		case "lock":
			r.Lock = 2
		case "dead":
			p.state.Dead = true
		case "adventure", "spectator":
			p.ext().GameMode = why
		case "far":
			m.X = 100
		case "face":
			m.F = 6
		case "occupied":
			r.Edits[blockKey(12, 60, 10)] = 1
		case "unsupported":
			r.Edits[blockKey(12, 59, 10)] = 0
		case "ceiling":
			m.F = 0
		case "stale":
			p.state.Inv.Slots[0].Meta.Lore[0] = "changed"
		}
		before := copyStack(p.state.Inv.Slots[0])
		old := s.block(r, 12, 60, 10)
		s.applyAction(api.Action{Type: "use_redstone", WorldID: r.ID, PlayerID: p.id, Slot: 0, Expected: expected, X: float64(m.X), Y: float64(m.Y), Z: float64(m.Z), Face: m.F})
		if !reflect.DeepEqual(before, p.state.Inv.Slots[0]) || s.block(r, 12, 60, 10) != old || redstoneSnapshot(r, blockKey(12, 60, 10)) != nil {
			t.Fatalf("invalid %s request committed", why)
		}
	}
}

func TestRedstoneComponentInteractionAndRestore(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, 356)
	r.Players[p.sid] = p.state
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
	k := blockKey(12, 60, 10)
	initial := cloneRedstone(redstoneSnapshot(r, k))
	count := s.heldItem(p).Count
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
	if redstoneSnapshot(r, k).Delay != 2 || s.heldItem(p).Count != count {
		t.Fatal("repeater interaction consumed an item or failed")
	}
	p.ext().GameMode = "adventure"
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
	if redstoneSnapshot(r, k).Delay != 2 {
		t.Fatal("adventure player changed repeater")
	}
	p.ext().GameMode = "survival"
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	state := redstoneSnapshot(loader.rooms[r.ID], k)
	if state.Delay != 2 || state.Facing != initial.Facing || !same(loader.rooms[r.ID].Players[p.sid].Inv.Slots[0], p.state.Inv.Slots[0]) {
		t.Fatal("restart lost component or item metadata")
	}
	// Legacy components with only a saved block receive default runtime state.
	r.Edits[blockKey(14, 60, 10)] = 108
	r.Edits[blockKey(14, 60, 11)] = 166
	r.Dirty = true
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.roomFile(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var disk diskRoom
	if err = json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Ext != nil {
		delete(disk.Ext.Blocks, blockKey(14, 60, 10))
		delete(disk.Ext.Blocks, blockKey(14, 60, 11))
	}
	raw, _ = json.Marshal(disk)
	os.WriteFile(s.roomFile(r.ID), raw, 0600)
	loader.rooms = map[string]*Room{}
	if err = loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	got := loader.rooms[r.ID]
	if _, ok := getRedstone(got).Wires[blockKey(14, 60, 10)]; !ok || getRedstone(got).Repeaters[blockKey(14, 60, 11)] == nil || getRedstone(got).Repeaters[blockKey(14, 60, 11)].Delay != 1 {
		t.Fatal("old saved redstone blocks were not restored")
	}
}

func TestRedstoneComponentPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "redstone-components-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.BlockPlacing, api.BlockPhysicsChanging, api.RedstoneComponentChanging, api.InventoryChanging} {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if !strings.HasPrefix(e.String("reason"), "redstone") && !strings.HasPrefix(e.String("reason"), "plugin.redstone") {
				return nil
			}
			if v := e.Item("stack"); v != nil && v.Meta != nil && v.Meta.CustomData["test:cancel"] == e.Name {
				e.Cancel("redstone.protected")
			}
			if e.String("actor") == "plugin:redstone-test" {
				e.Cancel("redstone.plugin_block_protected")
			}
			if e.Source == "plugin:piston-cancel-component" && e.Name == api.RedstoneComponentChanging || e.Source == "plugin:piston-cancel-landing" && e.Name == api.BlockPhysicsChanging && e.Number("x") == 12 {
				e.Cancel("redstone.piston_protected")
			}
			if e.Source == "plugin:wire-cancel-signal" && e.Name == api.RedstoneComponentChanging && e.String("reason") == "redstone_signal" {
				e.Cancel("redstone.signal_protected")
			}
			e.Data["block"] = 1
			e.Data["componentAfter"] = map[string]any{"kind": "wire", "power": 99}
			e.Data["after"] = &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
	}
	for _, name := range []string{api.BlockPlacing, api.BlockPlaced, api.BlockPhysicsChanging, api.BlockPhysicsChanged, api.RedstoneComponentChanging, api.RedstoneComponentChanged, api.InventoryChanging, api.InventoryChanged} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			if !strings.HasPrefix(e.String("reason"), "redstone") && !strings.HasPrefix(e.String("reason"), "plugin.redstone") {
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
			e.Data["block"] = 1
			c.GiveItem(e, "Stone", 64)
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func redstonePluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "redstone-test")
	os.MkdirAll(entry, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"redstone-test","name":"redstone-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestRedstoneComponentPluginProcess$","redstone-components-fixture"]}`
	os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600)
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: findTestEngineFile(), PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func redstoneEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "redstone-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	events := []*api.Event{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, &e)
	}
	return events
}

func TestRedstoneComponentPluginCancellationMonitorSDKAndSnapshots(t *testing.T) {
	s := redstonePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{api.BlockPlacing, api.RedstoneComponentChanging, api.InventoryChanging} {
		r, p := redstoneTestPeer(s, 356)
		p.state.Inv.Slots[0].Meta.CustomData["test:cancel"] = name
		start := len(redstoneEvents(t, s))
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
		if s.block(r, 12, 60, 10) != 0 || s.heldItem(p).Count != 2 || redstoneSnapshot(r, blockKey(12, 60, 10)) != nil {
			t.Fatalf("%s cancellation committed", name)
		}
		for _, e := range redstoneEvents(t, s)[start:] {
			if e.Name == api.BlockPlaced || e.Name == api.RedstoneComponentChanged || e.Name == api.InventoryChanged {
				t.Fatal("cancelled placement emitted success", e.Name)
			}
		}
	}
	r, p := redstoneTestPeer(s, 356)
	s.rooms[r.ID] = r
	start := len(redstoneEvents(t, s))
	ctx := &api.Context{}
	ctx.UseRedstone(s.event(api.CommandExecuting, r, p, nil), 12, 59, 10, 1)
	s.pluginSource = "plugin:test-use"
	s.applyAction(ctx.Actions[0])
	s.drainActions()
	if s.block(r, 12, 60, 10) != 166 || s.heldItem(p).Count != 1 || stackCount(p, s.rules.byName["Stone"]) != 0 {
		t.Fatal("SDK / monitor committed wrong state")
	}
	events := redstoneEvents(t, s)[start:]
	want := []string{api.BlockPlacing, api.RedstoneComponentChanging, api.InventoryChanging, api.BlockPlaced, api.RedstoneComponentChanged, api.InventoryChanged}
	if len(events) != len(want) {
		t.Fatalf("unexpected sequence %v", events)
	}
	for i, e := range events {
		if e.Name != want[i] || e.String("actor") != p.sid || e.Source != "plugin:test-use" || e.Item("stack").Meta.Lore[0] != "第一行" || e.Item("before").Count != 2 || e.Item("after").Count != 1 || e.Number("block") != 166 || e.Data["target"] == nil {
			t.Fatalf("event%d lost readonly attribution/metadata: %+v", i, e)
		}
		if i >= 3 && (e.Player.Item(0).Count != 1 || r.Ext.Blocks[blockKey(12, 60, 10)].Redstone.Delay != 1) {
			t.Fatal("success before all commits")
		}
	}
	// Stale action cannot now alter repeater delay via its interactive face.
	s.applyAction(ctx.Actions[0])
	if s.heldItem(p).Count != 1 {
		t.Fatal("stale SDK action consumed again")
	}
	// SDK SetBlock receives the same component protection as item use.
	s.pluginSource = "plugin:redstone-test"
	s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 14, Y: 60, Z: 10, Block: 166})
	if s.block(r, 14, 60, 10) != 0 {
		t.Fatal("SetBlock bypassed redstone protection")
	}
	// Interaction cancellation retains both the delay and selected full stack.
	p.state.Inv.Slots[0].Meta.CustomData["test:cancel"] = api.RedstoneComponentChanging
	s.pluginSource = ""
	start = len(redstoneEvents(t, s))
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
	if redstoneSnapshot(r, blockKey(12, 60, 10)).Delay != 1 || s.heldItem(p).Count != 1 {
		t.Fatal("cancelled interaction committed")
	}
	for _, e := range redstoneEvents(t, s)[start:] {
		if e.Name == api.RedstoneComponentChanged {
			t.Fatal("cancelled interaction emitted success")
		}
	}
}

func redstonePistonWorld(s *Server, count int) *Room {
	r, _ := dropTestWorld(s)
	r.Edits[blockKey(10, 60, 10)] = 169
	for x := 11; x <= 24; x++ {
		r.Edits[blockKey(x, 60, 10)] = 0
		if x <= 10+count {
			r.Edits[blockKey(x, 60, 10)] = 1
		}
	}
	s.trackRedstoneBlock(r, blockKey(10, 60, 10), 169)
	return r
}

func TestRedstonePistonAtomicCancellationAndCommittedEvents(t *testing.T) {
	s := redstonePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range []string{"plugin:piston-cancel-component", "plugin:piston-cancel-landing"} {
		r := redstonePistonWorld(s, 1)
		s.pluginSource = source
		start := len(redstoneEvents(t, s))
		if s.pushPiston(r, 10, 60, 10, facing6[5], true) || s.block(r, 11, 60, 10) != 1 || s.block(r, 12, 60, 10) != 0 || redstoneSnapshot(r, blockKey(10, 60, 10)).Extended {
			t.Fatal("protected extension changed blocks or state", source)
		}
		for _, e := range redstoneEvents(t, s)[start:] {
			if e.Name == api.BlockPlaced || e.Name == api.BlockPhysicsChanged || e.Name == api.RedstoneComponentChanged {
				t.Fatal("failed piston move emitted completion", e.Name)
			}
		}
		// Cancelling either component or landing-cell physics during pulling
		// keeps the source, destination and Extended state together.
		r.Edits[blockKey(11, 60, 10)], r.Edits[blockKey(12, 60, 10)] = 0, 1
		next := redstoneSnapshot(r, blockKey(10, 60, 10))
		next.Extended = true
		storeRedstoneComponent(r, blockKey(10, 60, 10), next)
		start = len(redstoneEvents(t, s))
		if s.pullPiston(r, 10, 60, 10, facing6[5]) || s.block(r, 11, 60, 10) != 0 || s.block(r, 12, 60, 10) != 1 || !redstoneSnapshot(r, blockKey(10, 60, 10)).Extended {
			t.Fatal("protected retraction changed blocks or state", source)
		}
		for _, e := range redstoneEvents(t, s)[start:] {
			if e.Name == api.BlockPlaced || e.Name == api.BlockPhysicsChanged || e.Name == api.RedstoneComponentChanged {
				t.Fatal("failed piston pull emitted completion", e.Name)
			}
		}
	}
	r := redstonePistonWorld(s, 1)
	s.pluginSource = ""
	start := len(redstoneEvents(t, s))
	if !s.pushPiston(r, 10, 60, 10, facing6[5], true) || s.block(r, 11, 60, 10) != 0 || s.block(r, 12, 60, 10) != 1 || !redstoneSnapshot(r, blockKey(10, 60, 10)).Extended {
		t.Fatal("valid extension not committed atomically")
	}
	completed := 0
	for _, e := range redstoneEvents(t, s)[start:] {
		if e.Name != api.RedstoneComponentChanged {
			continue
		}
		completed++
		var before, after RedstoneComponent
		b, _ := json.Marshal(e.Data["componentBefore"])
		a, _ := json.Marshal(e.Data["componentAfter"])
		json.Unmarshal(b, &before)
		json.Unmarshal(a, &after)
		if e.String("reason") != "redstone_piston_extend" || e.String("actor") != "server" || before.Extended || !after.Extended || after.Facing != 5 || e.Data["target"] == nil || len(e.Data["blocks"].([]any)) != 2 {
			t.Fatal("piston completion lost readonly context", e)
		}
	}
	if completed != 1 || !s.pullPiston(r, 10, 60, 10, facing6[5]) || s.block(r, 11, 60, 10) != 1 || s.block(r, 12, 60, 10) != 0 || redstoneSnapshot(r, blockKey(10, 60, 10)).Extended {
		t.Fatal("piston completion / valid pull incorrect")
	}
}

func TestRedstonePistonLimitAndMetadataSafety(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, count := range []int{12, 13} {
		r := redstonePistonWorld(s, count)
		if got := s.pushPiston(r, 10, 60, 10, facing6[5], true); got != (count == 12) {
			t.Fatal("incorrect twelve-block push limit", count)
		}
		if count == 13 && (s.block(r, 11, 60, 10) != 1 || s.block(r, 23, 60, 10) != 1 || redstoneSnapshot(r, blockKey(10, 60, 10)).Extended) {
			t.Fatal("thirteenth occupied cell was overwritten")
		}
	}
	for _, kind := range []string{"furnace", "bed", "torch", "container", "redstone", "bedrock", "obsidian"} {
		r := redstonePistonWorld(s, 1)
		k := blockKey(11, 60, 10)
		switch kind {
		case "container":
			r.Containers[k] = &Container{Kind: "chest", Slots: Stacks{customStack(1, 3)}}
		case "redstone":
			r.Edits[k] = 166
			storeRedstoneComponent(r, k, &RedstoneComponent{Kind: "repeater", Facing: 3, Delay: 4})
		case "bedrock", "obsidian":
			if kind == "bedrock" {
				r.Edits[k] = s.rules.byName["Bedrock"]
			} else {
				r.Edits[k] = s.rules.byName["Obsidian"]
			}
		default:
			r.blockExt().Blocks[k] = &BlockData{Kind: kind, Facing: 2, Power: 3}
		}
		old := s.block(r, 11, 60, 10)
		before, _ := json.Marshal(r.Ext)
		if s.pushPiston(r, 10, 60, 10, facing6[5], true) || s.block(r, 11, 60, 10) != old || s.block(r, 12, 60, 10) != 0 || redstoneSnapshot(r, blockKey(10, 60, 10)).Extended {
			t.Fatal("piston moved unsupported state", kind)
		}
		after, _ := json.Marshal(r.Ext)
		if !bytes.Equal(before, after) {
			t.Fatal("piston discarded metadata", kind)
		}
	}
	// An ore ID formerly confused with Minecraft's piston ID is never an actuator.
	r := redstonePistonWorld(s, 1)
	r.Edits[blockKey(10, 60, 10)] = 33
	if s.pushPiston(r, 10, 60, 10, facing6[5], false) {
		t.Fatal("lapis ore treated as piston")
	}
}

func TestRedstoneComponentMCCMigrationUnloadAndValidation(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("redstonechunk0001", "红石区块恢复", "survival", 0)
	r.Infinite, r.Generator = true, generatorVersion
	c := chunkCoord(-17, 18000)
	pistonKey, wireKey, repeaterKey := blockKey(-17, 95, 18000), blockKey(-18, 95, 18000), blockKey(-19, 95, 18000)
	r.Edits[pistonKey], r.Edits[wireKey], r.Edits[repeaterKey] = 168, 108, 166
	storeRedstoneComponent(r, repeaterKey, &RedstoneComponent{Kind: "repeater", Facing: 3, Delay: 4, Powered: true, Countdown: 6})
	r.blockExt().Blocks[pistonKey] = &BlockData{Kind: "piston", Facing: 2, Sticky: true, Power: 9}
	base := make([]byte, chunkVolume)
	d := chunkData(r, c, base)
	d.VegetationVersion = vegetationVersion
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(raw)
	gz.Close()
	file := s.chunkFile(r, c)
	os.MkdirAll(filepath.Dir(file), 0700)
	if err = os.WriteFile(file, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite, fresh.Generator = true, generatorVersion
	if _, err = s.ensureChunk(fresh, c); err != nil {
		t.Fatal(err)
	}
	piston, repeater := redstoneSnapshot(fresh, pistonKey), redstoneSnapshot(fresh, repeaterKey)
	if piston == nil || piston.Facing != 2 || !piston.Sticky || piston.Power != 9 || repeater == nil || repeater.Facing != 3 || repeater.Delay != 4 || repeater.Countdown != 6 {
		t.Fatal("MCC lost legacy or component fields", piston, repeater)
	}
	if _, ok := getRedstone(fresh).Wires[wireKey]; !ok || fresh.Edits[wireKey] != 108 || !fresh.Dirty {
		t.Fatal("MCC bare saved block was not migrated")
	}
	// Exercise the actual eviction path without generating 512 irrelevant chunks.
	fresh.chunks[c].Used = 0
	for i := 0; i < 512; i++ {
		fresh.chunks[ChunkCoord{1000 + i, 0}] = &WorldChunk{Base: base, Used: 1, VegetationVersion: vegetationVersion}
	}
	s.trimChunks(fresh)
	if fresh.chunks[c] != nil || redstoneSnapshot(fresh, pistonKey) != nil || redstoneSnapshot(fresh, repeaterKey) != nil {
		t.Fatal("evicted redstone remained active")
	}
	if _, err = s.ensureChunk(fresh, c); err != nil {
		t.Fatal(err)
	}
	if got := redstoneSnapshot(fresh, pistonKey); got == nil || got.Facing != 2 || !got.Sticky || got.Power != 9 {
		t.Fatal("reloaded chunk lost legacy metadata", got)
	}
	if got := redstoneSnapshot(fresh, repeaterKey); got == nil || got.Facing != 3 || got.Delay != 4 || got.Countdown != 6 {
		t.Fatal("reloaded chunk lost component metadata", got)
	}
	for _, state := range []*RedstoneComponent{{Kind: "repeater", Facing: 4, Delay: 1}, {Kind: "comparator", Facing: 5}, {Kind: "repeater", Facing: 0, Delay: 0}, {Kind: "wire", Power: 16}} {
		d.Blocks = map[int]*BlockData{chunkIndex(-17, 95, 18000): {Redstone: state}}
		if s.validateChunk(d) == nil {
			t.Fatal("unsafe component state accepted", state)
		}
	}
}

func TestRedstoneTopologyTickRefreshSourcesAndSlopes(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	s.rooms[r.ID] = r
	// Both ascending and descending links are initially open. Lay the wire
	// first: adding a source later must reconcile without placing another wire.
	for _, at := range [][3]int{{11, 60, 10}, {12, 61, 10}, {13, 61, 10}, {14, 60, 10}} {
		r.Edits[blockKey(at[0], at[1]-1, at[2])] = 1
		if !s.setBlock(r, at[0], at[1], at[2], 108, setQuiet) {
			t.Fatal("wire setup failed")
		}
	}
	s.extTick(r)
	if getRedstone(r).Wires[blockKey(14, 60, 10)] != 0 || r.rt().redstoneDirty {
		t.Fatal("unpowered topology did not settle")
	}
	// The SDK path commits a non-wire source and invalidates the circuit too.
	s.applyAction(api.Action{Type: "set_block", WorldID: r.ID, X: 10, Y: 60, Z: 10, Block: 40})
	if !r.rt().redstoneDirty || getRedstone(r).Wires[blockKey(11, 60, 10)] != 0 {
		t.Fatal("source commit failed to invalidate circuit or changed power before tick")
	}
	s.extTick(r)
	for i, at := range [][3]int{{11, 60, 10}, {12, 61, 10}, {13, 61, 10}, {14, 60, 10}} {
		if power := getRedstone(r).Wires[blockKey(at[0], at[1], at[2])]; power != 15-i {
			t.Fatal("wire slope/flat decay wrong", at, power)
		}
	}
	if r.rt().redstoneDirty {
		t.Fatal("signal reconciliation invalidated itself")
	}
	// An ascending link closes when the lower wire's headroom is blocked.
	s.setBlock(r, 11, 61, 10, 1, setQuiet)
	s.extTick(r)
	if getRedstone(r).Wires[blockKey(11, 60, 10)] != 15 || getRedstone(r).Wires[blockKey(12, 61, 10)] != 0 || getRedstone(r).Wires[blockKey(14, 60, 10)] != 0 {
		t.Fatal("blocked ascending wire continued to transmit")
	}
	s.setBlock(r, 11, 61, 10, 0, setQuiet)
	s.extTick(r)
	if getRedstone(r).Wires[blockKey(14, 60, 10)] != 12 {
		t.Fatal("cleared headroom did not reopen slope")
	}
	// A descending link closes when the destination edge is occupied.
	s.setBlock(r, 14, 61, 10, 1, setQuiet)
	s.extTick(r)
	if getRedstone(r).Wires[blockKey(13, 61, 10)] != 13 || getRedstone(r).Wires[blockKey(14, 60, 10)] != 0 {
		t.Fatal("blocked descending wire continued to transmit")
	}
	s.setBlock(r, 14, 61, 10, 0, setQuiet)
	s.extTick(r)
	// Removing the source through the player edit path also invalidates.
	s.commitPlayerBlock(p, blockKey(10, 60, 10), 0, s.blockData(r, blockKey(10, 60, 10), 0))
	s.extTick(r)
	for k, power := range getRedstone(r).Wires {
		if power != 0 {
			t.Fatal("removed source left power", k, power)
		}
	}
	// Observe the final authoritative power update sent to another player.
	last := -1
	for len(p.send) > 0 {
		var m struct {
			T      string                         `json:"t"`
			Blocks []struct{ X, Y, Z, Power int } `json:"blocks"`
		}
		packet := <-p.send
		if json.Unmarshal(packet.body, &m) == nil && m.T == "rs" {
			for _, block := range m.Blocks {
				if block.X == 14 && block.Y == 60 && block.Z == 10 {
					last = block.Power
				}
			}
		}
	}
	if last != 0 {
		t.Fatal("rs did not publish latest circuit power", last)
	}
}

func TestRedstoneTopologyDirectionalComponents(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kind := range []string{"repeater", "comparator"} {
		for facing := range horizontal4 {
			r, _ := dropTestWorld(s)
			k := blockKey(12, 60, 10)
			state := &RedstoneComponent{Kind: kind, Facing: facing, Delay: 1, Powered: kind == "repeater", Power: 8}
			block := 166
			if kind == "comparator" {
				block = 167
			}
			r.Edits[k] = block
			storeRedstoneComponent(r, k, state)
			for side, direction := range horizontal4 {
				power := s.emittedPower(r, 12, 60, 10, direction[0], 0, direction[2])
				want := 0
				if side == facing {
					want = 15
					if kind == "comparator" {
						want = 8
					}
				}
				if power != want {
					t.Fatal("component emitted sideways/backwards", kind, facing, side, power)
				}
			}
		}
	}
}

func TestRedstoneTopologySignalEventsCancellationAndReadonly(t *testing.T) {
	s := redstonePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := dropTestWorld(s)
	s.setBlock(r, 11, 60, 10, 108, setQuiet)
	s.recalculateWires(r)
	s.pluginSource = "plugin:wire-cancel-signal"
	s.setBlock(r, 10, 60, 10, 40, setQuiet)
	start := len(redstoneEvents(t, s))
	s.extTick(r)
	if getRedstone(r).Wires[blockKey(11, 60, 10)] != 0 || r.rt().redstoneDirty {
		t.Fatal("cancelled signal committed or stayed perpetually dirty")
	}
	beforeCount := 0
	for _, e := range redstoneEvents(t, s)[start:] {
		if e.String("reason") != "redstone_signal" {
			continue
		}
		if e.Name == api.RedstoneComponentChanged {
			t.Fatal("cancelled signal emitted success")
		}
		beforeCount++
		if e.String("actor") != "server" || e.Data["target"] == nil || e.Data["componentBefore"] == nil || e.Data["componentAfter"] == nil || e.Player != nil {
			t.Fatal("automatic signal lost attribution/component context", e)
		}
	}
	if beforeCount != 1 {
		t.Fatal("signal before not delivered", beforeCount)
	}
	s.pluginSource = ""
	start = len(redstoneEvents(t, s))
	s.recalculateWires(r)
	if getRedstone(r).Wires[blockKey(11, 60, 10)] != 15 {
		t.Fatal("unprotected signal did not commit")
	}
	completed := 0
	for _, e := range redstoneEvents(t, s)[start:] {
		if e.Name == api.RedstoneComponentChanged && e.String("reason") == "redstone_signal" {
			completed++
			var after RedstoneComponent
			raw, _ := json.Marshal(e.Data["componentAfter"])
			json.Unmarshal(raw, &after)
			if e.String("actor") != "server" || e.Data["target"] == nil || after.Power != 15 {
				t.Fatal("readonly signal completion snapshot was modified", e)
			}
		}
	}
	if completed != 1 {
		t.Fatal("committed signal completion missing", completed)
	}
}
