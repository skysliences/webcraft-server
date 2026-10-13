package game

import (
	"math"
	"time"

	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Projectiles: arrows (bow), snowballs, ender pearls and eyes of ender.
// Velocities are in blocks per game tick (50 ms); the server advances two
// game ticks per 100 ms server tick.
// ---------------------------------------------------------------------------

const (
	projArrow         = "arrow"
	projSnowball      = "snowball"
	projEgg           = "egg"
	projPearl         = "pearl"
	projEye           = "eye"
	projSmallFireball = "small_fireball"
	projWitherSkull   = "wither_skull"
	projShulkerBullet = "shulker_bullet"
)

type Projectile struct {
	entView
	Type       string
	VX, VY, VZ float64
	Owner      *Peer
	Damage     float64
	Age        int // game ticks
	Stuck      bool
	StuckAge   int
	Pickup     bool // arrow may be picked up as an item
	Crit       bool
	Flame      bool
	Punch      int
	Target     [3]float64 // eye of ender destination
	Gravity    float64
	Drag       float64
	Weapon     *api.ItemStack
	Shooter    *api.Combatant
	Actor      string
	Source     string
}

// Preparation lets a container launch share one atomic inventory transaction.
func (s *Server) prepareProjectile(r *Room, pr *Projectile, context map[string]any) (map[string]any, bool) {
	if pr == nil || len(r.rt().projectiles) >= 512 || !number(pr.X, r.minCoord(), r.maxCoord()) || !number(pr.Y, -64, 512) || !number(pr.Z, r.minCoord(), r.maxCoord()) {
		return nil, false
	}
	if actor, ok := context["actor"].(string); ok {
		pr.Actor = actor
	}
	if source, ok := context["source"].(string); ok {
		pr.Source = source
	}
	if pr.Actor == "" {
		pr.Actor = "server"
		if pr.Owner != nil {
			pr.Actor = pr.Owner.sid
		}
	}
	if pr.Source == "" {
		pr.Source = pr.Type
		if pr.Owner != nil {
			pr.Source = "player"
		}
	}
	data := projectileData(pr, "launch")
	for field, value := range context {
		data[field] = value
	}
	e := s.emit(api.ProjectileLaunching, r, pr.Owner, data)
	damage := e.Number("damage")
	if e.Cancelled || !number(damage, 0, 1000) {
		return nil, false
	}
	pr.Damage = damage
	data["damage"] = damage
	return data, true
}
func (s *Server) commitProjectile(r *Room, pr *Projectile) {
	pr.ID = s.newEntID(r)
	pr.Count = 1
	pr.face()
	r.rt().projectiles[pr.ID] = pr
}
func (s *Server) publishProjectile(r *Room, pr *Projectile, data map[string]any) {
	s.syncEnt(r, &pr.entView, false)
	data["entityId"] = pr.ID
	s.emit(api.ProjectileLaunched, r, pr.Owner, data)
}
func (s *Server) spawnProjectile(r *Room, pr *Projectile) bool {
	data, ok := s.prepareProjectile(r, pr, nil)
	if !ok {
		return false
	}
	s.commitProjectile(r, pr)
	s.publishProjectile(r, pr, data)
	return true
}

func (pr *Projectile) face() {
	h := math.Hypot(pr.VX, pr.VZ)
	if h > 1e-6 || math.Abs(pr.VY) > 1e-6 {
		pr.Yaw = math.Atan2(-pr.VX, -pr.VZ)
		pr.Pitch = math.Atan2(pr.VY, h)
	}
}

// launchFrom places a projectile at the player's eyes moving along the view.
func (s *Server) launchFrom(p *Peer, typ string, speed float64) *Projectile {
	st := p.state
	dx, dy, dz := lookDir(st)
	pr := &Projectile{Type: typ, Owner: p, VX: dx * speed, VY: dy * speed, VZ: dz * speed, Gravity: .03, Drag: .99}
	pr.Weapon = itemSnapshot(s.heldItem(p))
	pr.Shooter = playerTarget(p)
	pr.X, pr.Y, pr.Z = st.X+dx*.3, st.Y+1.52+dy*.3, st.Z+dz*.3
	switch typ {
	case projArrow:
		pr.Kind, pr.Gravity = 3, .068
	case projSnowball:
		pr.Kind, pr.Data = 3, 332
	case projEgg:
		pr.Kind, pr.Data = 3, itemEgg
	case projPearl:
		pr.Kind, pr.Data = 13, 334
	case projEye:
		pr.Kind, pr.Data, pr.Gravity = 13, 768, 0
	}
	return pr
}

func (s *Server) projectilesTick(r *Room) {
	rt := r.rt()
	for id, pr := range rt.projectiles {
		moved := false
		for step := 0; step < 2; step++ {
			if s.stepProjectile(r, pr) {
				moved = true
			}
			if pr.Age < 0 {
				break
			}
		}
		if pr.Age < 0 {
			s.killEnt(&pr.entView)
			delete(rt.projectiles, id)
			s.emit(api.ProjectileRemoved, r, pr.Owner, projectileData(pr, "finished"))
			continue
		}
		s.syncEnt(r, &pr.entView, moved)
	}
}

// stepProjectile advances one game tick. Setting Age<0 removes it.
func (s *Server) stepProjectile(r *Room, pr *Projectile) bool {
	pr.Age++
	if pr.Type == projEye {
		return s.stepEye(r, pr)
	}
	if pr.Stuck {
		pr.StuckAge++
		if pr.StuckAge > 1200 {
			pr.Age = -1
			return false
		}
		// Block removed under the arrow: fall again.
		if !s.solid(r, pr.X, pr.Y, pr.Z) && !s.solid(r, pr.X+pr.VX*.1, pr.Y+pr.VY*.1, pr.Z+pr.VZ*.1) {
			pr.Stuck = false
			pr.VX, pr.VY, pr.VZ = 0, 0, 0
			pr.Flags &^= 16
			s.entEvent(&pr.entView, 4, uint16(pr.Flags))
		} else {
			s.arrowPickup(r, pr)
			return false
		}
	}
	if pr.Age > 1200 || pr.Y < -20 {
		pr.Age = -1
		return false
	}
	ox, oy, oz := pr.X, pr.Y, pr.Z
	nx, ny, nz := ox+pr.VX, oy+pr.VY, oz+pr.VZ
	dist := math.Sqrt(pr.VX*pr.VX + pr.VY*pr.VY + pr.VZ*pr.VZ)
	n := max(1, int(math.Ceil(dist/.2)))
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		x, y, z := ox+(nx-ox)*t, oy+(ny-oy)*t, oz+(nz-oz)*t
		if !r.inside(int(math.Floor(x)), int(math.Floor(z))) {
			pr.Age = -1
			return false
		}
		if s.projectileHitEntity(r, pr, x, y, z) {
			return true
		}
		if s.solid(r, x, y, z) {
			pr.X, pr.Y, pr.Z = ox+(nx-ox)*(t-.5/float64(n)), oy+(ny-oy)*(t-.5/float64(n)), oz+(nz-oz)*(t-.5/float64(n))
			s.projectileHitBlock(r, pr, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
			return true
		}
	}
	pr.X, pr.Y, pr.Z = nx, ny, nz
	drag := pr.Drag
	if isWater(s.block(r, int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz)))) {
		drag = .6
	}
	pr.VX *= drag
	pr.VY = pr.VY*drag - pr.Gravity
	pr.VZ *= drag
	pr.face()
	return true
}

