package game

const (
	blkWitherSoulSand       = 181
	blkWitherSkeletonSkull  = 178
	itemWitherSkeletonSkull = 364
)

type witherStructure struct {
	X, Y, Z, DX, DZ int
	Before          [][4]int
}

func (s *Server) findWitherStructure(r *Room, x, y, z int, futureSkull bool) *witherStructure {
	if y < 3 || y > 125 || !r.inside(x, z) || s.difficulty(r) == "peaceful" {
		return nil
	}
	if futureSkull && s.block(r, x, y, z) != 0 {
		return nil
	}
	get := func(bx, by, bz int) int {
		if futureSkull && bx == x && by == y && bz == z {
			return blkWitherSkeletonSkull
		}
		return s.block(r, bx, by, bz)
	}
	for _, candidate := range []struct{ cx, cz, dx, dz int }{{x, z, 1, 0}, {x - 1, z, 1, 0}, {x + 1, z, 1, 0}, {x, z, 0, 1}, {x, z - 1, 0, 1}, {x, z + 1, 0, 1}} {
		cx, cz, dx, dz := candidate.cx, candidate.cz, candidate.dx, candidate.dz
		cells := [][4]int{{cx, y - 2, cz, blkWitherSoulSand}, {cx, y - 1, cz, blkWitherSoulSand}, {cx - dx, y - 1, cz - dz, blkWitherSoulSand}, {cx + dx, y - 1, cz + dz, blkWitherSoulSand}, {cx, y, cz, blkWitherSkeletonSkull}, {cx - dx, y, cz - dz, blkWitherSkeletonSkull}, {cx + dx, y, cz + dz, blkWitherSkeletonSkull}}
		valid := true
		for _, at := range cells {
			if !r.inside(at[0], at[2]) || get(at[0], at[1], at[2]) != at[3] {
				valid = false
				break
			}
		}
		if !valid || get(cx-dx, y-2, cz-dz) != 0 || get(cx+dx, y-2, cz+dz) != 0 {
			continue
		}
		before := make([][4]int, 0, len(cells))
		for _, at := range cells {
			before = append(before, [4]int{at[0], at[1], at[2], s.block(r, at[0], at[1], at[2])})
		}
		return &witherStructure{X: cx, Y: y, Z: cz, DX: dx, DZ: dz, Before: before}
	}
	return nil
}

// prepareWitherSummon protects both the entity and every consumed structural
// block before deleting any material. A failed mob spawn leaves the T intact.
func (s *Server) prepareWitherSummon(r *Room, p *Peer, x, y, z int, futureSkull bool, reason string, context map[string]any) (*dispenserFirePlan, bool, bool) {
	structure := s.findWitherStructure(r, x, y, z, futureSkull)
	if structure == nil {
		return nil, false, true
	}
	data := dispenserFireContext(context)
	actor := "server"
	if p != nil {
		actor = p.sid
	}
	if _, ok := data["actor"]; !ok {
		data["actor"] = actor
	}
	if _, ok := data["source"]; !ok {
		data["source"] = s.pluginSource
	}
	data["reason"], data["entityType"] = reason, "wither"
	data["target"] = map[string]any{"kind": "entity", "type": "wither", "x": float64(structure.X) + .5, "y": float64(y) + 1, "z": float64(structure.Z) + .5}
	data["structureBefore"] = append([][4]int(nil), structure.Before...)
	data["originTarget"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	plan := &dispenserFirePlan{Context: data}
	for _, at := range structure.Before {
		plan.Edits = append(plan.Edits, [4]int{at[0], at[1], at[2], 0})
	}
	data["structureAfter"] = append([][4]int(nil), plan.Edits...)
	mob, err := s.prepareMob(r, p, "wither", float64(structure.X)+.5, float64(y)+1, float64(structure.Z)+.5, reason, data)
	if err != nil || !validMob(mob) {
		return nil, true, false
	}
	data["entityId"] = mob.ID
	plan.Valid = func() bool {
		if !s.mobCapacity(r, 1) || r.Mobs[mob.ID] != nil {
			return false
		}
		fresh := s.findWitherStructure(r, x, y, z, futureSkull)
		if fresh == nil || fresh.X != structure.X || fresh.Y != structure.Y || fresh.Z != structure.Z || fresh.DX != structure.DX || fresh.DZ != structure.DZ {
			return false
		}
		for _, at := range structure.Before {
			if s.block(r, at[0], at[1], at[2]) != at[3] {
				return false
			}
		}
		return true
	}
	plan.Commit = func() { s.commitMob(r, mob); getMobExt(mob.ID).ChargeTicks = 100 }
	plan.Publish = func() {
		s.publishPreparedMob(r, p, mob, reason, data)
		s.tntSound(r, "entity.wither.spawn", mob.X, mob.Y, mob.Z)
		for peer := range r.Peers {
			peer.sys("§5凋灵已被召唤！它正在汇聚力量...")
		}
	}
	return plan, true, true
}

func (s *Server) prepareDispenserWither(r *Room, k uint64, slot int, original *Stack, x, y, z int, context map[string]any) (handled bool, plan *dispenserFirePlan, ok bool) {
	if original == nil || original.ID != itemWitherSkeletonSkull && original.ID != blkWitherSkeletonSkull {
		return false, nil, true
	}
	if !s.validStack(original, s.rules.max(original.ID)) {
		return true, nil, false
	}
	data := dispenserFireContext(context)
	ox, oy, oz := coords(k)
	after := sized(original, original.Count-1)
	data["actor"], data["source"], data["slot"] = "server", "dispenser", slot
	data["origin"] = map[string]any{"kind": "block", "x": ox, "y": oy, "z": oz}
	data["stack"], data["before"], data["after"] = itemSnapshot(sized(original, 1)), itemSnapshot(original), itemSnapshot(after)
	if slots, yes := data["containerBefore"].(Stacks); yes {
		next := cloneStacks(slots)
		if slot < 0 || slot >= len(next) {
			return true, nil, false
		}
		next[slot] = copyStack(after)
		data["containerAfter"] = next
	}
	plan, handled, ok = s.prepareWitherSummon(r, nil, x, y, z, true, "dispenser_wither", data)
	if plan != nil {
		plan.After = after
	}
	return handled, plan, ok
}

func (s *Server) checkWitherSpawn(r *Room, p *Peer, x, y, z int, snapshots ...*Stack) {
	var original *Stack
	if len(snapshots) > 0 && snapshots[0] != nil {
		original = copyStack(snapshots[0])
	} else if p != nil {
		original = copyStack(s.heldItem(p))
	}
	data := map[string]any{"stack": itemSnapshot(original), "before": itemSnapshot(original), "after": itemSnapshot(original), "slot": -1}
	if p != nil {
		data["slot"] = p.state.Inv.Selected
	}
	plan, found, ok := s.prepareWitherSummon(r, p, x, y, z, false, "wither_structure", data)
	if !found || !ok || !plan.Valid() {
		return
	}
	if !s.setBlocksValidated(r, plan.Edits, setQuiet, "wither_structure", plan.Commit, plan.Context, plan.Valid) {
		return
	}
	plan.Publish()
}
