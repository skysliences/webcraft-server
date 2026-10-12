package plugin

import "testing"

func TestDimensionSDKReadonlyCatalogAndDetachedSnapshot(t *testing.T) {
	e := &Event{World: &World{ID: "root.end"}, Player: &Player{ID: 4}, Data: map[string]any{"dimension": "end", "actor": "admin", "target": &Combatant{Kind: "player", PlayerID: 4}, "stack": &ItemStack{ID: 276, Count: 1, Meta: &ItemMeta{CustomData: map[string]any{"test:value": "original"}}}}}
	tr := e.PlayerTransfer()
	if tr == nil || tr.Dimension != "end" || tr.Actor != "admin" {
		t.Fatal(tr)
	}
	tr.Target.PlayerID = 5
	tr.Stack.Meta.CustomData["test:value"] = "changed"
	if e.PlayerTransfer().Target.PlayerID != 4 || e.PlayerTransfer().Stack.Meta.CustomData["test:value"] != "original" {
		t.Fatal("snapshot aliased metadata")
	}
	c := &Context{}
	c.ChangeDimension(e, "nether")
	c.ChangeDimension(nil, "end")
	if len(c.Actions) != 1 || c.Actions[0].Type != "change_dimension" || c.Actions[0].WorldID != "root.end" || c.Actions[0].PlayerID != 4 || c.Actions[0].Dimension != "nether" {
		t.Fatal(c.Actions)
	}
	wanted := map[string]bool{PlayerDimensionChanging: true, PlayerDimensionChanged: false, PlayerTeleporting: true, PlayerTeleported: false}
	for _, spec := range Events() {
		before, ok := wanted[spec.Name]
		if !ok {
			continue
		}
		if spec.Cancellable != before || len(spec.Mutable) != 0 {
			t.Fatal(spec)
		}
		for _, field := range []string{"actor", "targetAccountId", "stack", "dimension", "fromDimension", "fromX"} {
			found := false
			for _, f := range spec.Fields {
				if f == field {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s missing %s", spec.Name, field)
			}
		}
		delete(wanted, spec.Name)
	}
	if len(wanted) != 0 {
		t.Fatal(wanted)
	}
}
