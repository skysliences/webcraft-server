package game

import (
	"encoding/json"
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var automationTestEvents = []string{api.InventoryMoving, api.InventoryMoved, api.InventoryPickingUp, api.InventoryPickedUp, api.InventoryChanging, api.InventoryChanged, api.AnimalFeeding, api.AnimalFed, api.AnimalBreeding, api.AnimalBred, api.EntitySpawning, api.EntitySpawned, api.ItemConsuming, api.ItemConsumed, api.BlockDispensing, api.BlockDispensed, api.MechanicChanging, api.MechanicChanged}

func TestAutomationPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "automation-fixture" {
		return
	}
	p := api.New()
	for _, name := range automationTestEvents {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if item := e.Item("stack"); item != nil && item.Meta != nil && item.Meta.CustomData["test:cancel"] == e.Name {
				e.Cancel("automation.protected")
			}
			e.Data["count"], e.Data["stack"], e.Data["baby_id"] = 64, &api.ItemStack{ID: 2, Count: 64}, 999
			return nil
		})
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			_, err = f.Write(append(b, '\n'))
			e.Cancel("monitor ignored")
			e.Data["count"] = 99
			c.GiveItem(e, "Stone", 64)
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func automationApp(t *testing.T) *Server {
	root := t.TempDir()
	entry := filepath.Join(root, "automation")
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
	if err = os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(`{"id":"automation","name":"automation","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestAutomationPluginProcess$","automation-fixture"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../src/client/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func automationLog(t *testing.T, s *Server) []*api.Event {
	b, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "automation", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, &e)
	}
	return out
}
func assertNoAutomationSuccess(t *testing.T, events []*api.Event) {
	t.Helper()
	for _, e := range events {
		if !strings.HasSuffix(e.Name, ".before") {
			t.Fatalf("failed operation published %s", e.Name)
		}
	}
}
func automationStack(id, count int) *Stack {
	v := customStack(id, count)
	v.Ench = map[string]int{"unbreaking": 1}
	v.Meta.Attributes = []api.AttributeModifier{{ID: "test:power", Attribute: api.AttackDamage, Amount: 2, Operation: api.Add, Slot: "mainhand"}}
	v.Meta.CustomData["test:nested"] = map[string]any{"list": []any{"value"}}
	return v
}
func hopperFixture(s *Server) (*Room, *Peer, uint64, uint64) {
	r, p := dropTestWorld(s)
	s.rooms[r.ID] = r
	p.sid = "test-player"
	h, c := blockKey(12, 60, 10), blockKey(12, 61, 10)
	r.Edits[h], r.Edits[c] = blkHopper, 140
	r.Containers[h] = &Container{Kind: "hopper", Slots: make(Stacks, 5)}
	r.Containers[c] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	return r, p, h, c
}
func TestHopperMetadataStackLimitsPartialPickupAndPersistence(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, field := range []string{"lore", "attributes", "ench", "data"} {
		r, _, h, c := hopperFixture(s)
		v := automationStack(1, 2)
		at := copyStack(v)
		at.Count = 1
		switch field {
		case "lore":
			at.Meta.Lore[0] = "different"
		case "attributes":
			at.Meta.Attributes[0].Amount = 3
		case "ench":
			at.Ench["unbreaking"] = 2
		case "data":
			at.Meta.CustomData["test:nested"].(map[string]any)["list"].([]any)[0] = "different"
		}
		r.Containers[c].Slots[0] = v
		r.Containers[h].Slots[0] = at
		if !s.moveHopperItem(r, h, c, h, 0, 1, "hopper_pull") || r.Containers[h].Slots[0].Count != 1 || !same(v, r.Containers[h].Slots[1]) || v.Count != 2 {
			t.Fatalf("%s lost metadata or merged incompatible stacks", field)
		}
		r.Containers[h].Slots[1].Meta.Lore[0] = "changed"
		if v.Meta.Lore[0] != "第一行" {
			t.Fatal("transfer aliased source metadata")
		}
	}
	r, p, h, c := hopperFixture(s)
	v := automationStack(276, 1)
	r.Containers[c].Slots[0] = v
	r.Containers[h].Slots[0] = copyStack(v)
	if !s.moveHopperItem(r, h, c, h, 0, 1, "hopper_pull") || r.Containers[h].Slots[0].Count != 1 || r.Containers[h].Slots[1].Count != 1 {
		t.Fatal("unstackable item stacked")
	}
	r, p, h, _ = hopperFixture(s)
	v = automationStack(1, 10)
	for i := range r.Containers[h].Slots {
		r.Containers[h].Slots[i] = sized(v, 64)
	}
	r.Containers[h].Slots[0].Count = 62
	d := &ItemDrop{ID: 1, Stack: v, X: 12.5, Y: 60.5, Z: 10.5}
	r.Drops[1] = d
	p.knownDrops = map[uint32]bool{1: true}
	start := len(automationLog(t, s))
	if !s.pickupHopperDrop(r, h) || d.Stack.Count != 8 || r.Containers[h].Slots[0].Count != 64 || !same(v, d.Stack) || !same(v, r.Containers[h].Slots[0]) || !r.Dirty {
		t.Fatal("partial pickup lost or duplicated items/metadata")
	}
	for _, e := range automationLog(t, s)[start:] {
		if e.Item("stack").Count != 2 || !reflect.DeepEqual(e.Item("stack").Meta, v.Meta) || !reflect.DeepEqual(e.Item("stack").Ench, v.Ench) {
			t.Fatalf("snapshot mutated: %+v", e)
		}
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.rooms[r.ID]
	if !reflect.DeepEqual(loaded.Containers[h].Slots, r.Containers[h].Slots) || !reflect.DeepEqual(loaded.Drops[1].Stack, d.Stack) {
		t.Fatal("restart lost metadata")
	}
}
func TestHopperPluginCancellationSidedSlotsLockCooldownAndSDK(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range []string{api.InventoryMoving, api.InventoryChanging} {
		r, _, h, c := hopperFixture(s)
		v := automationStack(1, 2)
		v.Meta.CustomData["test:cancel"] = cancel
		r.Containers[c].Slots[0] = v
		sb, db := cloneStacks(r.Containers[c].Slots), cloneStacks(r.Containers[h].Slots)
		start := len(automationLog(t, s))
		if s.activateHopper(r, h) || !reflect.DeepEqual(sb, r.Containers[c].Slots) || !reflect.DeepEqual(db, r.Containers[h].Slots) || getRedstone(r).Hoppers[h] != 0 {
			t.Fatal("cancelled transfer partially committed")
		}
		assertNoAutomationSuccess(t, automationLog(t, s)[start:])
	}
	for _, cancel := range []string{api.InventoryPickingUp, api.InventoryChanging} {
		r, _, h, _ := hopperFixture(s)
		delete(r.Containers, blockKey(12, 61, 10))
		r.Edits[blockKey(12, 61, 10)] = 0
		v := automationStack(1, 2)
		v.Meta.CustomData["test:cancel"] = cancel
		r.Drops[1] = &ItemDrop{ID: 1, Stack: v, X: 12.5, Y: 60.5, Z: 10.5}
		before := copyStack(v)
		start := len(automationLog(t, s))
		if s.pickupHopperDrop(r, h) || !reflect.DeepEqual(before, r.Drops[1].Stack) || r.Containers[h].Slots[0] != nil {
			t.Fatal("cancelled pickup committed")
		}
		assertNoAutomationSuccess(t, automationLog(t, s)[start:])
	}
	r, _, h, c := hopperFixture(s)
	r.Containers[c].Slots[0] = automationStack(1, 2)
	getRedstone(r).Hoppers[h] = 4
	if s.activateHopper(r, h) {
		t.Fatal("ignored cooldown")
	}
	getRedstone(r).Hoppers[h] = 0
	r.Edits[blockKey(11, 60, 10)] = 40
	if s.activateHopper(r, h) {
		t.Fatal("ignored redstone lock")
	}
	r.Edits[blockKey(11, 60, 10)] = 0
	action := api.Action{Type: "activate_hopper", WorldID: r.ID, X: 12, Y: 60, Z: 10, ContainerExpected: make([]*api.ItemStack, 5)}
	r.Containers[h].Slots[1] = automationStack(1, 1)
	s.applyAction(action)
	if r.Containers[c].Slots[0].Count != 2 {
		t.Fatal("stale SDK snapshot overwrote inventory")
	}
	action.ContainerExpected[1] = itemSnapshot(r.Containers[h].Slots[1])
	s.applyAction(action)
	if r.Containers[c].Slots[0].Count != 1 {
		t.Fatal("SDK did not use hopper entry")
	}
	f := &Container{Kind: "furnace", Slots: make(Stacks, 3)}
	if s.automationSlot(f, 2, 1, stack(1, 1, 0), true) || s.automationSlot(f, 0, 0, stack(1, 1, 0), false) || !s.automationSlot(f, 2, 0, stack(1, 1, 0), false) || s.automationSlot(f, 1, 2, stack(1, 1, 0), true) {
		t.Fatal("furnace sided slots bypassed")
	}
}
func animalFixture(t *testing.T, s *Server) (*Room, *Peer, *Mob, *Mob) {
	t.Helper()
	r, p := dropTestWorld(s)
	s.rooms[r.ID] = r
	p.sid = "test-player"
	a, err := s.spawnMob(r, p, "cow", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.spawnMob(r, p, "cow", 14.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	p.state.Inv.Selected = 0
	p.state.Inv.Slots[0] = automationStack(itemWheat, 4)
	return r, p, a, b
}
func TestAnimalTwoParentsAtomicVetoAndBirthSnapshots(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range []string{api.AnimalFeeding, api.AnimalBreeding, api.EntitySpawning, api.InventoryChanging, api.ItemConsuming} {
		r, p, a, b := animalFixture(t, s)
		if !s.feedAnimal(p, a, nil, false) || len(r.Mobs) != 2 || a.LoveTicks != 600 {
			t.Fatal("first feed must not create a child")
		}
		p.state.Inv.Slots[0].Meta.CustomData["test:cancel"] = cancel
		food, aa, bb, next := copyStack(s.heldItem(p)), animalSnapshot(a), animalSnapshot(b), r.NextMobID
		start := len(automationLog(t, s))
		if s.feedAnimal(p, b, nil, false) || !reflect.DeepEqual(food, s.heldItem(p)) || !reflect.DeepEqual(aa, animalSnapshot(a)) || !reflect.DeepEqual(bb, animalSnapshot(b)) || r.NextMobID != next || len(r.Mobs) != 2 {
			t.Fatalf("%s veto changed inventory/parents/entity ID", cancel)
		}
		assertNoAutomationSuccess(t, automationLog(t, s)[start:])
	}
	r, p, a, b := animalFixture(t, s)
	original := copyStack(s.heldItem(p))
	if !s.feedAnimal(p, a, nil, false) {
		t.Fatal("first feed failed")
	}
	if !reflect.DeepEqual(a.BreedFood.Meta, original.Meta) || !reflect.DeepEqual(a.BreedFood.Ench, original.Ench) {
		t.Fatal("pending parent lost food metadata")
	}
	start := len(automationLog(t, s))
	if !s.feedAnimal(p, b, nil, false) || len(r.Mobs) != 3 || s.heldItem(p).Count != 2 || !same(original, s.heldItem(p)) || a.BreedCooldown != 6000 || b.BreedCooldown != 6000 || a.LoveTicks != 0 || b.LoveTicks != 0 {
		t.Fatal("pair did not atomically breed")
	}
	births := 0
	for _, e := range automationLog(t, s)[start:] {
		if e.Name == api.EntitySpawned {
			if e.Number("flags") != 1 || e.Number("growthTicks") != 24000 || e.Item("mateFood").Meta.Lore[0] != "第一行" {
				t.Fatal("baby published before flags or metadata set")
			}
		}
		if e.Name == api.AnimalBred {
			births++
			if e.Number("baby_id") == 0 || e.Item("food").Count != 1 || !reflect.DeepEqual(e.Item("food").Ench, original.Ench) {
				t.Fatal("invalid birth snapshot")
			}
		}
	}
	if births != 1 || s.feedAnimal(p, a, nil, false) {
		t.Fatal("cooldown missing or duplicate success")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	for _, m := range loader.rooms[r.ID].Mobs {
		if !validMob(m) || m.Flags&mobFlagBaby != 0 && m.GrowthTicks != 24000 || m.Flags&mobFlagBaby == 0 && m.BreedCooldown != 6000 {
			t.Fatal("restart lost baby growth/parent cooldown")
		}
	}
}
func TestAnimalInvalidCapacityStaleSDKAndPendingFoodPersistence(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, a, b := animalFixture(t, s)
	food := itemSnapshot(s.heldItem(p))
	stale := food.Clone()
	stale.Meta.Lore[0] = "stale"
	s.applyAction(api.Action{Type: "feed_animal", WorldID: r.ID, PlayerID: p.id, EntityID: a.ID, Slot: 0, Expected: stale})
	if a.LoveTicks != 0 {
		t.Fatal("stale food accepted")
	}
	p.state.X = 40
	if s.feedAnimal(p, a, nil, false) {
		t.Fatal("out-of-reach feed")
	}
	p.state.X = 10.5
	p.state.Dead = true
	if s.feedAnimal(p, a, nil, false) {
		t.Fatal("dead player fed")
	}
	p.state.Dead = false
	if !s.feedAnimal(p, a, nil, false) {
		t.Fatal("valid feed failed")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.rooms[r.ID].Mobs[a.ID]
	if !reflect.DeepEqual(loaded.BreedFood, a.BreedFood) || loaded.LoveTicks != 600 || loaded.Breeder != p.sid {
		t.Fatal("restart lost pending feeding metadata")
	}
	for i := len(r.Mobs); i < s.config.MobSpawning.TotalLimit; i++ {
		id := mobNamespace | uint32(i+100)
		r.Mobs[id] = &Mob{ID: id, Type: "cow"}
	}
	before := copyStack(s.heldItem(p))
	start := len(automationLog(t, s))
	if s.feedAnimal(p, b, nil, false) || !reflect.DeepEqual(before, s.heldItem(p)) || b.LoveTicks != 0 {
		t.Fatal("capacity failure consumed food")
	}
	assertNoAutomationSuccess(t, automationLog(t, s)[start:])
}

func TestHopperNativePlacementOrientationProtectionAndDoubleWindow(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range []string{api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, ""} {
		r, p := dropTestWorld(s)
		s.rooms[r.ID] = r
		food := automationStack(itemHopper, 2)
		if cancel != "" {
			food.Meta.CustomData["test:cancel"] = cancel
		}
		p.state.Inv.Slots[0] = food
		// Placement is a mechanic operation; BlockDispensing does not gate placement.
		shouldCancel := cancel == api.MechanicChanging || cancel == api.InventoryChanging
		s.handleMechanicUse(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1}, nil, false)
		k := blockKey(12, 60, 10)
		if shouldCancel {
			if s.block(r, 12, 60, 10) != 0 || !reflect.DeepEqual(food, s.heldItem(p)) || r.Containers[k] != nil {
				t.Fatal("cancelled hopper placement partially committed")
			}
			continue
		}
		if s.block(r, 12, 60, 10) != blkHopper || mechanicSnapshot(r, k).Facing != 0 || r.Containers[k] == nil || len(r.Containers[k].Slots) != 5 || s.heldItem(p).Count != 1 || !same(food, s.heldItem(p)) {
			t.Fatal("native hopper placement failed")
		}
	}
	r, p, h, c := hopperFixture(s)
	other := blockKey(13, 61, 10)
	r.Edits[other] = 140
	r.Containers[other] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	p.window = &Window{Kind: "large_chest", ID: 1, Key: other, Key2: c, Slots: make(Stacks, 54)}
	r.Containers[c].Slots[0] = automationStack(1, 2)
	copy(p.window.Slots[27:], r.Containers[c].Slots)
	if !s.moveHopperItem(r, h, c, h, 0, 1, "hopper_pull") || p.window.Slots[27].Count != 1 || p.window.Slots[27] != r.Containers[c].Slots[0] {
		t.Fatal("large chest second half retained stale inventory")
	}
}

func TestAnimalBlockedSpawnCreativeAndTimerSemantics(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, a, b := animalFixture(t, s)
	if !s.feedAnimal(p, a, nil, false) {
		t.Fatal("feed failed")
	}
	r.Edits[blockKey(13, 60, 10)] = 1
	before := copyStack(s.heldItem(p))
	start := len(automationLog(t, s))
	if s.feedAnimal(p, b, nil, false) || !reflect.DeepEqual(before, s.heldItem(p)) || b.LoveTicks != 0 {
		t.Fatal("obstructed birth consumed food")
	}
	assertNoAutomationSuccess(t, automationLog(t, s)[start:])
	r.Edits[blockKey(13, 60, 10)] = 0
	a.LoveTicks = 2
	s.tickAnimalBreeding(r)
	if a.LoveTicks != 0 || a.BreedFood != nil || a.Breeder != "" {
		t.Fatal("love expiry did not clear snapshots")
	}
	a.BreedCooldown = 2
	s.tickAnimalBreeding(r)
	if a.BreedCooldown != 0 {
		t.Fatal("cooldown did not expire")
	}
	p.ext().GameMode = "creative"
	before = copyStack(s.heldItem(p))
	if !s.feedAnimal(p, a, nil, false) || !s.feedAnimal(p, b, nil, false) || !reflect.DeepEqual(before, s.heldItem(p)) || len(r.Mobs) != 3 {
		t.Fatal("creative breeding consumed food or failed")
	}
}

func TestHopperHorizontalOutputAndReceivingCooldown(t *testing.T) {
	s := automationApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range []int{2, 3, 4, 5} {
		r, _, h, c := hopperFixture(s)
		delete(r.Containers, c)
		r.Edits[c] = 0
		storeMechanic(r, h, &MechanicState{Kind: "hopper", Facing: f})
		off := facing6[f]
		dest := blockKey(12+off[0], 60, 10+off[2])
		r.Edits[dest] = blkHopper
		r.Containers[dest] = &Container{Kind: "hopper", Slots: make(Stacks, 5)}
		item := automationStack(1, 2)
		r.Containers[h].Slots[0] = item
		if !s.activateHopper(r, h) || r.Containers[h].Slots[0].Count != 1 || r.Containers[dest].Slots[0] == nil || !same(item, r.Containers[dest].Slots[0]) || getRedstone(r).Hoppers[dest] != 4 || s.activateHopper(r, dest) {
			t.Fatalf("horizontal facing %d ignored or chain bypassed cooldown", f)
		}
	}
}