func (s *Server) projectileHitEntity(r *Room, pr *Projectile, x, y, z float64) bool {
	if pr.Age < 2 && pr.Owner != nil {
		// Don't hit the shooter right after launch.
	}
	for _, m := range r.Mobs {
		if pr.Shooter != nil && pr.Shooter.EntityID == m.ID {
			continue
		}
		spec := mobSpecs[m.Type]
		hw := spec.Width/2 + .15
		if math.Abs(x-m.X) <= hw && math.Abs(z-m.Z) <= hw && y >= m.Y-.1 && y <= m.Y+spec.Height+.1 {
			if m.Type == "wither" && pr.Type == projArrow {
				ext := getMobExt(m.ID)
				if ext.ChargeTicks > 0 || m.Health <= 150 {
					pr.VX = -pr.VX * 0.5
					pr.VY = 0.2
					pr.VZ = -pr.VZ * 0.5
					soundAt(r, "entity.shield.block", pr.X, pr.Y, pr.Z, nil)
					return true
				}
			}
			if s.projectileImpact(r, pr, nil, m) {
				return true
			}
		}
	}
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) || p == pr.Owner && pr.Age < 6 {
			continue
		}
		if math.Abs(x-p.state.X) <= .45 && math.Abs(z-p.state.Z) <= .45 && y >= p.state.Y-.1 && y <= p.state.Y+1.9 {
			if s.projectileImpact(r, pr, p, nil) {
				return true
			}
		}
	}
	return false
}

