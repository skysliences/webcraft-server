package game

import (
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"math"
	api "mcraft/backend/pkg/plugin"
	"sort"
	"time"
)

// Absolute save validation bound, independent from the configurable live limit.
const maxWorldMobs = 4096
const mobNamespace uint32 = 0x80000000

type mobSpec struct {
	Kind                         byte
	Health, Speed, Width, Height float64
	Hostile                      bool
	Drops                        []string
}

var mobSpecs = map[string]mobSpec{
	"cow":     {9, 10, 1, .9, 1.4, false, []string{"Raw Beef", "Leather"}},
	"pig":     {10, 10, 1.1, .9, .9, false, []string{"Raw Porkchop"}},
	"sheep":   {11, 8, 1, .9, 1.3, false, []string{"Raw Mutton", "White Wool"}},
	"chicken": {12, 4, 1.2, .4, .7, false, []string{"Raw Chicken", "Feather"}},
	"zombie":  {4, 20, 1.6, .6, 1.95, true, []string{"Rotten Flesh"}},
	"spider":  {7, 16, 2, 1.4, .9, true, []string{"String"}},
}

// Mob IDs occupy a separate namespace from item drops. Only the server moves,
// spawns, damages and removes mobs; clients receive the existing entity frames.
type Mob struct {
	ID                               uint32         `json:"id"`
	Type                             string         `json:"type"`
	X                                float64        `json:"x"`
	Y                                float64        `json:"y"`
	Z                                float64        `json:"z"`
	Yaw                              float64        `json:"yaw"`
	Health                           float64        `json:"health"`
	Flags                            byte           `json:"flags"`
	GrowthTicks                      int            `json:"growthTicks,omitempty"`
	LoveTicks                        int            `json:"loveTicks,omitempty"`
	BreedCooldown                    int            `json:"breedCooldown,omitempty"`
	BreedFood                        *api.ItemStack `json:"breedFood,omitempty"`
	Breeder                          string         `json:"breeder,omitempty"`
	SpawnReason                      string         `json:"spawnReason,omitempty"`
	Persistent                       bool           `json:"persistent,omitempty"`
	InactiveTicks                    int            `json:"inactiveTicks,omitempty"`
	walkMS, attackMS, burnMS, fleeMS int
	path                             []VoxelPos
	pathTicks                        int
	pathTarget                       [3]int
}

