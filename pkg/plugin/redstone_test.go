package plugin

import "testing"

func TestRedstoneActionSnapshotAndCatalog(t *testing.T) {
	item := &ItemStack{ID: 356, Count: 2, Meta: &ItemMeta{Lore: []string{"原始 Lore"}, CustomData: map[string]any{"test:data": map[string]any{"value": 1}}}}
	e := &Event{World: &World{ID: "world"}, Player: &Player{ID: 7, Selected: 0, Items: []*ItemStack{item}}}
	c := &Context{}
	c.UseRedstone(e, -2, 60, 3, 1)
	if len(c.Actions) != 1 {
		t.Fatal("missing action")
	}
	a := c.Actions[0]
	if a.Type != "use_redstone" || a.WorldID != "world" || a.PlayerID != 7 || a.Slot != 0 || a.X != -2 || a.Y != 60 || a.Z != 3 || a.Face != 1 || a.Expected.ID != 356 {
		t.Fatal("incorrect action", a)
	}
	item.Meta.Lore[0] = "changed"
	item.Meta.CustomData["test:data"].(map[string]any)["value"] = 2
	if a.Expected.Meta.Lore[0] != "原始 Lore" || a.Expected.Meta.CustomData["test:data"].(map[string]any)["value"] != float64(1) {
		t.Fatal("deferred action aliases item metadata")
	}
	c.UseRedstone(&Event{}, 0, 0, 0, 0)
	if len(c.Actions) != 1 {
		t.Fatal("invalid player enqueued action")
	}
	beforeFound, usedFound := false, false
	for _, spec := range Events() {
		if spec.Name == RedstoneComponentChanging {
			beforeFound = spec.Cancellable && len(spec.Mutable) == 0
		}
		if spec.Name == RedstoneComponentChanged {
			usedFound = !spec.Cancellable && len(spec.Mutable) == 0
		}
	}
	if !beforeFound || !usedFound {
		t.Fatal("redstone events missing or invalid")
	}
}
