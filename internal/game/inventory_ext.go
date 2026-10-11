package game

import (
	"math"
)

// ---------------------------------------------------------------------------
// Inventory extensions: extra window kinds (enchanting, hopper, brewing,
// ender chest), item dropping (Q key / click outside), furnace experience.
// ---------------------------------------------------------------------------

// Window slot counts by kind.
func windowSize(kind string) int {
	switch kind {
	case "table":
		return 10
	case "furnace":
		return 3
	case "dispenser", "dropper":
		return 9
	case "large_chest":
		return 54
	case "chest", "ender":
		return 27
	case "ench":
		return 2
	case "hopper":
		return 5
	case "brew":
		return 5
	}
	return 0
}

// transientWindow kinds are not backed by a container; items return to the
// player when closed.
func transientWindow(kind string) bool { return kind == "ench" }

const lapisID = 319

// Brewing stand slot layout (window-relative): 0..2 bottles, 3 ingredient, 4 fuel.
func (s *Server) isBottle(id int) bool {
	k := s.rules.byID[id].Key
	return k == "glass_bottle" || k == "water_bottle" || k == "potion" || len(k) > 7 && k[:7] == "potion_" || id >= 400 && id <= 410
}

func (s *Server) mayPlaceExt(p *Peer, slot int, v *Stack) (bool, bool) {
	if p.window == nil || slot < 46 || v == nil {
		return false, false
	}
	i := slot - 46
	switch p.kind() {
	case "ench":
		if i == 0 {
			it := s.rules.byID[v.ID]
			return v.Count == 1 && (it.Ench > 0 || it.Durability > 0 || it.Key == "book"), true
		}
		return v.ID == lapisID, true
	case "brew":
		switch {
		case i <= 2:
			return s.isBottle(v.ID) && v.Count == 1, true
		case i == 3:
			return brewIngredient(s, v.ID), true
		default:
			return s.rules.byID[v.ID].Key == "blaze_powder" || s.rules.byID[v.ID].Name == "Cinder Dust", true
		}
	}
	return false, false
}

// shiftTargets chooses destinations for shift-clicking from the player
// inventory into an extension window.
func (s *Server) shiftTargets(p *Peer, slot int, v *Stack) []int {
	if p.window == nil || slot >= 36 {
		return nil
	}
	switch p.kind() {
	case "ench":
		if v.ID == lapisID {
			return []int{47}
		}
		return []int{46}
	case "hopper":
		return []int{46, 47, 48, 49, 50}
	case "dispenser", "dropper":
		return []int{46, 47, 48, 49, 50, 51, 52, 53, 54}
	case "brew":
		if s.isBottle(v.ID) {
			return []int{46, 47, 48}
		}
		if s.rules.byID[v.ID].Name == "Cinder Dust" {
			return []int{50, 49}
		}
		return []int{49}
	}
	return nil
}

func (s *Server) dropAck(p *Peer, d int, drops []*ItemDrop) {
	if d == 0 {
		return
	}
	ids := make([]uint32, 0, len(drops))
	for _, x := range drops {
		ids = append(ids, x.ID)
	}
	p.json(map[string]any{"t": "dropack", "d": d, "ids": ids})
}

// furnaceXP returns the experience for smelted output items.
func (s *Server) furnaceXP(out *Stack, n int) int {
	per := 0.
	for _, it := range s.rules.Items {
		if it.Smelt != nil && it.Smelt.ID == out.ID {
			per = it.Smelt.XP
			break
		}
	}
	if per == 0 {
		per = smeltXP(s.rules.byID[out.ID].Name)
	}
	total := per * float64(n)
	xp := int(math.Floor(total))
	if s.rng.Float64() < total-float64(xp) {
		xp++
	}
	return xp
}