func (s *Server) projectileImpact(r *Room, pr *Projectile, target *Peer, m *Mob) bool {
	data := projectileData(pr, "entity_hit")
	if target != nil {
		data["target"] = playerTarget(target)
	} else if m != nil {
		data["target"] = mobTarget(m)
	}
	if !s.allowed(api.ProjectileHitting, r, pr.Owner, data) {
		return false
	}
	owner := pr.Owner
	if owner != nil && (owner.closed || owner.room != r) {
		owner = nil
	}
	dmg := 0.
	reason := "被雪球击中了。"
	switch pr.Type {
	case projArrow:
		speed := math.Sqrt(pr.VX*pr.VX + pr.VY*pr.VY + pr.VZ*pr.VZ)
		dmg = math.Ceil(speed * pr.Damage)
		if pr.Crit {
			dmg += float64(s.rng.Intn(int(dmg)/2 + 2))
		}
		reason = "被射杀了。"
	case projSnowball:
		dmg = 0
		if m != nil && m.Type == "blaze" {
			dmg = 3
		}
	case projEgg:
		s.hatchEgg(r, pr)
		pr.Age = -1
		s.emit(api.ProjectileHit, r, owner, data)
		return true
	case projPearl:
		s.pearlLand(r, pr)
		pr.Age = -1
		s.emit(api.ProjectileHit, r, owner, data)
		return true
	case projSmallFireball:
		dmg = 5
		reason = "被烈焰人的火球击中了。"
	case projWitherSkull:
		dmg = 8
		reason = "被凋灵之首炸成了碎片。"
	case projShulkerBullet:
		dmg = 4
		reason = "被潜影弹击中了。"
	}
	if m != nil && m.Type == "shulker" {
		ext := getMobExt(m.ID)
		if !ext.ShulkerOpen {
			dmg = math.Max(1, dmg*0.2)
		}
	}
	ctx := &api.DamageContext{Cause: "projectile", Actor: pr.Actor, Source: pr.Source, Weapon: pr.Weapon.Clone(), Attacker: pr.Shooter}
	if owner != nil {
		ctx.Attacker = playerTarget(owner)
	}
	if m != nil {
		if dmg > 0 {
			s.damageMobSource(r, owner, m, dmg, "projectile", ctx)
		} else {
			s.mobEvent(r, m, 1, 0)
		}
		if pr.Flame && m.Health > 0 {
			m.burnMS = 5000
		}
		if pr.Punch > 0 || pr.Type == projSnowball {
			h := math.Hypot(pr.VX, pr.VZ)
			if h > 0 {
				k := .3 + .6*float64(pr.Punch)
				nx, nz := m.X+pr.VX/h*k, m.Z+pr.VZ/h*k
				if s.mobFits(r, m.Type, nx, m.Y, nz) {
					m.X, m.Z = nx, nz
				}
			}
		}
	}
	if target != nil {
		if dmg > 0 {
			s.damagePlayer(target, dmg, reason, ctx)
		} else {
			target.json(map[string]any{"t": "hurt", "f": false})
		}
		if pr.Flame {
			s.ignite(target, 100)
		}
		if owner != nil && owner != target {
			owner.json(map[string]any{"t": "snd", "e": "entity.arrow.hit_player", "x": owner.state.X, "y": owner.state.Y, "z": owner.state.Z})
		}
	}
	if pr.Type == projSmallFireball {
		s.explode(r, pr.X, pr.Y, pr.Z, 1.2, false)
		if target != nil {
			s.ignite(target, 100)
		}
		if m != nil && m.Health > 0 {
			m.burnMS = 5000
		}
	} else if pr.Type == projWitherSkull {
		s.explode(r, pr.X, pr.Y, pr.Z, 1.5, true)
		if target != nil {
			s.addEffect(target, "wither", 200, 1)
		}
		if m != nil && m.Health > 0 && m.Type != "wither" && m.Type != "wither_skeleton" {
			s.damageMob(r, owner, m, 4, "wither")
		}
	} else if pr.Type == projShulkerBullet {
		if target != nil {
			s.addEffect(target, "levitation", 200, 0)
		}
	}
	if pr.Type == projArrow {
		soundAt(r, "entity.arrow.hit", pr.X, pr.Y, pr.Z, nil)
	}
	pr.Age = -1
	data["damage"] = ctx
	s.emit(api.ProjectileHit, r, owner, data)
	return true
}

