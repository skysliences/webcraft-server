package game

import (
	"reflect"

	api "mcraft/backend/pkg/plugin"
)

// dragInventory commits the complete mouse gesture under Server.mu. The client
// sends only slot IDs and the button; all quantities and metadata come from the
// current authoritative cursor. Refusals also acknowledge the prediction.
func (s *Server) dragInventory(p *Peer, m Message) bool {
	defer s.inventory(p, m.Q, 0)
	if p.state.Dead || s.isSpectator(p) || m.W != p.windowID() || m.B < 0 || m.B > 1 || !p.validSlot(m.S) {
		return false
	}
	cursor := copyStack(p.state.Cursor)
	limit := 46
	if p.window != nil {
		limit = 36 + len(p.window.Slots)
	}
	if cursor == nil || !s.validStack(cursor, s.rules.max(cursor.ID)) || len(m.L) == 0 || len(m.L) > limit {
		return false
	}
	seen := map[int]bool{}
	for _, slot := range m.L {
		if slot == -999 || !p.validSlot(slot) || seen[slot] {
			return false
		}
		seen[slot] = true
	}
	var slots []int
	before := map[int]*Stack{}
	for _, slot := range m.L {
		v := p.get(slot)
		had := 0
		if v != nil {
			had = v.Count
		}
		if len(slots) < cursor.Count && s.mayPlace(p, slot, cursor) && (v == nil || same(v, cursor)) && had < s.stackMax(slot, cursor) {
			slots = append(slots, slot)
			before[slot] = copyStack(v)
		}
	}
	if len(slots) == 0 {
		return false
	}
	changes := map[int]*Stack{}
	left, each := cursor.Count, cursor.Count/len(slots)
	dragType := "even"
	if m.B == 1 {
		each, dragType = 1, "single"
	}
	for _, slot := range slots {
		had := 0
		if before[slot] != nil {
			had = before[slot].Count
		}
		n := min(left, min(each, s.stackMax(slot, cursor)-had))
		changes[slot] = sized(cursor, had+n)
		left -= n
	}
	afterCursor := sized(cursor, left)
	data := map[string]any{
		"reason": "quick_craft", "actor": p.sid,
		"target": map[string]any{"kind": "inventory", "playerId": p.id, "window": m.W, "slots": append([]int(nil), slots...)},
		"window": m.W, "kind": p.kind(), "slot": m.S, "mode": 5, "button": m.B,
		"dragType": dragType, "slots": append([]int(nil), slots...),
		"stack": itemSnapshot(cursor), "cursor": itemSnapshot(cursor),
		"cursorBefore": itemSnapshot(cursor), "cursorAfter": itemSnapshot(afterCursor),
		"changes": creativeChanges(before, changes),
	}
	// Existing API v1 click/change protection remains in effect. Drag-specific
	// listeners can veto the entire plan; all its fields are read-only.
	for _, name := range []string{api.InventoryChanging, api.InventoryClicking, api.InventoryDragging} {
		if !s.allowed(name, p.room, p, data) {
			return false
		}
	}
	if m.W != p.windowID() || !reflect.DeepEqual(p.state.Cursor, cursor) {
		return false
	}
	for _, slot := range slots {
		v := changes[slot]
		if !p.validSlot(slot) || !reflect.DeepEqual(p.get(slot), before[slot]) || !s.validStack(v, s.stackMax(slot, v)) || !s.mayPlace(p, slot, v) {
			return false
		}
	}
	for _, slot := range slots {
		p.put(slot, copyStack(changes[slot]))
	}
	p.state.Cursor = copyStack(afterCursor)
	p.room.Dirty = true
	s.refreshCraft(p)
	for _, name := range []string{api.InventoryDragged, api.InventoryClicked, api.InventoryChanged} {
		s.emit(name, p.room, p, data)
	}
	return true
}
