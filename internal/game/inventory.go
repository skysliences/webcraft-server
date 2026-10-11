package game

import (
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

type Window struct {
	ID    int
	Kind  string
	Key   uint64
	Slots Stacks
}

func copyStack(v *Stack) *Stack { return (*Stack)(itemSnapshot(v)) }
func same(a, b *Stack) bool {
	return a != nil && b != nil && a.ID == b.ID && a.Damage == b.Damage && reflect.DeepEqual(a.Meta, b.Meta) && sameEnch(a.Ench, b.Ench)
}
func sized(v *Stack, n int) *Stack {
	if n <= 0 {
		return nil
	}
	o := copyStack(v)
	o.Count = n
	return o
}
func (s *Server) sanitizeInventory(inv *Inventory) {
	if len(inv.Slots) != 46 {
		inv.Slots = make(Stacks, 46)
	}
	if inv.Selected < 0 || inv.Selected > 8 {
		inv.Selected = 0
	}
	for i, v := range inv.Slots {
		if v == nil || !s.validStack(v, s.rules.max(v.ID)) {
			inv.Slots[i] = nil
		}
	}
	inv.Slots[45] = nil
}
func (s *Server) add(inv *Inventory, v *Stack) *Stack {
	if v == nil {
		return nil
	}
	left := v.Count
	maxStack := s.rules.max(v.ID)
	if maxStack < 1 {
		return v
	}
	for i := 0; i < 36; i++ {
		a := inv.Slots[i]
		if same(a, v) && a.Count < maxStack {
			n := min(left, maxStack-a.Count)
			a.Count += n
			left -= n
			if left == 0 {
				return nil
			}
		}
	}
	for i := 0; i < 36; i++ {
		if inv.Slots[i] == nil {
			n := min(left, maxStack)
			inv.Slots[i] = sized(v, n)
			left -= n
			if left == 0 {
				return nil
			}
		}
	}
	return sized(v, left)
}
func (p *Peer) kind() string {
	if p.window != nil {
		return p.window.Kind
	}
	return "inv"
}
func (p *Peer) windowID() int {
	if p.window != nil {
		return p.window.ID
	}
	return 0
}
func (p *Peer) get(slot int) *Stack {
	if slot < 46 {
		return p.state.Inv.Slots[slot]
	}
	return p.window.Slots[slot-46]
}
func (p *Peer) put(slot int, v *Stack) {
	if slot < 46 {
		p.state.Inv.Slots[slot] = v
	} else {
		p.window.Slots[slot-46] = v
	}
}
func (p *Peer) validSlot(slot int) bool {
	if slot == -999 {
		return true
	}
	if slot < 0 {
		return false
	}
	if p.window == nil {
		return slot < 46
	}
	return slot < 36 || (slot >= 46 && slot < 46+len(p.window.Slots))
}
func (p *Peer) result(slot int) bool {
	return p.window == nil && slot == 45 || p.kind() == "table" && slot == 55
}
func (s *Server) grid(p *Peer) (Stacks, int, int) {
	if p.window == nil {
		return p.state.Inv.Slots[41:45], 2, 45
	}
	if p.kind() == "table" {
		return p.window.Slots[:9], 3, 55
	}
	return nil, 0, -1
}
func (s *Server) refreshCraft(p *Peer) {
	g, w, result := s.grid(p)
	if result >= 0 {
		p.put(result, s.rules.craft(g, w))
	}
}
func (s *Server) takeCraft(p *Peer) {
	g, w, _ := s.grid(p)
	result := copyStack(s.rules.craft(g, w))
	for i, v := range g {
		if v != nil {
			g[i] = sized(v, v.Count-1)
		}
	}
	s.refreshCraft(p)
	s.emit(api.ItemCrafted, p.room, p, map[string]any{"result": result, "kind": p.kind()})
}
func (s *Server) mayPlace(p *Peer, slot int, v *Stack) bool {
	if ok, handled := s.mayPlaceExt(p, slot, v); handled {
		return ok
	}
	if p.result(slot) || p.kind() == "furnace" && slot == 48 {
		return false
	}
	if slot >= 36 && slot <= 39 {
		return s.equipmentSlot(v) == slot
	}
	if p.kind() == "furnace" && slot == 47 {
		return s.rules.byID[v.ID].Fuel > 0
	}
	return true
}
func (s *Server) stackMax(slot int, v *Stack) int {
	if slot >= 36 && slot <= 39 {
		return 1
	}
	return s.rules.max(v.ID)
}
func (s *Server) recoverItems(p *Peer) {
	var remaining Stacks
	for _, v := range p.state.Recovery {
		if left := s.add(p.state.Inv, v); left != nil {
			remaining = append(remaining, left)
		}
	}
	p.state.Recovery = remaining
}
func (s *Server) closeWindow(p *Peer) {
	if p.state == nil || p.state.Inv == nil {
		return
	}
	windowID, kind := p.windowID(), p.kind()
	if p.state.Cursor != nil {
		p.state.Recovery = append(p.state.Recovery, p.state.Cursor)
		p.state.Cursor = nil
	}
	g, _, result := s.grid(p)
	for i, v := range g {
		if v != nil {
			p.state.Recovery = append(p.state.Recovery, v)
			g[i] = nil
		}
	}
	if result >= 0 {
		p.put(result, nil)
	}
	if p.window != nil && transientWindow(p.window.Kind) {
		for i, v := range p.window.Slots {
			if v != nil {
				p.state.Recovery = append(p.state.Recovery, v)
				p.window.Slots[i] = nil
			}
		}
	}
	p.window = nil
	s.recoverItems(p)
	if windowID != 0 {
		s.emit(api.InventoryClosed, p.room, p, map[string]any{"window": windowID, "kind": kind})
	}
	p.room.Dirty = true
}
func (s *Server) inventory(p *Peer, q, d int) {
	s.refreshCraft(p)
	s.recoverItems(p)
	s.syncAttributes(p)
	data := s.inventorySnapshot(p)
	data["t"], data["d"] = "sinv", d
	p.json(data)
	if p.window != nil {
		p.json(map[string]any{"t": "wslot", "w": p.window.ID, "s": p.window.Slots})
	}
	p.json(map[string]any{"t": "cursor", "c": p.state.Cursor, "q": q})
	if p.window != nil && p.kind() != "table" && !transientWindow(p.kind()) {
		for other := range p.room.Peers {
			if other != p && other.window != nil && other.window.Key == p.window.Key {
				other.json(map[string]any{"t": "wslot", "w": other.window.ID, "s": other.window.Slots})
			}
		}
	}
	s.health(p)
}

// Session-only ordering metadata. Client predictions never enter Inventory or
// persistence; every snapshot contains the actual server-owned item metadata.
func (s *Server) inventorySnapshot(p *Peer) map[string]any {
	p.inventoryRevision++
	return map[string]any{"s": p.state.Inv.Slots, "sel": p.state.Inv.Selected, "rev": p.inventoryRevision, "placeQ": p.inventoryPlaceQ, "selQ": p.inventorySelQ, "epoch": p.epoch, "dim": p.room.dimName()}
}
func (s *Server) click(p *Peer, m Message) {
	// QUICK_CRAFT uses the outside sentinel to commit a list of target slots.
	// Dispatch it before outside-click handling; it never throws the cursor.
	if m.M == 5 {
		s.dragInventory(p, m)
		return
	}
	if p.state.Dead || m.W != p.windowID() || !p.validSlot(m.S) || m.B < 0 || m.B > 8 {
		return
	}
	s.refreshCraft(p)
	slot := m.S
	c := p.state.Cursor
	if slot == -999 {
		if c != nil {
			p.sys("基础生存模式暂不支持丢弃物品，物品已保留。")
		}
		s.inventory(p, m.Q, 0)
		return
	}
	v := p.get(slot)
	data := map[string]any{"window": m.W, "kind": p.kind(), "slot": slot, "mode": m.M, "button": m.B, "stack": copyStack(v), "cursor": copyStack(c), "slots": m.L}
	data["reason"] = "click"
	if !s.allowed(api.InventoryChanging, p.room, p, data) {
		s.inventory(p, m.Q, 0)
		return
	}
	if !s.allowed(api.InventoryClicking, p.room, p, data) {
		s.inventory(p, m.Q, 0)
		return
	}
	if p.result(slot) && v != nil && (m.M == 0 || m.M == 1 || m.M == 2) {
		if !s.allowed(api.ItemCrafting, p.room, p, map[string]any{"result": copyStack(v), "kind": p.kind(), "batch": m.M == 1}) {
			s.inventory(p, m.Q, 0)
			return
		}
	}
	switch m.M {
	case 0: // left/right pickup; crafting outputs are always derived on the server.
		if m.B > 1 {
			return
		}
		if v == nil && c != nil && s.mayPlace(p, slot, c) {
			n := min(c.Count, s.stackMax(slot, c))
			if m.B == 1 {
				n = 1
			}
			p.put(slot, sized(c, n))
			p.state.Cursor = sized(c, c.Count-n)
		} else if v != nil && c == nil {
			n := v.Count
			if m.B == 1 && !p.result(slot) {
				n = (n + 1) / 2
			}
			p.state.Cursor = sized(v, n)
			p.put(slot, sized(v, v.Count-n))
			if p.result(slot) {
				s.takeCraft(p)
			}
		} else if v != nil && c != nil && same(v, c) {
			n := min(c.Count, s.stackMax(slot, c)-v.Count)
			if m.B == 1 {
				n = min(n, 1)
			}
			if p.result(slot) || p.kind() == "furnace" && slot == 48 {
				n = min(v.Count, s.rules.max(c.ID)-c.Count)
				if n == v.Count {
					p.state.Cursor = sized(c, c.Count+n)
					p.put(slot, nil)
					if p.result(slot) {
						s.takeCraft(p)
					}
				}
			} else if n > 0 && s.mayPlace(p, slot, c) {
				p.put(slot, sized(v, v.Count+n))
				p.state.Cursor = sized(c, c.Count-n)
			}
		} else if v != nil && c != nil && s.mayPlace(p, slot, c) && c.Count <= s.stackMax(slot, c) {
			p.put(slot, c)
			p.state.Cursor = v
		}
	case 1: // shift-click, including repeated crafting.
		for i := 0; i < 100; i++ {
			v = p.get(slot)
			if v == nil {
				break
			}
			if p.result(slot) {
				clone := &Inventory{Slots: make(Stacks, 46)}
				for j, a := range p.state.Inv.Slots {
					clone.Slots[j] = copyStack(a)
				}
				if s.add(clone, v) != nil {
					break
				}
				p.state.Inv.Slots = clone.Slots
				s.takeCraft(p)
				continue
			}
			var to []int
			if slot >= 36 {
				for j := 9; j < 36; j++ {
					to = append(to, j)
				}
				for j := 0; j < 9; j++ {
					to = append(to, j)
				}
			} else if ext := s.shiftTargets(p, slot, v); ext != nil {
				to = ext
			} else if p.kind() == "chest" {
				for j := 46; j < 73; j++ {
					to = append(to, j)
				}
			} else if p.kind() == "furnace" && s.rules.byID[v.ID].Smelt != nil {
				to = []int{46}
			} else if p.kind() == "furnace" && s.rules.byID[v.ID].Fuel > 0 {
				to = []int{47}
			} else if slot < 9 {
				for j := 9; j < 36; j++ {
					to = append(to, j)
				}
			} else {
				for j := 0; j < 9; j++ {
					to = append(to, j)
				}
			}
			left := v.Count
			for _, j := range to {
				a := p.get(j)
				if same(a, v) && s.mayPlace(p, j, v) {
					n := min(left, s.stackMax(j, v)-a.Count)
					if n > 0 {
						a.Count += n
						left -= n
					}
				}
			}
			for _, j := range to {
				if left > 0 && p.get(j) == nil && s.mayPlace(p, j, v) {
					n := min(left, s.stackMax(j, v))
					p.put(j, sized(v, n))
					left -= n
				}
			}
			p.put(slot, sized(v, left))
			break
		}
	case 2: // number key swap.
		if c != nil || slot == m.B {
			break
		}
		h := p.state.Inv.Slots[m.B]
		if h == nil {
			p.state.Inv.Slots[m.B] = v
			p.put(slot, nil)
			if p.result(slot) && v != nil {
				s.takeCraft(p)
			}
		} else if s.mayPlace(p, slot, h) && h.Count <= s.stackMax(slot, h) {
			p.put(slot, h)
			p.state.Inv.Slots[m.B] = v
		}
	case 6: // double click collect.
		if c != nil {
			left := s.rules.max(c.ID) - c.Count
			for j := 0; j < 36 && left > 0; j++ {
				a := p.get(j)
				if same(a, c) {
					n := min(left, a.Count)
					c.Count += n
					left -= n
					p.put(j, sized(a, a.Count-n))
				}
			}
		}
	default:
		p.sys("基础生存模式暂不支持此物品操作。")
	}
	p.room.Dirty = true
	s.emit(api.InventoryClicked, p.room, p, data)
	s.emit(api.InventoryChanged, p.room, p, data)
	s.inventory(p, m.Q, 0)
}
func (s *Server) openWindow(p *Peer, key uint64, kind string, q int) {
	x, y, z := coords(key)
	data := map[string]any{"kind": kind, "x": x, "y": y, "z": z}
	if !s.allowed(api.InventoryOpening, p.room, p, data) {
		p.json(map[string]any{"t": "wopened", "q": q, "w": 0})
		return
	}
	s.closeWindow(p)
	p.nextWindow++
	size := windowSize(kind)
	slots := make(Stacks, size)
	if kind == "ender" {
		e := p.ext()
		if len(e.EnderChest) != 27 {
			e.EnderChest = make(Stacks, 27)
		}
		slots = e.EnderChest
		kind = "chest"
	} else if kind != "table" && !transientWindow(kind) {
		c := p.room.Containers[key]
		if c == nil {
			c = &Container{Kind: kind, Slots: slots}
			p.room.Containers[key] = c
		}
		if c.Kind != kind || len(c.Slots) != size {
			p.sys("容器存档格式错误。")
			return
		}
		slots = c.Slots
	}
	p.window = &Window{ID: p.nextWindow, Kind: kind, Key: key, Slots: slots}
	data["window"] = p.nextWindow
	s.emit(api.InventoryOpened, p.room, p, data)
	t := "win"
	if q > 0 {
		t = "wopened"
	}
	p.json(map[string]any{"t": t, "k": kind, "w": p.nextWindow, "s": slots, "q": q})
	s.inventory(p, 0, 0)
}
func (s *Server) furnaceTick(r *Room, c *Container, keys ...uint64) {
	var key uint64
	if len(keys) > 0 {
		key = keys[0]
	}
	x, y, z := coords(key)
	if len(c.Slots) != 3 {
		return
	}
	input, fuel, out := c.Slots[0], c.Slots[1], c.Slots[2]
	can := false
	var result int
	if input != nil {
		if recipe := s.rules.byID[input.ID].Smelt; recipe != nil {
			result = recipe.ID
			can = out == nil || same(out, stack(result, 1, 0)) && out.Count < s.rules.max(result)
		}
	}
	if c.Burn <= 0 && can && fuel != nil {
		ticks := s.rules.byID[fuel.ID].Fuel
		if ticks > 0 {
			c.Burn = ticks
			c.BurnTotal = ticks
			c.Slots[1] = sized(fuel, fuel.Count-1)
		}
	}
	if c.Burn > 0 {
		c.Burn = max(0, c.Burn-2)
		r.Dirty = true
	}
	if can && c.Burn > 0 {
		c.Progress += 2
		if c.Progress >= 200 {
			data := map[string]any{"x": x, "y": y, "z": z, "input": copyStack(input), "result": result, "count": 1}
			if !s.allowed(api.FurnaceSmelting, r, nil, data) {
				c.Progress = 198
				return
			}
			c.Slots[0] = sized(input, input.Count-1)
			if out == nil {
				c.Slots[2] = stack(result, 1, 0)
			} else {
				out.Count++
			}
			c.Progress = 0
			s.emit(api.FurnaceSmelted, r, nil, data)
		}
	} else {
		c.Progress = 0
	}
}
func (s *Server) armor(p *Peer) int {
	return int(s.attributes(p)[api.Armor])
}
func (s *Server) wear(p *Peer, amount int) {
	v := p.state.Inv.Slots[p.state.Inv.Selected]
	if v == nil {
		return
	}
	if amount = s.unbreakingWear(p, v, amount); amount <= 0 {
		return
	}
	maxWear := s.rules.byID[v.ID].Durability
	if maxWear > 0 && (v.Meta == nil || !v.Meta.Unbreakable) {
		data := map[string]any{"slot": p.state.Inv.Selected, "stack": itemSnapshot(v), "amount": amount, "damageBefore": v.Damage}
		e := s.emit(api.ItemWearing, p.room, p, data)
		wear := e.Number("amount")
		if e.Cancelled || !number(wear, 0, 1000) || wear != float64(int(wear)) || wear == 0 {
			return
		}
		v.Damage += int(wear)
		data["amount"] = int(wear)
		data["damageAfter"] = v.Damage
		data["broken"] = v.Damage >= maxWear
		if v.Damage >= maxWear {
			p.state.Inv.Slots[p.state.Inv.Selected] = nil
			p.sys(fmt.Sprintf("%s 已损坏。", s.rules.byID[v.ID].Name))
		}
		p.room.Dirty = true
		s.emit(api.ItemWorn, p.room, p, data)
	}
}
func sameEnch(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
