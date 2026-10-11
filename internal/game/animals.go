package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

const (
	mobFlagBaby         byte = 1
	mobFlagSheared      byte = 2
	itemWhiteWool            = 47
	animalLoveTicks          = 600
	animalBreedCooldown      = 6000
)

func animalFood(typ string, id int) bool {
	switch typ {
	case "cow", "sheep":
		return id == itemWheat
	case "pig":
		return id == itemCarrot
	case "chicken":
		return id == itemWheatSeeds
	}
	return false
}
func breedable(m *Mob) bool {
	return m != nil && m.Health > 0 && m.Flags&mobFlagBaby == 0 && m.BreedCooldown == 0
}
func animalSnapshot(m *Mob) map[string]any {
	if m == nil {
		return nil
	}
	d := mobData(m, "animal_breed")
	d["loveTicks"], d["breedCooldown"], d["breeder"], d["food"] = m.LoveTicks, m.BreedCooldown, m.Breeder, m.BreedFood.Clone()
	return d
}
func (s *Server) animalMate(r *Room, m *Mob) *Mob {
	for _, id := range sortedMobIDs(r) {
		other := r.Mobs[id]
		if other == m || !breedable(other) || other.Type != m.Type || other.LoveTicks <= 0 {
			continue
		}
		dx, dy, dz := m.X-other.X, m.Y-other.Y, m.Z-other.Z
		if dx*dx+dy*dy+dz*dz <= 9 && s.lineClear(r, [3]float64{m.X, m.Y + .5, m.Z}, [3]float64{other.X, other.Y + .5, other.Z}) {
			return other
		}
	}
	return nil
}

// Feeding an already-near loving pair and producing its baby is a single
// transaction. A distant first feeding is a completed feeding operation;
// any later birth is a separate protected operation and consumes no new food.
func (s *Server) feedAnimal(p *Peer, mob *Mob, expected *api.ItemStack, compare bool) bool {
	if p == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !s.canBuild(p) || !breedable(mob) || mob.LoveTicks > 0 || p.room.Mobs[mob.ID] != mob {
		return false
	}
	r := p.room
	dx, dy, dz := p.state.X-mob.X, p.state.Y-mob.Y, p.state.Z-mob.Z
	if dx*dx+dy*dy+dz*dz > 6.5*6.5 || !s.lineClear(r, [3]float64{p.state.X, p.state.Y + 1.62, p.state.Z}, [3]float64{mob.X, mob.Y + .5, mob.Z}) {
		return false
	}
	slot := p.state.Inv.Selected
	food := copyStack(s.heldItem(p))
	if food == nil || !animalFood(mob.Type, food.ID) || !s.validStack(food, s.rules.max(food.ID)) || compare && !reflect.DeepEqual(itemSnapshot(food), expected) {
		return false
	}
	before := animalSnapshot(mob)
	after := animalSnapshot(mob)
	after["loveTicks"], after["breeder"], after["food"] = animalLoveTicks, p.sid, itemSnapshot(sized(food, 1))
	consumed := 0
	if !s.isCreative(p) {
		consumed = 1
	}
	remainder := sized(food, food.Count-consumed)
	data := map[string]any{"reason": "animal_feed", "actor": p.sid, "source": "player", "target": mobTarget(mob), "mob_id": mob.ID, "mob_type": mob.Type, "stack": itemSnapshot(sized(food, 1)), "item": itemSnapshot(food), "food": itemSnapshot(sized(food, 1)), "count": 1, "consumed": consumed, "slot": slot, "before": itemSnapshot(food), "after": itemSnapshot(remainder), "parentBefore": before, "parentAfter": after, "breeder": p.sid}
	if !s.allowed(api.AnimalFeeding, r, p, data) {
		return false
	}
	mate := s.animalMate(r, mob)
	var plan *animalBirth
	if mate != nil {
		var ok bool
		plan, ok = s.prepareAnimalBirth(r, p, mob, mate, itemSnapshot(sized(food, 1)), p.sid, data)
		if !ok {
			return false
		}
	}
	if consumed > 0 && (!s.allowed(api.ItemConsuming, r, p, data) || !s.allowed(api.InventoryChanging, r, p, data)) {
		return false
	}
	if p.state.Dead || slot != p.state.Inv.Selected || !reflect.DeepEqual(food, s.heldItem(p)) || r.Mobs[mob.ID] != mob || !reflect.DeepEqual(before, animalSnapshot(mob)) || plan != nil && !plan.Validate() {
		return false
	}
	p.state.Inv.Slots[slot] = remainder
	mob.LoveTicks, mob.BreedFood, mob.Breeder = animalLoveTicks, itemSnapshot(sized(food, 1)), p.sid
	if plan != nil {
		plan.Commit()
	}
	data["parentAfter"] = animalSnapshot(mob)
	r.Dirty = true
	if consumed > 0 {
		s.emit(api.InventoryChanged, r, p, data)
		s.emit(api.ItemConsumed, r, p, data)
	}
	s.emit(api.AnimalFed, r, p, data)
	s.mobEvent(r, mob, 10, 0)
	if plan != nil {
		plan.Publish()
	}
	s.inventory(p, 0, 0)
	return true
}

