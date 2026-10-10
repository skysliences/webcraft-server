package game

import (
	"math"

	api "mcraft/backend/pkg/plugin"
)

const itemEgg = 333

// Effects never change a container or publish success while being prepared.
// The dispenser commits its source snapshot and this effect in one transaction.
type dispenserEntityEffect struct {
	Behavior string
	Validate func() bool
	Commit   func()
	Publish  func()
}

func dispenserEntityData(context map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range context {
		out[key] = value
	}
	out["actor"], out["source"] = "server", "dispenser"
	return out
}

func (s *Server) prepareDispenserEntity(r *Room, k uint64, state *MechanicState, one *Stack, data map[string]any) (*dispenserEntityEffect, bool, bool) {
	if state == nil || state.Kind != "dispenser" || one == nil || state.Facing < 0 || state.Facing >= len(facing6) {
		return nil, false, true
	}
	x, y, z := coords(k)
	dir := facing6[state.Facing]
	dx, dy, dz := x+dir[0], y+dir[1], z+dir[2]
	context := dispenserEntityData(data)
	context["origin"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	if typ := s.rules.byID[one.ID].SpawnEgg; typ != "" {
		context["reason"], context["behavior"], context["type"], context["count"] = "dispenser_spawn_egg", "spawn_egg", typ, 1
		context["x"], context["y"], context["z"] = float64(dx)+.5, float64(dy), float64(dz)+.5
		context["target"] = map[string]any{"kind": "position", "x": float64(dx) + .5, "y": float64(dy), "z": float64(dz) + .5}
		if !s.mobFits(r, typ, float64(dx)+.5, float64(dy), float64(dz)+.5) || !s.mobSolid(r, dx, dy-1, dz) || len(r.Mobs) >= maxWorldMobs {
			return nil, true, false
		}
		if !s.allowed(api.ItemSpawnEggUsing, r, nil, context) {
			return nil, true, false
		}
		mob, err := s.prepareMob(r, nil, typ, float64(dx)+.5, float64(dy), float64(dz)+.5, "dispenser_spawn_egg", context)
		if err != nil {
			return nil, true, false
		}
		context["entityId"], context["target"] = mob.ID, mobTarget(mob)
		data["entityId"], data["behavior"] = mob.ID, "spawn_egg"
		return &dispenserEntityEffect{Behavior: "spawn_egg", Validate: func() bool {
			return len(r.Mobs) < maxWorldMobs && r.Mobs[mob.ID] == nil && s.mobFits(r, typ, mob.X, mob.Y, mob.Z) && s.mobSolid(r, dx, dy-1, dz)
		}, Commit: func() { s.commitMob(r, mob) }, Publish: func() {
			s.publishPreparedMob(r, nil, mob, "dispenser_spawn_egg", context)
			s.emit(api.ItemSpawnEggUsed, r, nil, context)
		}}, true, true
	}
	if one.ID == itemEgg {
		pr := &Projectile{Type: projEgg, Gravity: .03, Drag: .99, Weapon: itemSnapshot(one), Shooter: &api.Combatant{Kind: "block", X: float64(x), Y: float64(y), Z: float64(z)}, Actor: "server", Source: "dispenser"}
		pr.X, pr.Y, pr.Z = float64(x)+.5+float64(dir[0])*.7, float64(y)+.5+float64(dir[1])*.7, float64(z)+.5+float64(dir[2])*.7
		pr.VX, pr.VY, pr.VZ = float64(dir[0])*1.1, float64(dir[1])*1.1, float64(dir[2])*1.1
		pr.Kind, pr.Data = 3, itemEgg
		context["reason"], context["behavior"] = "dispenser_egg", "egg_projectile"
		context["target"] = map[string]any{"kind": "projectile", "type": "egg", "x": pr.X, "y": pr.Y, "z": pr.Z}
		event, ok := s.prepareProjectile(r, pr, context)
		if !ok {
			return nil, true, false
		}
		data["behavior"] = "egg_projectile"
		return &dispenserEntityEffect{Behavior: "egg_projectile", Validate: func() bool { return len(r.rt().projectiles) < 512 }, Commit: func() {
			s.commitProjectile(r, pr)
			data["entityId"] = pr.ID
		}, Publish: func() { s.publishProjectile(r, pr, event) }}, true, true
	}
	if one.ID != itemMinecart && one.ID != itemBoat {
		return nil, false, true
	}
	v := &Vehicle{Type: "minecart", Health: 20, Item: copyStack(one), X: float64(dx) + .5, Y: float64(dy) + .1, Z: float64(dz) + .5}
	if one.ID == itemMinecart {
		ry := dy
		if !isRailBlock(s.block(r, dx, ry, dz)) {
			if s.block(r, dx, dy, dz) != 0 || !isRailBlock(s.block(r, dx, dy-1, dz)) {
				return nil, false, true // vanilla fallback: eject the minecart item
			}
			ry--
		}
		rail := railState(r, blockKey(dx, ry, dz))
		if rail == nil {
			return nil, false, true
		}
		var tx, tz float64
		v.X, v.Y, v.Z, tx, _, tz = vehicleRailPoint(dx, ry, dz, rail.Shape, .5)
		v.Yaw = math.Atan2(-tx, -tz)
	} else {
		v.Type, v.Health = "boat", 40
		v.Yaw = math.Atan2(-float64(dir[0]), -float64(dir[2]))
		// Keep the hull clear of the source block when the water surface is
		// one block below the mouth, while retaining it in the front cell.
		v.X, v.Z = float64(x)+.5+float64(dir[0])*1.25, float64(z)+.5+float64(dir[2])*1.25
		if isWater(s.block(r, dx, dy, dz)) {
			v.Y = float64(dy) + .8
		} else if s.block(r, dx, dy, dz) == 0 && isWater(s.block(r, dx, dy-1, dz)) {
			v.Y = float64(dy-1) + .8
		} else {
			return nil, false, true // vanilla fallback: eject the boat item
		}
	}
	if !s.vehicleFits(r, v.Type, v.X, v.Y, v.Z) {
		return nil, false, true
	}
	for _, other := range vehicleStore(r) {
		if math.Hypot(other.X-v.X, other.Z-v.Z) < .8 && math.Abs(other.Y-v.Y) < 1 {
			return nil, false, true
		}
	}
	if len(vehicleStore(r)) >= maxWorldVehicles {
		return nil, true, false
	}
	id, next := s.vehicleID(r)
	if id == 0 {
		return nil, true, false
	}
	v.ID = id
	for field, value := range vehicleData(v, nil, "dispenser_vehicle") {
		context[field] = value
	}
	context["behavior"], context["count"] = v.Type, 1
	if !s.allowed(api.VehiclePlacing, r, nil, context) {
		return nil, true, false
	}
	detectorCommit, detectorPublish, ok := s.prepareDispenserVehicleDetector(r, v, context)
	if !ok {
		return nil, true, false
	}
	data["entityId"], data["behavior"] = id, v.Type
	return &dispenserEntityEffect{Behavior: v.Type, Validate: func() bool {
		return len(vehicleStore(r)) < maxWorldVehicles && vehicleStore(r)[id] == nil && s.vehicleFits(r, v.Type, v.X, v.Y, v.Z)
	}, Commit: func() {
		vehicleStore(r)[id], r.blockExt().NextVehicleID, r.Dirty = v, next, true
		syncVehicleView(r)
		if detectorCommit != nil {
			detectorCommit()
		}
	}, Publish: func() {
		if detectorPublish != nil {
			detectorPublish()
		}
		s.emit(api.VehiclePlaced, r, nil, context)
		s.syncTransport(r)
	}}, true, true
}

func (s *Server) prepareDispenserVehicleDetector(r *Room, v *Vehicle, context map[string]any) (func(), func(), bool) {
	if v.Type != "minecart" {
		return nil, nil, true
	}
	k, x, y, z, rail := s.vehicleRail(r, v)
	if rail == nil || s.block(r, x, y, z) != blkDetectorRail || rail.Powered {
		return nil, nil, true
	}
	next := cloneRail(rail)
	next.Power, next.Powered = 15, true
	data := s.railData(r, nil, k, next, "rail_occupancy")
	for key, value := range context {
		switch key {
		case "target", "x", "y", "z", "railBefore", "railAfter", "block", "reason":
			continue
		}
		data[key] = value
	}
	data["vehicleBefore"], data["vehicleAfter"], data["trigger"] = (*Vehicle)(nil), cloneVehicle(v), vehicleTarget(v)
	if !s.allowed(api.RailChanging, r, nil, data) {
		return nil, nil, false
	}
	return func() {
			storeRail(r, k, next)
			r.rt().redstoneDirty = true
		}, func() {
			s.broadcastRail(r, k)
			s.emit(api.RailChanged, r, nil, data)
		}, true
}

// Hatching is a separate protected effect of an already launched egg. A veto
// prevents the chicks, while the impact still destroys the thrown egg.
func (s *Server) hatchEgg(r *Room, pr *Projectile) bool {
	count := 0
	if s.rng.Intn(8) == 0 {
		count = 1
		if s.rng.Intn(32) == 0 {
			count = 4
		}
	}
	return s.hatchEggCount(r, pr, count)
}

func (s *Server) hatchEggCount(r *Room, pr *Projectile, count int) bool {
	if pr == nil || pr.Type != projEgg || (count != 0 && count != 1 && count != 4) || len(r.Mobs)+count > maxWorldMobs {
		return false
	}
	data := projectileData(pr, "egg_hatch")
	data["count"], data["type"], data["baby"] = count, "chicken", true
	data["target"] = map[string]any{"kind": "position", "x": pr.X, "y": pr.Y, "z": pr.Z}
	if pr.Shooter != nil {
		data["origin"] = pr.Shooter
	}
	if !s.allowed(api.ItemEggHatching, r, pr.Owner, data) {
		return false
	}
	mobs := []*Mob{}
	reserved := map[uint32]bool{}
	for i := 0; i < count; i++ {
		// The current mob kernel walks on block surfaces. Use the first safe
		// surface below the impact instead of inventing a floating chick.
		found := false
		for by := int(math.Floor(pr.Y)); by >= max(1, int(math.Floor(pr.Y))-4); by-- {
			if !s.mobSolid(r, int(math.Floor(pr.X)), by-1, int(math.Floor(pr.Z))) || !s.mobFits(r, "chicken", pr.X, float64(by), pr.Z) {
				continue
			}
			mob, err := s.prepareMobWithFlags(r, pr.Owner, "chicken", pr.X, float64(by), pr.Z, "egg_hatch", data, mobFlagBaby, reserved)
			if err != nil {
				return false
			}
			reserved[mob.ID], found = true, true
			mobs = append(mobs, mob)
			break
		}
		if !found {
			return false
		}
	}
	ids := make([]uint32, 0, len(mobs))
	for _, mob := range mobs {
		s.commitMob(r, mob)
		ids = append(ids, mob.ID)
	}
	data["entityIds"] = ids
	for _, mob := range mobs {
		s.publishPreparedMob(r, pr.Owner, mob, "egg_hatch", data)
	}
	s.emit(api.ItemEggHatched, r, pr.Owner, data)
	return true
}

func (s *Server) growMob(r *Room, m *Mob) {
	if m.Flags&mobFlagBaby == 0 || m.GrowthTicks <= 0 {
		return
	}
	if m.GrowthTicks > 2 {
		m.GrowthTicks -= 2
		return
	}
	data := mobData(m, "maturation")
	data["actor"], data["source"], data["target"] = "server", "growth", mobTarget(m)
	data["flagsBefore"], data["flagsAfter"] = m.Flags, m.Flags & ^mobFlagBaby
	if !s.allowed(api.EntityGrowing, r, nil, data) {
		return
	}
	m.Flags &= ^mobFlagBaby
	m.GrowthTicks = 0
	r.Dirty = true
	data["flags"], data["growthTicks"] = m.Flags, 0
	s.emit(api.EntityGrown, r, nil, data)
}
