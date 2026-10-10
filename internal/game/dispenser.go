package game

import (
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

// Every output is prepared before either inventory or world state is changed.
// A cancelled effect retains the selected item and never emits success.
func (s *Server) dispenseMechanic(r *Room, k uint64, state *MechanicState) bool {
	if state == nil || (state.Kind != "dispenser" && state.Kind != "dropper") || !s.validMechanic(state) {
		return false
	}
	x, y, z := coords(k)
	if mechanicKinds[s.block(r, x, y, z)] != state.Kind || !reflect.DeepEqual(mechanicSnapshot(r, k), state) {
		return false
	}
	previous := cloneMechanic(state)
	source := r.Containers[k]
	c := source
	if c == nil {
		c = &Container{Kind: state.Kind, Slots: make(Stacks, 9)}
	}
	if c.Kind != state.Kind || len(c.Slots) != 9 {
		return false
	}
	nextID := r.NextDropID
	committed := false
	defer func() {
		if !committed {
			r.NextDropID = nextID
		}
	}()
	// Reservoir selection gives each occupied slot an equal chance.
	slot, occupied := -1, 0
	for i, v := range c.Slots {
		if v != nil && s.validStack(v, s.rules.max(v.ID)) {
			occupied++
			if rand.Intn(occupied) == 0 {
				slot = i
			}
		}
	}
	next := cloneMechanic(state)
	next.Powered, next.Power = true, 15
	if slot < 0 {
		return s.changeMechanic(r, nil, k, next, "mechanic_signal")
	}
	original := copyStack(c.Slots[slot])
	one := sized(original, 1)
	beforeSlots, afterSlots := cloneStacks(c.Slots), cloneStacks(c.Slots)
	afterSlots[slot] = sized(original, original.Count-1)
	dir := facing6[state.Facing]
	dx, dy, dz := x+dir[0], y+dir[1], z+dir[2]
	if !r.inside(dx, dz) || dy < 1 || dy >= 128 {
		return false
	}
	destKey := blockKey(dx, dy, dz)
	data := s.mechanicData(r, nil, k, next, "mechanic_dispense")
	data["actor"] = "server"
	data["source"], data["behavior"] = state.Kind, "drop"
	data["origin"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	data["target"] = map[string]any{"kind": "block", "x": dx, "y": dy, "z": dz}
	data["stack"], data["before"], data["after"] = itemSnapshot(one), itemSnapshot(original), itemSnapshot(afterSlots[slot])
	data["slot"], data["containerBefore"], data["containerAfter"] = slot, cloneStacks(beforeSlots), cloneStacks(afterSlots)
	var dest *Container
	var destBefore, destAfter Stacks
	var edits [][4]int
	var effectValidate func() bool
	var effectCommit, effectPublish func()
	blockContext := data
	output := Stacks{}
	if state.Kind == "dropper" && r.Containers[destKey] != nil {
		dest = r.Containers[destKey]
		destBefore, destAfter = cloneStacks(dest.Slots), cloneStacks(dest.Slots)
		if !s.insertDispensed(destAfter, one, func(i int) bool {
			return s.mayPlace(&Peer{window: &Window{Kind: dest.Kind, Slots: dest.Slots}}, 46+i, one)
		}) {
			return false
		}
		data["behavior"] = "container_transfer"
		data["destinationBefore"], data["destinationAfter"] = cloneStacks(destBefore), cloneStacks(destAfter)
	} else if state.Kind == "dispenser" {
		handled := false
		if yes, fire, ok := s.prepareDispenserFire(r, k, slot, original, dx, dy, dz, data); yes {
			if !ok {
				return false
			}
			handled = true
			afterSlots[slot], edits, blockContext = copyStack(fire.After), fire.Edits, fire.Context
			effectValidate, effectCommit, effectPublish = fire.Valid, fire.Commit, fire.Publish
			data["behavior"] = "flint_and_steel"
			if one.ID == blkExplosiveTNT {
				data["behavior"] = "tnt"
			}
		}
		if !handled {
			if yes, structure, ok := s.prepareDispenserWither(r, k, slot, original, dx, dy, dz, data); yes {
				if !ok {
					return false
				}
				handled = true
				afterSlots[slot], edits, blockContext = copyStack(structure.After), structure.Edits, structure.Context
				effectValidate, effectCommit, effectPublish = structure.Valid, structure.Commit, structure.Publish
				data["behavior"] = "wither_structure"
			}
		}
		if !handled {
			if entity, yes, ok := s.prepareDispenserEntity(r, k, state, one, data); yes {
				if !ok {
					return false
				}
				handled = true
				data["behavior"] = entity.Behavior
				effectValidate, effectCommit, effectPublish = entity.Validate, entity.Commit, entity.Publish
			}
		}
		if !handled {
			if box, yes, ok := s.prepareDispenserBox(r, dx, dy, dz, one, data); yes {
				if !ok {
					return false
				}
				handled = true
				edits = box.Edits
				data["behavior"] = box.Behavior
				effectValidate, effectCommit, effectPublish = box.Validate, box.Commit, box.Publish
			}
		}
		if !handled {
			if utility, yes, ok := s.prepareDispenserUtility(r, k, dx, dy, dz, one, original, data); yes {
				if !ok {
					return false
				}
				handled = true
				afterSlots[slot], edits = copyStack(utility.SourceAfter), utility.Edits
				data["behavior"] = utility.Behavior
				if utility.Target != nil {
					data["target"] = utility.Target
				}
				effectValidate, effectCommit, effectPublish = utility.Validate, utility.Commit, utility.Publish
			}
		}
		if !handled && (one.ID == itemArrow || one.ID == itemSnowball) {
			typ, gravity, damage := projArrow, .068, 3.
			if one.ID == itemSnowball {
				typ, gravity, damage = projSnowball, .03, 0
			}
			vy := float64(dir[1]) * 1.1
			if typ == projArrow && dir[1] == 0 {
				vy += 0.05 // 水平发射带有微弱上抛弧线
			}
			pr := &Projectile{Type: typ, VX: float64(dir[0]) * 1.2, VY: vy, VZ: float64(dir[2]) * 1.2, Gravity: gravity, Drag: .99, Damage: damage, Weapon: itemSnapshot(one), Shooter: &api.Combatant{Kind: "block", X: float64(x), Y: float64(y), Z: float64(z)}}
			pr.X, pr.Y, pr.Z = float64(x)+.5+float64(dir[0])*.7, float64(y)+.5+float64(dir[1])*.7, float64(z)+.5+float64(dir[2])*.7
			pr.Kind = 3
			if one.ID == itemSnowball {
				pr.Data = itemSnowball
			}
			data["behavior"] = "projectile"
			event, ok := s.prepareProjectile(r, pr, data)
			if !ok {
				return false
			}
			handled = true
			effectValidate = func() bool { return len(r.rt().projectiles) < 512 }
			effectCommit = func() { s.commitProjectile(r, pr); data["entityId"] = pr.ID }
			effectPublish = func() { s.publishProjectile(r, pr, event) }
		}
		if !handled {
			if fluid, yes, ok := s.prepareDispenserFluid(r, dx, dy, dz, one, data); yes {
				if !ok {
					return false
				}
				handled = true
				edits, effectPublish, effectValidate = fluid.Edits, fluid.Publish, fluid.Validate
				data["behavior"] = fluid.Behavior
				if afterSlots[slot] == nil {
					afterSlots[slot] = copyStack(fluid.Result)
				} else if !s.insertDispensed(afterSlots, fluid.Result, nil) {
					output = append(output, copyStack(fluid.Result))
				}
				fluid.Context["after"], fluid.Context["containerAfter"] = itemSnapshot(afterSlots[slot]), cloneStacks(afterSlots)
				blockContext = fluid.Context
			}
		}
		if !handled {
			output = append(output, one)
		}
	} else {
		output = append(output, one)
	}
	data["after"], data["containerAfter"] = itemSnapshot(afterSlots[slot]), cloneStacks(afterSlots)
	if !s.allowed(api.BlockDispensing, r, nil, data) || !s.allowed(api.MechanicChanging, r, nil, data) || !s.allowed(api.InventoryChanging, r, nil, data) {
		return false
	}
	dropContext := dispenserDropContext(data)
	drops, err := s.prepareDropsWithContext(r, nil, output, float64(dx)+.5, float64(dy)+.5, float64(dz)+.5, "mechanic_dispense", dropContext, true)
	if err != nil {
		return false
	}
	valid := func() bool {
		return r.Containers[k] == source && reflect.DeepEqual(c.Slots, beforeSlots) && reflect.DeepEqual(mechanicSnapshot(r, k), previous) && mechanicKinds[s.block(r, x, y, z)] == state.Kind && (dest == nil || r.Containers[destKey] == dest && reflect.DeepEqual(dest.Slots, destBefore)) && (effectValidate == nil || effectValidate())
	}
	if !valid() {
		return false
	}
	commit := func() {
		copy(c.Slots, afterSlots)
		r.Containers[k] = c
		if dest != nil {
			copy(dest.Slots, destAfter)
		}
		storeMechanic(r, k, next)
		for _, d := range drops {
			r.Drops[d.ID] = d
		}
		if effectCommit != nil {
			effectCommit()
		}
		r.Dirty = true
		committed = true
	}
	if len(edits) > 0 {
		reason := "mechanic_dispense"
		if v, ok := blockContext["reason"].(string); ok && v != "" {
			reason = v
		}
		if !s.setBlocksValidated(r, edits, 0, reason, commit, blockContext, valid) {
			return false
		}
	} else {
		commit()
	}
	s.broadcastMechanic(r, k)
	s.notifyMechanicContainers(r, k, destKey)
	s.emit(api.InventoryChanged, r, nil, data)
	s.emit(api.MechanicChanged, r, nil, data)
	s.emit(api.BlockDispensed, r, nil, data)
	if effectPublish != nil {
		effectPublish()
	}
	s.publishDropsWithContext(r, nil, drops, "mechanic_dispense", dropContext)
	return true
}

func dispenserDropContext(data map[string]any) map[string]any {
	out := map[string]any{}
	for field, value := range data {
		switch field {
		case "stack", "before", "after", "item", "itemId", "count", "entityId":
			continue
		}
		out[field] = value
	}
	out["input"], out["sourceBefore"], out["sourceAfter"] = data["stack"], data["before"], data["after"]
	return out
}

func (s *Server) insertDispensed(slots Stacks, v *Stack, allowed func(int) bool) bool {
	if v == nil || v.Count != 1 || !s.validStack(v, s.rules.max(v.ID)) {
		return false
	}
	for i, at := range slots {
		if at != nil && same(at, v) && at.Count < s.rules.max(v.ID) && (allowed == nil || allowed(i)) {
			slots[i] = sized(at, at.Count+1)
			return true
		}
	}
	for i, at := range slots {
		if at == nil && (allowed == nil || allowed(i)) {
			slots[i] = copyStack(v)
			return true
		}
	}
	return false
}