func (s *Server) projectileHitBlock(r *Room, pr *Projectile, bx, by, bz int) {
	data := projectileData(pr, "block_hit")
	data["blockX"], data["blockY"], data["blockZ"], data["block"] = bx, by, bz, s.block(r, bx, by, bz)
	if !s.allowed(api.ProjectileHitting, r, pr.Owner, data) {
		return
	}
	switch pr.Type {
	case projArrow:
		pr.Stuck = true
		pr.StuckAge = 0
		pr.Flags |= 16
		pr.Flags &^= 32
		s.entEvent(&pr.entView, 4, uint16(pr.Flags))
		soundAt(r, "entity.arrow.hit", pr.X, pr.Y, pr.Z, nil)
		if pr.Flame && s.nameOf(s.block(r, bx, by, bz)) == "TNT" {
			s.queue(r, bx, by, bz)
		}
		for _, h := range arrowBlockHooks {
			h(s, r, pr, bx, by, bz)
		}
	case projSnowball:
		pr.Age = -1
	case projEgg:
		s.hatchEgg(r, pr)
		pr.Age = -1
	case projPearl:
		s.pearlLand(r, pr)
		pr.Age = -1
	case projSmallFireball:
		s.explode(r, pr.X, pr.Y, pr.Z, 1.2, false)
		pr.Age = -1
	case projWitherSkull:
		s.explode(r, pr.X, pr.Y, pr.Z, 1.5, true)
		pr.Age = -1
	case projShulkerBullet:
		pr.Age = -1
	}
	s.emit(api.ProjectileHit, r, pr.Owner, data)
}

// arrowBlockHooks lets other modules react to arrows hitting blocks
// (wooden buttons, TNT with flame...).
var arrowBlockHooks []func(s *Server, r *Room, pr *Projectile, x, y, z int)

func (s *Server) arrowPickup(r *Room, pr *Projectile) {
	if pr.StuckAge < 10 {
		return
	}
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) {
			continue
		}
		if math.Abs(p.state.X-pr.X) > 1.2 || math.Abs(p.state.Z-pr.Z) > 1.2 || pr.Y < p.state.Y-.5 || pr.Y > p.state.Y+2.3 {
			continue
		}
		if pr.Pickup {
			if left := s.add(p.state.Inv, stack(302, 1, 0)); left != nil {
				continue
			}
			s.inventory(p, 0, 0)
			r.Dirty = true
		}
		s.entEvent(&pr.entView, 5, p.id)
		soundAt(r, "entity.item.pickup", pr.X, pr.Y, pr.Z, nil)
		pr.Age = -1
		return
	}
}

