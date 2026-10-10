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

func TestDispenserUtilitiesPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "utilities-fixture" {
		return
	}
	p := api.New()
	needed := map[string]bool{}
	for _, name := range []string{api.CropFertilizing, api.CropFertilized, api.CropGrowing, api.CropGrown, api.BlockDispensing, api.BlockDispensed, api.MechanicChanging, api.MechanicChanged, api.InventoryChanging, api.InventoryChanged, api.BlockPhysicsChanging, api.BlockPhysicsChanged, api.BlockPlacing, api.BlockPlaced, api.BlockChanged, api.AnimalShearing, api.AnimalSheared, api.EntityShearing, api.EntitySheared, api.ItemWearing, api.ItemWorn, api.ItemSpawning, api.ItemSpawned, api.PlayerEquipping, api.PlayerEquipped, api.ItemConsuming, api.ItemConsumed} {
		needed[name] = true
	}
	for _, spec := range api.Events() {
		if !needed[spec.Name] {
			continue
		}
		if spec.Cancellable {
			p.On(spec.Name, api.Subscription{}, func(c *api.Context, e *api.Event) error {
				if e.Source == "plugin:utility-cancel:"+e.Name {
					e.Cancel("utilities.protected")
				}
				if e.Name == api.ItemWearing && e.Source == "plugin:utility-wear-two" {
					e.Data["amount"] = 2
				}
				if e.Name == api.ItemWearing && e.Source == "plugin:utility-wear-invalid" {
					e.Data["amount"] = -1
				}
				e.Data["actor"] = "wrong"
				e.Data["source"] = "wrong"
				e.Data["target"] = map[string]any{"kind": "wrong"}
				e.Data["stack"] = &api.ItemStack{ID: 1, Count: 64}
				return nil
			})
		}
		p.On(spec.Name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
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
			e.Data["actor"] = "monitor"
			return err
		})
	}
	if p.Run() != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func utilitiesPluginApp(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "utilities-test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), bin, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"utilities-test","name":"utilities-test","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestDispenserUtilitiesPluginProcess$","utilities-fixture"]}`
	if err = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{DataDir: t.TempDir(), EngineFile: "../../../public/engine.wasm", PluginDir: root, PluginTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if s.plugins.Failed() != 0 || len(s.plugins.Status()) != 1 {
		t.Fatalf("utilities fixture did not load: %+v", s.plugins.Status())
	}
	return s
}

func utilitiesEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "utilities-test", "events.jsonl"))
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

func utilitiesWorld(s *Server, item int) (*Room, *Peer, uint64) {
	r, p := redstoneTestPeer(s, item)
	k := blockKey(12, 60, 10)
	r.Edits[k] = blkDispenser
	storeMechanic(r, k, &MechanicState{Kind: "dispenser", Facing: 5})
	count := 1
	if s.rules.max(item) > 1 {
		count = 5
	}
	r.Containers[k] = &Container{Kind: "dispenser", Slots: make(Stacks, 9)}
	r.Containers[k].Slots[0] = customStack(item, count)
	s.rooms[r.ID] = r
	return r, p, k
}

func assertUtilitiesNoSuccess(t *testing.T, events []*api.Event) {
	t.Helper()
	for _, e := range events {
		switch e.Name {
		case api.BlockDispensed, api.MechanicChanged, api.InventoryChanged, api.CropGrown, api.CropFertilized, api.AnimalSheared, api.EntitySheared, api.ItemWorn, api.ItemSpawned, api.PlayerEquipped, api.BlockPlaced, api.BlockChanged, api.BlockPhysicsChanged:
			t.Fatalf("cancelled utility sent success %s", e.Name)
		}
	}
}