func validMob(m *Mob) bool {
	if m == nil {
		return false
	}
	if len(m.SpawnReason) > 64 || m.InactiveTicks < 0 || m.InactiveTicks > 1200000 {
		return false
	}
	if !math.IsNaN(m.Yaw) && !math.IsInf(m.Yaw, 0) {
		m.Yaw = math.Remainder(m.Yaw, math.Pi*2)
	}
	spec, ok := mobSpecs[m.Type]
	if !ok {
		return false
	}
	minHealth := .01
	if m.Type == "ender_dragon" {
		minHealth = 0
	}
	maxHealth := spec.Health
	if m.Type == "ender_dragon" && maxHealth < 200 {
		maxHealth = 200
	}
	if m.Health > maxHealth {
		m.Health = maxHealth
	}
	return m.ID&mobNamespace != 0 && m.ID != mobNamespace && number(m.X, -worldBorder+1, worldBorder-1) && number(m.Z, -worldBorder+1, worldBorder-1) && number(m.Y, 1, 126) && number(m.Yaw, -math.Pi*2, math.Pi*2) && number(m.Health, minHealth, maxHealth) && m.Flags & ^byte(15) == 0 && m.GrowthTicks >= 0 && m.GrowthTicks <= 24000 && m.LoveTicks >= 0 && m.LoveTicks <= animalLoveTicks && m.BreedCooldown >= 0 && m.BreedCooldown <= animalBreedCooldown && len(m.Breeder) <= 128 && (m.BreedFood == nil || m.BreedFood.Count == 1 && m.BreedFood.ID > 0 && validMeta(m.BreedFood.Meta) && validEnch(m.BreedFood.Ench) && animalFood(m.Type, m.BreedFood.ID))
}
func sortedMobIDs(r *Room) []uint32 {
	ids := make([]uint32, 0, len(r.Mobs))
	for id := range r.Mobs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func mobData(m *Mob, reason string) map[string]any {
	return map[string]any{"entityId": m.ID, "type": m.Type, "x": m.X, "y": m.Y, "z": m.Z, "health": m.Health, "flags": m.Flags, "growthTicks": m.GrowthTicks, "reason": reason, "spawnReason": m.SpawnReason, "category": mobCategory(m.Type), "persistent": mobPersistent(m), "inactiveTicks": m.InactiveTicks}
}
func isNight(t uint64) bool { t %= 24000; return t >= 13000 && t < 23000 }
func (s *Server) worldTick(r *Room) {
	if r.parent == nil && len(r.Peers) == 0 && r.online() > 0 {
		if s.gameRule(r, "doDaylightCycle") {
			r.Time += 2
		} // players are only in other dimensions
		r.Dirty = true
	}
	if len(r.Peers) == 0 {
		return
	}
	if r.parent == nil && s.gameRule(r, "doDaylightCycle") {
		r.Time += 2
	}
	r.Dirty = true
	if s.tick%10 == 0 {
		s.emit(api.WorldTimeChanged, r, nil, map[string]any{"time": r.root().Time, "reason": "tick"})
	}
	if s.mobSpawningAllowed(r) {
		s.spawnAmbientMobs(r)
	}
	s.mobsTick(r)
	s.extTick(r)
}
func (s *Server) setWorldTime(r *Room, p *Peer, ticks uint64, reason string) bool {
	if ticks > 1<<48 {
		return false
	}
	r = r.root()
	data := map[string]any{"time": ticks, "previous": r.Time, "reason": reason}
	if !s.allowed(api.WorldTimeChanging, r, p, data) {
		return false
	}
	r.Time, r.Dirty = ticks, true
	broadcastWorld(r, map[string]any{"t": "time", "time": ticks}, nil)
	s.emit(api.WorldTimeChanged, r, p, data)
	return true
}
func (s *Server) mobSolid(r *Room, x, y, z int) bool {
	kind := s.rules.Kinds[s.block(r, x, y, z)]
	return kind == 1 || kind == 2 || kind == 3
}
func (s *Server) mobFits(r *Room, typ string, x, y, z float64) bool {
	spec, ok := mobSpecs[typ]
	if !ok || !number(x, r.minCoord()+1, r.maxCoord()-1) || !number(z, r.minCoord()+1, r.maxCoord()-1) || !number(y, 1, 126) {
		return false
	}
	for _, dx := range []float64{-spec.Width/2 + .01, spec.Width/2 - .01} {
		for _, dz := range []float64{-spec.Width/2 + .01, spec.Width/2 - .01} {
			for by := int(math.Floor(y + .01)); by <= int(math.Floor(y+spec.Height-.01)); by++ {
				b := s.block(r, int(math.Floor(x+dx)), by, int(math.Floor(z+dz)))
				k := s.rules.Kinds[b]
				if k != 0 && k != 5 {
					return false
				}
			}
		}
	}
	return true
}
func (s *Server) spawnMob(r *Room, p *Peer, typ string, x, y, z float64, reason string) (*Mob, error) {
	m, err := s.prepareMob(r, p, typ, x, y, z, reason, nil)
	if err != nil {
		return nil, err
	}
	s.publishMob(r, p, m, reason, nil)
	return m, nil
}

// Preparation emits protection events without committing an entity or advancing
// its ID. Multi-resource operations can commit inventory and entity together.
func (s *Server) prepareMob(r *Room, p *Peer, typ string, x, y, z float64, reason string, context map[string]any) (*Mob, error) {
	return s.prepareMobWithFlags(r, p, typ, x, y, z, reason, context, 0, nil)
}
func (s *Server) prepareMobWithFlags(r *Room, p *Peer, typ string, x, y, z float64, reason string, context map[string]any, flags byte, reserved map[uint32]bool) (*Mob, error) {
	spec, ok := mobSpecs[typ]
	if !ok {
		return nil, fmt.Errorf("未知生物类型：%s。", typ)
	}
	if !s.mobCapacity(r, len(reserved)+1) {
		return nil, fmt.Errorf("世界生物数量已达安全上限（%d）。", s.config.MobSpawning.TotalLimit)
	}
	if spec.Hostile && s.difficulty(r) == "peaceful" && typ != "ender_dragon" && typ != "wither" && typ != "end_crystal" {
		return nil, fmt.Errorf("和平难度不能生成敌对生物。")
	}
	if typ != "ender_dragon" && typ != "end_crystal" && typ != "wither" && typ != "blaze" && (!s.mobFits(r, typ, x, y, z) || !s.mobSolid(r, int(math.Floor(x)), int(math.Floor(y))-1, int(math.Floor(z)))) {
		return nil, fmt.Errorf("生物需要站在空旷的地面上。")
	}
	nextID := r.NextMobID
	for {
		if nextID == 0 || nextID >= mobNamespace {
			nextID = 1
		}
		if r.Mobs[mobNamespace|nextID] == nil && r.Drops[mobNamespace|nextID] == nil && !reserved[mobNamespace|nextID] {
			break
		}
		nextID++
	}
	m := &Mob{ID: mobNamespace | nextID, Type: typ, X: x, Y: y, Z: z, Health: spec.Health, Yaw: s.rng.Float64() * math.Pi * 2, Flags: flags, SpawnReason: reason}
	if persistent, ok := context["persistent"].(bool); ok {
		m.Persistent = persistent
	}
	if flags&mobFlagBaby != 0 {
		m.GrowthTicks = 24000
	}
	if !validMob(m) {
		return nil, fmt.Errorf("无效生物状态。")
	}
	if !s.allowed(api.EntitySpawning, r, p, mobLifecycleData(p, m, reason, context)) {
		return nil, fmt.Errorf("生物生成已被插件阻止。")
	}
	return m, nil
}
func contextualMobData(m *Mob, reason string, context map[string]any) map[string]any {
	data := map[string]any{}
	for k, value := range context {
		data[k] = value
	}
	for k, value := range mobData(m, reason) {
		data[k] = value
	}
	data["target"] = mobTarget(m)
	data["actor"], data["source"] = "server", "server"
	if reason == "natural" {
		data["source"] = "natural"
	}
	if reason == "plugin" {
		data["source"] = "plugin"
	}
	if pActor, ok := context["actor"]; ok {
		data["actor"] = pActor
	}
	if source, ok := context["source"]; ok {
		data["source"] = source
	}
	return data
}
func mobLifecycleData(p *Peer, m *Mob, reason string, context map[string]any) map[string]any {
	data := contextualMobData(m, reason, context)
	if p != nil {
		data["actor"] = p.sid
		if context["source"] == nil {
			data["source"] = "player"
		}
	}
	return data
}
func (s *Server) publishMob(r *Room, p *Peer, m *Mob, reason string, context map[string]any) {
	s.commitMob(r, m)
	s.publishPreparedMob(r, p, m, reason, context)
}
func (s *Server) commitMob(r *Room, m *Mob) {
	if r.Mobs == nil {
		r.Mobs = map[uint32]*Mob{}
	}
	r.NextMobID = (m.ID & ^mobNamespace) + 1
	r.Mobs[m.ID] = m
	r.Dirty = true

}
func (s *Server) publishPreparedMob(r *Room, p *Peer, m *Mob, reason string, context map[string]any) {
	s.emit(api.EntitySpawned, r, p, mobLifecycleData(p, m, reason, context))
	for peer := range r.Peers {
		s.syncMobsFor(peer)
	}
}
func (s *Server) surface(r *Room, x, z int) int {
	if float64(x) < r.minCoord()+1 || float64(x) > r.maxCoord()-2 || float64(z) < r.minCoord()+1 || float64(z) > r.maxCoord()-2 {
		return 0
	}
	for y := 125; y > 0; y-- {
		b := s.block(r, x, y, z)
		k := s.rules.Kinds[b]
		if k == 0 || k == 5 {
			continue
		}
		if k == 1 || k == 3 {
			return y + 1
		}
		return 0 // water, lava and tree canopies are not spawn floors
	}
	return 0
}
func (s *Server) skyOpen(r *Room, x, y, z int) bool {
	for by := y; by < 128; by++ {
		k := s.rules.Kinds[s.block(r, x, by, z)]
		if k == 1 || k == 2 {
			return false
		}
	}
	return true
}

// A bounded light flood, rather than a global "torch present" switch. Only
// light sources that could reach the query in <= 14 steps need examining.
// Solid blocks block propagation; water/leaves consume an extra level.
func (s *Server) blockLight(r *Room, x, y, z int) int {
	type cell struct{ x, y, z, l int }
	queue := []cell{}
	for key, b := range r.Edits {
		light := 0
		switch {
		case isTorch(b):
			light = 14
		case b == 41 || b == 68 || b == 95:
			light = 15
		}
		if light == 0 {
			continue
		}
		bx, by, bz := coords(key)
		if abs(bx-x)+abs(by-y)+abs(bz-z) < light {
			queue = append(queue, cell{bx, by, bz, light})
		}
	}
	if len(queue) == 0 {
		return 0
	}
	seen := map[uint64]int{}
	best := 0
	for i := 0; i < len(queue); i++ {
		c := queue[i]
		key := blockKey(c.x, c.y, c.z)
		if seen[key] >= c.l {
			continue
		}
		seen[key] = c.l
		if c.x == x && c.y == y && c.z == z {
			best = max(best, c.l)
			continue
		}
		for _, d := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			nx, ny, nz := c.x+d[0], c.y+d[1], c.z+d[2]
			l := c.l - 1
			if !r.inside(nx, nz) || ny < 0 || ny >= 128 || l <= best || abs(nx-x)+abs(ny-y)+abs(nz-z) >= l {
				continue
			}
			k := s.rules.Kinds[s.block(r, nx, ny, nz)]
			if k == 1 {
				continue
			}
			if k == 2 || k == 4 || k == 6 {
				l--
			}
			if l > best {
				queue = append(queue, cell{nx, ny, nz, l})
			}
		}
	}
	return best
}
func (s *Server) canNaturalSpawn(r *Room, typ string, x, y, z int) bool {
	spec, ok := mobSpecs[typ]
	if !ok || spec.Hostile && s.difficulty(r) == "peaceful" {
		return false
	}
	floor := s.block(r, x, y-1, z)
	if s.kind(floor) != 1 || s.nameOf(floor) == "Bedrock" || s.nameOf(floor) == "Glass" || isLava(floor) {
		return false
	}
	if !s.mobFits(r, typ, float64(x)+.5, float64(y), float64(z)+.5) {
		return false
	}
	if r.dimName() == "nether" {
		// Our supported Nether ambient mobs belong to fortresses. Do not
		// populate every netherrack cave with blazes and wither skeletons.
		bricks := s.rules.byName["Under Bricks"]
		if bricks == 0 {
			bricks = 183
		}
		return (typ == "blaze" || typ == "wither_skeleton") && floor == bricks && (typ == "blaze" || s.blockLight(r, x, y, z) == 0)
	}
	if r.dimName() == "end" {
		return typ == "enderman" && s.blockLight(r, x, y, z) == 0
	}
	if spec.Hostile {
		return s.mobSkyLight(r, x, y, z) <= 7 && s.blockLight(r, x, y, z) == 0
	}
	return floor == s.rules.byName["Grass Block"] && (s.mobSkyLight(r, x, y, z) >= 9 || s.blockLight(r, x, y, z) >= 9)
}
func mobPosition(out []byte, m *Mob) []byte {
	out = entityPosition(out, &ItemDrop{X: m.X, Y: m.Y, Z: m.Z})
	yaw := byte(int(math.Round(m.Yaw * 256 / (math.Pi * 2))))
	out[len(out)-3], out[len(out)-2] = yaw, yaw
	return out
}
func mobVisible(p *Peer, m *Mob) bool {
	if p.closed || p.state == nil {
		return false
	}
	cx, cz := floorDiv(int(math.Floor(m.X)), 16), floorDiv(int(math.Floor(m.Z)), 16)
	return (!p.room.Infinite && p.room.Mode == "creative" || p.knows(int(math.Floor(m.X)), int(math.Floor(m.Z)))) && math.Abs(float64(cx)-math.Floor(p.state.X/16)) <= float64(p.rd+1) && math.Abs(float64(cz)-math.Floor(p.state.Z/16)) <= float64(p.rd+1)
}
func (s *Server) syncMobsFor(p *Peer) {
	if p.knownMobs == nil {
		p.knownMobs = map[uint32]bool{}
	}
	spawn, gone := []byte{7, 0, 0}, []byte{9, 0, 0}
	ns, ng := 0, 0
	for _, id := range sortedMobIDs(p.room) {
		m := p.room.Mobs[id]
		if !mobVisible(p, m) || p.knownMobs[id] {
			continue
		}
		spawn = binary.LittleEndian.AppendUint32(spawn, id)
		spawn = append(spawn, mobSpecs[m.Type].Kind)
		copy := *m
		copy.X -= float64(p.originX)
		copy.Z -= float64(p.originZ)
		spawn = mobPosition(spawn, &copy)
		spawn = append(spawn, m.Flags, 0, 0, 1)
		p.knownMobs[id] = true
		ns++
	}
	for id := range p.knownMobs {
		m := p.room.Mobs[id]
		if m != nil && mobVisible(p, m) {
			continue
		}
		gone = binary.LittleEndian.AppendUint32(gone, id)
		delete(p.knownMobs, id)
		ng++
	}
	if ns > 0 {
		binary.LittleEndian.PutUint16(spawn[1:], uint16(ns))
		p.enqueue(websocket.BinaryMessage, spawn)
	}
	if ng > 0 {
		binary.LittleEndian.PutUint16(gone[1:], uint16(ng))
		p.enqueue(websocket.BinaryMessage, gone)
	}
}
func (s *Server) mobEvent(r *Room, m *Mob, event byte, arg uint16) {
	for p := range r.Peers {
		if p.knownMobs[m.ID] {
			p.enqueue(websocket.BinaryMessage, entityEvent(m.ID, event, arg))
		}
	}
}
func (s *Server) removeMob(r *Room, m *Mob, reason string) bool {
	return s.removeMobWithData(r, m, mobData(m, reason))
}
func (s *Server) removeMobWithData(r *Room, m *Mob, data map[string]any) bool {
	if m == nil {
		return false
	}
	data["actor"], data["source"], data["target"] = "server", "server", mobTarget(m)
	if m == nil || r.Mobs[m.ID] != m || !s.allowed(api.EntityDespawning, r, nil, data) || r.Mobs[m.ID] != m {
		return false
	}
	delete(r.Mobs, m.ID)
	r.Dirty = true
	for p := range r.Peers {
		s.syncMobsFor(p)
	}
	s.emit(api.EntityDespawned, r, nil, data)
	return true
}
func (s *Server) damageMob(r *Room, p *Peer, m *Mob, amount float64, reason string) bool {
	ctx := &api.DamageContext{Cause: damageCause(reason)}
	if p != nil {
		ctx.Cause = "player_attack"
		ctx.Attacker = playerTarget(p)
		ctx.Weapon = itemSnapshot(s.heldItem(p))
	}
	return s.damageMobSource(r, p, m, amount, reason, ctx)
}
func (s *Server) damageMobSource(r *Room, p *Peer, m *Mob, amount float64, reason string, ctx *api.DamageContext) bool {
	if m == nil || m.Health <= 0 || !number(amount, 0, 1000) || amount == 0 {
		return false
	}
	ctx.Target = mobTarget(m)
	ctx.HealthBefore, ctx.HealthAfter = m.Health, m.Health
	if ctx.RawAmount == 0 {
		ctx.RawAmount = amount
	}
	ctx.Amount = amount
	data := mobData(m, reason)
	for k, v := range damageData(ctx, reason) {
		data[k] = v
	}
	e := s.emit(api.EntityDamaging, r, p, data)
	amount = e.Number("amount")
	if e.Cancelled || !number(amount, .01, 1000) {
		return false
	}
	ctx.Amount = amount
	for k, v := range damageData(ctx, reason) {
		data[k] = v
	}
	lethal := amount >= m.Health
	if lethal && !s.allowed(api.EntityDying, r, p, data) {
		return false
	}

	// 末影龙专属扣血、Boss血条联动与死亡序列处理
	if m.Type == "ender_dragon" && r.dimName() == "end" {
		df := getDragonFight(r)
		df.Health = max(0, m.Health-amount)
		m.Health = df.Health
		r.Dirty = true
		ctx.HealthAfter = m.Health
		ctx.AppliedAmount = ctx.HealthBefore - ctx.HealthAfter
		for k, v := range damageData(ctx, reason) {
			data[k] = v
		}
		data["health"] = m.Health
		s.mobEvent(r, m, 1, 0)
		s.emit(api.EntityDamaged, r, p, data)

		if lethal || m.Health <= 0 {
			df.Health = 0
			m.Health = 0
			df.State = "dying"
			if df.DyingTicks == 0 {
				df.DyingTicks = 1
				soundAt(r, "entity.ender_dragon.death", df.X, df.Y, df.Z, nil)
			}
			s.emit(api.EntityDead, r, p, data)
		}
		return true
	}

	var drops []*ItemDrop
	if lethal && r.usesSurvival() {
		stacks := Stacks{}
		for _, name := range mobSpecs[m.Type].Drops {
			stacks = append(stacks, stack(s.rules.byName[name], 1+s.rng.Intn(2), 0))
		}
		var err error
		drops, err = s.prepareDrops(r, p, stacks, m.X, m.Y+.3, m.Z, "mob_death")
		if err != nil {
			return false
		} // do not delete loot when drop capacity is exhausted
	}
	m.Health = max(0, m.Health-amount)
	r.Dirty = true
	ctx.HealthAfter = m.Health
	ctx.AppliedAmount = ctx.HealthBefore - ctx.HealthAfter
	for k, v := range damageData(ctx, reason) {
		data[k] = v
	}
	data["health"] = m.Health
	s.mobEvent(r, m, 1, 0)
	s.emit(api.EntityDamaged, r, p, data)
	if lethal {
		s.mobEvent(r, m, 2, 0)
		delete(r.Mobs, m.ID)
		s.publishDrops(r, p, drops, "mob_death")
		s.mobKilledExt(r, p, m)
		s.emit(api.EntityDead, r, p, data)
		for peer := range r.Peers {
			s.syncMobsFor(peer)
		}
	} else if p != nil && !mobSpecs[m.Type].Hostile {
		m.fleeMS = 3000
		m.Yaw = math.Atan2(-(m.X - p.state.X), -(m.Z - p.state.Z))
	}
	return true
}
func (s *Server) lineClear(r *Room, a, b [3]float64) bool {
	distance := math.Sqrt(math.Pow(b[0]-a[0], 2) + math.Pow(b[1]-a[1], 2) + math.Pow(b[2]-a[2], 2))
	steps := max(1, int(math.Ceil(distance*4)))
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		if s.mobSolid(r, int(math.Floor(a[0]+(b[0]-a[0])*t)), int(math.Floor(a[1]+(b[1]-a[1])*t)), int(math.Floor(a[2]+(b[2]-a[2])*t))) {
			return false
		}
	}
	return true
}
func (s *Server) attackMob(p *Peer, id uint32) {
	r := p.room
	m := r.Mobs[id]
	if m == nil || !p.knownMobs[id] || p.state.Dead || time.Since(p.attackAt) < s.attackCooldown(p) || r.Lock != 0 && !p.owner {
		return
	}
	dx, dz := m.X-p.state.X, m.Z-p.state.Z
	reach := s.anticheatConfig().AttackReach
	if m.Type == "ender_dragon" {
		reach = math.Max(reach, 12.0) // 末影龙体型庞大(12x5)，放宽近战距离至 12 格
	}
	if math.Hypot(dx, dz) > reach || math.Abs(m.Y-p.state.Y) > reach*0.75 || !s.lineClear(r, [3]float64{p.state.X, p.state.Y + 1.62, p.state.Z}, [3]float64{m.X, m.Y + mobSpecs[m.Type].Height/2, m.Z}) {
		return
	}
	amount := s.attributes(p)[api.AttackDamage]
	if r.Mode == "creative" {
		amount = 100
	}
	ctx := s.playerAttack(p, mobTarget(m), amount)
	if ctx != nil && s.damageMobSource(r, p, m, ctx.Amount, "player_attack", ctx) {
		p.attackAt = time.Now()
		if r.usesSurvival() {
			s.wear(p, 1)
			s.inventory(p, 0, 0)
		}
		s.emit(api.PlayerAttacked, r, p, damageData(ctx, "player_attack"))
	}
}
func (s *Server) walkMob(r *Room, m *Mob, dx, dz float64) bool {
	x, z := m.X+dx, m.Z+dz
	bx, bz := int(math.Floor(x)), int(math.Floor(z))
	base := int(math.Round(m.Y))
	for _, y := range []int{base, base + 1, base - 1} {
		stepBlock := s.block(r, bx, y-1, bz)
		if y > base && s.rules.Kinds[stepBlock] == 2 {
			continue // 禁止把树叶当成台阶爬到树顶悬空
		}
		if s.mobSolid(r, bx, y-1, bz) && s.mobFits(r, m.Type, x, float64(y), z) {
			m.X, m.Y, m.Z = x, float64(y), z
			return true
		}
	}
	return false // stop at walls, liquids and cliff edges
}
func (s *Server) mobsTick(r *Room) {
	for _, id := range sortedMobIDs(r) {
		m := r.Mobs[id]
		if m == nil {
			continue
		}
		target, nearest := s.nearestMobPlayer(r, m.X, m.Y, m.Z)
		if s.despawnMob(r, m, nearest) {
			continue
		}
		// Saved passive mobs pause outside active terrain instead of loading it.
		if nearest > 128 || !s.mobAreaLoaded(r, m.Type, m.X, m.Z) {
			continue
		}
		spec := mobSpecs[m.Type]
		// A removed floor cannot leave an animal suspended in the air.
		// 飞行生物、Boss 和固定实体免除地面坠落检测
		isAirborneOrBoss := m.Type == "ender_dragon" || m.Type == "end_crystal" || m.Type == "wither" || m.Type == "blaze" || m.Type == "shulker"
		mx, mz := int(math.Floor(m.X)), int(math.Floor(m.Z))
		if !isAirborneOrBoss && !s.mobSolid(r, mx, int(math.Floor(m.Y))-1, mz) {
			fallen := false
			for y := int(math.Floor(m.Y)) - 1; y >= max(1, int(m.Y)-4); y-- {
				if s.mobFits(r, m.Type, m.X, float64(y), m.Z) {
					m.Y = float64(y)
					fallen = true
					if s.mobSolid(r, mx, y-1, mz) {
						break
					}
				} else {
					break
				}
			}
			if !fallen && m.Y <= 1 {
				s.removeMob(r, m, "void")
				continue
			}
		}
		m.attackMS = max(0, m.attackMS-100)
		m.fleeMS = max(0, m.fleeMS-100)
		oldFlags := m.Flags
		m.Flags &= mobFlagBaby | mobFlagSheared
		s.growMob(r, m)
		if m.Type == "zombie" && !isNight(r.Time) && s.tick%10 == 0 {
			m.burnMS = 0
			if s.skyOpen(r, mx, int(m.Y+spec.Height), mz) {
				m.burnMS = 1000
			}
		}
		if m.burnMS > 0 {
			m.Flags |= 8
			m.burnMS -= 100
			if m.burnMS == 0 {
				if s.damageMob(r, nil, m, 1, "sunlight") && r.Mobs[id] == nil {
					continue
				}
			}
		}
		chase := spec.Hostile && target != nil && r.usesSurvival() && nearest < 20 && math.Abs(target.state.Y-m.Y) < 8 && (m.Type != "spider" || isNight(r.Time))
		if chase {
			m.Flags |= 4
			tx, ty, tz := int(math.Floor(target.state.X)), int(math.Floor(target.state.Y)), int(math.Floor(target.state.Z))
			m.pathTicks--
			if len(m.path) == 0 || m.pathTicks <= 0 || m.pathTarget[0] != tx || m.pathTarget[2] != tz {
				m.path = s.findVoxelPath(r, m, int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z)), tx, ty, tz)
				m.pathTarget = [3]int{tx, ty, tz}
				m.pathTicks = 10
			}
			if len(m.path) > 0 {
				next := m.path[0]
				distToNode := math.Hypot((float64(next.X)+0.5)-m.X, (float64(next.Z)+0.5)-m.Z)
				if distToNode < 0.45 {
					m.path = m.path[1:]
					if len(m.path) > 0 {
						next = m.path[0]
					}
				}
				m.Yaw = math.Atan2(-((float64(next.X) + 0.5) - m.X), -((float64(next.Z) + 0.5) - m.Z))
			} else {
				m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
			}
		}
		if oldFlags != m.Flags {
			s.mobEvent(r, m, 4, uint16(m.Flags))
		}
		m.walkMS -= 100
		if !chase && m.walkMS <= 0 && m.fleeMS == 0 {
			m.Yaw = s.rng.Float64() * math.Pi * 2
			m.walkMS = 1500 + s.rng.Intn(2500)
		}
		if chase && nearest < 1.8 && math.Abs(target.state.Y-m.Y) < 1.5 && m.attackMS == 0 && s.lineClear(r, [3]float64{m.X, m.Y + .6, m.Z}, [3]float64{target.state.X, target.state.Y + 1, target.state.Z}) {
			ctx := &api.DamageContext{Cause: "mob_attack", Attacker: mobTarget(m), Target: playerTarget(target), RawAmount: 3, Amount: 3, HealthBefore: target.state.Health, HealthAfter: target.state.Health}
			data := mobData(m, "melee")
			for k, v := range damageData(ctx, "melee") {
				data[k] = v
			}
			data["targetId"] = target.id
			e := s.emit(api.EntityAttacking, r, target, data)
			amount := e.Number("amount")
			if !e.Cancelled && number(amount, 0, 1000) && amount > 0 {
				ctx.RawAmount, ctx.Amount = amount, amount
				if s.damagePlayer(target, amount, "被"+map[string]string{"zombie": "僵尸", "spider": "蜘蛛"}[m.Type]+"击败了。", ctx) {
					s.mobEvent(r, m, 3, 0)
					for k, v := range damageData(ctx, "melee") {
						data[k] = v
					}
					s.emit(api.EntityAttacked, r, target, data)
				}
			}
			m.attackMS = 1000
		}
		speed := spec.Speed * .1
		if m.fleeMS > 0 {
			speed *= 2
		}
		if chase || m.fleeMS > 0 || m.walkMS > 1000 {
			if !s.walkMob(r, m, -math.Sin(m.Yaw)*speed, -math.Cos(m.Yaw)*speed) {
				m.Yaw += math.Pi / 2
				if m.Yaw > math.Pi*2 {
					m.Yaw -= math.Pi * 2
				}
			}
		}
		r.Dirty = true
	}
	for p := range r.Peers {
		s.syncMobsFor(p)
		out := []byte{8, 0, 0}
		n := 0
		for _, id := range sortedMobIDs(r) {
			if p.knownMobs[id] {
				out = binary.LittleEndian.AppendUint32(out, id)
				copy := *r.Mobs[id]
				copy.X -= float64(p.originX)
				copy.Z -= float64(p.originZ)
				out = mobPosition(out, &copy)
				n++
			}
		}
		if n > 0 {
			binary.LittleEndian.PutUint16(out[1:], uint16(n))
			p.enqueue(websocket.BinaryMessage, out)
		}
	}
}
