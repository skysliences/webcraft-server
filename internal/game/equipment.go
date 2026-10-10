package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

// Hold the same inventory until every protection event has completed. A late
// change of selection or either stack invalidates this planned transfer.
type heldEquipment struct {
	player         *Peer
	room           *Room
	state          *Player
	inventory      *Inventory
	selected, slot int
	before, after  Stacks
	data           map[string]any
}

func (s *Server) prepareHeldEquipment(p *Peer, slot int) *heldEquipment {
	if p == nil || p.room == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || s.isSpectator(p) || slot < 36 || slot > 39 {
		return nil
	}
	inv := p.state.Inv
	if len(inv.Slots) != 46 || inv.Selected < 0 || inv.Selected > 8 || inv.Slots[slot] != nil {
		return nil
	}
	held := copyStack(inv.Slots[inv.Selected])
	if held == nil || !s.validStack(held, s.rules.max(held.ID)) || s.equipmentSlot(held) != slot || !s.mayPlace(p, slot, held) {
		return nil
	}
	before, after := cloneStacks(inv.Slots), cloneStacks(inv.Slots)
	one := sized(held, 1)
	after[slot], after[inv.Selected] = one, sized(held, held.Count-1)
	data := map[string]any{
		"actor": p.sid, "source": "player", "reason": "equip", "behavior": "equip",
		"target": map[string]any{"kind": "player", "playerId": p.id, "accountId": p.sid, "x": p.state.X, "y": p.state.Y, "z": p.state.Z},
		"item":   held.ID, "count": 1, "sourceSlot": inv.Selected, "slot": slot,
		"equipmentSlot": []string{"feet", "legs", "chest", "head"}[slot-36],
		"stack":         itemSnapshot(one), "before": itemSnapshot(before[slot]), "after": itemSnapshot(one),
		"sourceBefore": itemSnapshot(held), "sourceAfter": itemSnapshot(after[inv.Selected]),
		"inventoryBefore": cloneStacks(before), "inventoryAfter": cloneStacks(after),
	}
	plan := &heldEquipment{player: p, room: p.room, state: p.state, inventory: inv, selected: inv.Selected, slot: slot, before: before, after: after, data: data}
	if !s.allowed(api.PlayerEquipping, p.room, p, data) || !s.allowed(api.InventoryChanging, p.room, p, data) {
		return nil
	}
	return plan
}

func (s *Server) commitHeldEquipment(plan *heldEquipment) bool {
	if plan == nil {
		return false
	}
	p := plan.player
	if p.closed || p.room != plan.room || !plan.room.Peers[p] || p.state != plan.state || p.state.Inv != plan.inventory || p.state.Dead || s.isSpectator(p) || plan.inventory.Selected != plan.selected || !reflect.DeepEqual(plan.inventory.Slots, plan.before) || !s.mayPlace(p, plan.slot, plan.after[plan.slot]) {
		return false
	}
	copy(plan.inventory.Slots, plan.after)
	plan.room.Dirty = true
	s.emit(api.InventoryChanged, plan.room, p, plan.data)
	s.emit(api.PlayerEquipped, plan.room, p, plan.data)
	s.inventory(p, 0, 0) // Includes attribute, health and client inventory sync.
	soundAt(plan.room, "item.armor.equip_generic", p.state.X, p.state.Y, p.state.Z, nil)
	return true
}

func (s *Server) equipHeld(p *Peer, slot int) bool {
	return s.commitHeldEquipment(s.prepareHeldEquipment(p, slot))
}
