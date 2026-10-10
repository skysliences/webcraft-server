package plugin

import "testing"

func TestDispenseBlockCompleteSnapshotAndNoPlayerRequirement(t *testing.T) {
	expected := make([]*ItemStack, 9)
	expected[3] = &ItemStack{ID: 420, Count: 2, Ench: map[string]int{"unbreaking": 1}, Meta: &ItemMeta{DisplayName: "named", Lore: []string{"lore"}, Attributes: []AttributeModifier{{ID: "test:power", Attribute: AttackDamage, Amount: 2, Operation: Add, Slot: "mainhand"}}, CustomData: map[string]any{"test:nested": map[string]any{"list": []any{"value"}}}}}
	c := &Context{}
	c.DispenseBlock(&Event{World: &World{ID: "world"}}, -20, 64, 32, expected)
	expected[3].Count = 1
	expected[3].Meta.Lore[0] = "changed"
	expected[3].Meta.Attributes[0].Amount = 999
	expected[3].Ench["unbreaking"] = 3
	expected[3].Meta.CustomData["test:nested"].(map[string]any)["list"].([]any)[0] = "changed"
	if len(c.Actions) != 1 {
		t.Fatal("world event without player did not create dispense intent")
	}
	a := c.Actions[0]
	v := a.ContainerExpected[3]
	if a.Type != "dispense_block" || a.WorldID != "world" || a.PlayerID != 0 || a.X != -20 || a.Y != 64 || a.Z != 32 || len(a.ContainerExpected) != 9 || a.ContainerExpected[0] != nil || v.Count != 2 || v.Meta.Lore[0] != "lore" || v.Meta.Attributes[0].Amount != 2 || v.Ench["unbreaking"] != 1 || v.Meta.CustomData["test:nested"].(map[string]any)["list"].([]any)[0] != "value" {
		t.Fatal("dispense intent lost metadata or aliases source snapshot")
	}
	c.DispenseBlock(nil, 0, 0, 0, expected)
	c.DispenseBlock(&Event{}, 0, 0, 0, expected)
	c.DispenseBlock(&Event{World: &World{ID: "world"}}, 0, 0, 0, expected[:8])
	if len(c.Actions) != 1 {
		t.Fatal("invalid dispense snapshot created an intent")
	}
}

func TestMechanicsSDKSnapshotsAndCatalog(t *testing.T) {
	e := &Event{World: &World{ID: "world"}, Player: &Player{ID: 2, Selected: 0, Items: []*ItemStack{{ID: 368, Count: 1, Meta: &ItemMeta{DisplayName: "cart", Lore: []string{"custom"}}}}}}
	c := &Context{}
	lines := []string{"one", "two", "three", "four"}
	c.UseVehicle(e, 0, -2, 61, 3, 1)
	c.UseMechanic(e, -2, 61, 3, 1)
	c.WriteSign(e, -2, 61, 3, lines)
	c.InteractVehicle(e, 0x20000001)
	e.Player.Items[0].Meta.Lore[0] = "changed"
	lines[0] = "changed"
	if len(c.Actions) != 4 || c.Actions[0].Expected.Meta.Lore[0] != "custom" || c.Actions[2].Lines[0] != "one" {
		t.Fatal("actions must carry independent snapshots")
	}
	c.UseVehicle(e, 1, 0, 0, 0, 0)
	c.UseMechanic(nil, 0, 0, 0, 0)
	c.WriteSign(nil, 0, 0, 0, nil)
	c.InteractVehicle(nil, 1)
	if len(c.Actions) != 4 {
		t.Fatal("invalid context created intent")
	}
	names := map[string]bool{}
	for _, s := range Events() {
		names[s.Name] = true
	}
	for _, name := range []string{MechanicChanging, MechanicChanged, RailChanging, RailChanged, BlockDispensing, BlockDispensed, SignChanging, SignChanged, VehiclePlacing, VehiclePlaced, VehicleEntering, VehicleEntered, VehicleExiting, VehicleExited, VehicleMoving, VehicleMoved, VehicleDestroying, VehicleDestroyed, FishingCasting, FishingCast, FishingBiting, FishingBit, FishingReeling, FishingReeled} {
		if !names[name] {
			t.Fatalf("missing catalog event %s", name)
		}
		for _, s := range Events() {
			if s.Name == name && (len(s.Mutable) != 0 || s.Cancellable != (len(name) > 7 && name[len(name)-7:] == ".before")) {
				t.Fatalf("incorrect event contract %+v", s)
			}
		}
	}
}