type animalBirth struct {
	Validate func() bool
	Commit   func()
	Publish  func()
}

func (s *Server) prepareAnimalBirth(r *Room, p *Peer, a, b *Mob, food *api.ItemStack, breeder string, context map[string]any) (*animalBirth, bool) {
	if a == b || !breedable(a) || !breedable(b) || b.LoveTicks <= 0 || a.Type != b.Type || !s.mobCapacity(r, 1) {
		return nil, false
	}
	ab, bb := animalSnapshot(a), animalSnapshot(b)
	data := map[string]any{}
	for k, v := range context {
		data[k] = v
	}
	data["reason"], data["source"], data["actor"] = "animal_breed", "animal", breeder
	data["mob_id"], data["mob_type"], data["target"] = a.ID, a.Type, mobTarget(a)
	data["parent"], data["mate"] = mobTarget(a), mobTarget(b)
	data["parentBefore"], data["mateBefore"] = ab, bb
	data["food"], data["mateFood"], data["breeder"], data["mateBreeder"] = food.Clone(), b.BreedFood.Clone(), breeder, b.Breeder
	data["stack"], data["item"] = food.Clone(), food.Clone()
	if !s.allowed(api.AnimalBreeding, r, p, data) {
		return nil, false
	}
	baby, err := s.prepareMobWithFlags(r, p, a.Type, (a.X+b.X)/2, math.Min(a.Y, b.Y), (a.Z+b.Z)/2, "breed", data, mobFlagBaby, nil)
	if err != nil {
		return nil, false
	}
	data["baby_id"], data["baby"] = baby.ID, animalSnapshot(baby)
	valid := func() bool {
		return r.Mobs[a.ID] == a && r.Mobs[b.ID] == b && reflect.DeepEqual(ab, animalSnapshot(a)) && reflect.DeepEqual(bb, animalSnapshot(b)) && s.mobCapacity(r, 1) && r.Mobs[baby.ID] == nil && s.mobFits(r, baby.Type, baby.X, baby.Y, baby.Z) && s.mobSolid(r, int(math.Floor(baby.X)), int(math.Floor(baby.Y))-1, int(math.Floor(baby.Z)))
	}
	return &animalBirth{Validate: valid, Commit: func() {
		for _, parent := range []*Mob{a, b} {
			parent.LoveTicks = 0
			parent.BreedCooldown = animalBreedCooldown
			parent.BreedFood = nil
			parent.Breeder = ""
		}
		s.commitMob(r, baby)
		data["parentAfter"], data["mateAfter"] = animalSnapshot(a), animalSnapshot(b)
	}, Publish: func() {
		s.publishPreparedMob(r, p, baby, "breed", data)
		s.mobEvent(r, baby, 4, uint16(baby.Flags))
		s.spawnOrbs(r, baby.X, baby.Y+.5, baby.Z, 1+s.rng.Intn(5))
		s.emit(api.AnimalBred, r, p, data)
	}}, true
}

func (s *Server) tickAnimalBreeding(r *Room) {
	for _, id := range sortedMobIDs(r) {
		m := r.Mobs[id]
		if !s.mobActive(r, m) {
			continue
		}
		if m.BreedCooldown > 0 {
			m.BreedCooldown = max(0, m.BreedCooldown-2)
			r.Dirty = true
		}
		if m.LoveTicks > 0 {
			m.LoveTicks = max(0, m.LoveTicks-2)
			r.Dirty = true
			if m.LoveTicks == 0 {
				m.BreedFood = nil
				m.Breeder = ""
			}
		}
	}
	// Avoid repeatedly invoking synchronous plugins on every 100ms simulation tick.
	if s.tick%20 != 0 {
		return
	}
	for _, id := range sortedMobIDs(r) {
		m := r.Mobs[id]
		if !s.mobActive(r, m) {
			continue
		}
		if !breedable(m) || m.LoveTicks <= 0 || m.BreedFood == nil {
			continue
		}
		mate := s.animalMate(r, m)
		if mate == nil {
			continue
		}
		var breeder *Peer
		for p := range r.Peers {
			if p.sid == m.Breeder {
				breeder = p
				break
			}
		}
		plan, ok := s.prepareAnimalBirth(r, breeder, m, mate, m.BreedFood, m.Breeder, map[string]any{})
		if ok && plan.Validate() {
			plan.Commit()
			plan.Publish()
		}
	}
}

func init() {
	onMessage("animal", func(s *Server, p *Peer, m Message) bool {
		mob := p.room.Mobs[m.E]
		held := s.heldItem(p)
		if mob != nil && held != nil && mob.Type == "sheep" && held.ID == 304 {
			s.shearAnimal(p, mob)
		} else {
			s.feedAnimal(p, mob, nil, false)
		}
		return true
	})
	onRoomTick(func(s *Server, r *Room) { s.tickAnimalBreeding(r) })
}
