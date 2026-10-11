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

func TestMechanicPlacementModesSupportFacingAndPackets(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for item, b := range mechanicItems {
		for _, id := range []int{item, b} {
			for _, mode := range []string{"survival", "creative"} {
				r, p := redstoneTestPeer(s, id)
				p.ext().GameMode = mode
				other := dropTestPlayer(r, 2, 11.5, 60, 10.5)
				other.epoch = 31
				r.Peers[other] = true
				before := copyStack(s.heldItem(p))
				s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
				k := blockKey(12, 60, 10)
				count := 1
				if mode == "creative" {
					count = 2
				}
				if s.block(r, 12, 60, 10) != b || s.heldItem(p).Count != count || !same(before, s.heldItem(p)) {
					t.Fatalf("%s item%d did not place %d with metadata/count", mode, id, b)
				}
				v := mechanicSnapshot(r, k)
				rail := railState(r, k)
				if v == nil && rail == nil {
					t.Fatal("missing persistent state")
				}
				if !s.validMechanic(v) || !validRail(rail) {
					t.Fatal("invalid facing/state")
				}
				seen := false
				for len(other.send) > 0 {
					raw := (<-other.send).body
					var packet struct {
						T      string
						Epoch  uint32
						Blocks []struct{ X, Y, Z, ID int }
					}
					if json.Unmarshal(raw, &packet) == nil && (packet.T == "mech" || packet.T == "rail") && len(packet.Blocks) == 1 && packet.Blocks[0].ID == b {
						if packet.Epoch != 31 || packet.Blocks[0].X != 12 {
							t.Fatal("missing epoch/absolute coordinates")
						}
						seen = true
					}
				}
				if !seen {
					t.Fatalf("missing block%d state packet", b)
				}
			}
		}
	}
	for _, item := range []int{itemRail, itemPoweredRail, itemDetectorRail, itemDaylightDetector, itemOakSign} {
		r, p := redstoneTestPeer(s, item)
		r.Edits[blockKey(12, 59, 10)] = 0
		before := copyStack(s.heldItem(p))
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
		if s.block(r, 12, 60, 10) != 0 || !reflect.DeepEqual(before, s.heldItem(p)) {
			t.Fatalf("unsupported item%d placed/consumed", item)
		}
	}
}
func TestRailCurvesSlopesPowerLimitAndDetection(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := createTransportationTestWorld(s)
	for _, at := range [][4]int{{10, 61, 10, blkRail}, {10, 61, 9, blkRail}, {11, 61, 10, blkRail}, {15, 61, 10, blkRail}, {16, 61, 10, 1}, {16, 62, 10, blkRail}} {
		if !s.setBlock(r, at[0], at[1], at[2], at[3], 0) {
			t.Fatalf("cannot set %v", at)
		}
	}
	s.tickRails(r)
	if railState(r, blockKey(10, 61, 10)).Shape != "north_east" || railState(r, blockKey(15, 61, 10)).Shape != "ascending_east" {
		t.Fatal("rail curve or slope missing")
	}
	for x := 2; x < 13; x++ {
		s.setBlock(r, x, 61, 20, blkPoweredRail, 0)
	}
	s.setBlock(r, 2, 60, 20, 40, 0)
	s.tickRails(r)
	for x := 2; x < 13; x++ {
		if railState(r, blockKey(x, 61, 20)).Powered != (x <= 9) {
			t.Fatalf("powered rail range wrong at%d: %+v", x, railState(r, blockKey(x, 61, 20)))
		}
	}
	s.setBlock(r, 2, 60, 20, 1, 0)
	s.tickRails(r)
	if railState(r, blockKey(5, 61, 20)).Powered {
		t.Fatal("removed source still powered")
	}
	s.setBlock(r, 20, 61, 20, blkDetectorRail, 0)
	k := blockKey(20, 61, 20)
	commit, ok := s.prepareRailOccupied(r, k, true)
	if !ok || railState(r, k).Powered {
		t.Fatal("occupancy committed before vehicle")
	}
	commit()
	if s.directPowerSource(r, 20, 61, 20, 1, 0, 0) != 15 {
		t.Fatal("detector did not emit")
	}
	s.setRailOccupied(r, k, false)
	if s.directPowerSource(r, 20, 61, 20, 1, 0, 0) != 0 {
		t.Fatal("detector stayed active")
	}
}
func TestMechanicRisingEdgeDirectionalObserverLampAndDaylight(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := createTransportationTestWorld(s)
	s.setBlock(r, 10, 61, 10, blkDispenser, 0)
	k := blockKey(10, 61, 10)
	v := mechanicSnapshot(r, k)
	v.Facing = 5
	storeMechanic(r, k, v)
	arrows := customStack(itemArrow, 3)
	r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[k].Slots[0] = copyStack(arrows)
	s.setBlock(r, 10, 60, 10, 40, 0)
	s.tickMechanics(r)
	s.tickMechanics(r)
	if len(r.rt().projectiles) != 1 || r.Containers[k].Slots[0].Count != 2 {
		t.Fatal("continuous power fired more than once")
	}
	for _, pr := range r.rt().projectiles {
		if pr.VX <= 0 || pr.VZ != 0 || pr.Actor != "server" || pr.Source != "dispenser" || !reflect.DeepEqual(pr.Weapon.Meta, arrows.Meta) {
			t.Fatal("projectile lost facing/metadata")
		}
	}
	s.setBlock(r, 10, 60, 10, 1, 0)
	s.tickMechanics(r)
	s.setBlock(r, 10, 60, 10, 40, 0)
	s.tickMechanics(r)
	if len(r.rt().projectiles) != 2 || r.Containers[k].Slots[0].Count != 1 {
		t.Fatal("second rising edge did not fire")
	}
	s.setBlock(r, 15, 61, 15, blkObserver, 0)
	okey := blockKey(15, 61, 15)
	o := mechanicSnapshot(r, okey)
	o.Facing = 2
	o.Observed = s.observerObservation(r, okey, 2)
	storeMechanic(r, okey, o)
	s.setBlock(r, 16, 61, 15, 1, 0)
	s.tickMechanics(r)
	if mechanicSnapshot(r, okey).Power != 0 {
		t.Fatal("side update triggered observer")
	}
	s.setBlock(r, 15, 61, 14, 1, 0)
	s.tickMechanics(r)
	if s.directPowerSource(r, 15, 61, 15, 0, 0, 1) != 15 || s.directPowerSource(r, 15, 61, 15, 0, 0, -1) != 0 {
		t.Fatal("observer output not opposite sensed front")
	}
	s.tickMechanics(r)
	s.tickMechanics(r)
	if mechanicSnapshot(r, okey).Power != 0 {
		t.Fatal("observer pulse did not expire")
	}
	// Internal countdowns do not constitute a visible block state update.
	s.setBlock(r, 15, 61, 14, 166, 0)
	rep := redstoneSnapshot(r, blockKey(15, 61, 14))
	rep.Countdown = 4
	storeRedstoneComponent(r, blockKey(15, 61, 14), rep)
	s.tickMechanics(r)
	s.tickMechanics(r)
	s.tickMechanics(r)
	rep.Countdown = 3
	storeRedstoneComponent(r, blockKey(15, 61, 14), rep)
	s.tickMechanics(r)
	if mechanicSnapshot(r, okey).Power != 0 {
		t.Fatal("internal repeater timer retriggered observer")
	}
	s.setBlock(r, 20, 61, 20, blkDaylightDetector, 0)
	s.setBlock(r, 21, 61, 20, blkRedstoneLamp, 0)
	r.Time = 6000
	s.tickMechanics(r)
	s.tickMechanics(r)
	if mechanicSnapshot(r, blockKey(20, 61, 20)).Power != 15 || !mechanicSnapshot(r, blockKey(21, 61, 20)).Powered {
		t.Fatal("noon sensor/lamp failed")
	}
	r.Time = 18000
	s.tickMechanics(r)
	s.tickMechanics(r)
	if mechanicSnapshot(r, blockKey(20, 61, 20)).Power != 0 || mechanicSnapshot(r, blockKey(21, 61, 20)).Powered {
		t.Fatal("night sensor/lamp failed")
	}
}
func TestMechanicDropperBucketsUtilitiesAndMetadataRestore(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, itemDropper)
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
	k := blockKey(12, 60, 10)
	v := mechanicSnapshot(r, k)
	v.Facing = 5
	storeMechanic(r, k, v)
	r.Containers[k] = &Container{Kind: "dropper", Slots: make(Stacks, 9)}
	a := customStack(1, 2)
	r.Containers[k].Slots[0] = copyStack(a)
	dest := blockKey(13, 60, 10)
	r.Edits[dest] = 140
	r.Containers[dest] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	b := customStack(1, 2)
	b.Meta.Lore = []string{"different"}
	r.Containers[dest].Slots[0] = b
	s.setBlock(r, 12, 59, 10, 40, 0)
	s.tickMechanics(r)
	if r.Containers[k].Slots[0].Count != 1 || r.Containers[dest].Slots[0].Count != 2 || !same(r.Containers[dest].Slots[1], a) {
		t.Fatal("dropper merged distinct metadata/lost item")
	}
	// Filled cauldrons are consumed, empty ones cannot generate water freely.
	ck := blockKey(14, 60, 10)
	s.setBlock(r, 14, 60, 10, blkCauldron, 0)
	p.state.Inv.Slots[0] = customStack(itemBucket, 1)
	if s.useUtilityMechanic(p, ck, "cauldron") {
		t.Fatal("empty cauldron filled bucket")
	}
	p.state.Inv.Slots[0] = customStack(itemWaterBucket, 1)
	before := copyStack(s.heldItem(p))
	if !s.useUtilityMechanic(p, ck, "cauldron") || mechanicSnapshot(r, ck).Level != 3 || s.heldItem(p).ID != itemBucket || !reflect.DeepEqual(s.heldItem(p).Meta, before.Meta) {
		t.Fatal("cauldron fill lost state/metadata")
	}
	if !s.useUtilityMechanic(p, ck, "cauldron") || mechanicSnapshot(r, ck).Level != 0 || s.heldItem(p).ID != itemWaterBucket {
		t.Fatal("cauldron drain failed")
	}
	jk := blockKey(14, 60, 11)
	s.setBlock(r, 14, 60, 11, blkJukebox, 0)
	record := customStack(500, 1)
	p.state.Inv.Slots[0] = copyStack(record)
	if !s.useUtilityMechanic(p, jk, "jukebox") || !same(mechanicSnapshot(r, jk).Record, record) || s.heldItem(p) != nil {
		t.Fatal("disc insertion failed")
	}
	sk := blockKey(14, 60, 12)
	s.setBlock(r, 14, 60, 12, blkSign, 0)
	if err := s.writeSign(p, 14, 60, 12, []string{"服务器", "欢迎", "hello", "✓"}, nil, false); err != nil {
		t.Fatal(err)
	}
	r.Players[p.sid] = p.state
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	restored := loader.rooms[r.ID]
	if !same(mechanicSnapshot(restored, jk).Record, record) || mechanicSnapshot(restored, sk).Lines[0] != "服务器" || mechanicSnapshot(restored, k).Facing != 5 || !same(restored.Containers[k].Slots[0], a) {
		t.Fatal("restart lost mechanical state/item metadata")
	}
	if !s.useUtilityMechanic(p, jk, "jukebox") || mechanicSnapshot(r, jk).Record != nil {
		t.Fatal("disc eject failed")
	}
	found := false
	for _, d := range r.Drops {
		if same(d.Stack, record) {
			found = true
		}
	}
	if !found {
		t.Fatal("ejected disc lost metadata")
	}
}

func TestMechanicPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "mechanics-fixture" {
		return
	}
	p := api.New()
	names := []string{api.MechanicChanging, api.MechanicChanged, api.RailChanging, api.RailChanged, api.BlockDispensing, api.BlockDispensed, api.SignChanging, api.SignChanged, api.BlockPlacing, api.BlockPlaced, api.BlockBreaking, api.BlockBroken, api.BlockPhysicsChanging, api.BlockPhysicsChanged, api.InventoryChanging, api.InventoryChanged, api.ItemSpawning, api.ItemSpawned, api.ProjectileLaunching, api.ProjectileLaunched, api.BucketFilling, api.BucketFilled, api.BucketEmptying, api.BucketEmptied, api.BottleFilling, api.BottleFilled}
	for _, name := range names {
		if strings.HasSuffix(name, ".before") {
			p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
				if e.Source == "plugin:cancel-mechanic" && e.Name == e.String("cancelEvent") {
					e.Cancel("protected")
				}
				if v := e.Item("stack"); v != nil && v.Meta != nil && v.Meta.CustomData["test:cancel"] == e.Name {
					e.Cancel("protected")
				}
				if e.Source == "plugin:cancel-"+e.Name {
					e.Cancel("protected")
				}
				e.Data["mechanicAfter"] = map[string]any{"kind": "lamp", "power": 99}
				e.Data["railAfter"] = map[string]any{"kind": "rail", "shape": "bad"}
				e.Data["linesAfter"] = []string{"bad"}
				return nil
			})
		}
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
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
			e.Data["block"] = 0
			e.Data["railAfter"] = nil
			c.GiveItem(e, "Stone", 64)
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func mechanicPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "mechanics-test")
	os.MkdirAll(dir, 0700)
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"mechanics-test","name":"mechanics-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestMechanicPluginProcess$","mechanics-fixture"]}`
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600)
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func mechanicEvents(t *testing.T, s *Server) []*api.Event {
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "mechanics-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, &e)
	}
	return events
}
func TestMechanicPluginCancellationMonitorSDKAndSnapshots(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.BlockPlacing, api.MechanicChanging, api.InventoryChanging, api.RailChanging} {
		item := itemDispenser
		if event == api.RailChanging {
			item = itemRail
		}
		r, p := redstoneTestPeer(s, item)
		s.rooms[r.ID] = r
		s.heldItem(p).Meta.CustomData["test:cancel"] = event
		before := copyStack(s.heldItem(p))
		s.applyAction(api.Action{Type: "use_mechanic", WorldID: r.ID, PlayerID: p.id, Slot: 0, Expected: itemSnapshot(s.heldItem(p)), X: 12, Y: 59, Z: 10, Face: 1})
		if s.block(r, 12, 60, 10) != 0 || !reflect.DeepEqual(before, s.heldItem(p)) || mechanicSnapshot(r, blockKey(12, 60, 10)) != nil || railState(r, blockKey(12, 60, 10)) != nil {
			t.Fatalf("%s cancellation mutated placement", event)
		}
	}
	r, p := redstoneTestPeer(s, itemDispenser)
	s.rooms[r.ID] = r
	before := copyStack(s.heldItem(p))
	s.applyAction(api.Action{Type: "use_mechanic", WorldID: r.ID, PlayerID: p.id, Slot: 0, Expected: itemSnapshot(before), X: 12, Y: 59, Z: 10, Face: 1})
	if s.block(r, 12, 60, 10) != blkDispenser || s.heldItem(p).Count != 1 {
		t.Fatal("readonly mutations/Monitor changed successful SDK place")
	}
	s.drainActions()
	if s.heldItem(p).Count != 1 {
		t.Fatal("Monitor action bypassed protection")
	}
	events := mechanicEvents(t, s)
	for _, e := range events {
		if e.Name == api.MechanicChanged && e.String("reason") == "mechanic_place" {
			if e.String("actor") != p.sid || e.Data["target"] == nil || e.Item("stack") == nil || e.Item("stack").Meta == nil || e.Item("after").Count != 1 {
				t.Fatal("mechanic success lacks attribution/full snapshots")
			}
		}
	}
	k := blockKey(12, 60, 10)
	v := mechanicSnapshot(r, k)
	v.Facing = 5
	storeMechanic(r, k, v)
	r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[k].Slots[0] = customStack(itemArrow, 2)
	s.setBlock(r, 12, 59, 10, 40, 0)
	for _, event := range []string{api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, api.ProjectileLaunching} {
		s.pluginSource = "plugin:cancel-" + event
		n := len(mechanicEvents(t, s))
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || r.Containers[k].Slots[0].Count != 2 || len(r.rt().projectiles) != 0 || mechanicSnapshot(r, k).Powered {
			t.Fatalf("%s canceled launch committed", event)
		}
		for _, e := range mechanicEvents(t, s)[n:] {
			if e.Name == api.BlockDispensed || e.Name == api.MechanicChanged || e.Name == api.InventoryChanged || e.Name == api.ProjectileLaunched {
				t.Fatalf("%s canceled launch sent success %s", event, e.Name)
			}
		}
	}
	s.pluginSource = ""
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || r.Containers[k].Slots[0].Count != 1 || len(r.rt().projectiles) != 1 {
		t.Fatal("successful protected dispenser did not commit")
	}
	// A blocked destination drop and a blocked sign edit keep exact old state.
	s.setBlock(r, 14, 60, 10, blkJukebox, 0)
	jk := blockKey(14, 60, 10)
	record := customStack(500, 1)
	p.state.Inv.Slots[0] = copyStack(record)
	if !s.useUtilityMechanic(p, jk, "jukebox") {
		t.Fatal("insert failed")
	}
	nextID := r.NextDropID
	s.pluginSource = "plugin:cancel-" + api.ItemSpawning
	n := len(mechanicEvents(t, s))
	if s.useUtilityMechanic(p, jk, "jukebox") || !same(mechanicSnapshot(r, jk).Record, record) || r.NextDropID != nextID {
		t.Fatal("cancelled disc eject lost item/id")
	}
	for _, e := range mechanicEvents(t, s)[n:] {
		if e.Name == api.MechanicChanged || e.Name == api.ItemSpawned {
			t.Fatal("cancelled disc eject sent success")
		}
	}
	s.pluginSource = ""
	s.setBlock(r, 14, 60, 11, blkSign, 0)
	sk := blockKey(14, 60, 11)
	s.pluginSource = "plugin:cancel-" + api.SignChanging
	if s.writeSign(p, 14, 60, 11, []string{"a", "b", "c", "d"}, nil, false) == nil || mechanicSnapshot(r, sk).Lines[0] != "" {
		t.Fatal("cancelled sign committed")
	}
	s.pluginSource = ""
	if err := s.writeSign(p, 14, 60, 11, []string{"a", "b", "c", "d"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if mechanicSnapshot(r, sk).Lines[0] != "a" {
		t.Fatal("undeclared sign mutation escaped")
	}
	// World/SDK support updates cannot remove a protected dependent rail.
	s.setBlock(r, 14, 60, 12, blkRail, 0)
	s.pluginSource = "plugin:cancel-" + api.RailChanging
	support := s.block(r, 14, 59, 12)
	if s.setBlock(r, 14, 59, 12, 0, 0) || s.block(r, 14, 59, 12) != support || s.block(r, 14, 60, 12) != blkRail {
		t.Fatal("rail veto did not protect supporting source")
	}
	s.pluginSource = ""
	if !s.setBlock(r, 14, 59, 12, 0, 0) || s.block(r, 14, 60, 12) != 0 {
		t.Fatal("support removal did not commit rail removal")
	}
}

func TestMechanicMCCStateMigrationUnloadAndValidation(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("mechanicchunk0011", "机械区块", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	c := chunkCoord(-17, 18000)
	railKey, jukeKey, discKey := blockKey(-17, 95, 18000), blockKey(-18, 95, 18000), blockKey(-19, 95, 18000)
	r.Edits[railKey] = blkPoweredRail
	r.Edits[jukeKey] = blkJukebox
	r.Edits[discKey] = blkDispenser
	storeMechanic(r, jukeKey, &MechanicState{Kind: "jukebox", Facing: 5, Record: customStack(500, 1)})
	storeMechanic(r, discKey, &MechanicState{Kind: "dispenser", Facing: 2, Powered: true, Power: 15})
	r.Containers[discKey] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[discKey].Slots[8] = customStack(itemArrow, 3)
	base := make([]byte, chunkVolume)
	d := chunkData(r, c, base)
	d.VegetationVersion = vegetationVersion
	raw, _ := json.Marshal(d)
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(raw)
	gz.Close()
	file := s.chunkFile(r, c)
	os.MkdirAll(filepath.Dir(file), 0700)
	if err := os.WriteFile(file, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite = true
	fresh.Generator = generatorVersion
	if _, err := s.ensureChunk(fresh, c); err != nil {
		t.Fatal(err)
	}
	if railState(fresh, railKey) == nil || railState(fresh, railKey).Shape != "north_south" || !same(mechanicSnapshot(fresh, jukeKey).Record, mechanicSnapshot(r, jukeKey).Record) || mechanicSnapshot(fresh, discKey).Facing != 2 || !mechanicSnapshot(fresh, discKey).Powered || !same(fresh.Containers[discKey].Slots[8], r.Containers[discKey].Slots[8]) {
		t.Fatal("MCC did not restore bare rail/full mechanical item state")
	}
	fresh.chunks[c].Used = 0
	for i := 0; i < 512; i++ {
		fresh.chunks[ChunkCoord{1000 + i, 0}] = &WorldChunk{Base: base, Used: 1, VegetationVersion: vegetationVersion}
	}
	s.trimChunks(fresh)
	if fresh.chunks[c] != nil || mechanicSnapshot(fresh, jukeKey) != nil || railState(fresh, railKey) != nil {
		t.Fatal("evicted mechanics remained live")
	}
	if _, err := s.ensureChunk(fresh, c); err != nil {
		t.Fatal(err)
	}
	if !same(mechanicSnapshot(fresh, jukeKey).Record, mechanicSnapshot(r, jukeKey).Record) || mechanicSnapshot(fresh, discKey).Facing != 2 {
		t.Fatal("MCC reload lost state")
	}
	for _, v := range []*MechanicState{{Kind: "observer", Facing: 6}, {Kind: "cauldron", Level: 4}, {Kind: "jukebox", Record: stack(500, 2, 0)}, {Kind: "sign", Lines: [4]string{"line\n", "", "", ""}}} {
		d.Blocks = map[int]*BlockData{chunkIndex(-17, 95, 18000): {Mechanic: v}}
		if s.validateChunk(d) == nil {
			t.Fatalf("accepted invalid mechanic %+v", v)
		}
	}
	d.Blocks = map[int]*BlockData{chunkIndex(-17, 95, 18000): {Rail: &RailState{Kind: "powered_rail", Shape: "north_east"}}}
	if s.validateChunk(d) == nil {
		t.Fatal("accepted powered curve")
	}
}
func TestMechanicMiningAndExplosionSpillsProtectedMetadata(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, block := range []int{blkJukebox, blkDispenser} {
		r, p := redstoneTestPeer(s, 1)
		k := blockKey(12, 60, 10)
		s.setBlock(r, 12, 60, 10, block, 0)
		original := customStack(500, 1)
		if block == blkJukebox {
			v := mechanicSnapshot(r, k)
			v.Record = copyStack(original)
			storeMechanic(r, k, v)
		} else {
			original = customStack(itemArrow, 3)
			r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
			r.Containers[k].Slots[0] = copyStack(original)
		}
		p.state.Inv.Slots[0] = nil
		s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 60, Z: 10, F: 1})
		if p.mining == nil {
			t.Fatal("cannot start mechanical mining")
		}
		p.mining.At = time.Now().Add(-time.Minute)
		n := len(mechanicEvents(t, s))
		nextID := r.NextDropID
		s.pluginSource = "plugin:cancel-" + api.ItemSpawning
		s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 60, Z: 10, F: 1})
		if s.block(r, 12, 60, 10) != block || len(r.Drops) > 0 || r.NextDropID != nextID {
			t.Fatal("cancelled spill removed source/loot")
		}
		for _, e := range mechanicEvents(t, s)[n:] {
			if e.Name == api.MechanicChanged || e.Name == api.BlockBroken || e.Name == api.ItemSpawned {
				t.Fatal("cancelled source emitted success")
			}
		}
		s.pluginSource = ""
		s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 60, Z: 10, F: 1})
		p.mining.At = time.Now().Add(-time.Minute)
		s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 60, Z: 10, F: 1})
		count := 0
		for _, drop := range r.Drops {
			if same(drop.Stack, original) {
				count += drop.Stack.Count
			}
		}
		if s.block(r, 12, 60, 10) != 0 || mechanicSnapshot(r, k) != nil || r.Containers[k] != nil || count != original.Count {
			t.Fatal("mining spill duplicated/lost state/item metadata")
		}
	}
	// The world batch owns exactly one contents spill; dropsAt supplies only the
	// primary block, avoiding duplicate inventory/record loot during explosions.
	r, _ := redstoneTestPeer(s, 1)
	k := blockKey(12, 60, 10)
	s.setBlock(r, 12, 60, 10, blkJukebox, 0)
	record := customStack(500, 1)
	v := mechanicSnapshot(r, k)
	v.Record = copyStack(record)
	storeMechanic(r, k, v)
	s.pluginSource = "plugin:cancel-" + api.ItemSpawning
	if s.setBlocks(r, [][4]int{{12, 60, 10, 0}}, 0, "explosion") || mechanicSnapshot(r, k).Record == nil {
		t.Fatal("protected explosion lost record")
	}
	s.pluginSource = ""
	if !s.setBlocks(r, [][4]int{{12, 60, 10, 0}}, 0, "explosion") {
		t.Fatal("explosion did not commit")
	}
	n := 0
	for _, drop := range r.Drops {
		if same(drop.Stack, record) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("world spill duplicate/loss:%d", n)
	}
	r, _ = redstoneTestPeer(s, 1)
	s.setBlock(r, 12, 60, 10, blkJukebox, 0)
	v = mechanicSnapshot(r, k)
	v.Record = record
	storeMechanic(r, k, v)
	r.ext().Rules["doTileDrops"] = false
	if !s.setBlocks(r, [][4]int{{12, 60, 10, 0}}, 0, "explosion") || len(r.Drops) > 0 {
		t.Fatal("doTileDrops=false produced record loot")
	}
}

func TestMechanicBucketCancellationBeforeCommit(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := redstoneTestPeer(s, itemWaterBucket)
	k := blockKey(12, 60, 10)
	s.setBlock(r, 12, 60, 10, blkCauldron, 0)
	before := copyStack(s.heldItem(p))
	s.pluginSource = "plugin:cancel-" + api.BucketEmptying
	n := len(mechanicEvents(t, s))
	if s.useUtilityMechanic(p, k, "cauldron") || mechanicSnapshot(r, k).Level != 0 || !reflect.DeepEqual(s.heldItem(p), before) {
		t.Fatal("bucket before did not protect cauldron/input")
	}
	for _, e := range mechanicEvents(t, s)[n:] {
		if e.Name == api.BucketEmptied || e.Name == api.MechanicChanged || e.Name == api.InventoryChanged {
			t.Fatal("cancelled bucket emitted success")
		}
	}
	s.pluginSource = ""
	if !s.useUtilityMechanic(p, k, "cauldron") {
		t.Fatal("cauldron accepted bucket did not commit")
	}
	s.pluginSource = "plugin:cancel-" + api.BucketFilling
	before = copyStack(s.heldItem(p))
	if s.useUtilityMechanic(p, k, "cauldron") || mechanicSnapshot(r, k).Level != 3 || !reflect.DeepEqual(s.heldItem(p), before) {
		t.Fatal("fill veto lost source")
	}
	s.pluginSource = ""
	s.setBlock(r, 14, 60, 10, blkDispenser, 0)
	dk := blockKey(14, 60, 10)
	v := mechanicSnapshot(r, dk)
	v.Facing = 5
	storeMechanic(r, dk, v)
	r.Containers[dk] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[dk].Slots[0] = customStack(itemWaterBucket, 1)
	s.pluginSource = "plugin:cancel-" + api.BucketEmptying
	if s.dispenseMechanic(r, dk, v) || s.block(r, 15, 60, 10) != 0 || r.Containers[dk].Slots[0].ID != itemWaterBucket || mechanicSnapshot(r, dk).Powered {
		t.Fatal("dispenser bucket veto partially committed")
	}
	s.pluginSource = ""
	if !s.dispenseMechanic(r, dk, v) || s.block(r, 15, 60, 10) != 9 || r.Containers[dk].Slots[0].ID != itemBucket {
		t.Fatal("dispenser bucket did not atomically commit")
	}
}
