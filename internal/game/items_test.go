package game

import (
	"encoding/json"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func customStack(id, count int) *Stack {
	return &Stack{ID: id, Count: count, Meta: &api.ItemMeta{DisplayName: "测试物品", Lore: []string{"第一行", "[吸血]"}, CustomData: map[string]any{"test:rate": .25}}}
}
func TestItemTupleCompatibilityAndDeepCopies(t *testing.T) {
	var old Stack
	if json.Unmarshal([]byte(`[276,1,3]`), &old) != nil || old.ID != 276 || old.Damage != 3 || old.Meta != nil {
		t.Fatal("legacy tuple lost")
	}
	v := customStack(276, 1)
	v.Meta.Attributes = []api.AttributeModifier{{ID: "test:power", Attribute: api.AttackDamage, Amount: 4, Operation: api.Add, Slot: "mainhand"}}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var restored Stack
	if json.Unmarshal(raw, &restored) != nil || !reflect.DeepEqual(v, &restored) {
		t.Fatal("metadata tuple round trip failed")
	}
	clone := sized(v, 2)
	clone.Meta.Lore[0] = "changed"
	clone.Meta.CustomData["test:rate"] = .9
	if v.Meta.Lore[0] != "第一行" || v.Meta.CustomData["test:rate"] != .25 || v.Count != 1 {
		t.Fatal("stack copy aliases metadata")
	}
	item := (&api.Event{Data: map[string]any{"stack": json.RawMessage(raw)}}).Item("stack")
	if !reflect.DeepEqual(item, itemSnapshot(v)) {
		t.Fatal("SDK could not decode compact metadata")
	}
}
func TestMetadataValidationAndStacking(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	inv := &Inventory{Slots: make(Stacks, 46)}
	a := customStack(s.rules.byName["Oak Planks"], 20)
	b := copyStack(a)
	b.Meta.Lore[0] = "不同的 Lore"
	s.add(inv, a)
	s.add(inv, b)
	if inv.Slots[0].Count != 20 || inv.Slots[1].Count != 20 {
		t.Fatal("different metadata merged")
	}
	s.add(inv, copyStack(a))
	if inv.Slots[0].Count != 40 {
		t.Fatal("same metadata did not merge")
	}
	for _, meta := range []*api.ItemMeta{
		{Lore: []string{strings.Repeat("字", 161)}},
		{CustomData: map[string]any{"not_namespaced": true}},
		{CustomData: map[string]any{"test:x": math.Inf(1)}},
		{Attributes: []api.AttributeModifier{{ID: "test:x", Attribute: api.AttackDamage, Amount: math.NaN(), Operation: api.Add, Slot: "mainhand"}}},
		{Attributes: []api.AttributeModifier{{ID: "test:x", Attribute: "bad:attribute", Amount: 1, Operation: api.Add, Slot: "mainhand"}}},
	} {
		if validMeta(meta) {
			t.Fatal("invalid metadata accepted", meta)
		}
	}
}
func TestMetadataSurvivesSplitChestDropPickupAndSave(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	v := customStack(s.rules.byName["Oak Planks"], 10)
	p.state.Inv.Slots[0] = v
	s.click(p, Message{T: "click", S: 0, B: 1})
	if p.state.Cursor.Count != 5 || !same(p.state.Cursor, v) {
		t.Fatal("right-click split lost metadata")
	}
	s.click(p, Message{T: "click", S: 1, B: 0})
	key := blockKey(12, 60, 12)
	s.openWindow(p, key, "chest", 0)
	s.click(p, Message{T: "click", W: p.window.ID, S: 1, M: 1})
	stored := r.Containers[key].Slots[0]
	if stored == nil || stored.Count != 5 || !same(stored, v) {
		t.Fatal("chest shift-click lost metadata")
	}
	s.closeWindow(p)
	drops, err := s.prepareDrops(r, p, Stacks{sized(stored, 3)}, 10.5, 60.125, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	s.publishDrops(r, p, drops, "test")
	d := drops[0]
	d.PickupDelayMS = 0
	s.pickupDrop(r, d)
	if len(r.Drops) != 0 || p.state.Inv.Slots[0].Count != 8 || !same(p.state.Inv.Slots[0], v) {
		t.Fatal("drop pickup lost metadata")
	}
	// Keep one custom ground stack, cursor and recovery item to cover every storage path.
	drops, err = s.prepareDrops(r, p, Stacks{sized(v, 2)}, 15.5, 60, 15.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	s.publishDrops(r, p, drops, "test")
	p.state.Cursor = sized(v, 1)
	p.state.Recovery = Stacks{sized(v, 1)}
	r.Players["ItemPersistPlayer123"] = p.state
	r.Dirty = true
	if err = s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.roomFile(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var saved diskRoom
	if json.Unmarshal(raw, &saved) != nil {
		t.Fatal("save could not decode")
	}
	if saved.Version < 6 || !same(saved.Players["ItemPersistPlayer123"].Inv.Slots[0], v) || !same(saved.Players["ItemPersistPlayer123"].Cursor, v) || !same(saved.Players["ItemPersistPlayer123"].Recovery[0], v) || !same(saved.Containers[key].Slots[0], v) || !same(saved.Drops[0].Stack, v) {
		t.Fatal("metadata missing from persisted storage")
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}}
	if err = loader.loadRooms(); err != nil {
		t.Fatal(err)
	}
	if !same(loader.rooms[r.ID].Drops[drops[0].ID].Stack, v) {
		t.Fatal("metadata not restored by world loader")
	}
}
func TestAttributesOperationsEquipmentAndHealth(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	v := customStack(s.rules.byName["Diamond Sword"], 1)
	v.Meta.Attributes = []api.AttributeModifier{
		{ID: "test:add", Attribute: api.AttackDamage, Amount: 3, Operation: api.Add, Slot: "mainhand"},
		{ID: "test:base", Attribute: api.AttackDamage, Amount: .5, Operation: api.MultiplyBase, Slot: "mainhand"},
		{ID: "test:total", Attribute: api.AttackDamage, Amount: 1, Operation: api.MultiplyTotal, Slot: "mainhand"},
		{ID: "test:health", Attribute: api.MaxHealth, Amount: 4, Operation: api.Add, Slot: "mainhand"},
		{ID: "test:speed", Attribute: api.AttackSpeed, Amount: 1 / .6, Operation: api.Add, Slot: "mainhand"},
	}
	p.state.Inv.Slots[0] = v
	attrs := s.attributes(p)
	if attrs[api.AttackDamage] != 30 || attrs[api.MaxHealth] != 24 || s.attackCooldown(p) != 300*time.Millisecond {
		t.Fatal("attribute order/cooldown incorrect", attrs)
	}
	s.heal(p, 20, "plugin")
	if p.state.Health != 24 {
		t.Fatal("custom max health not used")
	}
	p.state.Inv.Selected = 1
	s.inventory(p, 0, 0)
	if p.state.Health != 20 || s.attributes(p)[api.AttackDamage] != 1 {
		t.Fatal("inactive hand attribute remained active")
	}
	p.state.Inv.Slots[40] = copyStack(v)
	if s.maxHealth(p) != 20 {
		t.Fatal("mainhand modifier activated in offhand")
	}
	p.state.Inv.Slots[38] = customStack(s.rules.byName["Diamond Chestplate"], 1)
	p.state.Inv.Slots[38].Meta.Attributes = []api.AttributeModifier{{ID: "test:chest", Attribute: api.Armor, Amount: 2, Operation: api.Add, Slot: "chest"}}
	if s.attributes(p)[api.Armor] != 10 {
		t.Fatal("armor modifier did not apply")
	}
}
func readItemEvents(t *testing.T, s *Server) []*api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins/test/events.jsonl"))
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
func TestPluginAttackDamageContextAndCancellation(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	s.rooms[r.ID] = r
	p.name = "攻击者"
	p.state.Inv.Slots[0] = customStack(s.rules.byName["Diamond Sword"], 1)
	other := dropTestPlayer(r, 2, 11.5, 60, 10.5)
	other.name = "受伤者"
	r.Peers[other] = true
	defer delete(r.Peers, other)
	other.state.Inv.Slots[38] = stack(s.rules.byName["Diamond Chestplate"], 1, 0)
	p.state.Inv.Slots[0].Meta.CustomData["test:mode"] = "cancel_attack"
	s.survivalMessage(p, Message{T: "attack", P: other.id})
	if other.state.Health != 20 || p.state.Inv.Slots[0].Damage != 0 || !p.attackAt.IsZero() {
		t.Fatal("cancelled attack committed")
	}
	p.state.Inv.Slots[0].Meta.CustomData["test:mode"] = "scale_attack"
	s.survivalMessage(p, Message{T: "attack", P: other.id})
	if other.state.Health != 18 || p.state.Inv.Slots[0].Damage != 1 {
		t.Fatal("plugin-modified damage not committed")
	}
	events := readItemEvents(t, s)
	var before, after, attack *api.Event
	for _, e := range events {
		switch e.Name {
		case api.PlayerDamaging:
			before = e
		case api.PlayerDamaged:
			after = e
		case api.PlayerAttacked:
			attack = e
		}
	}
	if before == nil || after == nil || attack == nil || before.Damage.Attacker.PlayerID != p.id || before.Damage.Target.PlayerID != other.id || before.Damage.Cause != "player_attack" || before.Damage.Weapon.Meta.Lore[1] != "[吸血]" || before.Damage.Armor != 8 || after.Damage.AppliedAmount != 2 || after.Damage.HealthBefore != 20 || after.Damage.HealthAfter != 18 || attack.Player.ID != p.id || attack.Damage.Amount != 2 {
		t.Fatal("damage context missing or incorrect", before, after, attack)
	}
	// The fixture cancels death; no completed attack or durability event follows.
	p.attackAt = time.Time{}
	other.state.Health = 1
	s.survivalMessage(p, Message{T: "attack", P: other.id})
	if other.state.Health != 1 || other.state.Dead || p.state.Inv.Slots[0].Damage != 1 {
		t.Fatal("cancelled death changed state")
	}
}
func TestPluginItemMetaCompareAndSetDurabilityAndHealing(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	s.rooms[r.ID] = r
	p.state.Inv.Slots[0] = customStack(s.rules.byName["Diamond Sword"], 1)
	ctx := &api.Context{}
	e := s.event(api.CommandExecuting, r, p, nil)
	meta := &api.ItemMeta{DisplayName: "blocked"}
	ctx.SetItemMeta(e, 0, meta)
	s.applyAction(ctx.Actions[0])
	if p.state.Inv.Slots[0].Meta.DisplayName == "blocked" {
		t.Fatal("cancelled metadata change applied")
	}
	ctx.Actions = nil
	meta = &api.ItemMeta{DisplayName: "更新后的剑", Lore: []string{"新 Lore"}, CustomData: map[string]any{"test:cancel_wear": true}}
	ctx.SetItemMeta(e, 0, meta)
	action := ctx.Actions[0]
	p.state.Inv.Slots[0].Damage = 1
	s.applyAction(action)
	if p.state.Inv.Slots[0].Meta.DisplayName == meta.DisplayName {
		t.Fatal("stale compare-and-set overwrote changed stack")
	}
	ctx.Actions = nil
	ctx.SetItemMeta(s.event(api.CommandExecuting, r, p, nil), 0, meta)
	s.applyAction(ctx.Actions[0])
	if p.state.Inv.Slots[0].Meta.DisplayName != meta.DisplayName {
		t.Fatal("valid metadata action not applied")
	}
	s.wear(p, 1)
	if p.state.Inv.Slots[0].Damage != 1 {
		t.Fatal("cancelled durability event changed item")
	}
	p.state.Health = 10
	s.heal(p, 8, "cancel_heal")
	if p.state.Health != 10 {
		t.Fatal("cancelled heal changed health")
	}
	s.heal(p, 8, "scale_heal")
	if p.state.Health != 12 {
		t.Fatal("mutable heal ignored")
	}
	events := readItemEvents(t, s)
	found := map[string]bool{}
	for _, e := range events {
		found[e.Name] = true
	}
	if !found[api.ItemMetaChanged] || !found[api.ItemWearing] || !found[api.PlayerHealed] || !found[api.PlayerAttributesChanged] {
		t.Fatal("completed event missing", found)
	}
}

func TestPluginMobDamageContainsTargetWeaponAndActualLoss(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	s.rooms[r.ID] = r
	p.state.Inv.Slots[0] = customStack(s.rules.byName["Diamond Sword"], 1)
	m, err := s.spawnMob(r, p, "cow", 11.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	p.knownMobs = map[uint32]bool{m.ID: true}
	s.attackMob(p, m.ID)
	if m.Health != 3 || p.state.Inv.Slots[0].Damage != 1 {
		t.Fatal("mob attack did not commit")
	}
	p.attackAt = time.Time{}
	s.attackMob(p, m.ID)
	if r.Mobs[m.ID] != nil {
		t.Fatal("lethal mob attack did not remove entity")
	}
	events := readItemEvents(t, s)
	var dead, attack *api.Event
	for _, e := range events {
		if e.Name == api.EntityDead {
			dead = e
		}
		if e.Name == api.PlayerAttacked {
			attack = e
		}
	}
	if dead == nil || attack == nil || dead.Damage.Target.Kind != "entity" || dead.Damage.Target.EntityID != m.ID || dead.Damage.Target.EntityType != "cow" || dead.Damage.Attacker.PlayerID != p.id || dead.Damage.Weapon.Meta.DisplayName != "测试物品" || dead.Damage.AppliedAmount != 3 || attack.Damage.AppliedAmount != 3 || attack.Damage.Amount != 7 {
		t.Fatal("mob death/attack context lost actual damage", dead, attack)
	}
}
func TestItemVersion5UpgradeBackup(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("123456789abcdef0", "旧物品世界", "survival", 0)
	r.saveVersion = 5
	raw, _ := json.Marshal(diskRoom{Version: 5, Name: r.Name, Mode: r.Mode, Players: map[string]*Player{"LegacyItemPlayer123": {Inv: &Inventory{Slots: make(Stacks, 46)}}}})
	if err := os.WriteFile(s.roomFile(r.ID), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(s.roomFile(r.ID) + ".pre-item-meta-v5.bak")
	if err != nil || string(backup) != string(raw) {
		t.Fatal("original save not preserved")
	}
}
