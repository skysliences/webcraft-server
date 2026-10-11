package plugin

import "testing"

func TestAutomationSDKCopiesSnapshotsAndReadonlyCatalog(t *testing.T) {
	item := &ItemStack{ID: 385, Count: 2, Meta: &ItemMeta{Lore: []string{"lore"}, CustomData: map[string]any{"test:nested": map[string]any{"list": []any{"value"}}}}, Ench: map[string]int{"unbreaking": 1}}
	e := &Event{World: &World{ID: "world"}, Player: &Player{ID: 7, Selected: 0, Items: []*ItemStack{item}}}
	c := &Context{}
	slots := make([]*ItemStack, 5)
	slots[0] = item
	c.ActivateHopper(e, 1, 60, 2, slots)
	c.FeedAnimal(e, 123)
	item.Meta.Lore[0] = "changed"
	item.Ench["unbreaking"] = 3
	item.Meta.CustomData["test:nested"].(map[string]any)["list"].([]any)[0] = "changed"
	if len(c.Actions) != 2 || c.Actions[0].ContainerExpected[0].Meta.Lore[0] != "lore" || c.Actions[1].Expected.Ench["unbreaking"] != 1 || c.Actions[1].Expected.Meta.CustomData["test:nested"].(map[string]any)["list"].([]any)[0] != "value" {
		t.Fatal("automation actions aliased their input")
	}
	names := map[string]bool{InventoryMoving: true, InventoryMoved: false, InventoryPickingUp: true, InventoryPickedUp: false, AnimalFeeding: true, AnimalFed: false, AnimalBreeding: true, AnimalBred: false}
	for _, spec := range Events() {
		if before, ok := names[spec.Name]; ok {
			if spec.Cancellable != before || len(spec.Mutable) != 0 || len(spec.Fields) == 0 {
				t.Fatal("missing readonly event contract", spec.Name)
			}
			delete(names, spec.Name)
		}
	}
	if len(names) != 0 {
		t.Fatal("missing automation events", names)
	}
}
