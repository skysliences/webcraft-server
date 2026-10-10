package game

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func dispenserEntityTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func dispenserEntityFixture(s *Server, id, count int) (*Room, uint64, *MechanicState, *Stack, map[string]any) {
	r, _ := dropTestWorld(s)
	k := blockKey(12, 60, 10)
	r.Edits[k] = blkDispenser
	state := &MechanicState{Kind: "dispenser", Facing: 5}
	storeMechanic(r, k, state)
	original := customStack(id, count)
	r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[k].Slots[0] = original
	after := cloneStacks(r.Containers[k].Slots)
	after[0] = sized(original, count-1)
	data := s.mechanicData(r, nil, k, state, "mechanic_dispense")
	data["actor"], data["source"] = "server", "dispenser"
	data["slot"], data["stack"], data["before"], data["after"] = 0, itemSnapshot(sized(original, 1)), itemSnapshot(original), itemSnapshot(after[0])
	data["containerBefore"], data["containerAfter"] = cloneStacks(r.Containers[k].Slots), after
	return r, k, state, sized(original, 1), data
}

func commitDispenserEntityFixture(t *testing.T, s *Server, r *Room, k uint64, state *MechanicState, one *Stack, data map[string]any) *dispenserEntityEffect {
	t.Helper()
	plan, handled, ok := s.prepareDispenserEntity(r, k, state, one, data)
	if !handled || !ok || plan == nil || !plan.Validate() {
		t.Fatalf("could not prepare %+v: handled=%v ok=%v", one, handled, ok)
	}
	if len(r.Mobs) != 0 || len(vehicleStore(r)) != 0 || len(r.rt().projectiles) != 0 {
		t.Fatal("preparation committed an entity")
	}
	copy(r.Containers[k].Slots, data["containerAfter"].(Stacks))
	plan.Commit()
	plan.Publish()
	return plan
}

func TestDispenserEntityAllEggTypesAtomicConsumptionAndReplication(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, typ := range testEggTypes {
		for _, mode := range []string{"survival", "creative"} {
			r, k, state, _, _ := dispenserEntityFixture(s, 420+i, 2)
			r.Mode = mode
			original := copyStack(r.Containers[k].Slots[0])
			if !s.dispenseMechanic(r, k, state) || len(r.Mobs) != 1 || r.Containers[k].Slots[0].Count != 1 || !same(original, r.Containers[k].Slots[0]) {
				t.Fatalf("%s %s consumption/full metadata invalid", mode, typ)
			}
			for _, m := range r.Mobs {
				if m.Type != typ || m.X != 13.5 || m.Y != 60 || m.Z != 10.5 || !validMob(m) {
					t.Fatalf("incorrect entity: %+v", m)
				}
				for p := range r.Peers {
					if !p.knownMobs[m.ID] {
						t.Fatal("successful spawn not replicated")
					}
				}
			}
		}
	}
}

