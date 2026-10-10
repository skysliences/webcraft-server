package plugin

import "testing"

func TestSpawnEggActionSnapshotAndCatalog(t *testing.T) {
	item := &ItemStack{ID: 420, Count: 2, Meta: &ItemMeta{Lore: []string{"原始 Lore"}, CustomData: map[string]any{"test:data": map[string]any{"value": 1}}}}
	e := &Event{World: &World{ID: "world"}, Player: &Player{ID: 7, Items: []*ItemStack{item}}}
	c := &Context{}
	c.UseSpawnEgg(e, 0, -2, 60, 3, 1)
	if len(c.Actions) != 1 {
		t.Fatal("missing action")
	}
	a := c.Actions[0]
	if a.Type != "use_spawn_egg" || a.WorldID != "world" || a.PlayerID != 7 || a.Slot != 0 || a.X != -2 || a.Y != 60 || a.Z != 3 || a.Face != 1 || a.Expected.ID != 420 {
		t.Fatal("incorrect action", a)
	}
	item.Meta.Lore[0] = "changed"
	item.Meta.CustomData["test:data"].(map[string]any)["value"] = 2
	if a.Expected.Meta.Lore[0] != "原始 Lore" || a.Expected.Meta.CustomData["test:data"].(map[string]any)["value"] != float64(1) {
		t.Fatal("deferred action aliases item metadata")
	}
	c.UseSpawnEgg(e, 9, 0, 0, 0, 0)
	c.UseSpawnEgg(&Event{}, 0, 0, 0, 0, 0)
	if len(c.Actions) != 1 {
		t.Fatal("invalid player/slot enqueued action")
	}
	beforeFound, usedFound := false, false
	for _, spec := range Events() {
		if spec.Name == ItemSpawnEggUsing {
			beforeFound = spec.Cancellable && len(spec.Mutable) == 0
		}
		if spec.Name == ItemSpawnEggUsed {
			usedFound = !spec.Cancellable && len(spec.Mutable) == 0
		}
	}
	if !beforeFound || !usedFound {
		t.Fatal("spawn egg events missing or invalid")
	}
}
