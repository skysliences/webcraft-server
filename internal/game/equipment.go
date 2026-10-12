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
	s.broadcastPeerEquipment(p)
	soundAt(plan.room, "item.armor.equip_generic", p.state.X, p.state.Y, p.state.Z, nil)
	return true
}

func (s *Server) equipHeld(p *Peer, slot int) bool {
	return s.commitHeldEquipment(s.prepareHeldEquipment(p, slot))
}

func init() {
	onMessage("watch", func(s *Server, p *Peer, m Message) bool {
		s.handleWatch(p, m)
		return true
	})
}

// peerEquipmentRow constructs the 12-element equipment array expected by the client:
// [id, main, off, boots, legs, chest, helm, flags, glint, bedX, bedY, bedZ]
func (s *Server) peerEquipmentRow(target *Peer) []any {
	if target == nil || target.state == nil {
		return nil
	}
	st := target.state
	var mainID, offID, bootsID, legsID, chestID, helmID int
	glint := 0

	if st.Inv != nil {
		slots := st.Inv.Slots
		sel := st.Inv.Selected
		if sel >= 0 && sel < len(slots) && slots[sel] != nil {
			mainID = slots[sel].ID
		}
		if len(slots) > 40 && slots[40] != nil {
			offID = slots[40].ID
		}
		if len(slots) > 36 && slots[36] != nil {
			bootsID = slots[36].ID
		}
		if len(slots) > 37 && slots[37] != nil {
			legsID = slots[37].ID
		}
		if len(slots) > 38 && slots[38] != nil {
			chestID = slots[38].ID
		}
		if len(slots) > 39 && slots[39] != nil {
			helmID = slots[39].ID
		}

		checkGlint := func(stk *Stack, bit int) {
			if stk == nil {
				return
			}
			if len(stk.Ench) > 0 {
				glint |= (1 << bit)
				return
			}
			if stk.Meta != nil && (len(stk.Meta.Attributes) > 0 || stk.Meta.CustomData != nil) {
				glint |= (1 << bit)
			}
		}
		if sel >= 0 && sel < len(slots) {
			checkGlint(slots[sel], 0)
		}
		if len(slots) > 40 {
			checkGlint(slots[40], 1)
		}
		if len(slots) > 36 {
			checkGlint(slots[36], 2)
		}
		if len(slots) > 37 {
			checkGlint(slots[37], 3)
		}
		if len(slots) > 38 {
			checkGlint(slots[38], 4)
		}
		if len(slots) > 39 {
			checkGlint(slots[39], 5)
		}
	}

	flags := 0
	isBlocking := peerBlocking[target]
	isUsing := isBlocking || !target.useAt.IsZero()
	if isUsing {
		flags |= 1
	}
	if isBlocking {
		if offID == 382 {
			flags |= 2 // off-hand
		}
	}
	if target.flags&4 != 0 {
		flags |= 4 // sleeping
	}
	if target.ext().FireTicks > 0 {
		flags |= 8 // burning
	}

	bedX, bedY, bedZ := 0, 0, 0
	if target.flags&4 != 0 {
		e := target.ext()
		bedX, bedY, bedZ = e.SpawnX, e.SpawnY, e.SpawnZ
	}

	return []any{
		target.id,
		mainID,
		offID,
		bootsID,
		legsID,
		chestID,
		helmID,
		flags,
		glint,
		bedX, bedY, bedZ,
	}
}

func (s *Server) handleWatch(p *Peer, m Message) {
	if p == nil || p.room == nil || len(m.Add) == 0 {
		return
	}
	r := p.room
	rows := make([][]any, 0, len(m.Add))
	for _, id := range m.Add {
		for other := range r.Peers {
			if other != nil && other.id == id && other != p {
				if row := s.peerEquipmentRow(other); row != nil {
					rows = append(rows, row)
				}
				break
			}
		}
	}
	if len(rows) > 0 {
		p.json(map[string]any{
			"t": "eq",
			"r": rows,
		})
	}
}

func (s *Server) broadcastPeerEquipment(target *Peer) {
	if target == nil || target.room == nil {
		return
	}
	r := target.room
	row := s.peerEquipmentRow(target)
	if row == nil {
		return
	}
	msg := map[string]any{
		"t": "eq",
		"r": [][]any{row},
	}
	for other := range r.Peers {
		if other != nil && other != target && !other.closed {
			other.json(msg)
		}
	}
}

