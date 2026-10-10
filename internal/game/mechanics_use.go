package game

import (
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

func cloneStacks(a Stacks) Stacks {
	out := make(Stacks, len(a))
	for i, v := range a {
		out[i] = copyStack(v)
	}
	return out
}
func (s *Server) mechanicPlacementID(v *Stack, it Item) int {
	if v == nil {
		return 0
	}
	b := it.PlacesID
	if mechanicKinds[b] != "" || isRailBlock(b) {
		return b
	}
	if b = mechanicItems[v.ID]; b > 0 {
		return b
	}
	return 0
}
func (s *Server) handleMechanicUse(p *Peer, m Message, expected *api.ItemStack, compare bool) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Inv == nil {
		return false
	}
	r := p.room
	target := s.block(r, m.X, m.Y, m.Z)
	kind := mechanicKinds[target]
	held, it := s.heldStack(p)
	placed := s.mechanicPlacementID(held, it)
	interactive := kind == "composter" || kind == "cauldron" || kind == "sign" || kind == "jukebox" || kind == "daylight_detector" || kind == "dispenser" || kind == "dropper"
	if placed == 0 && (!interactive || p.flags&1 != 0) {
		return false
	}
	if !s.reachable(p, m) || p.state.Dead || !s.canBuild(p) || compare && !reflect.DeepEqual(itemSnapshot(held), expected) {
		return true
	}
	if interactive && p.flags&1 == 0 {
		k := blockKey(m.X, m.Y, m.Z)
		s.trackMechanicBlock(r, k, target)
		switch kind {
		case "dispenser", "dropper":
			s.openWindow(p, k, kind, m.Q)
		case "sign":
			data := s.mechanicData(r, p, k, mechanicSnapshot(r, k), "sign_edit_open")
			if s.allowed(api.MechanicChanging, r, p, data) {
				p.json(map[string]any{"t": "sign_editor", "epoch": p.epoch, "worldId": r.ID, "dim": r.dimName(), "x": m.X, "y": m.Y, "z": m.Z, "lines": mechanicSnapshot(r, k).Lines})
				s.emit(api.MechanicChanged, r, p, data)
			}
		case "daylight_detector":
			v := mechanicSnapshot(r, k)
			v.Inverted = !v.Inverted
			s.changeMechanic(r, p, k, v, "daylight_invert")
		default:
			s.useUtilityMechanic(p, k, kind)
		}
		return true
	}
	if placed == 0 || held == nil || !s.validStack(held, s.rules.max(held.ID)) || placed >= len(s.rules.Names) || placed >= len(s.rules.Kinds) || s.rules.Kinds[placed] == 0 {
		return true
	}
	off := faceOffsets[m.F]
	x, y, z := m.X+off[0], m.Y+off[1], m.Z+off[2]
	face := m.F
	if target > 0 && target < len(s.rules.Kinds) && s.rules.Kinds[target] == 5 && s.redstoneKind(target) == "" && !isTorch(target) && s.nameOf(target) != "Fire" {
		x, y, z = m.X, m.Y, m.Z
		face = 1
	}
	if !r.inside(x, z) || y < 1 || y >= 128 {
		return true
	}
	old := s.block(r, x, y, z)
	if old != 0 && !(old < len(s.rules.Kinds) && s.rules.Kinds[old] == 5 && s.redstoneKind(old) == "" && !isTorch(old) && s.nameOf(old) != "Fire") {
		return true
	}
	k := blockKey(x, y, z)
	next := s.defaultMechanic(r, k, placed)
	var rail *RailState
	if next != nil {
		next.Facing = pistonFacingFromPlayer(p)
		next.SupportFace = face
		if next.Kind == "observer" {
			next.Observed = s.observerObservation(r, k, next.Facing)
		}
		if next.Kind == "sign" {
			if face == 0 {
				return true
			}
			d := faceOffsets[face]
			if !s.supportsTorch(s.block(r, x-d[0], y-d[1], z-d[2])) {
				return true
			}
		}
	}
	if isRailBlock(placed) || placed == blkDaylightDetector {
		if face != 1 || !s.supportsTorch(s.block(r, x, y-1, z)) {
			return true
		}
		if isRailBlock(placed) {
			shape := "north_south"
			f := pistonFacingFromPlayer(p)
			if f == 4 || f == 5 {
				shape = "east_west"
			}
			rail = &RailState{Kind: railKind(placed), Shape: shape}
			rail.Shape = s.desiredRailShape(r, k, rail)
		}
	}
	if s.rules.Kinds[placed] == 1 {
		for other := range r.Peers {
			if a := other.state; a != nil && !a.Dead && float64(x+1) > a.X-.3 && float64(x) < a.X+.3 && float64(z+1) > a.Z-.3 && float64(z) < a.Z+.3 && float64(y+1) > a.Y && float64(y) < a.Y+1.8 {
				return true
			}
		}
	}
	slot := p.state.Inv.Selected
	before := itemSnapshot(held)
	after := copyStack(held)
	count := 0
	if !s.isCreative(p) {
		after = sized(held, held.Count-1)
		count = 1
	}
	data := s.blockData(r, k, placed)
	if next != nil {
		data = s.mechanicData(r, p, k, next, "mechanic_place")
	}
	if rail != nil {
		data = s.railData(r, p, k, rail, "rail_place")
	}
	data["oldBlock"] = old
	data["block"] = placed
	data["before"] = before.Clone()
	data["stack"] = before.Clone()
	data["after"] = itemSnapshot(after)
	data["item"] = held.ID
	data["count"] = count
	data["face"] = face
	if !s.allowed(api.BlockPlacing, r, p, data) || !s.mechanicBlockBefore(r, p, data) || count > 0 && !s.allowed(api.InventoryChanging, r, p, data) {
		return true
	}
	torchChanges, err := s.prepareTorchChanges(r, p, k, placed, 0)
	if err != nil {
		return true
	}
	if slot != p.state.Inv.Selected || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), before) {
		return true
	}
	p.state.Inv.Slots[slot] = after
	if next != nil {
		storeMechanic(r, k, next)
	}
	if rail != nil {
		storeRail(r, k, rail)
	}
	s.commitPlayerBlock(p, k, placed, data)
	s.applyTorchChanges(r, p, torchChanges)
	if next != nil {
		s.broadcastMechanic(r, k)
	}
	if rail != nil {
		s.broadcastRail(r, k)
		s.tickRails(r)
	}
	if count > 0 {
		s.emit(api.InventoryChanged, r, p, data)
	}
	s.inventory(p, 0, 0)
	s.afterPlace(p, x, y, z, placed, m)
	return true
}