func (s *Server) pearlLand(r *Room, pr *Projectile) {
	o := pr.Owner
	soundAt(r, "entity.enderman.teleport", pr.X, pr.Y, pr.Z, nil)
	if o == nil || o.closed || o.room != r || o.state.Dead {
		return
	}
	x := clampF(pr.X, r.minCoord()+.5, r.maxCoord()-.5)
	z := clampF(pr.Z, r.minCoord()+.5, r.maxCoord()-.5)
	y := clampF(pr.Y, 1, 126)
	for i := 0; i < 3 && (s.solid(r, x, y, z) || s.solid(r, x, y+1, z)); i++ {
		y = math.Floor(y) + 1
	}
	if s.teleport(o, x, y, z, "ender_pearl") && !s.isCreative(o) {
		s.damagePlayer(o, 5, "摔落了。", &api.DamageContext{Cause: "fall"})
	}
}

// ---------------------------------------------------------------------------
// Eye of ender flight
// ---------------------------------------------------------------------------

func (s *Server) stepEye(r *Room, pr *Projectile) bool {
	if pr.Age > 80 {
		x, y, z := pr.X, pr.Y, pr.Z
		if s.rng.Intn(5) > 0 && r.usesSurvival() {
			if drops, err := s.prepareDrops(r, nil, Stacks{stack(768, 1, 0)}, clampF(x, r.minCoord()+.2, r.maxCoord()-.2), clampF(y, 0, 127.5), clampF(z, r.minCoord()+.2, r.maxCoord()-.2), "eye_drop"); err == nil {
				for _, d := range drops {
					d.VX, d.VY, d.VZ = 0, 0, 0
				}
				s.publishDrops(r, nil, drops, "eye_drop")
			}
		} else {
			broadcast(r, map[string]any{"t": "eyefx", "x": int(math.Floor(x)), "y": y, "z": int(math.Floor(z))}, nil)
			soundAt(r, "entity.ender_eye.death", x, y, z, nil)
		}
		pr.Age = -1
		return false
	}
	dx, dz := pr.Target[0]-pr.X, pr.Target[2]-pr.Z
	h := math.Hypot(dx, dz)
	speed := .25
	if h < 1 {
		speed = h / 4
	}
	if h > 0 {
		pr.VX = dx / h * speed
		pr.VZ = dz / h * speed
	}
	ty := pr.Target[1]
	pr.VY += (ty - pr.Y) * .01
	pr.VY = clampF(pr.VY, -.15, .15)
	pr.X += pr.VX
	pr.Y += pr.VY
	pr.Z += pr.VZ
	pr.face()
	return true
}

func (s *Server) throwEye(p *Peer) {
	r := p.room
	held := s.heldItem(p)
	if held == nil || held.ID != 768 || !r.usesSurvival() || s.isSpectator(p) {
		return
	}
	if r.dimName() != "overworld" {
		p.sys("末影之眼在这个维度没有反应。")
		return
	}
	sh, ok := s.engine.strongholdNear(r, int(p.state.X), int(p.state.Z))
	if !ok {
		p.sys("附近没有要塞。")
		return
	}
	pr := s.launchFrom(p, projEye, 0)
	dx, dz := float64(sh[0])+.5-p.state.X, float64(sh[2])+.5-p.state.Z
	d := math.Hypot(dx, dz)
	tx, tz, ty := float64(sh[0])+.5, float64(sh[2])+.5, pr.Y+8
	if d > 12 {
		tx, tz = p.state.X+dx/d*12, p.state.Z+dz/d*12
	} else {
		ty = pr.Y - 1
	}
	pr.Target = [3]float64{tx, ty, tz}
	pr.VY = .1
	if !s.spawnProjectile(r, pr) {
		return
	}
	s.consumeHeld(p, 1)
	s.inventory(p, 0, 0)
	soundAt(r, "entity.ender_eye.launch", p.state.X, p.state.Y, p.state.Z, nil)
}

// ---------------------------------------------------------------------------
// Bow
// ---------------------------------------------------------------------------

func (s *Server) findArrow(p *Peer) int {
	inv := p.state.Inv.Slots
	if v := inv[40]; v != nil && v.ID == 302 {
		return 40
	}
	if v := inv[p.state.Inv.Selected]; v != nil && v.ID == 302 {
		return p.state.Inv.Selected
	}
	for i := 0; i < 36; i++ {
		if v := inv[i]; v != nil && v.ID == 302 {
			return i
		}
	}
	return -1
}

