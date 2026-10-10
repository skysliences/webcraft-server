package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func dispenserFireWorld(s *Server, item int) (*Room, uint64, *Stack) {
	r, _ := redstoneTestPeer(s, item)
	k := blockKey(10, 60, 10)
	r.Edits[k] = blkDispenser
	storeMechanic(r, k, &MechanicState{Kind: "dispenser", Facing: 5})
	r.Edits[blockKey(11, 59, 10)] = 1
	r.Edits[blockKey(11, 60, 10)] = 0
	original := customStack(item, 1)
	if item == blkExplosiveTNT {
		original.Count = 2
	}
	r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[k].Slots[0] = copyStack(original)
	return r, k, original
}
func dispenserPortalFrame(r *Room) {
	for z := 9; z <= 12; z++ {
		for y := 59; y <= 63; y++ {
			b := 0
			if y == 59 || y == 63 || z == 9 || z == 12 {
				b = blkObsidian
			}
			r.Edits[blockKey(11, y, z)] = b
		}
	}
}
func TestDispenserFlintFireTNTAndPortalAtomicMetadata(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, k, original := dispenserFireWorld(s, itemFlintAndSteel)
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.block(r, 11, 60, 10) != blkFlintFire || r.Containers[k].Slots[0].Damage != 1 {
		t.Fatal("flint failed to create fire with one durability")
	}
	origin := s.fireOrigin(r, blockKey(11, 60, 10))
	if origin == nil || origin.Actor != "server" || origin.Source != "dispenser" || origin.Attacker == nil || origin.Attacker.Kind != "block" || origin.Attacker.X != 10 || !reflect.DeepEqual(origin.Stack.Meta, original.Meta) {
		t.Fatal("fire lost dispenser/full item attribution")
	}
	r, k, original = dispenserFireWorld(s, itemFlintAndSteel)
	original.Damage = 63
	r.Containers[k].Slots[0] = copyStack(original)
	r.Edits[blockKey(11, 60, 10)] = blkExplosiveTNT
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !s.primedTNT(r, blockKey(11, 60, 10)) || r.Containers[k].Slots[0] != nil {
		t.Fatal("last use did not atomically ignite TNT/break flint")
	}
	origin = s.tntOrigin(r, blockKey(11, 60, 10))
	if origin.Stack.Damage != 63 || !reflect.DeepEqual(origin.Stack.Meta, original.Meta) || origin.Source != "dispenser" {
		t.Fatal("TNT lost pre-wear weapon snapshot")
	}
	r, k, original = dispenserFireWorld(s, blkExplosiveTNT)
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !s.primedTNT(r, blockKey(11, 60, 10)) || r.Containers[k].Slots[0].Count != 1 || len(r.Drops) != 0 {
		t.Fatal("TNT item did not become primed TNT")
	}
	if !same(r.Containers[k].Slots[0], original) || s.tntOrigin(r, blockKey(11, 60, 10)).Stack.Count != 1 {
		t.Fatal("TNT metadata/splitting lost")
	}
	r, k, original = dispenserFireWorld(s, itemFlintAndSteel)
	dispenserPortalFrame(r)
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) {
		t.Fatal("flint did not light portal")
	}
	for z := 10; z <= 11; z++ {
		for y := 60; y <= 62; y++ {
			if s.block(r, 11, y, z) != blkPortalZ {
				t.Fatal("portal partially filled")
			}
		}
	}
	if r.Containers[k].Slots[0].Damage != 1 || !reflect.DeepEqual(r.Containers[k].Slots[0].Meta, original.Meta) {
		t.Fatal("portal flint lost metadata/durability")
	}
}
func TestDispenserFireObstructionNoDurabilityOrFallbackDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range []int{itemFlintAndSteel, blkExplosiveTNT} {
		for _, target := range []int{1, blkFlintFire} {
			r, k, original := dispenserFireWorld(s, item)
			r.Edits[blockKey(11, 60, 10)] = target
			if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(original, r.Containers[k].Slots[0]) || mechanicSnapshot(r, k).Powered || len(r.Drops) != 0 {
				t.Fatal("obstructed special item was consumed/ejected")
			}
		}
	}
	r, k, original := dispenserFireWorld(s, itemFlintAndSteel)
	for _, dir := range faceOffsets {
		r.Edits[blockKey(11+dir[0], 60+dir[1], 10+dir[2])] = 0
	}
	r.Edits[k] = blkDispenser
	if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(original, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
		t.Fatal("unsupported fire consumed tool")
	}
	r, k, original = dispenserFireWorld(s, itemFlintAndSteel)
	original.Meta.Unbreakable = true
	r.Containers[k].Slots[0] = copyStack(original)
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(original, r.Containers[k].Slots[0]) {
		t.Fatal("unbreakable flint durability changed")
	}
}

func TestDispenserFirePluginFixtureProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "dispenser-fire-fixture" {
		return
	}
	p := api.New()
	names := []string{api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, api.ItemWearing, api.TNTPriming, api.BlockPhysicsChanging, api.BlockPlacing, api.BlockBreaking, api.EntitySpawning, "block.ignite.before", "portal.create.before"}
	for _, name := range names {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			stack := e.Item("stack")
			if stack != nil && stack.Meta != nil {
				if stack.Meta.CustomData["test:cancel"] == e.Name {
					e.Cancel("dispenser.test.protected")
				}
				if value, exists := stack.Meta.CustomData["test:wear"]; exists && e.Name == api.ItemWearing {
					e.Data["amount"] = value
				}
				if value, exists := stack.Meta.CustomData["test:fuse"]; exists && e.Name == api.TNTPriming {
					e.Data["fuseTicks"] = value
				}
				if e.Name == api.BlockPlacing && stack.Meta.CustomData["test:last_portal"] == true && e.Number("y") == 62 && e.Number("z") == 11 {
					e.Cancel("dispenser.portal.last_cell")
				}
			}
			e.Data["actor"] = "forged"
			e.Data["source"] = "forged"
			e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
			e.Data["origin"] = map[string]any{"kind": "player"}
			return nil
		})
	}
	names = append(names, api.BlockDispensed, api.MechanicChanged, api.InventoryChanged, api.ItemWorn, api.TNTPrimed, api.BlockPhysicsChanged, api.BlockPlaced, api.BlockBroken, api.EntitySpawned, "block.ignited", "portal.created")
	for _, name := range names {
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
			e.Data["amount"] = 1000
			e.Data["fuseTicks"] = 1
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func dispenserFirePluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "fire-test")
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
	manifest := `{"id":"fire-test","name":"fire-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestDispenserFirePluginFixtureProcess$","dispenser-fire-fixture"]}`
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
func dispenserFireEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "fire-test", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, &e)
	}
	return out
}
func TestDispenserFireRealPluginProtectionMutableWearFuseAndSuccess(t *testing.T) {
	s := dispenserFirePluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	cases := []struct {
		item   int
		portal bool
		tnt    bool
		events []string
	}{
		{itemFlintAndSteel, false, false, []string{api.ItemWearing, "block.ignite.before", api.BlockPhysicsChanging, api.BlockPlacing, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging}},
		{itemFlintAndSteel, false, true, []string{api.ItemWearing, api.TNTPriming, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging}},
		{itemFlintAndSteel, true, false, []string{api.ItemWearing, "block.ignite.before", "portal.create.before", api.BlockPhysicsChanging, api.BlockPlacing, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging}},
		{blkExplosiveTNT, false, false, []string{api.TNTPriming, api.BlockPhysicsChanging, api.BlockPlacing, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging}},
	}
	for _, test := range cases {
		for _, event := range test.events {
			r, k, original := dispenserFireWorld(s, test.item)
			if test.portal {
				dispenserPortalFrame(r)
			}
			if test.tnt {
				r.Edits[blockKey(11, 60, 10)] = blkExplosiveTNT
			}
			original.Meta.CustomData["test:cancel"] = event
			r.Containers[k].Slots[0] = copyStack(original)
			start := len(dispenserFireEvents(t, s))
			before := map[uint64]int{}
			for at, b := range r.Edits {
				before[at] = b
			}
			if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(r.Edits, before) || !reflect.DeepEqual(r.Containers[k].Slots[0], original) || mechanicSnapshot(r, k).Powered || len(r.Drops) != 0 || s.primedTNT(r, blockKey(11, 60, 10)) {
				t.Fatalf("%s veto partly committed item %d", event, test.item)
			}
			for _, e := range dispenserFireEvents(t, s)[start:] {
				if !strings.HasSuffix(e.Name, ".before") {
					t.Fatalf("%s veto emitted success %s", event, e.Name)
				}
			}
		}
	}
	for _, key := range []string{"test:wear", "test:fuse"} {
		for _, value := range []any{float64(-1), 1.5, "fast", float64(100000)} {
			r, k, original := dispenserFireWorld(s, itemFlintAndSteel)
			r.Edits[blockKey(11, 60, 10)] = blkExplosiveTNT
			original.Meta.CustomData[key] = value
			r.Containers[k].Slots[0] = copyStack(original)
			start := len(dispenserFireEvents(t, s))
			if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.primedTNT(r, blockKey(11, 60, 10)) || !reflect.DeepEqual(original, r.Containers[k].Slots[0]) {
				t.Fatalf("invalid %s=%v committed: events=%#v", key, value, dispenserFireEvents(t, s)[start:])
			}
			for _, e := range dispenserFireEvents(t, s)[start:] {
				if !strings.HasSuffix(e.Name, ".before") {
					t.Fatal("invalid mutation emitted success")
				}
			}
		}
	}
	r, k, original := dispenserFireWorld(s, itemFlintAndSteel)
	dispenserPortalFrame(r)
	original.Meta.CustomData["test:last_portal"] = true
	r.Containers[k].Slots[0] = copyStack(original)
	if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || r.Containers[k].Slots[0].Damage != 0 {
		t.Fatal("late portal veto consumed tool")
	}
	for z := 10; z <= 11; z++ {
		for y := 60; y <= 62; y++ {
			if s.block(r, 11, y, z) != 0 {
				t.Fatal("late portal veto left partial portal")
			}
		}
	}
	r, k, original = dispenserFireWorld(s, itemFlintAndSteel)
	r.Edits[blockKey(11, 60, 10)] = blkExplosiveTNT
	original.Meta.CustomData["test:wear"] = float64(2)
	original.Meta.CustomData["test:fuse"] = float64(7)
	r.Containers[k].Slots[0] = copyStack(original)
	start := len(dispenserFireEvents(t, s))
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || r.Containers[k].Slots[0].Damage != 2 || r.Ext.Blocks[blockKey(11, 60, 10)].Fuse != 7 {
		t.Fatal("mutable wear/fuse or Monitor semantics lost")
	}
	counts := map[string]int{}
	for _, e := range dispenserFireEvents(t, s)[start:] {
		counts[e.Name]++
		if e.Name == api.TNTPrimed || e.Name == api.ItemWorn || e.Name == api.BlockDispensed {
			stack := e.Item("stack")
			if e.String("actor") != "server" || e.String("source") != "dispenser" || stack == nil || !reflect.DeepEqual(stack.Meta, original.Meta) {
				t.Fatalf("%s lost attribution/full metadata", e.Name)
			}
			if e.Item("after") == nil || e.Item("after").Damage != 2 {
				t.Fatalf("%s missing final source snapshot", e.Name)
			}
			var slots []*Stack
			raw, _ := json.Marshal(e.Data["containerAfter"])
			if json.Unmarshal(raw, &slots) != nil || len(slots) != 9 || slots[0].Damage != 2 {
				t.Fatalf("%s missing final container", e.Name)
			}
		}
	}
	for _, name := range []string{api.TNTPrimed, api.ItemWorn, api.InventoryChanged, api.MechanicChanged, api.BlockDispensed} {
		if counts[name] != 1 {
			t.Fatalf("%s successes = %d", name, counts[name])
		}
	}
}