// Plan a replacement against cloned inventory. Both produced bucket/bottle and
// the remaining stack retain their complete metadata; a full inventory vetoes
// the transaction instead of losing or silently relocating the output.
func (s *Server) mechanicReplacement(p *Peer, result int) (Stacks, bool) {
	slots := cloneStacks(p.state.Inv.Slots)
	if s.isCreative(p) {
		return slots, true
	}
	sel := p.state.Inv.Selected
	v := slots[sel]
	if v == nil {
		return nil, false
	}
	output := sized(v, 1)
	if result > 0 {
		output.ID = result
		output.Damage = 0
		if !s.validStack(output, s.rules.max(result)) {
			return nil, false
		}
	} else {
		output = nil
	}
	slots[sel] = sized(v, v.Count-1)
	if output != nil {
		if slots[sel] == nil {
			slots[sel] = output
		} else {
			inv := *p.state.Inv
			inv.Slots = slots
			if s.add(&inv, output) != nil {
				return nil, false
			}
		}
	}
	return slots, true
}
func (s *Server) useUtilityMechanic(p *Peer, k uint64, kind string) bool {
	r := p.room
	next := mechanicSnapshot(r, k)
	if next == nil {
		return false
	}
	held := s.heldItem(p)
	slots := cloneStacks(p.state.Inv.Slots)
	var output Stacks
	reason := ""
	consume := false
	switch kind {
	case "composter":
		if next.Level >= 7 {
			reason = "composter_harvest"
			next.Level = 0
			output = Stacks{stack(itemBoneMeal, 1, 0)}
		} else if held != nil && isCompostable(held.ID) {
			reason = "composter_insert"
			consume = true
			if s.rng.Float64() < .65 {
				next.Level++
			}
			if !s.isCreative(p) {
				slots[p.state.Inv.Selected] = sized(held, held.Count-1)
			}
		} else {
			return false
		}
	case "cauldron":
		if held == nil {
			return false
		}
		result := 0
		switch held.ID {
		case itemWaterBucket:
			if next.Level == 3 {
				return false
			}
			next.Level = 3
			result = itemBucket
			reason = "cauldron_fill"
		case itemBucket:
			if next.Level != 3 {
				return false
			}
			next.Level = 0
			result = itemWaterBucket
			reason = "cauldron_drain"
		case itemGlassBottle:
			if next.Level == 0 {
				return false
			}
			next.Level--
			result = itemWaterBottle
			reason = "cauldron_bottle_fill"
		case itemWaterBottle:
			if next.Level == 3 {
				return false
			}
			next.Level++
			result = itemGlassBottle
			reason = "cauldron_bottle_empty"
		default:
			return false
		}
		var ok bool
		slots, ok = s.mechanicReplacement(p, result)
		if !ok {
			return false
		}
		consume = true
	case "jukebox":
		if next.Record != nil {
			reason = "jukebox_eject"
			output = Stacks{copyStack(next.Record)}
			next.Record = nil
		} else if held != nil && s.registeredDisc(held.ID) {
			reason = "jukebox_insert"
			next.Record = sized(held, 1)
			consume = true
			if !s.isCreative(p) {
				slots[p.state.Inv.Selected] = sized(held, held.Count-1)
			}
		} else {
			return false
		}
	default:
		return false
	}
	before := cloneStacks(p.state.Inv.Slots)
	data := s.mechanicData(r, p, k, next, reason)
	data["inventoryBefore"] = before
	data["inventoryAfter"] = cloneStacks(slots)
	data["after"] = itemSnapshot(slots[p.state.Inv.Selected])
	data["contents"] = cloneStacks(output)
	bucketBefore, bucketAfter := "", ""
	if kind == "cauldron" && held != nil {
		if held.ID == itemWaterBucket {
			bucketBefore, bucketAfter = api.BucketEmptying, api.BucketEmptied
		} else if held.ID == itemBucket {
			bucketBefore, bucketAfter = api.BucketFilling, api.BucketFilled
		}
	}
	if bucketBefore != "" {
		data["fluid"] = 9
	}
	if bucketBefore != "" && !s.allowed(bucketBefore, r, p, data) {
		return false
	}
	if !s.allowed(api.MechanicChanging, r, p, data) || consume && !s.isCreative(p) && !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	nextID := r.NextDropID
	prepared, err := s.prepareDropsWithContext(r, p, output, float64(data["x"].(int))+.5, float64(data["y"].(int))+.5, float64(data["z"].(int))+.5, reason, map[string]any{"actor": p.sid, "source": "mechanic", "target": data["target"]}, true)
	if err != nil {
		r.NextDropID = nextID
		return false
	}
	if !reflect.DeepEqual(before, p.state.Inv.Slots) {
		r.NextDropID = nextID
		return false
	}
	p.state.Inv.Slots = slots
	storeMechanic(r, k, next)
	for _, d := range prepared {
		r.Drops[d.ID] = d
	}
	s.broadcastMechanic(r, k)
	s.emit(api.MechanicChanged, r, p, data)
	if bucketAfter != "" {
		s.emit(bucketAfter, r, p, data)
	}
	if consume && !s.isCreative(p) {
		s.emit(api.InventoryChanged, r, p, data)
	}
	s.publishDropsWithContext(r, p, prepared, reason, map[string]any{"actor": p.sid, "source": "mechanic", "target": data["target"]})
	s.inventory(p, 0, 0)
	return true
}
func (s *Server) writeSign(p *Peer, x, y, z int, lines []string, expected *api.ItemStack, compare bool) error {
	if p == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !s.canBuild(p) || !s.reachable(p, Message{X: x, Y: y, Z: z, F: 1}) {
		return fmt.Errorf("cannot edit sign")
	}
	if !validSignLines(lines) {
		return fmt.Errorf("sign requires four printable lines of at most 80 characters")
	}
	if compare && !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), expected) {
		return fmt.Errorf("selected item changed")
	}
	r := p.room
	k := blockKey(x, y, z)
	if s.block(r, x, y, z) != blkSign {
		return fmt.Errorf("target is not a sign")
	}
	s.trackMechanicBlock(r, k, blkSign)
	next := mechanicSnapshot(r, k)
	copy(next.Lines[:], lines)
	data := s.mechanicData(r, p, k, next, "sign_write")
	data["linesBefore"] = mechanicSnapshot(r, k).Lines
	data["linesAfter"] = next.Lines
	if !s.allowed(api.SignChanging, r, p, data) || !s.allowed(api.MechanicChanging, r, p, data) {
		return fmt.Errorf("sign edit cancelled")
	}
	storeMechanic(r, k, next)
	s.broadcastMechanic(r, k)
	s.emit(api.SignChanged, r, p, data)
	s.emit(api.MechanicChanged, r, p, data)
	return nil
}
func init() {
	onPlace(1, func(s *Server, p *Peer, m Message, target int) bool { return s.handleMechanicUse(p, m, nil, false) })
	onMessage("wopen", func(s *Server, p *Peer, m Message) bool {
		kind := mechanicKinds[s.block(p.room, m.X, m.Y, m.Z)]
		if kind != "dispenser" && kind != "dropper" {
			return false
		}
		return s.handleMechanicUse(p, m, nil, false)
	})
	onMessage("sign_text", func(s *Server, p *Peer, m Message) bool {
		if err := s.writeSign(p, m.X, m.Y, m.Z, m.Lines, nil, false); err != nil {
			p.sys(err.Error())
		}
		return true
	})
	onRoomTick(func(s *Server, r *Room) { s.tickMechanics(r) })
}
