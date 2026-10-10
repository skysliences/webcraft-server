package game

import (
	"math"
	"reflect"
	"time"

	api "mcraft/backend/pkg/plugin"
)

// Creative inventory requests use the same authoritative stacks as survival.
// Clients provide only registered item IDs; metadata comes from server items.
func (s *Server) creativeInventory(p *Peer, m Message) bool {
	defer s.inventory(p, 0, 0) // Reconcile predictions even on refusal/cancellation.
	if p.state.Dead || !s.isCreative(p) {
		p.sys("只有创造模式可以从创造物品栏领取物品。")
		return false
	}
	inv := p.state.Inv
	if m.I < 0 || m.I > 8 || m.Pick < 0 || m.Pick > 1 {
		return false
	}
	changes := map[int]*Stack{}
	reason := "creative.take"
	var next *Stack
	if m.T == "cswap" {
		if m.S < 0 || m.S > 8 || m.S == m.I {
			return false
		}
		reason = "creative.swap"
		changes[m.I], changes[m.S] = copyStack(inv.Slots[m.S]), copyStack(inv.Slots[m.I])
	} else {
		if m.ID == 0 {
			if m.N != 0 || m.Pick != 0 {
				return false
			}
			reason = "creative.clear"
		} else {
			next = stack(int(m.ID), m.N, 0)
			if !s.validStack(next, s.rules.max(next.ID)) {
				return false
			}
		}
		changes[m.I] = next
		if m.Pick == 1 {
			reason = "creative.pick"
			// Pick-block must not discard the displaced stack, including its Lore.
			if old := inv.Slots[m.I]; old != nil {
				store := -1
				for i := 9; i < 36; i++ {
					if inv.Slots[i] == nil {
						store = i
						break
					}
				}
				if store < 0 {
					p.sys("背包已满，无法保存原来的快捷栏物品。")
					return false
				}
				changes[store] = copyStack(old)
			}
		}
	}
	before := map[int]*Stack{}
	for slot := range changes {
		before[slot] = copyStack(inv.Slots[slot])
	}
	data := map[string]any{
		"reason": reason, "slot": m.I, "item": int(m.ID), "count": m.N,
		"before": itemSnapshot(before[m.I]), "after": itemSnapshot(changes[m.I]),
		"stack": itemSnapshot(changes[m.I]), "changes": creativeChanges(before, changes),
		"target": map[string]any{"kind": "inventory", "playerId": p.id, "slot": m.I},
	}
	if m.T == "cswap" {
		data["otherSlot"] = m.S
	}
	e := s.emit(api.InventoryCreativeChanging, p.room, p, data)
	if e.Cancelled {
		if e.Reason != "" {
			p.sys(limitText(e.Reason, 240))
		}
		return false
	}
	n := e.Number("count")
	if next != nil {
		if !number(n, 1, float64(s.rules.max(next.ID))) || n != math.Trunc(n) {
			return false
		}
		next.Count = int(n)
		changes[m.I] = next
	} else if n != 0 {
		return false
	}
	data["after"], data["stack"] = itemSnapshot(changes[m.I]), itemSnapshot(changes[m.I])
	data["changes"] = creativeChanges(before, changes)
	if !s.allowed(api.InventoryChanging, p.room, p, data) {
		return false
	}
	for slot, old := range before {
		if !reflect.DeepEqual(inv.Slots[slot], old) {
			return false
		}
	}
	changed := false
	for slot, v := range changes {
		changed = changed || !reflect.DeepEqual(inv.Slots[slot], v)
	}
	if !changed {
		return false
	}
	for slot, v := range changes {
		inv.Slots[slot] = copyStack(v)
	}
	p.mining = nil
	p.useAt = time.Time{}
	p.room.Dirty = true
	s.emit(api.InventoryCreativeChanged, p.room, p, data)
	s.emit(api.InventoryChanged, p.room, p, data)
	return true
}

func creativeChanges(before, after map[int]*Stack) []map[string]any {
	out := make([]map[string]any, 0, len(after))
	// Stable slot order makes event data predictable across plugin languages.
	for slot := 0; slot < 36; slot++ {
		if v, ok := after[slot]; ok {
			out = append(out, map[string]any{"slot": slot, "before": itemSnapshot(before[slot]), "after": itemSnapshot(v)})
		}
	}
	return out
}

func init() {
	for _, kind := range []string{"cset", "cswap"} {
		onMessage(kind, func(s *Server, p *Peer, m Message) bool {
			s.creativeInventory(p, m)
			return true
		})
	}
}