func init() {
	onMessage("drop", func(s *Server, p *Peer, m Message) bool {
		if !p.room.usesSurvival() || p.state.Dead || s.isSpectator(p) {
			s.dropAck(p, m.D, nil)
			return true
		}
		st := p.state
		v := st.Inv.Slots[st.Inv.Selected]
		if v == nil {
			s.dropAck(p, m.D, nil)
			s.inventory(p, 0, m.D)
			return true
		}
		n := 1
		if m.All {
			n = v.Count
		}
		drops := s.dropFromPlayer(p, sized(v, n))
		if len(drops) > 0 {
			st.Inv.Slots[st.Inv.Selected] = sized(v, v.Count-n)
			p.room.Dirty = true
		}
		s.dropAck(p, m.D, drops)
		s.inventory(p, 0, m.D)
		return true
	})
	onMessage("click", func(s *Server, p *Peer, m Message) bool {
		if !p.room.usesSurvival() || p.state.Dead || m.W != p.windowID() {
			return false
		}
		st := p.state
		// Click outside the window: drop the cursor stack (right click = one).
		if m.S == -999 && (m.M == 0 || m.M == 4) {
			c := st.Cursor
			var drops []*ItemDrop
			if c != nil {
				n := c.Count
				if m.B == 1 {
					n = 1
				}
				drops = s.dropFromPlayer(p, sized(c, n))
				if len(drops) > 0 {
					st.Cursor = sized(c, c.Count-n)
					p.room.Dirty = true
				}
			}
			s.dropAck(p, m.D, drops)
			s.inventory(p, m.Q, m.D)
			return true
		}
		// Throw (Q over a slot): one item, or the whole stack with Ctrl.
		if m.M == 4 && p.validSlot(m.S) && m.S != -999 {
			v := p.get(m.S)
			var drops []*ItemDrop
			if v != nil && st.Cursor == nil && !p.result(m.S) && !(p.kind() == "furnace" && m.S == 48) {
				n := 1
				if m.B == 1 {
					n = v.Count
				}
				drops = s.dropFromPlayer(p, sized(v, n))
				if len(drops) > 0 {
					p.put(m.S, sized(v, v.Count-n))
					p.room.Dirty = true
				}
			}
			s.dropAck(p, m.D, drops)
			s.inventory(p, m.Q, m.D)
			if p.kind() == "ench" {
				s.sendEnchOffer(p)
			}
			return true
		}
		// Furnace output: award experience for what was taken.
		if p.kind() == "furnace" && m.S == 48 {
			before := p.get(48)
			var snap *Stack
			n0 := 0
			if before != nil {
				snap, n0 = copyStack(before), before.Count
			}
			s.click(p, m)
			n1 := 0
			if after := p.get(48); after != nil {
				n1 = after.Count
			}
			if snap != nil && n1 < n0 {
				if xp := s.furnaceXP(snap, n0-n1); xp > 0 {
					s.spawnOrbs(p.room, st.X, st.Y+.5, st.Z, xp)
				}
			}
			return true
		}
		if p.kind() == "ench" {
			s.click(p, m)
			s.sendEnchOffer(p)
			return true
		}
		return false
	})
	// Opening extension containers by right click.
	open := func(s *Server, p *Peer, m Message, target int) bool {
		if !p.room.usesSurvival() || p.flags&1 != 0 && s.heldItem(p) != nil {
			return false
		}
		key := blockKey(m.X, m.Y, m.Z)
		switch s.nameOf(target) {
		case "Enchanting Table":
			s.openWindow(p, key, "ench", m.Q)
			s.sendEnchOffer(p)
		case "Hopper":
			s.openWindow(p, key, "hopper", m.Q)
		case "Brewing Stand":
			s.openWindow(p, key, "brew", m.Q)
		case "Ender Chest":
			s.openWindow(p, key, "ender", m.Q)
			soundAt(p.room, "block.ender_chest.open", float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
		case "Shulker Box":
			s.openWindow(p, key, "chest", m.Q)
		default:
			return false
		}
		return true
	}
	onPlace(10, open)
	onMessage("wopen", func(s *Server, p *Peer, m Message) bool {
		if !p.room.usesSurvival() || !p.room.inside(m.X, m.Z) || m.Y < 0 || m.Y >= 128 {
			return false
		}
		if !s.reachable(p, m) {
			return false
		}
		return open(s, p, m, s.block(p.room, m.X, m.Y, m.Z))
	})
}
