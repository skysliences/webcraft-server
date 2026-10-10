package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"sort"
)

// Utilities prepare their secondary actions without changing the world. The
// dispenser commits these callbacks with its container and signal transaction.
type dispenserUtility struct {
	Behavior    string
	Target      map[string]any
	Edits       [][4]int
	SourceAfter *Stack
	Validate    func() bool
	Commit      func()
	Publish     func()
}

func utilityContext(context map[string]any, reason string) map[string]any {
	data := map[string]any{"reason": reason}
	for field, value := range context {
		data[field] = value
	}
	data["reason"] = reason
	return data
}

func utilityContainerAfter(data map[string]any, after *Stack) {
	if slots, ok := data["containerBefore"].(Stacks); ok {
		if slot, ok := data["slot"].(int); ok && slot >= 0 && slot < len(slots) {
			next := cloneStacks(slots)
			next[slot] = copyStack(after)
			data["containerAfter"] = next
			data["sourceSlot"] = slot
		}
	}
}

func (s *Server) prepareDispenserUtility(r *Room, origin uint64, x, y, z int, one, original *Stack, context map[string]any) (*dispenserUtility, bool, bool) {
	if one == nil || original == nil {
		return nil, false, true
	}
	if one.ID == itemBoneMeal {
		growth, ok := s.prepareFertilization(r, nil, x, y, z, one, context)
		if !ok {
			return nil, true, false
		}
		return &dispenserUtility{Behavior: "bone_meal", Edits: growth.Edits, SourceAfter: sized(original, original.Count-1), Validate: growth.Validate, Publish: growth.Publish}, true, true
	}
	if s.rules.byID[one.ID].Key == "shears" {
		for _, id := range sortedMobIDs(r) {
			mob := r.Mobs[id]
			if mob.Type != "sheep" || !utilityMobIntersects(mob, x, y, z) || !shearable(mob) {
				continue
			}
			shear, ok := s.prepareShearing(r, nil, mob, original, context)
			if !ok {
				return nil, true, false
			}
			return &dispenserUtility{Behavior: "shears", Target: map[string]any{"kind": "entity", "entityId": mob.ID, "type": mob.Type, "x": mob.X, "y": mob.Y, "z": mob.Z, "health": mob.Health}, SourceAfter: shear.After, Validate: func() bool { return shear.Validate() && utilityMobIntersects(mob, x, y, z) }, Commit: shear.Commit, Publish: shear.Publish}, true, true
		}
		return nil, true, false
	}
	if slot := s.equipmentSlot(one); slot >= 0 {
		peers := make([]*Peer, 0, len(r.Peers))
		for p := range r.Peers {
			if p.state != nil && p.state.Inv != nil && !p.state.Dead && !s.isSpectator(p) && p.state.Inv.Slots[slot] == nil && utilityPlayerIntersects(p, x, y, z) {
				peers = append(peers, p)
			}
		}
		sort.Slice(peers, func(i, j int) bool { return peers[i].id < peers[j].id })
		if len(peers) == 0 {
			return nil, false, true // Equipment without a wearer is a normal drop.
		}
		p := peers[0]
		before, after := cloneStacks(p.state.Inv.Slots), cloneStacks(p.state.Inv.Slots)
		after[slot] = copyStack(one)
		data := utilityContext(context, "dispenser_equip")
		utilityContainerAfter(data, sized(original, original.Count-1))
		data["behavior"] = "equip"
		data["target"] = map[string]any{"kind": "player", "playerId": p.id, "accountId": p.sid, "x": p.state.X, "y": p.state.Y, "z": p.state.Z}
		data["stack"], data["before"], data["after"] = itemSnapshot(one), itemSnapshot(before[slot]), itemSnapshot(after[slot])
		data["equipmentSlot"], data["slot"] = []string{"feet", "legs", "chest", "head"}[slot-36], slot
		data["inventoryBefore"], data["inventoryAfter"] = cloneStacks(before), cloneStacks(after)
		if !s.allowed(api.PlayerEquipping, r, p, data) || !s.allowed(api.InventoryChanging, r, p, data) || !reflect.DeepEqual(p.state.Inv.Slots, before) || !s.mayPlace(p, slot, one) {
			return nil, true, false
		}
		return &dispenserUtility{Behavior: "equip", Target: data["target"].(map[string]any), SourceAfter: sized(original, original.Count-1), Validate: func() bool {
			return p.room == r && r.Peers[p] && p.state != nil && p.state.Inv != nil && !p.state.Dead && !s.isSpectator(p) && reflect.DeepEqual(p.state.Inv.Slots, before) && utilityPlayerIntersects(p, x, y, z)
		}, Commit: func() { copy(p.state.Inv.Slots, after) }, Publish: func() {
			s.emit(api.InventoryChanged, r, p, data)
			s.emit(api.PlayerEquipped, r, p, data)
			s.inventory(p, 0, 0)
			soundAt(r, "item.armor.equip_generic", p.state.X, p.state.Y, p.state.Z, nil)
		}}, true, true
	}
	return nil, false, true
}

