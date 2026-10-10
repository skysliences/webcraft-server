package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// dispenserFirePlan contains no committed state. The caller merges After into
// its container snapshot, validates its own protection events, then commits the
// block edits, source inventory and this effect together.
type dispenserFirePlan struct {
	After   *Stack
	Edits   [][4]int
	Context map[string]any
	Valid   func() bool
	Commit  func()
	Publish func()
}

func dispenserFireContext(context map[string]any) map[string]any {
	data := make(map[string]any, len(context)+4)
	for field, value := range context {
		data[field] = value
	}
	return data
}

// prepareDispenserFire handles only a dispenser's flint-and-steel and TNT.
// A recognized item with an obstructed or protected target is not ejected as a
// normal item, and neither its count nor its durability changes.
func (s *Server) prepareDispenserFire(r *Room, k uint64, slot int, original *Stack, x, y, z int, context map[string]any) (handled bool, plan *dispenserFirePlan, ok bool) {
	if original == nil || original.ID != itemFlintAndSteel && original.ID != blkExplosiveTNT {
		return false, nil, true
	}
	if !s.validStack(original, s.rules.max(original.ID)) || !r.inside(x, z) || y < 1 || y >= 128 {
		return true, nil, false
	}
	one := sized(original, 1)
	after := sized(original, original.Count-1)
	reason := "dispenser_tnt"
	if original.ID == itemFlintAndSteel {
		reason = "dispenser_flint_and_steel"
		after = copyStack(original)
	}
	ox, oy, oz := coords(k)
	target := s.block(r, x, y, z)
	futureTNT := original.ID == blkExplosiveTNT
	if futureTNT && target != 0 || !futureTNT && target != 0 && target != blkExplosiveTNT {
		return true, nil, false
	}
	var frame *portalFrame
	if !futureTNT && target == 0 {
		if r.usesSurvival() && r.dimName() != "end" {
			frame = s.findFrame(r, x, y, z)
		}
		if frame == nil && !s.dispenserFireSupported(r, x, y, z) {
			return true, nil, false
		}
	}
	if !futureTNT && target == blkExplosiveTNT && s.primedTNT(r, blockKey(x, y, z)) {
		return true, nil, false
	}
	data := dispenserFireContext(context)
	data["actor"], data["source"], data["reason"] = "server", "dispenser", reason
	data["origin"] = map[string]any{"kind": "block", "x": ox, "y": oy, "z": oz}
	data["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	data["x"], data["y"], data["z"], data["slot"] = x, y, z, slot
	data["stack"], data["before"], data["after"] = itemSnapshot(one), itemSnapshot(original), itemSnapshot(after)
	var wearData map[string]any
	if !futureTNT {
		var wearOK bool
		after, wearData, wearOK = s.prepareDispenserFireWear(r, original, data)
		if !wearOK {
			return true, nil, false
		}
		data["after"] = itemSnapshot(after)
	}
	if slots, yes := data["containerBefore"].(Stacks); yes {
		next := cloneStacks(slots)
		if slot < 0 || slot >= len(next) {
			return true, nil, false
		}
		next[slot] = copyStack(after)
		data["containerAfter"] = next
	}
	origin := &TNTState{Actor: "server", Source: "dispenser", Reason: reason, Attacker: &api.Combatant{Kind: "block", X: float64(ox), Y: float64(oy), Z: float64(oz)}, Stack: itemSnapshot(one), Slot: slot}
	plan = &dispenserFirePlan{After: after, Context: data}
	publishWear := func() {
		if wearData != nil {
			s.emit(api.ItemWorn, r, nil, wearData)
		}
	}
	if futureTNT || target == blkExplosiveTNT {
		tnt, accepted := s.prepareTNTPrime(r, nil, x, y, z, defaultTNTFuse, origin, futureTNT, data)
		if !accepted {
			return true, nil, false
		}
		if futureTNT {
			plan.Edits = [][4]int{{x, y, z, blkExplosiveTNT}}
		}
		plan.Valid, plan.Commit = tnt.valid, tnt.commit
		plan.Publish = func() { publishWear(); tnt.publish() }
		return true, plan, true
	}
	data["block"] = blkFlintFire
	if frame != nil {
		b, dx, dz := blkPortalZ, 0, 1
		if frame.axisX {
			b, dx, dz = blkPortalX, 1, 0
		}
		for j := 0; j < frame.height; j++ {
			for i := 0; i < frame.width; i++ {
				plan.Edits = append(plan.Edits, [4]int{frame.x + i*dx, frame.y + j, frame.z + i*dz, b})
			}
		}
		data["block"], data["axis"], data["width"], data["height"] = b, map[bool]string{true: "x", false: "z"}[frame.axisX], frame.width, frame.height
		data["blocks"] = append([][4]int(nil), plan.Edits...)
		data["dimension"] = r.dimName()
	} else {
		plan.Edits = [][4]int{{x, y, z, blkFlintFire}}
	}
	if !s.allowed(api.BlockIgniting, r, nil, data) {
		return true, nil, false
	}
	if frame != nil && !s.allowed(api.PortalCreating, r, nil, data) {
		return true, nil, false
	}
	previous := make(map[uint64]int, len(plan.Edits))
	for _, at := range plan.Edits {
		previous[blockKey(at[0], at[1], at[2])] = s.block(r, at[0], at[1], at[2])
	}
	plan.Valid = func() bool {
		for at, old := range previous {
			a, b, c := coords(at)
			if s.block(r, a, b, c) != old {
				return false
			}
		}
		if frame != nil {
			fresh := s.findFrame(r, x, y, z)
			if fresh == nil || *fresh != *frame {
				return false
			}
		}
		return true
	}
	plan.Commit = func() {
		if frame == nil {
			s.inheritFire(r, blockKey(x, y, z), origin)
		}
	}
	plan.Publish = func() {
		publishWear()
		s.emit(api.BlockIgnited, r, nil, data)
		if frame != nil {
			s.emit(api.PortalCreated, r, nil, data)
			s.tntSound(r, "block.portal.trigger", float64(x)+.5, float64(y)+.5, float64(z)+.5)
		}
		s.tntSound(r, "item.flintandsteel.use", float64(x)+.5, float64(y)+.5, float64(z)+.5)
	}
	return true, plan, true
}

func (s *Server) dispenserFireSupported(r *Room, x, y, z int) bool {
	if s.mobSolid(r, x, y-1, z) {
		return true
	}
	for _, dir := range faceOffsets {
		if s.isFlammable(s.block(r, x+dir[0], y+dir[1], z+dir[2])) {
			return true
		}
	}
	return false
}

func (s *Server) prepareDispenserFireWear(r *Room, before *Stack, context map[string]any) (*Stack, map[string]any, bool) {
	after := copyStack(before)
	if before.Meta != nil && before.Meta.Unbreakable {
		return after, nil, true
	}
	maximum := s.rules.byID[before.ID].Durability
	if maximum <= 0 {
		return nil, nil, false
	}
	amount := 1
	if level := enchOf(before, "unbreaking"); level > 0 && s.rng.Intn(level+1) != 0 {
		amount = 0
	}
	if amount == 0 {
		return after, nil, true
	}
	data := dispenserFireContext(context)
	data["amount"], data["damageBefore"] = amount, before.Damage
	event := s.emit(api.ItemWearing, r, nil, data)
	wear := event.Number("amount")
	if event.Cancelled || !dispenserNumericField(event, "amount") || !number(wear, 0, 1000) || wear != math.Trunc(wear) {
		return nil, nil, false
	}
	if wear == 0 {
		return after, nil, true
	}
	after.Damage += int(wear)
	data["amount"], data["damageAfter"], data["broken"] = int(wear), after.Damage, after.Damage >= maximum
	if after.Damage >= maximum {
		after = nil
	}
	data["after"] = itemSnapshot(after)
	if slots, yes := data["containerBefore"].(Stacks); yes {
		next := cloneStacks(slots)
		slot, _ := data["slot"].(int)
		if slot < 0 || slot >= len(next) {
			return nil, nil, false
		}
		next[slot] = copyStack(after)
		data["containerAfter"] = next
	}
	return after, data, true
}

func dispenserNumericField(event *api.Event, field string) bool {
	switch event.Data[field].(type) {
	case float64, int, uint64:
		return true
	}
	return false
}
