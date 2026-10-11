package plugin

import "testing"

func TestPlacementSnapshotDoesNotMutateHost(t *testing.T) {
	v := &ItemStack{ID: 1, Count: 2, Meta: &ItemMeta{Lore: []string{"kept"}, CustomData: map[string]any{"test:flag": true}}}
	e := &Event{Name: BlockPlacing, Data: map[string]any{"x": 12, "y": 60, "z": 10, "block": 1, "oldBlock": 0, "reason": "place", "actor": "player-id", "count": 1, "consumed": 1, "stack": v, "before": v, "after": &ItemStack{ID: 1, Count: 1, Meta: v.Meta}}}
	p := e.BlockPlacement()
	if p == nil || p.Actor != "player-id" || p.Block != 1 || p.Consumed != 1 {
		t.Fatal("missing snapshot")
	}
	p.Stack.Meta.Lore[0] = "bad"
	p.Before.Meta.CustomData["test:flag"] = false
	if v.Meta.Lore[0] != "kept" || v.Meta.CustomData["test:flag"] != true {
		t.Fatal("SDK snapshot leaked mutation")
	}
	for _, spec := range Events() {
		if spec.Name == BlockPlacing {
			if !spec.Cancellable || len(spec.Mutable) != 0 || len(spec.Fields) < 10 {
				t.Fatal("placement protection contract changed")
			}
		}
	}
	delete(e.Data, "consumed")
	if e.BlockPlacement().Consumed != 1 {
		t.Fatal("legacy component snapshot lost consumption")
	}
	e.Name = InventoryChanging
	if p := e.PlacementInventory(); p == nil || p.Consumed != 1 || p.After.Count != 1 {
		t.Fatal("inventory placement snapshot lost consumption")
	}
	e.Data["reason"] = "click"
	if e.PlacementInventory() != nil {
		t.Fatal("unrelated inventory change decoded as placement")
	}
	e.Name = ItemPickedUp
	if e.BlockPlacement() != nil {
		t.Fatal("unrelated event decoded as placement")
	}
}
