package plugin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorldPhysicsEventCatalogAndPrimeSnapshot(t *testing.T) {
	want := map[string]EventSpec{
		LeavesDecaying: {Name: LeavesDecaying, Cancellable: true},
		LeavesDecayed:  {Name: LeavesDecayed},
		TNTPriming:     {Name: TNTPriming, Cancellable: true, Mutable: []string{"fuseTicks"}},
		TNTPrimed:      {Name: TNTPrimed},
	}
	for _, spec := range Events() {
		if expected, ok := want[spec.Name]; ok {
			if !reflect.DeepEqual(spec, expected) {
				t.Fatal("unstable world physics contract", spec)
			}
			delete(want, spec.Name)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing world physics events", want)
	}
	item := &ItemStack{ID: 305, Count: 1, Meta: &ItemMeta{DisplayName: "测试点火器", Lore: []string{"original"}, CustomData: map[string]any{"test:origin": map[string]any{"id": 7}}}}
	e := &Event{World: &World{ID: "world"}, Player: &Player{ID: 7, Selected: 0, Items: []*ItemStack{item}}}
	c := &Context{}
	c.PrimeTNT(e, -2, 80, 3, 40)
	if len(c.Actions) != 1 {
		t.Fatal("missing TNT action")
	}
	a := c.Actions[0]
	if a.Type != "prime_tnt" || a.WorldID != "world" || a.PlayerID != 7 || a.Slot != 0 || a.X != -2 || a.Y != 80 || a.Z != 3 || a.FuseTicks != 40 || a.Expected.ID != 305 {
		t.Fatal("incorrect TNT action", a)
	}
	item.Meta.Lore[0] = "changed"
	item.Meta.CustomData["test:origin"].(map[string]any)["id"] = 9
	if a.Expected.Meta.Lore[0] != "original" || a.Expected.Meta.CustomData["test:origin"].(map[string]any)["id"] != float64(7) {
		t.Fatal("deferred TNT action aliases item metadata")
	}
	c.PrimeTNT(nil, 0, 0, 0, 40)
	c.PrimeTNT(&Event{}, 0, 0, 0, 40)
	if len(c.Actions) != 1 {
		t.Fatal("invalid context enqueued TNT action")
	}
}

func TestDelayedDamageStableIdentityRoundtrip(t *testing.T) {
	ctx := DamageContext{Cause: "explosion", Actor: "fixed-account", Source: "plugin:example", Attacker: &Combatant{Kind: "player", PlayerID: 7, Name: "original"}, Weapon: &ItemStack{ID: 305, Count: 1, Meta: &ItemMeta{Lore: []string{"origin"}}}}
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var restored DamageContext
	if err = json.Unmarshal(raw, &restored); err != nil || !reflect.DeepEqual(ctx, restored) {
		t.Fatal("delayed damage lost attribution or item snapshot", restored, err)
	}
}