// Head decorations use the same placement rule in manual inventory and
// automatic equipment. An uncarved pumpkin and a shield are ordinary drops.
func (s *Server) equipmentSlot(v *Stack) int {
	if v == nil {
		return -1
	}
	it := s.rules.byID[v.ID]
	if it.Armor != nil {
		if slot, ok := map[string]int{"feet": 36, "legs": 37, "chest": 38, "head": 39}[it.Armor.Slot]; ok {
			return slot
		}
	}
	if it.Key == "wither_skeleton_skull" || it.Name == "Wither Skeleton Skull" {
		return 39
	}
	return -1
}

func utilityPlayerIntersects(p *Peer, x, y, z int) bool {
	return p.state.X+.3 > float64(x) && p.state.X-.3 < float64(x+1) && p.state.Y+1.8 > float64(y) && p.state.Y < float64(y+1) && p.state.Z+.3 > float64(z) && p.state.Z-.3 < float64(z+1)
}

func utilityMobIntersects(mob *Mob, x, y, z int) bool {
	spec := mobSpecs[mob.Type]
	return mob.X+spec.Width/2 > float64(x) && mob.X-spec.Width/2 < float64(x+1) && mob.Y+spec.Height > float64(y) && mob.Y < float64(y+1) && mob.Z+spec.Width/2 > float64(z) && mob.Z-spec.Width/2 < float64(z+1)
}

type preparedGrowth struct {
	Edits    [][4]int
	Validate func() bool
	Publish  func()
}

func nextCropBlock(b int, mature bool) int {
	switch {
	case b >= blkWheatMin && b < blkWheatMax:
		if mature {
			return blkWheatMax
		}
		return b + 1
	case b >= blkCarrotMin && b < blkCarrotMax:
		if mature {
			return blkCarrotMax
		}
		return b + 1
	case b >= blkPotatoMin && b < blkPotatoMid:
		if mature {
			return blkPotatoMax
		}
		return b + 1
	case b == blkPotatoMid:
		return blkPotatoMax
	}
	return 0
}

