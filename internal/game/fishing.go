package game

import (
	"math"
	"reflect"

	api "mcraft/backend/pkg/plugin"
)

type FishingBobber struct {
	ID        uint32  `json:"id"`
	OwnerID   uint16  `json:"ownerId"`
	Owner     string  `json:"owner"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	VX        float64 `json:"vx"`
	VY        float64 `json:"vy"`
	VZ        float64 `json:"vz"`
	InWater   bool    `json:"inWater"`
	WaitTicks   int     `json:"waitTicks"`
	BiteTicks   int     `json:"biteTicks"`
	HookedTicks int     `json:"hookedTicks"`
	Slot        int     `json:"slot"`
	Rod         *Stack  `json:"rod"`
	age         int
}
type fishingRuntime struct{ bobbers map[uint16]*FishingBobber }

func fishingState(r *Room) *fishingRuntime {
	if r.rt().fishing == nil {
		r.rt().fishing = &fishingRuntime{bobbers: map[uint16]*FishingBobber{}}
	}
	return r.rt().fishing
}
func cloneBobber(b *FishingBobber) *FishingBobber {
	if b == nil {
		return nil
	}
	v := *b
	v.Rod = copyStack(b.Rod)
	return &v
}
func fishingData(p *Peer, b *FishingBobber, reason string) map[string]any {
	return map[string]any{"reason": reason, "actor": transportActor(p), "target": map[string]any{"kind": "entity", "entityId": b.ID, "type": "fishing_bobber", "x": b.X, "y": b.Y, "z": b.Z}, "entityId": b.ID, "owner": b.Owner, "slot": b.Slot, "stack": itemSnapshot(b.Rod), "bobber": cloneBobber(b), "x": b.X, "y": b.Y, "z": b.Z}
}
func (s *Server) fishingID(r *Room) (uint32, uint32) {
	n := r.rt().nextEnt
	for i := 0; i < maxWorldVehicles+1; i++ {
		n = (n + 1) & transportIDMask
		if n == 0 {
			n = 1
		}
		id := fishingNamespace | n
		used := r.Drops[id] != nil || r.Mobs[id] != nil
		for _, b := range fishingState(r).bobbers {
			used = used || b.ID == id
		}
		if !used {
			return id, n
		}
	}
	return 0, 0
}
func (s *Server) useFishingRod(p *Peer, expected *api.ItemStack, compare bool) bool {
	if p == nil || p.room == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !p.room.usesSurvival() || !s.canBuild(p) {
		return false
	}
	held := s.heldItem(p)
	if held == nil || held.ID != itemFishingRod || !s.validStack(held, s.rules.max(held.ID)) || compare && !reflect.DeepEqual(itemSnapshot(held), expected) {
		return false
	}
	if b := fishingState(p.room).bobbers[p.id]; b != nil {
		return s.reelFishing(p, b)
	}
	if len(fishingState(p.room).bobbers) >= maxWorldVehicles {
		return false
	}
	dx, dy, dz := lookDir(p.state)
	id, next := s.fishingID(p.room)
	if id == 0 {
		return false
	}
	b := &FishingBobber{ID: id, OwnerID: p.id, Owner: p.sid, X: p.state.X + dx*.4, Y: p.state.Y + 1.4, Z: p.state.Z + dz*.4, VX: dx * .85, VY: dy*.85 + .16, VZ: dz * .85, WaitTicks: 60 + s.rng.Intn(100), Slot: p.state.Inv.Selected, Rod: copyStack(held)}
	if !number(b.X, p.room.minCoord()+.15, p.room.maxCoord()-.15) || !number(b.Y, .1, 127.5) || !number(b.Z, p.room.minCoord()+.15, p.room.maxCoord()-.15) || s.mobSolid(p.room, int(math.Floor(b.X)), int(math.Floor(b.Y)), int(math.Floor(b.Z))) {
		return false
	}
	data := fishingData(p, b, "fishing_cast")
	data["before"], data["after"] = itemSnapshot(held), itemSnapshot(held)
	if !s.allowed(api.FishingCasting, p.room, p, data) || p.state.Inv.Selected != b.Slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), itemSnapshot(b.Rod)) {
		return false
	}
	fishingState(p.room).bobbers[p.id], p.room.rt().nextEnt = b, next
	getVehicles(p.room).Bobbers[p.id] = b
	s.emit(api.FishingCast, p.room, p, data)
	soundAt(p.room, "entity.fishing_bobber.throw", b.X, b.Y, b.Z, nil)
	s.syncTransport(p.room)
	return true
}
func (s *Server) fishingLoot(r *Room, b *FishingBobber) *Stack {
	roll := s.rng.Float64()
	name := "Raw Cod"
	if roll < .6 {
		name = "Raw Cod"
	} else if roll < .85 {
		name = "Raw Salmon"
	} else if roll < .95 {
		name = "Pufferfish"
	} else if s.fishingOpenWater(r, b) {
		names := []string{"Bow", "Saddle", "Book"}
		name = names[s.rng.Intn(len(names))]
	} else {
		name = "Stick"
	}
	id := s.id(name)
	if id == 0 {
		switch name {
		case "Raw Cod":
			id = itemCod
		case "Raw Salmon":
			id = itemSalmon
		case "Pufferfish":
			id = itemPufferfish
		default:
			id = s.id("Stick")
		}
	}
	return stack(id, 1, 0)
}
func (s *Server) fishingOpenWater(r *Room, b *FishingBobber) bool {
	x, y, z := int(math.Floor(b.X)), int(math.Floor(b.Y)), int(math.Floor(b.Z))
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if !r.inside(x+dx, z+dz) || !isWater(s.block(r, x+dx, y, z+dz)) || s.mobSolid(r, x+dx, y+1, z+dz) {
				return false
			}
		}
	}
	return true
}
func (s *Server) reelFishing(p *Peer, b *FishingBobber) (committed bool) {
	r := p.room
	held := s.heldItem(p)
	if b == nil || fishingState(r).bobbers[p.id] != b || b.OwnerID != p.id || b.Owner != p.sid || p.state.Inv.Selected != b.Slot || held == nil || held.ID != itemFishingRod || !reflect.DeepEqual(itemSnapshot(held), itemSnapshot(b.Rod)) {
		return false
	}
	beforeBob, beforeRod := cloneBobber(b), itemSnapshot(held)
	slot := p.state.Inv.Selected
	stacks := Stacks{}
	xpAmount, wear := 0, 0
	if b.InWater && (b.BiteTicks > 0 || b.HookedTicks > 0) {
		stacks = append(stacks, s.fishingLoot(r, b))
		xpAmount = 1 + s.rng.Intn(5)
		wear = 1
	}
	data := fishingData(p, b, "fishing_reel")
	data["before"], data["after"], data["caught"], data["drops"], data["experience"] = beforeRod.Clone(), beforeRod.Clone(), len(stacks) > 0, leafDropSnapshots(stacks), xpAmount
	if !s.allowed(api.FishingReeling, r, p, data) {
		return false
	}
	previousID := r.NextDropID
	defer func() {
		if !committed {
			r.NextDropID = previousID
		}
	}()
	context := map[string]any{"actor": p.sid, "target": data["target"], "rod": beforeRod.Clone(), "bobber": beforeBob}
	drops, err := s.prepareDropsWithContext(r, p, stacks, p.state.X, p.state.Y+.8, p.state.Z, "fishing_reel", context, true)
	if err != nil {
		return false
	}
	var xp *experienceChange
	if xpAmount > 0 {
		xp = s.prepareExperience(p, xpAmount, false, "fishing_reel", context)
		if xp == nil {
			return false
		}
		data["experience"] = xp.Data["amount"]
	}
	after, wearData, ok := s.prepareTransportWear(p, held, wear, "fishing_reel", context)
	if !ok {
		return false
	}
	data["after"] = itemSnapshot(after)
	changed := !reflect.DeepEqual(beforeRod, itemSnapshot(after))
	if changed && !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	if slot != p.state.Inv.Selected || !reflect.DeepEqual(beforeRod, itemSnapshot(s.heldItem(p))) || !reflect.DeepEqual(beforeBob, cloneBobber(b)) || fishingState(r).bobbers[p.id] != b {
		return false
	}
	actual := Stacks{}
	for _, d := range drops {
		if !s.validDrop(d) || r.Drops[d.ID] != nil || r.Mobs[d.ID] != nil {
			return false
		}
		actual = append(actual, copyStack(d.Stack))
	}
	data["drops"] = leafDropSnapshots(actual)
	p.state.Inv.Slots[slot] = after
	delete(fishingState(r).bobbers, p.id)
	delete(getVehicles(r).Bobbers, p.id)
	if xp != nil {
		p.ext().XPLevel, p.ext().XPPoints, p.ext().XPTotal = xp.Next.XPLevel, xp.Next.XPPoints, xp.Next.XPTotal
	}
	r.Dirty, committed = true, true
	s.publishDropsWithContext(r, p, drops, "fishing_reel", context)
	if xp != nil {
		s.commitExperience(p, xp)
	}
	if wearData != nil {
		s.emit(api.ItemWorn, r, p, wearData)
	}
	if changed {
		s.emit(api.InventoryChanged, r, p, data)
		s.inventory(p, 0, 0)
	}
	s.emit(api.FishingReeled, r, p, data)
	soundAt(r, "entity.fishing_bobber.retrieve", p.state.X, p.state.Y+1.2, p.state.Z, nil)
	s.syncTransport(r)
	return true
}
func (s *Server) cleanupFishing(p *Peer, reason string) bool {
	if p == nil || p.room == nil {
		return false
	}
	b := fishingState(p.room).bobbers[p.id]
	if b == nil {
		return true
	}
	data := fishingData(p, b, reason)
	data["caught"], data["drops"], data["experience"] = false, []*api.ItemStack{}, 0
	data["before"], data["after"] = itemSnapshot(b.Rod), itemSnapshot(b.Rod)
	if !s.allowed(api.FishingReeling, p.room, p, data) {
		return false
	}
	delete(fishingState(p.room).bobbers, p.id)
	delete(getVehicles(p.room).Bobbers, p.id)
	s.emit(api.FishingReeled, p.room, p, data)
	s.syncTransport(p.room)
	return true
}
func (s *Server) tickFishing(r *Room) {
	for owner, b := range fishingState(r).bobbers {
		p := findPeerByID(r, owner)
		if p == nil || p.state == nil || p.state.Dead || p.sid != b.Owner || p.state.Inv == nil || p.state.Inv.Selected != b.Slot || !reflect.DeepEqual(itemSnapshot(s.heldItem(p)), itemSnapshot(b.Rod)) || math.Hypot(b.X-p.state.X, b.Z-p.state.Z) > 32 || b.age > 600 {
			// Offline ownership retains the original attribution and rod snapshot
			// until cleanup is approved; a veto never emits a false reel success.
			if p != nil {
				s.cleanupFishing(p, "fishing_owner_unavailable")
			} else {
				data := fishingData(nil, b, "fishing_owner_unavailable")
				data["actor"] = b.Owner
				data["caught"], data["experience"], data["drops"] = false, 0, []*api.ItemStack{}
				data["before"], data["after"] = itemSnapshot(b.Rod), itemSnapshot(b.Rod)
				if s.allowed(api.FishingReeling, r, nil, data) {
					delete(fishingState(r).bobbers, owner)
					delete(getVehicles(r).Bobbers, owner)
					s.emit(api.FishingReeled, r, nil, data)
				}
			}
			continue
		}
		b.age++
		bx, by, bz := int(math.Floor(b.X)), int(math.Floor(b.Y)), int(math.Floor(b.Z))
		if isWater(s.block(r, bx, by, bz)) {
			for by+1 < 128 && isWater(s.block(r, bx, by+1, bz)) {
				by++
			}
			b.InWater = true
			b.Y = float64(by) + .875
			b.VY = 0
			b.VX *= .8
			b.VZ *= .8
			if b.HookedTicks > 0 {
				b.HookedTicks--
				if b.HookedTicks == 0 {
					b.WaitTicks = 60 + s.rng.Intn(120)
					soundAt(r, "entity.fishing_bobber.splash", b.X, b.Y, b.Z, nil)
				}
			} else if b.BiteTicks > 0 {
				b.BiteTicks--
			} else if b.WaitTicks > 1 {
				b.WaitTicks--
			} else {
				data := fishingData(p, b, "fishing_bite")
				next := cloneBobber(b)
				next.WaitTicks, next.BiteTicks = 0, 40
				data["before"], data["after"] = cloneBobber(b), next
				if s.allowed(api.FishingBiting, r, p, data) {
					b.WaitTicks, b.BiteTicks = 0, 40
					s.emit(api.FishingBit, r, p, data)
					soundAt(r, "entity.fishing_bobber.splash", b.X, b.Y, b.Z, nil)
				} else {
					b.WaitTicks = 20
				}
			}
			if b.HookedTicks == 0 && b.BiteTicks == 0 && b.WaitTicks == 0 {
				b.WaitTicks = 60 + s.rng.Intn(120)
			}
		} else {
			b.InWater = false
			b.VY = max(-1, b.VY-.04)
			for i := 0; i < 8; i++ {
				x, y, z := b.X+b.VX/8, b.Y+b.VY/8, b.Z+b.VZ/8
				if !number(x, r.minCoord()+.1, r.maxCoord()-.1) || !number(y, .05, 127.75) || !number(z, r.minCoord()+.1, r.maxCoord()-.1) {
					b.VX, b.VY, b.VZ = 0, 0, 0
					break
				}
				if s.mobSolid(r, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))) {
					if b.VY < 0 && !s.mobSolid(r, int(math.Floor(b.X)), int(math.Floor(y))+1, int(math.Floor(b.Z))) {
						b.Y = math.Floor(y) + 1.02
					}
					b.VX, b.VY, b.VZ = 0, 0, 0
					break
				}
				b.X, b.Y, b.Z = x, y, z
				if isWater(s.block(r, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))) {
					b.InWater = true
					break
				}
			}
		}
	}
	s.syncTransport(r)
}
func init() {
	onMessage("use", func(s *Server, p *Peer, m Message) bool {
		held := s.heldItem(p)
		if held == nil || held.ID != itemFishingRod {
			return false
		}
		if m.S == 1 {
			if b := fishingState(p.room).bobbers[p.id]; b != nil && b.InWater && b.BiteTicks > 0 {
				b.HookedTicks = 300
				b.BiteTicks = 0
				s.syncTransport(p.room)
			}
			return true
		}
		if m.S == 2 {
			if b := fishingState(p.room).bobbers[p.id]; b != nil {
				b.HookedTicks = 0
				b.BiteTicks = 0
				b.WaitTicks = 60 + s.rng.Intn(120)
				s.syncTransport(p.room)
			}
			return true
		}
		if m.S == 0 {
			s.useFishingRod(p, nil, false)
		}
		return true
	})
	onRoomTick(func(s *Server, r *Room) {
		if r.usesSurvival() && len(r.Peers) > 0 {
			s.tickFishing(r)
		}
	})
}