func (s *Server) shootBow(p *Peer) {
	st := p.state
	bow := s.heldItem(p)
	if bow == nil || bow.ID != 301 || p.useAt.IsZero() {
		return
	}
	ticks := time.Since(p.useAt).Seconds() * 20
	p.useAt = time.Time{}
	t := ticks / 20
	power := math.Min(1, (t*t+2*t)/3)
	if power < .1 {
		return
	}
	creative := s.isCreative(p)
	infinity := enchOf(bow, "infinity") > 0
	slot := s.findArrow(p)
	if slot < 0 && !creative {
		return
	}
	speed := 0.6 + power*1.6
	pr := s.launchFrom(p, projArrow, speed)
	// 抛物线仰角拱形微补偿 (Loft Arc)
	pr.VY += 0.08 * power
	pr.face()
	pr.Damage = 2.0 + power*6.0
	if l := enchOf(bow, "power"); l > 0 {
		pr.Damage += float64(l)*1.0 + 0.5
	}
	pr.Punch = enchOf(bow, "punch")
	pr.Flame = enchOf(bow, "flame") > 0
	if power >= 1 {
		pr.Crit = true
		pr.Flags |= 32
	}
	if pr.Flame {
		pr.Flags |= 8
	}
	pr.Pickup = !creative && !infinity
	if !s.spawnProjectile(p.room, pr) {
		return
	}
	if !creative && !infinity && slot >= 0 {
		st.Inv.Slots[slot] = sized(st.Inv.Slots[slot], st.Inv.Slots[slot].Count-1)
	}
	s.wear(p, 1)
	p.room.Dirty = true
	soundAt(p.room, "entity.arrow.shoot", st.X, st.Y+1.5, st.Z, nil)
	s.inventory(p, 0, 0)
}

func init() {
	onRoomTick(func(s *Server, r *Room) {
		if r.usesSurvival() {
			s.projectilesTick(r)
		}
	})
	onMessage("snowball", func(s *Server, p *Peer, m Message) bool {
		held := s.heldItem(p)
		if held == nil || held.ID != 332 || !p.room.usesSurvival() || p.state.Dead || s.isSpectator(p) {
			return true
		}
		if !s.spawnProjectile(p.room, s.launchFrom(p, projSnowball, 1.5)) {
			return true
		}
		s.consumeHeld(p, 1)
		s.inventory(p, 0, 0)
		soundAt(p.room, "entity.snowball.throw", p.state.X, p.state.Y+1.5, p.state.Z, nil)
		return true
	})
	onMessage("pearl", func(s *Server, p *Peer, m Message) bool {
		held := s.heldItem(p)
		if held == nil || held.ID != 334 || !p.room.usesSurvival() || p.state.Dead || s.isSpectator(p) {
			return true
		}
		if !s.spawnProjectile(p.room, s.launchFrom(p, projPearl, 1.5)) {
			return true
		}
		s.consumeHeld(p, 1)
		s.inventory(p, 0, 0)
		soundAt(p.room, "entity.ender_pearl.throw", p.state.X, p.state.Y+1.5, p.state.Z, nil)
		return true
	})
	onMessage("eye", func(s *Server, p *Peer, m Message) bool {
		if !p.state.Dead {
			s.throwEye(p)
		}
		return true
	})
	onMessage("use", func(s *Server, p *Peer, m Message) bool {
		held := s.heldItem(p)
		if held == nil || held.ID != 301 || !p.room.usesSurvival() {
			return false
		}
		switch m.S {
		case 0:
			if s.isCreative(p) || s.findArrow(p) >= 0 {
				p.useAt = time.Now()
				p.useSlot = p.state.Inv.Selected
			}
		case 1:
			if p.useSlot == p.state.Inv.Selected {
				s.shootBow(p)
			}
			p.useAt = time.Time{}
		}
		return true
	})
}
