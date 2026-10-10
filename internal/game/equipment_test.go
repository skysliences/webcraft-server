package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"testing"
)

func TestManualEquipmentSharesPluginProtection(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range []string{api.PlayerEquipping, api.InventoryChanging} {
		r, p := redstoneTestPeer(s, 297)
		p.state.Inv.Slots[0] = customStack(297, 1)
		p.state.Inv.Slots[0].Meta.Attributes = []api.AttributeModifier{{ID: "test:health", Attribute: api.MaxHealth, Slot: "head", Amount: 4, Operation: api.Add}}
		r.Dirty = false
		before := cloneStacks(p.state.Inv.Slots)
		s.pluginSource = "plugin:utility-cancel:" + event
		n := len(utilitiesEvents(t, s))
		s.survivalMessage(p, Message{T: "use", S: 0})
		if r.Dirty || !reflect.DeepEqual(before, p.state.Inv.Slots) || s.maxHealth(p) != 20 {
			t.Fatalf("%s manual equipment cancellation changed items/attributes", event)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
}

func TestManualEquipmentMetadataAttributesAndStackSplit(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mode := range []string{"survival", "creative"} {
		for _, id := range []int{297, 364} {
			r, p := redstoneTestPeer(s, id)
			p.ext().GameMode = mode
			count := 1
			if id == 364 {
				count = 4
			}
			p.state.Inv.Slots[0] = customStack(id, count)
			p.state.Inv.Slots[0].Meta.Attributes = []api.AttributeModifier{{ID: "test:health", Attribute: api.MaxHealth, Slot: "head", Amount: 4, Operation: api.Add}}
			source := copyStack(p.state.Inv.Slots[0])
			n := len(utilitiesEvents(t, s))
			s.survivalMessage(p, Message{T: "use", S: 0})
			if !r.Dirty || !reflect.DeepEqual(p.state.Inv.Slots[39], sized(source, 1)) || !reflect.DeepEqual(p.state.Inv.Slots[0], sized(source, count-1)) || s.maxHealth(p) != 24 || p.attributes[api.MaxHealth] != 24 {
				t.Fatalf("%s/%d manual equipment lost metadata, quantity or attribute sync", mode, id)
			}
			seen := map[string]bool{}
			for _, e := range utilitiesEvents(t, s)[n:] {
				if e.Name != api.PlayerEquipping && e.Name != api.PlayerEquipped && e.Name != api.InventoryChanging && e.Name != api.InventoryChanged {
					continue
				}
				seen[e.Name] = true
				target, _ := e.Data["target"].(map[string]any)
				if e.String("actor") != p.sid || e.String("source") != "player" || e.String("reason") != "equip" || target["kind"] != "player" || e.Number("sourceSlot") != 0 || e.Number("slot") != 39 || e.Data["inventoryBefore"] == nil || e.Data["inventoryAfter"] == nil || !reflect.DeepEqual(e.Item("sourceBefore"), itemSnapshot(source)) || !reflect.DeepEqual(e.Item("sourceAfter"), itemSnapshot(sized(source, count-1))) || !reflect.DeepEqual(e.Item("after"), itemSnapshot(sized(source, 1))) {
					t.Fatalf("%s missing stable attribution or full inventory/source snapshots", e.Name)
				}
				if e.Name == api.PlayerEquipped && (e.Player == nil || !reflect.DeepEqual(e.Player.Items[39], itemSnapshot(sized(source, 1)))) {
					t.Fatal("successful manual equipment did not capture equipped player")
				}
			}
			for _, name := range []string{api.PlayerEquipping, api.PlayerEquipped, api.InventoryChanging, api.InventoryChanged} {
				if !seen[name] {
					t.Fatalf("missing %s", name)
				}
			}
		}
	}
}

func TestManualEquipmentRejectsStaleSourceOrTarget(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, change := range []string{"source", "target", "selection", "state"} {
		_, p := redstoneTestPeer(s, 297)
		p.state.Inv.Slots[0] = customStack(297, 1)
		n := len(utilitiesEvents(t, s))
		plan := s.prepareHeldEquipment(p, 39)
		if plan == nil {
			t.Fatal("valid equipment could not prepare")
		}
		switch change {
		case "source":
			p.state.Inv.Slots[0] = customStack(1, 1)
		case "target":
			p.state.Inv.Slots[39] = customStack(293, 1)
		case "selection":
			p.state.Inv.Selected = 1
		case "state":
			other := *p.state
			p.state = &other
		}
		before := cloneStacks(p.state.Inv.Slots)
		if s.commitHeldEquipment(plan) || !reflect.DeepEqual(before, p.state.Inv.Slots) {
			t.Fatalf("stale %s was overwritten", change)
		}
		assertUtilitiesNoSuccess(t, utilitiesEvents(t, s)[n:])
	}
}