func TestDispenserEntityVehicleGeometryMetadataAndRestart(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range []int{itemMinecart, itemBoat} {
		for _, below := range []bool{false, true} {
			r, k, state, one, data := dispenserEntityFixture(s, id, 1)
			dy := 60
			if below {
				dy--
			}
			if id == itemMinecart {
				r.Edits[blockKey(13, dy, 10)] = blkDetectorRail
				storeRail(r, blockKey(13, dy, 10), &RailState{Kind: "detector_rail", Shape: "east_west"})
			} else {
				for x := 12; x <= 14; x++ {
					for z := 9; z <= 11; z++ {
						if x == 12 && dy == 60 && z == 10 {
							continue
						}
						r.Edits[blockKey(x, dy, z)] = 9
					}
				}
			}
			commitDispenserEntityFixture(t, s, r, k, state, one, data)
			if len(vehicleStore(r)) != 1 || r.Containers[k].Slots[0] != nil || len(r.Mobs) != 0 || len(r.Drops) != 0 {
				t.Fatal("vehicle output duplicated or retained its item")
			}
			for _, v := range vehicleStore(r) {
				if !same(one, v.Item) || !s.validVehicle(r, v) || v.Rider != "" {
					t.Fatal("vehicle lost item metadata or produced invalid state")
				}
			}
			if id == itemMinecart && !railState(r, blockKey(13, dy, 10)).Powered {
				t.Fatal("minecart did not trigger detector rail")
			}
			if err := s.saveRoom(r); err != nil {
				t.Fatal(err)
			}
			loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
			if err := loader.loadRooms(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(loader.rooms[r.ID].Ext.Vehicles, r.Ext.Vehicles) || !reflect.DeepEqual(loader.rooms[r.ID].Containers[k].Slots, r.Containers[k].Slots) {
				t.Fatal("restart lost a dispensed vehicle or full metadata")
			}
		}
	}
}

func TestDispenserEntityInvalidAndCapacityHaveNoSideEffects(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range []int{itemMinecart, itemBoat} {
		r, k, state, one, data := dispenserEntityFixture(s, id, 1)
		if _, handled, ok := s.prepareDispenserEntity(r, k, state, one, data); handled || !ok || len(vehicleStore(r)) != 0 || r.Containers[k].Slots[0] == nil {
			t.Fatal("invalid vehicle target should use ordinary item ejection")
		}
	}
	for _, capacity := range []string{"mob", "projectile", "vehicle"} {
		id := 420
		if capacity == "projectile" {
			id = itemEgg
		} else if capacity == "vehicle" {
			id = itemMinecart
		}
		r, k, state, one, data := dispenserEntityFixture(s, id, 1)
		if capacity == "mob" {
			for i := 1; i <= maxWorldMobs; i++ {
				r.Mobs[mobNamespace|uint32(i)] = &Mob{ID: mobNamespace | uint32(i), Type: "cow"}
			}
		} else if capacity == "projectile" {
			for i := 0; i < 512; i++ {
				r.rt().projectiles[uint32(i)] = &Projectile{}
			}
		} else {
			r.Edits[blockKey(13, 60, 10)] = blkRail
			storeRail(r, blockKey(13, 60, 10), &RailState{Kind: "rail", Shape: "east_west"})
			for i := 1; i <= maxWorldVehicles; i++ {
				vehicleStore(r)[vehicleNamespace|uint32(i)] = &Vehicle{X: 2, Y: 60, Z: 2}
			}
		}
		before := cloneStacks(r.Containers[k].Slots)
		if _, handled, ok := s.prepareDispenserEntity(r, k, state, one, data); !handled || ok || !reflect.DeepEqual(before, r.Containers[k].Slots) || r.NextMobID != 1 || r.Ext.NextVehicleID != 0 {
			t.Fatalf("%s capacity partially committed", capacity)
		}
	}
	r, k, state, one, data := dispenserEntityFixture(s, 420, 2)
	r.Edits[blockKey(13, 60, 10)] = 1
	if _, handled, ok := s.prepareDispenserEntity(r, k, state, one, data); !handled || ok || len(r.Mobs) != 0 {
		t.Fatal("obstructed egg invented an entity")
	}
}

func TestDispenserEggProjectileHatchFlagsGrowthAndPersistence(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, k, state, one, data := dispenserEntityFixture(s, itemEgg, 2)
	commitDispenserEntityFixture(t, s, r, k, state, one, data)
	var pr *Projectile
	for _, value := range r.rt().projectiles {
		pr = value
	}
	if pr == nil || pr.Type != projEgg || pr.Data != itemEgg || pr.VX != 1.1 || pr.Actor != "server" || pr.Source != "dispenser" || !reflect.DeepEqual(pr.Weapon.Meta, one.Meta) {
		t.Fatal("egg was ejected as an item or lost its projectile attribution")
	}
	pr.X, pr.Y, pr.Z = 13.5, 60.8, 10.5
	// Pick a deterministic four-chick roll, then run the actual block impact.
	for seed := int64(0); ; seed++ {
		rng := rand.New(rand.NewSource(seed))
		if rng.Intn(8) == 0 && rng.Intn(32) == 0 {
			s.rng = rand.New(rand.NewSource(seed))
			break
		}
	}
	s.projectileHitBlock(r, pr, 13, 59, 10)
	if pr.Age != -1 || len(r.Mobs) != 4 {
		t.Fatal("four chick hatch failed")
	}
	for _, mob := range r.Mobs {
		if mob.Type != "chicken" || mob.Flags&mobFlagBaby == 0 || mob.GrowthTicks != 24000 || !validMob(mob) {
			t.Fatal("chick lost its persistent baby state")
		}
	}
	s.mobsTick(r)
	for _, mob := range r.Mobs {
		if mob.Flags&mobFlagBaby == 0 || mob.GrowthTicks != 23998 {
			t.Fatal("mob tick erased baby state or did not advance growth")
		}
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil || len(loader.rooms[r.ID].Mobs) != 4 {
		t.Fatalf("restart lost chicks: %v", err)
	}
	for _, m := range loader.rooms[r.ID].Mobs {
		m.GrowthTicks = 2
		s.growMob(r, m)
		if m.Flags&mobFlagBaby != 0 || m.GrowthTicks != 0 {
			t.Fatal("maturation did not commit")
		}
	}
}

func TestDispenserEntitiesPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "dispenser-entities-fixture" {
		return
	}
	p := api.New()
	names := []string{api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, api.ItemSpawnEggUsing, api.EntitySpawning, api.VehiclePlacing, api.RailChanging, api.ProjectileLaunching, api.ItemEggHatching, api.EntityGrowing, api.BlockDispensed, api.MechanicChanged, api.InventoryChanged, api.ItemSpawnEggUsed, api.EntitySpawned, api.VehiclePlaced, api.RailChanged, api.ProjectileLaunched, api.ItemEggHatched, api.EntityGrown}
	for _, name := range names {
		p.On(name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
			if value := e.Item("stack"); value != nil && value.Meta != nil && value.Meta.CustomData["test:cancel"] == e.Name || e.Name == api.EntityGrowing && e.Number("health") == 3 {
				e.Cancel("dispenser_entities.protected")
			}
			e.Data["type"], e.Data["count"], e.Data["flags"], e.Data["x"], e.Data["stack"] = "ender_dragon", 64, 0, 99999, &api.ItemStack{ID: 1, Count: 64}
			return nil
		})
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
			e.Data["type"] = "ender_dragon"
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func dispenserEntitiesPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "dispenser-entities")
	if err := os.MkdirAll(entry, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"dispenser-entities","name":"dispenser entities","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestDispenserEntitiesPluginProcess$","dispenser-entities-fixture"]}`
	if err := os.WriteFile(filepath.Join(entry, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func readDispenserEntityEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "dispenser-entities", "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []*api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event api.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		out = append(out, &event)
	}
	return out
}

func TestDispenserEntityPluginVetoReadonlyAndNoFalseSuccess(t *testing.T) {
	s := dispenserEntitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.VehiclePlacing, api.RailChanging, api.ProjectileLaunching} {
		id := 420
		if name == api.VehiclePlacing || name == api.RailChanging {
			id = itemMinecart
		} else if name == api.ProjectileLaunching {
			id = itemEgg
		}
		r, k, state, one, data := dispenserEntityFixture(s, id, 1)
		one.Meta.CustomData["test:cancel"] = name
		data["stack"] = itemSnapshot(one)
		if id == itemMinecart {
			r.Edits[blockKey(13, 60, 10)] = blkDetectorRail
			storeRail(r, blockKey(13, 60, 10), &RailState{Kind: "detector_rail", Shape: "east_west"})
		}
		start := len(readDispenserEntityEvents(t, s))
		if _, handled, ok := s.prepareDispenserEntity(r, k, state, one, data); !handled || ok || len(r.Mobs) != 0 || len(vehicleStore(r)) != 0 || len(r.rt().projectiles) != 0 || r.Containers[k].Slots[0].Count != 1 {
			t.Fatalf("%s veto partially committed an effect", name)
		}
		for _, event := range readDispenserEntityEvents(t, s)[start:] {
			if !strings.HasSuffix(event.Name, ".before") {
				t.Fatalf("cancelled effect emitted success %s", event.Name)
			}
		}
	}
	r, k, state, one, data := dispenserEntityFixture(s, 420, 2)
	start := len(readDispenserEntityEvents(t, s))
	commitDispenserEntityFixture(t, s, r, k, state, one, data)
	events := readDispenserEntityEvents(t, s)[start:]
	want := []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.EntitySpawned, api.ItemSpawnEggUsed}
	if len(events) != len(want) {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
	for i, event := range events {
		if event.Name != want[i] || event.Player != nil || event.String("actor") != "server" || event.String("source") != "dispenser" || event.String("type") != "zombie" || event.Number("x") != 13.5 || event.Item("stack").Meta.Lore[0] != "第一行" || event.Item("before").Count != 2 || event.Item("after").Count != 1 || event.Data["origin"] == nil {
			t.Fatalf("event missing readonly attribution/metadata: %+v", event)
		}
	}
	pr := &Projectile{Type: projEgg, Actor: "server", Source: "dispenser", Weapon: itemSnapshot(customStack(itemEgg, 1))}
	pr.X, pr.Y, pr.Z = 14.5, 60.5, 10.5
	for _, name := range []string{api.ItemEggHatching, api.EntitySpawning} {
		pr.Weapon.Meta.CustomData["test:cancel"] = name
		start = len(readDispenserEntityEvents(t, s))
		if s.hatchEggCount(r, pr, 4) || len(r.Mobs) != 1 {
			t.Fatal("cancelled hatch produced partial chicks")
		}
		for _, e := range readDispenserEntityEvents(t, s)[start:] {
			if e.Name == api.EntitySpawned || e.Name == api.ItemEggHatched {
				t.Fatal("cancelled hatch published success")
			}
		}
	}
	delete(pr.Weapon.Meta.CustomData, "test:cancel")
	if !s.hatchEggCount(r, pr, 1) {
		t.Fatal("readonly/monitor attempts prevented successful hatch")
	}
	for _, m := range r.Mobs {
		if m.Type != "chicken" {
			continue
		}
		m.GrowthTicks, m.Health = 2, 3
		start = len(readDispenserEntityEvents(t, s))
		s.growMob(r, m)
		if m.GrowthTicks != 2 || m.Flags&mobFlagBaby == 0 {
			t.Fatal("cancelled growth committed")
		}
		for _, e := range readDispenserEntityEvents(t, s)[start:] {
			if e.Name == api.EntityGrown {
				t.Fatal("cancelled growth emitted success")
			}
		}
		m.Health = 4
		s.growMob(r, m)
		if m.GrowthTicks != 0 || m.Flags&mobFlagBaby != 0 {
			t.Fatal("successful growth did not commit")
		}
	}
}

func TestDispenserEntityIntegratedTransactionVetoAndEvents(t *testing.T) {
	s := dispenserEntitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging} {
		r, k, state, _, _ := dispenserEntityFixture(s, 420, 2)
		r.Containers[k].Slots[0].Meta.CustomData["test:cancel"] = name
		before := cloneStacks(r.Containers[k].Slots)
		start := len(readDispenserEntityEvents(t, s))
		if s.dispenseMechanic(r, k, state) || len(r.Mobs) != 0 || r.NextMobID != 1 || !reflect.DeepEqual(before, r.Containers[k].Slots) || mechanicSnapshot(r, k).Powered {
			t.Fatalf("%s veto consumed source inventory, signal or entity", name)
		}
		for _, event := range readDispenserEntityEvents(t, s)[start:] {
			if !strings.HasSuffix(event.Name, ".before") {
				t.Fatalf("cancelled transaction emitted success %s", event.Name)
			}
		}
	}
	r, k, state, _, _ := dispenserEntityFixture(s, 420, 2)
	start := len(readDispenserEntityEvents(t, s))
	if !s.dispenseMechanic(r, k, state) || r.Containers[k].Slots[0].Count != 1 || len(r.Mobs) != 1 || !mechanicSnapshot(r, k).Powered {
		t.Fatal("integrated egg transaction did not commit all its state")
	}
	want := []string{api.ItemSpawnEggUsing, api.EntitySpawning, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, api.InventoryChanged, api.MechanicChanged, api.BlockDispensed, api.EntitySpawned, api.ItemSpawnEggUsed}
	events := readDispenserEntityEvents(t, s)[start:]
	if len(events) != len(want) {
		t.Fatalf("unexpected integrated sequence (%d events)", len(events))
	}
	for i, event := range events {
		if event.Name != want[i] || event.Item("stack").Meta.Lore[0] != "第一行" || event.Item("before").Count != 2 || event.Item("after").Count != 1 || event.String("actor") != "server" || event.String("source") != "dispenser" {
			t.Fatalf("wrong committed event %d: %+v", i, event)
		}
		if i >= 5 {
			var after Stacks
			raw, err := json.Marshal(event.Data["containerAfter"])
			if err != nil || json.Unmarshal(raw, &after) != nil || len(after) != 9 || after[0].Count != 1 {
				t.Fatal("success omitted full committed container snapshot")
			}
		}
	}
}

func TestDispenserSDKContainerCASProtectionAndNoPlayerDependency(t *testing.T) {
	s := dispenserEntitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, k, _, _, _ := dispenserEntityFixture(s, 420, 2)
	s.rooms[r.ID] = r
	for p := range r.Peers {
		delete(r.Peers, p)
	}
	expected := make([]*api.ItemStack, 9)
	for i, value := range r.Containers[k].Slots {
		expected[i] = itemSnapshot(value)
	}
	c := &api.Context{}
	c.DispenseBlock(s.event(api.CommandExecuting, r, nil, nil), 12, 60, 10, expected)
	if len(c.Actions) != 1 {
		t.Fatal("SDK refused a world operation without player")
	}
	a := c.Actions[0]
	// Matching IDs/counts do not permit an old Lore snapshot to overwrite a
	// moved or modified item; a fractional coordinate is also never coerced.
	r.Containers[k].Slots[0].Meta.Lore[0] = "changed"
	s.applyAction(a)
	if r.Containers[k].Slots[0].Count != 2 || len(r.Mobs) != 0 {
		t.Fatal("stale metadata snapshot dispensed")
	}
	r.Containers[k].Slots[0].Meta.Lore[0] = "第一行"
	bad := a
	bad.X = 12.5
	s.applyAction(bad)
	if r.Containers[k].Slots[0].Count != 2 || len(r.Mobs) != 0 {
		t.Fatal("fractional coordinate bypassed validation")
	}
	s.pluginSource = "plugin:dispenser-entities"
	start := len(readDispenserEntityEvents(t, s))
	s.applyAction(a)
	if r.Containers[k].Slots[0].Count != 1 || len(r.Mobs) != 1 {
		t.Fatal("valid SDK intent did not use normal protected transaction")
	}
	for _, event := range readDispenserEntityEvents(t, s)[start:] {
		if event.Source != "plugin:dispenser-entities" || event.Item("stack").Meta.Lore[0] != "第一行" {
			t.Fatal("SDK lost plugin provenance or item metadata")
		}
	}
	s.applyAction(a)
	if r.Containers[k].Slots[0].Count != 1 || len(r.Mobs) != 1 {
		t.Fatal("replayed SDK intent consumed changed source contents")
	}
	// A fresh intent is still subject to the operation's inventory protection.
	r, k, _, _, _ = dispenserEntityFixture(s, 420, 2)
	s.rooms[r.ID] = r
	r.Containers[k].Slots[0].Meta.CustomData["test:cancel"] = api.InventoryChanging
	for i, value := range r.Containers[k].Slots {
		expected[i] = itemSnapshot(value)
	}
	c = &api.Context{}
	c.DispenseBlock(s.event(api.CommandExecuting, r, nil, nil), 12, 60, 10, expected)
	start = len(readDispenserEntityEvents(t, s))
	s.applyAction(c.Actions[0])
	if r.Containers[k].Slots[0].Count != 2 || len(r.Mobs) != 0 || r.NextMobID != 1 {
		t.Fatal("SDK bypassed protection")
	}
	for _, event := range readDispenserEntityEvents(t, s)[start:] {
		if !strings.HasSuffix(event.Name, ".before") {
			t.Fatal("cancelled SDK intent published success")
		}
	}
}

func TestDispenserProjectileContextSnapshotsRemainIndependent(t *testing.T) {
	pr := &Projectile{Type: projEgg, Weapon: itemSnapshot(customStack(itemEgg, 1)), Shooter: &api.Combatant{Kind: "block", X: 12, Y: 60, Z: 10}, Actor: "server", Source: "dispenser"}
	data := projectileData(pr, "block_hit")
	data["origin"].(*api.Combatant).X = 99
	data["attacker"].(*api.Combatant).Y = 99
	data["stack"].(*api.ItemStack).Meta.Lore[0] = "changed"
	if pr.Shooter.X != 12 || pr.Shooter.Y != 60 || pr.Weapon.Meta.Lore[0] != "第一行" || data["weapon"].(*api.ItemStack).Meta.Lore[0] != "第一行" {
		t.Fatal("delayed projectile context aliases its source or item snapshots")
	}
}