func (s *Server) treeGrowthEdits(r *Room, x, y, z, sapling int) [][4]int {
	logBlock, leafBlock := blkOakLog, blkOakLeaves
	if sapling == blkBirchSapling {
		logBlock, leafBlock = blkBirchLog, blkBirchLeaves
	}
	if sapling == blkSpruceSapling {
		logBlock, leafBlock = blkSpruceLog, blkSpruceLeaves
	}
	height := 5 + s.rng.Intn(2)
	if !r.inside(x, z) || y < 1 || y+height+1 >= 128 {
		return nil
	}
	for dy := 1; dy <= height+1; dy++ {
		if b := s.block(r, x, y+dy, z); b != 0 && !s.leafBlock(b) {
			return nil
		}
	}
	changes := map[uint64][4]int{}
	for dy := 0; dy < height; dy++ {
		changes[blockKey(x, y+dy, z)] = [4]int{x, y + dy, z, logBlock}
	}
	top := y + height - 1
	for ly := top - 2; ly <= top+1; ly++ {
		rad := 2
		if ly >= top {
			rad = 1
		}
		for dx := -rad; dx <= rad; dx++ {
			for dz := -rad; dz <= rad; dz++ {
				if abs(dx) == rad && abs(dz) == rad && (ly == top+1 || s.rng.Intn(2) == 0) {
					continue
				}
				nx, nz := x+dx, z+dz
				k := blockKey(nx, ly, nz)
				if !r.inside(nx, nz) || changes[k][3] != 0 {
					continue
				}
				if s.block(r, nx, ly, nz) == 0 {
					changes[k] = [4]int{nx, ly, nz, leafBlock}
				}
			}
		}
	}
	keys := make([]uint64, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	edits := make([][4]int, 0, len(keys))
	for _, k := range keys {
		edits = append(edits, changes[k])
	}
	return edits
}

func (s *Server) prepareFertilization(r *Room, p *Peer, x, y, z int, item *Stack, context map[string]any) (*preparedGrowth, bool) {
	b := s.block(r, x, y, z)
	var edits [][4]int
	switch {
	case isCrop(b):
		n := b
		for i, steps := 0, 2+s.rng.Intn(4); i < steps; i++ {
			if next := nextCropBlock(n, false); next != 0 {
				n = next
			}
		}
		if n == b {
			return nil, false
		}
		edits = [][4]int{{x, y, z, n}}
	case isSapling(b):
		edits = s.treeGrowthEdits(r, x, y, z, b)
	case b == blkGrassBlock:
		changes := map[uint64][4]int{}
		for i := 0; i < 64; i++ {
			nx, nz := x+s.rng.Intn(9)-4, z+s.rng.Intn(9)-4
			if !r.inside(nx, nz) || y >= 126 || s.block(r, nx, y, nz) != blkGrassBlock || s.block(r, nx, y+1, nz) != 0 {
				continue
			}
			plant := 63
			if s.rng.Intn(5) == 0 {
				plant = 64 + s.rng.Intn(3)
			}
			changes[blockKey(nx, y+1, nz)] = [4]int{nx, y + 1, nz, plant}
		}
		keys := make([]uint64, 0, len(changes))
		for k := range changes {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			edits = append(edits, changes[k])
		}
	case b == 63 && y < 126 && s.block(r, x, y+1, z) == 0:
		edits = [][4]int{{x, y, z, 74}, {x, y + 1, z, 75}}
	}
	if len(edits) == 0 {
		return nil, false
	}
	data := utilityContext(context, "bone_meal")
	if data["source"] == "dispenser" {
		data["behavior"] = "bone_meal"
	}
	data["x"], data["y"], data["z"], data["targetBlock"] = x, y, z, b
	data["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
	data["stack"], data["item"], data["changes"] = itemSnapshot(item), itemSnapshot(item), append([][4]int(nil), edits...)
	data["from"], data["to"] = b, edits[0][3]
	for _, at := range edits {
		if at[0] == x && at[1] == y && at[2] == z {
			data["to"] = at[3]
			break
		}
	}
	if !s.allowed(api.CropFertilizing, r, p, data) || !s.allowed(api.CropGrowing, r, p, data) {
		return nil, false
	}
	before := make([]int, len(edits))
	for i, at := range edits {
		before[i] = s.block(r, at[0], at[1], at[2])
	}
	return &preparedGrowth{Edits: edits, Validate: func() bool {
		if s.block(r, x, y, z) != b {
			return false
		}
		for i, at := range edits {
			if s.block(r, at[0], at[1], at[2]) != before[i] {
				return false
			}
		}
		return true
	}, Publish: func() {
		s.emit(api.CropGrown, r, p, data)
		s.emit(api.CropFertilized, r, p, data)
		soundAt(r, "item.bone_meal.use", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
	}}, true
}

type preparedShear struct {
	After           *Stack
	Validate        func() bool
	Commit, Publish func()
}

func shearable(mob *Mob) bool {
	return mob != nil && mob.Type == "sheep" && mob.Health > 0 && mob.Flags&(mobFlagBaby|mobFlagSheared) == 0
}

// Durability is prepared against a snapshot for both player tools and tools in
// a dispenser. A plugin can change only amount, with the same range validation.
func (s *Server) prepareUtilityWear(r *Room, p *Peer, before *Stack, reason string, context map[string]any) (*Stack, map[string]any, bool) {
	after := copyStack(before)
	maxWear := s.rules.byID[before.ID].Durability
	if maxWear <= 0 || before.Meta != nil && before.Meta.Unbreakable || p != nil && s.isCreative(p) {
		return after, nil, true
	}
	amount := 1
	if level := enchOf(before, "unbreaking"); level > 0 && s.rng.Intn(level+1) > 0 {
		amount = 0
	}
	if amount == 0 {
		return after, nil, true
	}
	data := utilityContext(context, reason)
	utilityContainerAfter(data, after)
	data["stack"], data["before"], data["amount"], data["damageBefore"] = itemSnapshot(before), itemSnapshot(before), amount, before.Damage
	e := s.emit(api.ItemWearing, r, p, data)
	wear := e.Number("amount")
	if e.Cancelled || !number(wear, 0, 1000) || wear != math.Trunc(wear) {
		return nil, nil, false
	}
	after.Damage += int(wear)
	data["amount"], data["damageAfter"], data["broken"] = int(wear), after.Damage, after.Damage >= maxWear
	if after.Damage >= maxWear {
		after = nil
	}
	data["after"] = itemSnapshot(after)
	utilityContainerAfter(data, after)
	return after, data, true
}

func (s *Server) prepareShearing(r *Room, p *Peer, mob *Mob, tool *Stack, context map[string]any) (*preparedShear, bool) {
	if !shearable(mob) || tool == nil || !s.validStack(tool, s.rules.max(tool.ID)) || s.rules.byID[tool.ID].Key != "shears" {
		return nil, false
	}
	data := utilityContext(context, "shear")
	if data["source"] == "dispenser" {
		data["behavior"] = "shears"
	}
	count := 1 + s.rng.Intn(3)
	data["mob_id"], data["mob_type"], data["x"], data["y"], data["z"], data["wool_count"] = mob.ID, mob.Type, mob.X, mob.Y, mob.Z, count
	data["target"] = map[string]any{"kind": "entity", "entityId": mob.ID, "type": mob.Type, "x": mob.X, "y": mob.Y, "z": mob.Z}
	data["stack"], data["tool"], data["flagsBefore"], data["flagsAfter"] = itemSnapshot(tool), itemSnapshot(tool), mob.Flags, mob.Flags|mobFlagSheared
	if !s.allowed(api.AnimalShearing, r, p, data) || !s.allowed(api.EntityShearing, r, p, data) {
		return nil, false
	}
	after, wear, ok := s.prepareUtilityWear(r, p, tool, "shear", data)
	if !ok {
		return nil, false
	}
	dropContext := map[string]any{}
	for k, v := range data {
		if k != "stack" && k != "before" && k != "after" && k != "item" && k != "count" {
			dropContext[k] = v
		}
	}
	beforeNext := r.NextDropID
	drops, err := s.prepareDropsWithContext(r, p, Stacks{stack(itemWhiteWool, count, 0)}, mob.X, mob.Y+.5, mob.Z, "shear", dropContext, true)
	if err != nil {
		r.NextDropID = beforeNext
		return nil, false
	}
	data["before"], data["after"] = itemSnapshot(tool), itemSnapshot(after)
	utilityContainerAfter(data, after)
	data["drops"] = make(Stacks, 0, len(drops))
	for _, d := range drops {
		data["drops"] = append(data["drops"].(Stacks), copyStack(d.Stack))
	}
	flags, health, mx, my, mz := mob.Flags, mob.Health, mob.X, mob.Y, mob.Z
	return &preparedShear{After: after, Validate: func() bool {
		return r.Mobs[mob.ID] == mob && shearable(mob) && mob.Flags == flags && mob.Health == health && mob.X == mx && mob.Y == my && mob.Z == mz
	}, Commit: func() {
		mob.Flags |= mobFlagSheared
		for _, d := range drops {
			r.Drops[d.ID] = d
		}
	}, Publish: func() {
		s.mobEvent(r, mob, 4, uint16(mob.Flags))
		soundAt(r, "entity.sheep.shear", mob.X, mob.Y, mob.Z, nil)
		s.publishDropsWithContext(r, p, drops, "shear", dropContext)
		if wear != nil {
			s.emit(api.ItemWorn, r, p, wear)
		}
		s.emit(api.AnimalSheared, r, p, data)
		s.emit(api.EntitySheared, r, p, data)
	}}, true
}

func (s *Server) useFertilizer(p *Peer, m Message) bool {
	if p == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !s.canBuild(p) || !s.reachable(p, m) {
		return false
	}
	r := p.room
	before := copyStack(s.heldItem(p))
	slot := p.state.Inv.Selected
	if before == nil || before.ID != itemBoneMeal || !s.validStack(before, s.rules.max(before.ID)) {
		return false
	}
	after := copyStack(before)
	if !s.isCreative(p) {
		after = sized(before, before.Count-1)
	}
	context := map[string]any{"actor": p.sid, "source": "player", "slot": slot, "stack": itemSnapshot(before), "before": itemSnapshot(before), "after": itemSnapshot(after), "count": 1}
	growth, ok := s.prepareFertilization(r, p, m.X, m.Y, m.Z, before, context)
	if !ok {
		return false
	}
	context["target"] = map[string]any{"kind": "block", "x": m.X, "y": m.Y, "z": m.Z}
	context["reason"] = "bone_meal"
	if !s.isCreative(p) && (!s.allowed(api.ItemConsuming, r, p, context) || !s.allowed(api.InventoryChanging, r, p, context)) {
		return false
	}
	if slot != p.state.Inv.Selected || !reflect.DeepEqual(s.heldItem(p), before) || !growth.Validate() {
		return false
	}
	if !s.setBlocksWithContext(r, growth.Edits, setQuiet, "bone_meal", func() { p.state.Inv.Slots[slot] = after }, context) {
		return false
	}
	if !s.isCreative(p) {
		s.emit(api.InventoryChanged, r, p, context)
		s.emit(api.ItemConsumed, r, p, context)
	}
	growth.Publish()
	s.inventory(p, 0, 0)
	return true
}

func (s *Server) shearAnimal(p *Peer, mob *Mob) (committed bool) {
	if p == nil || p.state == nil || p.state.Inv == nil || p.state.Dead || !s.canBuild(p) || !shearable(mob) {
		return false
	}
	dx, dy, dz := p.state.X-mob.X, p.state.Y-mob.Y, p.state.Z-mob.Z
	if dx*dx+dy*dy+dz*dz > 6.5*6.5 {
		return false
	}
	r := p.room
	slot := p.state.Inv.Selected
	tool := copyStack(s.heldItem(p))
	beforeNext := r.NextDropID
	defer func() {
		if !committed {
			r.NextDropID = beforeNext
		}
	}()
	context := map[string]any{"actor": p.sid, "source": "player", "slot": slot}
	shear, ok := s.prepareShearing(r, p, mob, tool, context)
	if !ok {
		return false
	}
	data := utilityContext(context, "shear")
	data["stack"], data["before"], data["after"] = itemSnapshot(tool), itemSnapshot(tool), itemSnapshot(shear.After)
	data["target"] = map[string]any{"kind": "entity", "entityId": mob.ID, "type": mob.Type, "x": mob.X, "y": mob.Y, "z": mob.Z}
	if !reflect.DeepEqual(tool, shear.After) && !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	if slot != p.state.Inv.Selected || !reflect.DeepEqual(tool, s.heldItem(p)) || !shear.Validate() {
		return false
	}
	p.state.Inv.Slots[slot] = shear.After
	shear.Commit()
	r.Dirty = true
	committed = true
	if !reflect.DeepEqual(tool, shear.After) {
		s.emit(api.InventoryChanged, r, p, data)
	}
	shear.Publish()
	s.inventory(p, 0, 0)
	return true
}