func TestDispenserBoneMealProtectionAndNoOp(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rules.byName["Grass Block"] != blkGrassBlock || s.rules.byName["Dirt"] != blkDirt || s.rules.byName["White Wool"] != itemWhiteWool {
		t.Fatal("farming/shearing constants differ from actual native registry")
	}
	for _, event := range []string{api.CropFertilizing, api.CropGrowing, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging, api.BlockPhysicsChanging, api.BlockPlacing} {
		r, _, k := utilitiesWorld(s, itemBoneMeal)
		r.Edits[blockKey(13, 60, 10)] = blkWheatMin
		before := copyStack(r.Containers[k].Slots[0])
		s.pluginSource = "plugin:utility-cancel:" + event
		n := len(utilitiesEvents(t, s))
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.block(r, 13, 60, 10) != blkWheatMin || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || mechanicSnapshot(r, k).Powered {
			t.Fatalf("%s fertilizer veto partially committed", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
	s.pluginSource = ""
	for _, b := range []int{0, 1, blkWheatMax, blkCarrotMax, blkPotatoMax} {
		r, _, k := utilitiesWorld(s, itemBoneMeal)
		r.Edits[blockKey(13, 60, 10)] = b
		before := copyStack(r.Containers[k].Slots[0])
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
			t.Fatalf("ineligible block%d consumed/ejected fertilizer", b)
		}
	}
	for _, b := range []int{blkWheatMin, blkCarrotMin, blkPotatoMin} {
		r, _, k := utilitiesWorld(s, itemBoneMeal)
		r.Edits[blockKey(13, 60, 10)] = b
		before := copyStack(r.Containers[k].Slots[0])
		n := len(utilitiesEvents(t, s))
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.block(r, 13, 60, 10) <= b || r.Containers[k].Slots[0].Count != 4 || !same(before, r.Containers[k].Slots[0]) {
			t.Fatalf("crop%d fertilizer failed", b)
		}
		seen := false
		for _, e := range utilitiesEvents(t, s)[n:] {
			if e.Name == api.CropFertilized {
				seen = true
				if e.String("actor") != "server" || e.String("source") != "dispenser" || e.Data["origin"] == nil || e.Item("stack") == nil || e.Item("stack").Meta == nil {
					t.Fatal("fertilizer attribution/meta missing")
				}
			}
		}
		if !seen {
			t.Fatal("fertilizer success absent")
		}
	}
}

func TestDispenserTreeAndGrassGrowthAreAtomic(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sapling := range []int{blkOakSapling, blkBirchSapling, blkSpruceSapling} {
		r, _, k := utilitiesWorld(s, itemBoneMeal)
		for x := 8; x < 21; x++ {
			for z := 5; z < 17; z++ {
				for y := 60; y < 74; y++ {
					r.Edits[blockKey(x, y, z)] = 0
				}
			}
		}
		r.Edits[blockKey(13, 60, 10)] = sapling
		r.Edits[k] = blkDispenser
		s.pluginSource = "plugin:utility-cancel:" + api.BlockPlacing
		before := len(r.Edits)
		n := len(utilitiesEvents(t, s))
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || len(r.Edits) != before || s.block(r, 13, 60, 10) != sapling || r.Containers[k].Slots[0].Count != 5 {
			t.Fatal("tree veto grew/consumed partially")
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
		s.pluginSource = ""
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !s.leafLog(s.block(r, 13, 60, 10)) || r.Containers[k].Slots[0].Count != 4 {
			t.Fatalf("sapling%d tree failed", sapling)
		}
	}
	r, _, k := utilitiesWorld(s, itemBoneMeal)
	for x := 8; x < 18; x++ {
		for z := 6; z < 15; z++ {
			r.Edits[blockKey(x, 60, z)] = blkGrassBlock
			r.Edits[blockKey(x, 61, z)] = 0
		}
	}
	r.Edits[k] = blkDispenser
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) {
		t.Fatal("grass fertilizer failed")
	}
	count := 0
	for _, b := range r.Edits {
		if b >= 63 && b <= 66 {
			count++
		}
	}
	if count == 0 {
		t.Fatal("grass fertilizer generated no vegetation")
	}
	r, _, k = utilitiesWorld(s, itemBoneMeal)
	r.Edits[blockKey(13, 60, 10)] = 63
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.block(r, 13, 60, 10) != 74 || s.block(r, 13, 61, 10) != 75 {
		t.Fatal("short grass did not become protected two-block grass")
	}
}

func TestDispenserShearingProtectionDurabilityAndMetadata(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.AnimalShearing, api.EntityShearing, api.ItemWearing, api.ItemSpawning, api.BlockDispensing, api.MechanicChanging, api.InventoryChanging} {
		r, _, k := utilitiesWorld(s, 304)
		mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
		r.Mobs[mob.ID] = mob
		before := copyStack(r.Containers[k].Slots[0])
		next := r.NextDropID
		s.pluginSource = "plugin:utility-cancel:" + event
		n := len(utilitiesEvents(t, s))
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || mob.Flags != 0 || len(r.Drops) != 0 || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || r.NextDropID != next || mechanicSnapshot(r, k).Powered {
			t.Fatalf("%s shear veto partially committed", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
	for _, source := range []string{"plugin:utility-wear-invalid", "plugin:utility-wear-two", ""} {
		r, _, k := utilitiesWorld(s, 304)
		mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
		r.Mobs[mob.ID] = mob
		s.pluginSource = source
		before := copyStack(r.Containers[k].Slots[0])
		ok := s.dispenseMechanic(r, k, mechanicSnapshot(r, k))
		if source == "plugin:utility-wear-invalid" {
			if ok || mob.Flags != 0 || len(r.Drops) != 0 {
				t.Fatal("invalid wear was accepted")
			}
			continue
		}
		wear := 1
		if source == "plugin:utility-wear-two" {
			wear = 2
		}
		if !ok || mob.Flags&mobFlagSheared == 0 || len(r.Drops) != 1 || r.Containers[k].Slots[0].Damage != wear || !reflect.DeepEqual(before.Meta, r.Containers[k].Slots[0].Meta) {
			t.Fatal("shear success/wear/metadata incorrect")
		}
		for _, d := range r.Drops {
			if d.Stack.ID != itemWhiteWool || d.Stack.Count < 1 || d.Stack.Count > 3 || d.Stack.Meta != nil {
				t.Fatal("shears metadata incorrectly leaked into wool")
			}
		}
	}
	s.pluginSource = ""
	r, _, k := utilitiesWorld(s, 304)
	before := copyStack(r.Containers[k].Slots[0])
	if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
		t.Fatal("empty shears ejected/wore")
	}
	r, _, k = utilitiesWorld(s, 304)
	r.Containers[k].Slots[0].Damage = 237
	mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
	r.Mobs[mob.ID] = mob
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || r.Containers[k].Slots[0] != nil || mob.Flags&mobFlagSheared == 0 {
		t.Fatal("shears breaking did not commit with successful shear")
	}
}

func TestDispenserEquipmentAndStorage(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.PlayerEquipping, api.InventoryChanging, api.BlockDispensing, api.MechanicChanging} {
		r, p, k := utilitiesWorld(s, 297)
		p.state.X = 13.5
		before := copyStack(r.Containers[k].Slots[0])
		s.pluginSource = "plugin:utility-cancel:" + event
		n := len(utilitiesEvents(t, s))
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || p.state.Inv.Slots[39] != nil || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) {
			t.Fatalf("%s equip veto partially committed", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
	s.pluginSource = ""
	for id := 281; id <= 300; id++ {
		r, p, k := utilitiesWorld(s, id)
		p.state.X = 13.5
		original := copyStack(r.Containers[k].Slots[0])
		slot := s.equipmentSlot(original)
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(p.state.Inv.Slots[slot], original) || r.Containers[k].Slots[0] != nil {
			t.Fatalf("armor%d auto-equipment failed", id)
		}
	}
	r, p, k := utilitiesWorld(s, 364)
	p.state.X = 13.5
	original := copyStack(r.Containers[k].Slots[0])
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || p.state.Inv.Slots[39].Count != 1 || !same(p.state.Inv.Slots[39], original) || r.Containers[k].Slots[0].Count != 4 {
		t.Fatal("head decoration failed")
	}
	r.Players[p.sid] = p.state
	r.Dirty = true
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	restored := loader.rooms[r.ID]
	if !reflect.DeepEqual(restored.Players[p.sid].Inv.Slots[39], p.state.Inv.Slots[39]) || !reflect.DeepEqual(restored.Containers[k].Slots, r.Containers[k].Slots) {
		t.Fatal("equipped/source metadata lost after restart")
	}
	for _, id := range []int{297, 303} {
		r, p, k := utilitiesWorld(s, id)
		p.state.X = 13.5
		if id == 297 {
			p.state.Inv.Slots[39] = stack(293, 1, 0)
		}
		before := copyStack(r.Containers[k].Slots[0])
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || len(r.Drops) != 1 || r.Containers[k].Slots[0] != nil {
			t.Fatal("unusable equipment did not fall back to a drop")
		}
		for _, d := range r.Drops {
			if !reflect.DeepEqual(d.Stack, before) {
				t.Fatal("fallback drop lost metadata")
			}
		}
		if p.state.Inv.Slots[40] != nil {
			t.Fatal("shield incorrectly auto-equipped")
		}
	}
}

func TestPlayerUtilityPathsShareProtection(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.CropFertilizing, api.CropGrowing, api.ItemConsuming, api.InventoryChanging, api.BlockPhysicsChanging, api.BlockPlacing} {
		r, p := redstoneTestPeer(s, itemBoneMeal)
		r.Edits[blockKey(12, 60, 10)] = blkOakSapling
		before := copyStack(s.heldItem(p))
		s.pluginSource = "plugin:utility-cancel:" + event
		n := len(utilitiesEvents(t, s))
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 60, Z: 10, F: 1})
		if s.block(r, 12, 60, 10) != blkOakSapling || !reflect.DeepEqual(before, s.heldItem(p)) {
			t.Fatalf("player fertilizer %s bypassed protection", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
	for _, event := range []string{api.AnimalShearing, api.EntityShearing, api.ItemWearing, api.ItemSpawning, api.InventoryChanging} {
		r, p := redstoneTestPeer(s, 304)
		p.state.Inv.Slots[0] = customStack(304, 1)
		mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 12.5, Y: 60, Z: 10.5, Health: 8}
		r.Mobs[mob.ID] = mob
		before := copyStack(s.heldItem(p))
		s.pluginSource = "plugin:utility-cancel:" + event
		next := r.NextDropID
		n := len(utilitiesEvents(t, s))
		s.survivalMessage(p, Message{T: "animal", E: mob.ID})
		if mob.Flags != 0 || len(r.Drops) != 0 || !reflect.DeepEqual(before, s.heldItem(p)) || r.NextDropID != next {
			t.Fatalf("player shear %s bypassed protection", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
}

func TestDispenserUtilitySnapshotsAndRetainedTools(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, k := utilitiesWorld(s, 297)
	p.state.X = 13.5
	r.Containers[k].Slots[0].Meta.Attributes = []api.AttributeModifier{{ID: "test:health", Attribute: api.MaxHealth, Slot: "head", Amount: 4, Operation: api.Add}}
	before := copyStack(r.Containers[k].Slots[0])
	n := len(utilitiesEvents(t, s))
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || s.maxHealth(p) != 24 || !reflect.DeepEqual(before, p.state.Inv.Slots[39]) {
		t.Fatal("equipment attributes or metadata were lost")
	}
	seen := false
	for _, e := range utilitiesEvents(t, s)[n:] {
		if e.Name == api.PlayerEquipped {
			seen = true
			target, _ := e.Data["target"].(map[string]any)
			if e.String("actor") != "server" || e.String("source") != "dispenser" || target["kind"] != "player" || e.Data["origin"] == nil || e.Data["inventoryAfter"] == nil || !reflect.DeepEqual(e.Item("after"), itemSnapshot(before)) || e.Player == nil || e.Player.Items[39] == nil {
				t.Fatal("equipment event missing attribution/state/item snapshot")
			}
		}
	}
	if !seen {
		t.Fatal("equipment event absent")
	}
	for _, flag := range []byte{mobFlagBaby, mobFlagSheared} {
		r, _, k := utilitiesWorld(s, 304)
		mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8, Flags: flag}
		r.Mobs[mob.ID] = mob
		before := copyStack(r.Containers[k].Slots[0])
		if s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
			t.Fatal("ineligible sheep consumed tool or produced wool")
		}
	}
	for _, unbreakable := range []bool{false, true} {
		r, _, k := utilitiesWorld(s, 304)
		tool := r.Containers[k].Slots[0]
		if unbreakable {
			tool.Meta.Unbreakable = true
		} else {
			tool.Ench = map[string]int{"unbreaking": 3}
			s.rng = rand.New(rand.NewSource(1))
		}
		mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
		r.Mobs[mob.ID] = mob
		before := copyStack(tool)
		if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) || !reflect.DeepEqual(before, r.Containers[k].Slots[0]) || mob.Flags&mobFlagSheared == 0 {
			t.Fatal("unbreakable/unbreaking dispenser tool changed incorrectly")
		}
	}
	r, _, k = utilitiesWorld(s, 304)
	tool := r.Containers[k].Slots[0]
	tool.Damage = 20
	mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
	r.Mobs[mob.ID] = mob
	if !s.dispenseMechanic(r, k, mechanicSnapshot(r, k)) {
		t.Fatal("storage shear failed")
	}
	r.Dirty = true
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, engine: s.engine}
	if err := loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	restored := loader.rooms[r.ID]
	if restored.Mobs[mob.ID] == nil || restored.Mobs[mob.ID].Flags&mobFlagSheared == 0 || !reflect.DeepEqual(restored.Containers[k].Slots, r.Containers[k].Slots) || len(restored.Drops) != 1 {
		t.Fatal("sheep flags, worn tool or wool drop lost on restart")
	}
}

func TestDispenserUtilityValidateRejectsChangedTargets(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := map[string]any{"actor": "server", "source": "dispenser", "slot": 0, "origin": map[string]any{"kind": "block", "x": 12, "y": 60, "z": 10}}
	r, _, k := utilitiesWorld(s, itemBoneMeal)
	r.Edits[blockKey(13, 60, 10)] = blkWheatMin
	original := copyStack(r.Containers[k].Slots[0])
	plan, handled, ok := s.prepareDispenserUtility(r, k, 13, 60, 10, sized(original, 1), original, ctx)
	if !ok || !handled || plan.Validate == nil || !plan.Validate() {
		t.Fatal("fertilizer validation was not prepared")
	}
	r.Edits[blockKey(13, 60, 10)] = blkDirt
	if plan.Validate() {
		t.Fatal("fertilizer accepted a changed source block")
	}
	r, p, k := utilitiesWorld(s, 297)
	p.state.X = 13.5
	original = copyStack(r.Containers[k].Slots[0])
	plan, handled, ok = s.prepareDispenserUtility(r, k, 13, 60, 10, sized(original, 1), original, ctx)
	if !ok || !handled || !plan.Validate() {
		t.Fatal("equipment validation was not prepared")
	}
	p.state.X = 15
	if plan.Validate() {
		t.Fatal("equipment accepted a wearer who moved away")
	}
	p.state.X = 13.5
	p.state.Inv.Slots[39] = stack(293, 1, 0)
	if plan.Validate() {
		t.Fatal("equipment overwrote a newly equipped item")
	}
	r, _, k = utilitiesWorld(s, 304)
	mob := &Mob{ID: mobNamespace + 1, Type: "sheep", X: 13.5, Y: 60, Z: 10.5, Health: 8}
	r.Mobs[mob.ID] = mob
	original = copyStack(r.Containers[k].Slots[0])
	plan, handled, ok = s.prepareDispenserUtility(r, k, 13, 60, 10, sized(original, 1), original, ctx)
	if !ok || !handled || !plan.Validate() {
		t.Fatal("sheep validation was not prepared")
	}
	mob.Flags = mobFlagSheared
	if plan.Validate() {
		t.Fatal("shearing accepted an already sheared sheep")
	}
	mob.Flags = 0
	mob.X = 20
	if plan.Validate() {
		t.Fatal("shearing accepted a sheep that moved away")
	}
	mob.X = 13.5
	delete(r.Mobs, mob.ID)
	if plan.Validate() {
		t.Fatal("shearing accepted a removed entity")
	}
	if !reflect.DeepEqual(original, r.Containers[k].Slots[0]) || len(r.Drops) != 0 {
		t.Fatal("utility preparation committed source inventory or drops")
	}
}