func TestDispenserIgnitionMCCCrossChunkAndDimensionRestore(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, dim := range []string{"overworld", "nether"} {
		for _, item := range []int{itemFlintAndSteel, blkExplosiveTNT} {
			id := "dispfiremcc000001"
			if index == 1 {
				id = "dispfiremcc000002"
			}
			if item == blkExplosiveTNT {
				id += "tnt"
			}
			root := newRoom(id, "发射器恢复", "survival", 0)
			root.Infinite = true
			root.Generator = generatorVersion
			r := s.dimRoom(root, dim)
			sourceKey, targetKey := blockKey(-17, 95, 18000), blockKey(-16, 95, 18000)
			r.Edits[sourceKey] = blkDispenser
			r.Edits[targetKey] = 0
			r.Edits[blockKey(-16, 94, 18000)] = 1
			storeMechanic(r, sourceKey, &MechanicState{Kind: "dispenser", Facing: 5})
			original := customStack(item, 1)
			if item == blkExplosiveTNT {
				original.Count = 2
			}
			r.Containers[sourceKey] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
			r.Containers[sourceKey].Slots[0] = copyStack(original)
			if !s.dispenseMechanic(r, sourceKey, mechanicSnapshot(r, sourceKey)) {
				t.Fatalf("%s ignition failed", dim)
			}
			expected := copyStack(r.Containers[sourceKey].Slots[0])
			origin := s.fireOrigin(r, targetKey)
			if item == blkExplosiveTNT {
				origin = s.tntOrigin(r, targetKey)
			}
			if err := s.saveRoom(r); err != nil {
				t.Fatal(err)
			}
			freshRoot := newRoom(id, root.Name, root.Mode, 0)
			freshRoot.Infinite = true
			freshRoot.Generator = generatorVersion
			fresh := s.dimRoom(freshRoot, dim)
			for _, c := range []ChunkCoord{chunkCoord(-17, 18000), chunkCoord(-16, 18000)} {
				if _, err := s.ensureChunk(fresh, c); err != nil {
					t.Fatal(err)
				}
			}
			if fresh.Containers[sourceKey] == nil || !reflect.DeepEqual(expected, fresh.Containers[sourceKey].Slots[0]) || mechanicSnapshot(fresh, sourceKey) == nil || !mechanicSnapshot(fresh, sourceKey).Powered {
				t.Fatal("MCC restore lost source container/signal metadata")
			}
			restored := s.fireOrigin(fresh, targetKey)
			if item == blkExplosiveTNT {
				restored = s.tntOrigin(fresh, targetKey)
				if !s.primedTNT(fresh, targetKey) || fresh.Ext.Blocks[targetKey].Fuse != defaultTNTFuse {
					t.Fatal("MCC restore lost primed fuse")
				}
			} else if s.block(fresh, -16, 95, 18000) != blkFlintFire {
				t.Fatal("MCC restore lost flame")
			}
			if !reflect.DeepEqual(restored, origin) || restored.Source != "dispenser" || !reflect.DeepEqual(restored.Stack.Meta, original.Meta) {
				t.Fatal("MCC restore lost ignition origin/full metadata")
			}
		}
	}
}
