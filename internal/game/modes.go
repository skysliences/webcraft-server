package game

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Game modes, difficulty, hardcore, gamerules, weather, status effects,
// death/respawn and the extended chat commands.
// ---------------------------------------------------------------------------

var gameModes = map[string]string{"0": "survival", "1": "creative", "2": "adventure", "3": "spectator", "s": "survival", "c": "creative", "a": "adventure", "sp": "spectator", "survival": "survival", "creative": "creative", "adventure": "adventure", "spectator": "spectator"}
var gameModeNames = map[string]string{"survival": "生存模式", "creative": "创造模式", "adventure": "冒险模式", "spectator": "旁观模式"}

// Defaults for boolean gamerules. keepInventory defaults to true for worlds
// created before gamerules existed (they always kept inventories).
var gameRuleDefaults = map[string]bool{
	"keepInventory":       true,
	"doDaylightCycle":     true,
	"doWeatherCycle":      true,
	"doMobSpawning":       true,
	"doFireTick":          true,
	"mobGriefing":         true,
	"naturalRegeneration": true,
	"doImmediateRespawn":  false,
	"fallDamage":          true,
	"fireDamage":          true,
	"drowningDamage":      true,
	"pvp":                 true,
	"tntExplodes":         true,
	"doTileDrops":         true,
	"showDeathMessages":   true,
}

func (s *Server) gameRule(r *Room, name string) bool {
	if e := r.root().Ext; e != nil && e.Rules != nil {
		if v, ok := e.Rules[name]; ok {
			return v
		}
	}
	return gameRuleDefaults[name]
}

func (s *Server) difficulty(r *Room) string {
	if e := r.root().Ext; e != nil && e.Difficulty != "" {
		return e.Difficulty
	}
	return "normal"
}

func (s *Server) gameMode(p *Peer) string {
	if p == nil || p.state == nil {
		return "survival"
	}
	if p.state.Ext != nil && p.state.Ext.GameMode != "" {
		return p.state.Ext.GameMode
	}
	if p.room != nil && p.room.Mode == "creative" {
		return "creative"
	}
	return "survival"
}
func (s *Server) isCreative(p *Peer) bool {
	return p.room.usesSurvival() && s.gameMode(p) == "creative"
}
func (s *Server) isSpectator(p *Peer) bool { return s.gameMode(p) == "spectator" }
func (s *Server) mayFly(p *Peer) bool {
	gm := s.gameMode(p)
	return gm == "creative" || gm == "spectator"
}

// prepareStateView fills the client-visible fields of the player state.
func (s *Server) prepareStateView(p *Peer) {
	st := p.state
	st.GM = s.gameMode(p) == "creative" && p.room.usesSurvival()
	st.EndCredits = st.Ext != nil && st.Ext.EndCredits
}

func (s *Server) setGameMode(p *Peer, gm string) bool {
	return s.setPlayerGameMode(p, gm, s.commandActor(p), s.commandReason())
}
func (s *Server) setPlayerGameMode(p *Peer, gm, actor, reason string) bool {
	if p == nil || p.room == nil || p.state == nil {
		return false
	}
	gm = gameModes[strings.ToLower(strings.TrimSpace(gm))]
	if gm == "" {
		return false
	}
	if !p.room.usesSurvival() {
		p.sys("创造世界不支持切换游戏模式。")
		return false
	}
	if p.room.hardcore() && p.state.Dead && gm != "spectator" {
		p.sys("极限模式死亡后只能进入旁观模式。")
		return false
	}
	data := map[string]any{"previous": s.gameMode(p), "gamemode": gm, "reason": reason, "actor": actor, "target": p.sid}
	if !s.allowed(api.PlayerGameModeChanging, p.room, p, data) {
		return false
	}
	p.ext().GameMode = gm
	s.prepareStateView(p)
	p.json(map[string]any{"t": "gm", "gm": map[bool]int{true: 1, false: 0}[gm == "creative"], "gamemode": gm})
	if !s.mayFly(p) {
		p.state.Fly = 0
		p.peakY = p.state.Y
	}
	if gm == "spectator" || gm == "creative" {
		p.state.Ext.FireTicks = 0
	}
	p.room.Dirty = true
	s.health(p)
	p.sys("游戏模式已切换为" + gameModeNames[gm] + "。")
	s.emit(api.PlayerGameModeChanged, p.room, p, data)
	return true
}

// joinedExt runs after a player finishes joining a world.
func (s *Server) joinedExt(p *Peer) {
	if !p.room.usesSurvival() {
		return
	}
	s.sendXP(p)
	s.sendWeather(p)
	if s.isCreative(p) {
		p.json(map[string]any{"t": "gm", "gm": 1})
	}
	if p.state.Ext != nil && p.state.Ext.EndCredits {
		p.json(map[string]any{"t": "credits", "on": 1})
	}
}

func (s *Server) spawnPoint(r *Room) [3]int {
	if r != nil && r.dimName() == "end" {
		return endSpawn(r)
	}
	if r != nil {
		root := r.root()
		if ext := root.Ext; ext != nil && ext.SpawnSet && root.inside(ext.SpawnX, ext.SpawnZ) && ext.SpawnY >= 1 && ext.SpawnY < 128 {
			return [3]int{ext.SpawnX, ext.SpawnY, ext.SpawnZ}
		}
	}
	return s.engine.spawn(r)
}

func (s *Server) setWorldSpawn(r *Room, p *Peer, x, y, z int, yaw, pitch float64, reason string) (bool, [3]int) {
	if r == nil {
		return false, [3]int{}
	}
	root := r.root()
	if !root.inside(x, z) || y < 1 || y >= 128 {
		return false, [3]int{}
	}
	prev := s.spawnPoint(root)
	data := map[string]any{
		"x":         x,
		"y":         y,
		"z":         z,
		"yaw":       yaw,
		"pitch":     pitch,
		"previousX": prev[0],
		"previousY": prev[1],
		"previousZ": prev[2],
		"reason":    reason,
	}
	e := s.emit(api.WorldSpawnChanging, root, p, data)
	if e.Cancelled {
		if p != nil && e.Reason != "" {
			p.sys(limitText(e.Reason, 240))
		}
		return false, prev
	}
	nx := int(math.Floor(e.Number("x")))
	ny := int(math.Floor(e.Number("y")))
	nz := int(math.Floor(e.Number("z")))
	nyaw := e.Number("yaw")
	npitch := e.Number("pitch")
	if !root.inside(nx, nz) || ny < 1 || ny >= 128 {
		return false, prev
	}
	ext := root.ext()
	ext.SpawnSet = true
	ext.SpawnX = nx
	ext.SpawnY = ny
	ext.SpawnZ = nz
	ext.SpawnYaw = nyaw
	ext.SpawnPitch = npitch
	root.Dirty = true
	_ = s.saveRoom(root)

	data["x"], data["y"], data["z"], data["yaw"], data["pitch"] = nx, ny, nz, nyaw, npitch
	s.emit(api.WorldSpawnChanged, root, p, data)
	return true, [3]int{nx, ny, nz}
}

// healthExtra adds spectator/fire/effect/absorption fields to an hp message.
func (s *Server) healthExtra(p *Peer, msg map[string]any) {
	if p.room == nil || !p.room.usesSurvival() {
		return
	}
	msg["sp"] = s.isSpectator(p)
	e := p.state.Ext
	if e == nil {
		return
	}
	msg["fi"] = e.FireTicks > 0 && !s.isSpectator(p)
	if e.Absorb > 0 {
		msg["ab"] = e.Absorb
	}
	fx := 0
	if s.hasEffect(p, "poison") {
		fx |= 1
	}
	if s.hasEffect(p, "wither") {
		fx |= 2
	}
	if s.hasEffect(p, "hunger") {
		fx |= 4
	}
	if s.hasEffect(p, "regeneration") {
		fx |= 8
	}
	if fx != 0 {
		msg["fx"] = fx
	}
	if len(e.Effects) > 0 {
		list := map[string]int{}
		for k, v := range e.Effects {
			list[k] = v.Amp
		}
		msg["effects"] = list
	}
}

func (s *Server) deathInfo(p *Peer) map[string]any {
	hc := p.room.hardcore()
	m := "你死了。"
	if s.gameRule(p.room, "keepInventory") {
		m += "背包已保留。"
	} else {
		m += "物品已掉落在死亡位置。"
	}
	if hc {
		m = "游戏结束！极限模式下死亡后只能旁观。"
	}
	score := 0
	if p.state.Ext != nil {
		score = p.state.Ext.XPTotal
	}
	return map[string]any{"t": "dead", "m": m, "score": score, "hc": hc}
}

// onDeathExt drops inventory / experience according to the gamerules.
func (s *Server) onDeathExt(p *Peer) {
	r := p.room
	if !r.usesSurvival() {
		return
	}
	e := p.ext()
	e.FireTicks = 0
	e.Effects = nil
	e.Absorb = 0
	if !s.gameRule(r, "keepInventory") {
		var stacks Stacks
		for i, v := range p.state.Inv.Slots {
			if v != nil && i != 45 {
				stacks = append(stacks, copyStack(v))
				p.state.Inv.Slots[i] = nil
			}
		}
		if p.state.Cursor != nil {
			stacks = append(stacks, p.state.Cursor)
			p.state.Cursor = nil
		}
		x := clampF(p.state.X, r.minCoord()+.2, r.maxCoord()-.2)
		z := clampF(p.state.Z, r.minCoord()+.2, r.maxCoord()-.2)
		y := clampF(p.state.Y+.5, 1, 127)
		for len(stacks) > 0 {
			n := min(len(stacks), 8)
			drops, err := s.prepareDrops(r, p, stacks[:n], x, y, z, "player_death")
			if err != nil {
				p.state.Recovery = append(p.state.Recovery, stacks...)
				break
			}
			for _, d := range drops {
				d.VX = (s.rng.Float64() - .5) * .4
				d.VZ = (s.rng.Float64() - .5) * .4
			}
			s.publishDrops(r, p, drops, "player_death")
			stacks = stacks[n:]
		}
		// Experience: drop up to 7 points per level (max 100), lose the rest.
		dropXP := min(100, e.XPLevel*7)
		e.XPLevel, e.XPPoints = 0, 0
		if dropXP > 0 {
			s.spawnOrbs(r, x, y, z, dropXP)
		}
		s.sendXP(p)
		s.inventory(p, 0, 0)
	}
	if r.hardcore() {
		e.GameMode = "spectator"
	}
	r.Dirty = true
}

// respawnPlayer handles the respawn request (bed spawn, dimension return,
// hardcore spectating).
func (s *Server) respawnPlayer(p *Peer) bool {
	st := p.state
	r := p.room.root()
	e := p.ext()
	target := r
	var pos [3]float64
	sp := s.spawnPoint(r)
	pos = [3]float64{float64(sp[0]) + .5, float64(sp[1]), float64(sp[2]) + .5}
	if e.SpawnSet {
		bx, by, bz := e.SpawnX, e.SpawnY, e.SpawnZ
		if s.nameOf(s.block(r, bx, by, bz)) == "Bed" {
			if spot, ok := s.bedExit(r, bx, by, bz); ok {
				pos = spot
			}
		} else {
			e.SpawnSet = false
			p.sys("你的床已丢失或被阻挡，已回到世界出生点。")
		}
	}
	if s.residence != nil {
		rx, ry, rz := int(math.Floor(pos[0])), int(math.Floor(pos[1])), int(math.Floor(pos[2]))
		res := s.residence.FindAt(target.ID, rx, ry, rz)
		if res == nil {
			res = s.residence.FindAt(target.ID, rx, ry+1, rz)
		}
		if res != nil && !s.residence.HasPermission(res, p, "enter") {
			pos[0], pos[1], pos[2] = s.residence.FindSafeOutsidePos(target, res, pos[0], pos[1], pos[2])
		}
	}
	oldDead, oldHealth, oldFood, oldSat, oldExh, oldAir := st.Dead, st.Health, st.Food, st.Saturation, st.Exhaustion, p.air
	oldGM := e.GameMode
	st.Dead = false
	st.Health = s.maxHealth(p)
	st.Food = 20
	st.Saturation = 5
	st.Exhaustion = 0
	p.air = 300
	if r.hardcore() {
		e.GameMode = "spectator"
	}
	if !s.moveToDimension(p, target, pos[0], pos[1], pos[2], true) {
		st.Dead, st.Health, st.Food, st.Saturation, st.Exhaustion, p.air = oldDead, oldHealth, oldFood, oldSat, oldExh, oldAir
		e.GameMode = oldGM
		return false
	}
	s.health(p)
	return true
}

// moveToDimension places the player in a dimension (possibly the same one).
func (s *Server) moveToDimension(p *Peer, target *Room, x, y, z float64, forceSV bool) bool {
	return s.moveToDimensionWithContext(p, target, x, y, z, forceSV, p, "dimension_transfer")
}

func (s *Server) moveToDimensionWithContext(p *Peer, target *Room, x, y, z float64, forceSV bool, actor *Peer, reason string) bool {
	if p == nil || p.room == nil || p.state == nil || target == nil || p.closed || target.root() != p.room.root() {
		return false
	}
	old := p.room
	if !number(x, target.minCoord(), target.maxCoord()) || !number(y, 1, 127) || !number(z, target.minCoord(), target.maxCoord()) {
		return false
	}
	st := p.state
	fromX, fromY, fromZ := st.X, st.Y, st.Z
	data := map[string]any{"fromWorldId": old.ID, "worldId": target.ID, "fromDimension": old.dimName(), "dimension": target.dimName(), "fromX": fromX, "fromY": fromY, "fromZ": fromZ, "x": x, "y": y, "z": z, "reason": reason, "actor": s.commandActor(actor), "target": playerTarget(p), "targetAccountId": p.sid, "stack": itemSnapshot(s.heldItem(p)), "source": s.pluginSource}
	if actor != nil {
		data["actorPlayer"] = playerTarget(actor)
	}
	if !s.allowed(api.PlayerDimensionChanging, old, p, data) || !s.allowed(api.PlayerTeleporting, old, p, data) {
		return false
	}
	if p.room != old || p.state != st || st.X != fromX || st.Y != fromY || st.Z != fromZ {
		return false
	}
	departure, ok := s.prepareTransportDeparture(p, "transport_dimension_transfer")
	if !ok {
		return false
	}
	if departure != nil {
		departure()
	}
	s.closeWindow(p)
	changed := old != target
	if changed {
		delete(old.Peers, p)
		broadcast(old, map[string]any{"t": "leave", "id": p.id}, nil)
		target.Peers[p] = true
		p.room = target
		for other := range target.Peers {
			if other != p {
				other.json(map[string]any{"t": "join", "p": p.public()})
			}
		}
		old.Dirty = true
		if target.dimName() != "end" {
			p.json(map[string]any{
				"t":       "bossbar",
				"name":    "末影龙",
				"percent": 0,
				"visible": false,
			})
		}
	}
	e := p.ext()
	e.Dim = ""
	if target.dimName() != "overworld" {
		e.Dim = target.dimName()
	}
	st.X, st.Y, st.Z = x, y, z
	p.peakY = y
	p.mining = nil
	p.useAt = time.Time{}
	p.known = map[int]bool{}
	p.knownDrops = map[uint32]bool{}
	p.knownMobs = map[uint32]bool{}
	s.prepareStateView(p)
	msg := map[string]any{"t": "place", "st": st}
	if changed || forceSV || target.Infinite {
		p.setOrigin()
		msg["sv"] = s.peerSurvivalInfo(p)
	}
	p.json(msg)
	s.stream(p)
	s.inventory(p, 0, 0)
	p.json(map[string]string{"t": "synced"})
	s.syncMobsFor(p)
	s.sendWeather(p)
	target.Dirty = true
	target.root().Dirty = true
	s.emit(api.PlayerDimensionChanged, target, p, data)
	s.emit(api.PlayerTeleported, target, p, data)
	return true
}

// bedExit finds a free spot next to a bed.
func (s *Server) bedExit(r *Room, x, y, z int) ([3]float64, bool) {
	for _, d := range [][2]int{{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}, {2, 0}, {-2, 0}, {0, 2}, {0, -2}} {
		for dy := 0; dy <= 1; dy++ {
			bx, by, bz := x+d[0], y+dy, z+d[1]
			if d[0] == 0 && d[1] == 0 {
				by = y + 1
			}
			if !r.inside(bx, bz) || by < 1 || by > 125 {
				continue
			}
			floor := s.kind(s.block(r, bx, by-1, bz))
			a, b := s.kind(s.block(r, bx, by, bz)), s.kind(s.block(r, bx, by+1, bz))
			if (floor == 1 || floor == 7) && (a == 0 || a == 5) && (b == 0 || b == 5) {
				return [3]float64{float64(bx) + .5, float64(by), float64(bz) + .5}, true
			}
		}
	}
	return [3]float64{}, false
}

// ---------------------------------------------------------------------------
// Damage adjustments, invulnerability, effects and fire.
// ---------------------------------------------------------------------------

// vulnerable reports whether damage should apply at all.
func (s *Server) vulnerable(p *Peer, ctx *api.DamageContext) bool {
	if !p.room.usesSurvival() {
		return true
	}
	gm := s.gameMode(p)
	if gm == "creative" || gm == "spectator" {
		return ctx != nil && ctx.Cause == "void" && gm == "creative" && false
	}
	if ctx != nil {
		switch ctx.Cause {
		case "fall":
			return s.gameRule(p.room, "fallDamage")
		case "drowning":
			return s.gameRule(p.room, "drowningDamage")
		case "lava", "fire":
			return s.gameRule(p.room, "fireDamage") && !s.hasEffect(p, "fire_resistance")
		case "player_attack":
			return s.gameRule(p.room, "pvp")
		}
	}
	return true
}

// scaleDamage applies difficulty, resistance-like effects and absorption.
func (s *Server) scaleDamage(p *Peer, amount float64, ctx *api.DamageContext) float64 {
	if !p.room.usesSurvival() {
		return amount
	}
	if ctx != nil && (ctx.Cause == "mob_attack" || ctx.Cause == "projectile" && ctx.Attacker != nil && ctx.Attacker.Kind == "entity") {
		switch s.difficulty(p.room) {
		case "peaceful":
			return 0
		case "easy":
			amount = math.Min(amount/2+1, amount)
		case "hard":
			amount *= 1.5
		}
	}
	return amount
}

func (s *Server) hasEffect(p *Peer, name string) bool {
	if p == nil || p.state == nil || p.state.Ext == nil || p.state.Ext.Effects == nil {
		return false
	}
	e := p.state.Ext.Effects[name]
	return e != nil && e.Ticks > 0
}
func (s *Server) effectAmp(p *Peer, name string) int {
	if !s.hasEffect(p, name) {
		return -1
	}
	return p.state.Ext.Effects[name].Amp
}

// addEffect applies a status effect (ticks are 50ms game ticks).
func (s *Server) addEffect(p *Peer, name string, ticks, amp int, commit ...func() bool) bool {
	if effectNames[name] == "" || ticks < 1 || ticks > 2000000 || amp < 0 || amp > 9 {
		return false
	}
	data := map[string]any{"effect": name, "ticks": ticks, "amplifier": amp, "reason": "apply", "stack": itemSnapshot(s.heldItem(p))}
	event := s.emit(api.PlayerEffectApplying, p.room, p, data)
	t, a := event.Number("ticks"), event.Number("amplifier")
	if event.Cancelled || !number(t, 1, 2000000) || !number(a, 0, 9) || t != math.Trunc(t) || a != math.Trunc(a) {
		return false
	}
	ticks, amp = int(t), int(a)
	data["ticks"], data["amplifier"] = ticks, amp
	e := p.ext()
	if e.Effects == nil {
		e.Effects = map[string]*Effect{}
	}
	switch name {
	case "instant_health":
		if !s.heal(p, float64(int(4)<<min(amp, 4)), "potion", commit...) {
			return false
		}
		s.emit(api.PlayerEffectApplied, p.room, p, data)
		return true
	case "instant_damage":
		if !s.damagePlayer(p, float64(int(6)<<min(amp, 4)), "被魔法杀死了。", &api.DamageContext{Cause: "magic", Weapon: itemSnapshot(s.heldItem(p)), Attacker: playerTarget(p)}, commit...) {
			return false
		}
		s.emit(api.PlayerEffectApplied, p.room, p, data)
		return true
	}
	if cur := e.Effects[name]; cur != nil && cur.Amp > amp {
		return false
	}
	if len(commit) > 0 && !commit[0]() {
		return false
	}
	if name == "absorption" {
		e.Absorb = math.Max(e.Absorb, float64(4*(amp+1)))
	}
	e.Effects[name] = &Effect{Ticks: ticks, Amp: amp}
	p.room.Dirty = true
	s.health(p)
	s.emit(api.PlayerEffectApplied, p.room, p, data)
	return true
}

func (s *Server) ignite(p *Peer, ticks int) bool {
	if p == nil || ticks < 1 || ticks > 2000000 || s.hasEffect(p, "fire_resistance") || s.isCreative(p) || s.isSpectator(p) {
		return false
	}
	e := p.ext()
	if e.FireTicks >= ticks {
		return false
	}
	data := map[string]any{"previous": e.FireTicks, "ticks": ticks, "reason": "fire"}
	event := s.emit(api.PlayerIgniting, p.room, p, data)
	n := event.Number("ticks")
	if event.Cancelled || !number(n, 1, 2000000) || n != math.Trunc(n) {
		return false
	}
	e.FireTicks = int(n)
	p.room.Dirty = true
	s.emit(api.PlayerIgnited, p.room, p, data)
	return true
}

func (s *Server) softLanding(p *Peer) bool {
	x, z := int(math.Floor(p.state.X)), int(math.Floor(p.state.Z))
	below := s.block(p.room, x, int(math.Floor(p.state.Y-0.05)), z)
	switch s.nameOf(below) {
	case "Hay Bale":
		// Hay bales absorb 80% of fall damage: emulate by halving the peak.
		p.peakY = p.state.Y + (p.peakY-p.state.Y)*0.2
	case "Bed":
		p.peakY = p.state.Y + (p.peakY-p.state.Y)*0.5
	case "Cobweb":
		return true
	}
	return s.hasEffect(p, "slow_falling")
}

// lifeExt runs every second before the built-in environment checks.
// Returning true skips the built-in checks (creative/spectator players).
func (s *Server) lifeExt(p *Peer) bool {
	if !p.room.usesSurvival() {
		return false
	}
	gm := s.gameMode(p)
	if gm == "creative" || gm == "spectator" {
		p.air = 300
		p.peakY = p.state.Y
		if p.state.Health < s.maxHealth(p) {
			p.state.Health = s.maxHealth(p)
		}
		p.state.Food = 20
		s.health(p)
		return true
	}
	if s.difficulty(p.room) == "peaceful" {
		if p.state.Health < s.maxHealth(p) {
			s.heal(p, 1, "peaceful")
		}
		if p.state.Food < 20 {
			p.state.Food++
		}
	}
	return false
}

// effectsTick runs every 100ms per survival player: effects, fire, cactus,
// berry-like contact damage and weather lightning.
func effectsTick(s *Server, p *Peer) {
	e := p.state.Ext
	tick := p.room.rt().tickCount
	if e != nil {
		changed := false
		for name, fx := range e.Effects {
			if fx.Ticks <= 2 {
				if s.removeEffects(p, []string{name}, "expired") {
					changed = true
				} else {
					fx.Ticks = 1
				}
				continue
			}
			fx.Ticks -= 2
			switch name {
			case "regeneration":
				every := max(1, 25>>min(fx.Amp, 4)/2)
				if int(tick)%every == 0 {
					s.heal(p, 1, "regeneration")
				}
			case "poison":
				every := max(1, 25>>min(fx.Amp, 4)/2)
				if int(tick)%every == 0 && p.state.Health > 1 {
					s.damagePlayer(p, 1, "中毒身亡。", &api.DamageContext{Cause: "magic"})
				}
			case "wither":
				if tick%20 == 0 {
					s.damagePlayer(p, 1, "凋零了。", &api.DamageContext{Cause: "wither"})
				}
			case "hunger":
				p.state.Exhaustion += 0.01 * float64(fx.Amp+1)
			case "saturation":
				p.state.Food = min(20, p.state.Food+1)
				p.state.Saturation = math.Min(float64(p.state.Food), p.state.Saturation+2)
			}
		}
		if changed {
			if len(e.Effects) == 0 {
				e.Effects = nil
			}
			s.health(p)
		}
		if e.FireTicks > 0 {
			x, y, z := int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z))
			if isWater(s.block(p.room, x, y, z)) || s.raining(p.room) && s.skyOpen(p.room, x, y+2, z) {
				e.FireTicks = 0
				s.health(p)
			} else {
				e.FireTicks -= 2
				if tick%10 == 0 && !s.hasEffect(p, "fire_resistance") {
					s.damagePlayer(p, 1, "被烧死了。", &api.DamageContext{Cause: "fire"})
				}
				if e.FireTicks <= 0 {
					e.FireTicks = 0
					s.health(p)
				}
			}
		}
	}
	if tick%5 == 0 {
		// Contact damage: fire blocks, cactus, magma.
		x0, x1 := int(math.Floor(p.state.X-.31)), int(math.Floor(p.state.X+.31))
		z0, z1 := int(math.Floor(p.state.Z-.31)), int(math.Floor(p.state.Z+.31))
		y0, y1 := int(math.Floor(p.state.Y-.05)), int(math.Floor(p.state.Y+1.7))
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				for y := y0; y <= y1; y++ {
					b := s.block(p.room, x, y, z)
					switch s.nameOf(b) {
					case "Cactus":
						if y >= int(math.Floor(p.state.Y)) || y == y0 {
							s.damagePlayer(p, 1, "被仙人掌刺死了。", &api.DamageContext{Cause: "cactus"})
							return
						}
					case "Fire":
						if y >= int(math.Floor(p.state.Y)) {
							s.ignite(p, 160)
							s.damagePlayer(p, 1, "被烧死了。", &api.DamageContext{Cause: "fire"})
							return
						}
					case "Magma Block":
						if y == y0 && p.flags&1 == 0 && !s.hasEffect(p, "fire_resistance") {
							s.damagePlayer(p, 1, "发现了地板是熔岩做的。", &api.DamageContext{Cause: "fire"})
							return
						}
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Weather.
// ---------------------------------------------------------------------------

func (s *Server) raining(r *Room) bool {
	return r.dimName() == "overworld" && r.root().Ext != nil && r.root().Ext.Rain
}
func (s *Server) thundering(r *Room) bool {
	return s.raining(r) && r.root().Ext.Thunder
}
func (s *Server) sendWeather(p *Peer) {
	r := p.room
	p.json(map[string]any{"t": "weather", "rain": s.raining(r), "thunder": s.thundering(r)})
}
func (s *Server) setWeather(r *Room, rain, thunder bool, duration int) bool {
	e := r.ext()
	if duration <= 0 {
		duration = 6000 + s.rng.Intn(12000)
	}
	data := map[string]any{"previousRain": e.Rain, "previousThunder": e.Thunder, "rain": rain, "thunder": rain && thunder, "duration": duration, "reason": "weather_change"}
	event := s.emit(api.WorldWeatherChanging, r, nil, data)
	n := event.Number("duration")
	if event.Cancelled || !number(n, 1, 20000000) || n != math.Trunc(n) {
		return false
	}
	e.Rain, e.Thunder = rain, rain && thunder
	e.WeatherT = int(n)
	r.root().Dirty = true
	for _, p := range worldPeers(r) {
		s.sendWeather(p)
	}
	s.emit(api.WorldWeatherChanged, r, nil, data)
	return true
}

// weatherTick runs per room; only the root advances the cycle.
func weatherTick(s *Server, r *Room) {
	if r.parent != nil || !r.usesSurvival() {
		return
	}
	e := r.ext()
	if s.gameRule(r, "doWeatherCycle") {
		e.WeatherT -= 2
		if e.WeatherT <= 0 {
			if e.Rain {
				s.setWeather(r, false, false, 12000+s.rng.Intn(24000))
			} else {
				s.setWeather(r, true, s.rng.Intn(4) == 0, 3000+s.rng.Intn(9000))
			}
		}
	}
	if !e.Thunder || !e.Rain || r.rt().tickCount%10 != 0 {
		return
	}
	// Roughly one strike per 10s per player area.
	for p := range r.Peers {
		if p.state.Dead || s.rng.Intn(100) != 0 {
			continue
		}
		x := int(p.state.X) + s.rng.Intn(64) - 32
		z := int(p.state.Z) + s.rng.Intn(64) - 32
		if !r.inside(x, z) {
			continue
		}
		y := s.highestBlock(r, x, z)
		if y <= 0 || !s.skyOpen(r, x, y+1, z) {
			continue
		}
		s.lightning(r, x, y+1, z)
	}
}

func (s *Server) highestBlock(r *Room, x, z int) int {
	for y := 126; y > 0; y-- {
		k := s.kind(s.block(r, x, y, z))
		if k != 0 && k != 5 && k != 8 {
			return y
		}
	}
	return 0
}

// lightning strikes a position: fire, damage and thunder.
func (s *Server) lightning(r *Room, x, y, z int) {
	soundAt(r, "entity.lightning_bolt.thunder", float64(x)+.5, float64(y), float64(z)+.5, nil)
	soundAt(r, "entity.lightning_bolt.impact", float64(x)+.5, float64(y), float64(z)+.5, nil)
	for p := range r.Peers {
		p.json(map[string]any{"t": "lightning", "x": x, "y": y, "z": z})
	}
	if s.gameRule(r, "doFireTick") && s.difficulty(r) != "peaceful" && s.block(r, x, y, z) == 0 && s.kind(s.block(r, x, y-1, z)) == 1 {
		s.setBlock(r, x, y, z, 95, 0)
	}
	for p := range r.Peers {
		if p.state.Dead {
			continue
		}
		if math.Hypot(p.state.X-float64(x)-.5, p.state.Z-float64(z)-.5) < 3 && math.Abs(p.state.Y-float64(y)) < 4 {
			s.damagePlayer(p, 5, "被闪电击中了。", &api.DamageContext{Cause: "lightning"})
			s.ignite(p, 160)
		}
	}
	for _, m := range r.Mobs {
		if math.Hypot(m.X-float64(x)-.5, m.Z-float64(z)-.5) < 3 {
			s.damageMob(r, nil, m, 5, "lightning")
		}
	}
}

// mobSpawningAllowed: gamerule and peaceful difficulty.
func (s *Server) mobSpawningAllowed(r *Room) bool {
	return s.gameRule(r, "doMobSpawning")
}

// peacefulTick removes hostile mobs on peaceful difficulty.
func peacefulTick(s *Server, r *Room) {
	if r.rt().tickCount%10 != 0 || s.difficulty(r) != "peaceful" {
		return
	}
	for _, id := range sortedMobIDs(r) {
		if m := r.Mobs[id]; m != nil && mobSpecs[m.Type].Hostile && m.Type != "ender_dragon" && m.Type != "wither" {
			s.removeMob(r, m, "peaceful")
		}
	}
}

func init() {
	onPeerTick(effectsTick)
	onRoomTick(weatherTick)
	onRoomTick(peacefulTick)
}

// ---------------------------------------------------------------------------
// Commands.
// ---------------------------------------------------------------------------

func (s *Server) findPlayer(r *Room, name string) *Peer {
	name = strings.ToLower(strings.TrimPrefix(name, "@"))
	for _, p := range worldPeers(r) {
		if strings.ToLower(p.name) == name || strings.ToLower(p.sid) == name {
			return p
		}
	}
	return nil
}

func (s *Server) targets(p *Peer, sel string) []*Peer {
	switch sel {
	case "", "@s":
		return []*Peer{p}
	case "@a":
		return worldPeers(p.room)
	case "@p":
		return []*Peer{s.nearestPlayer(p)}
	case "@r":
		all := worldPeers(p.room)
		return []*Peer{all[s.rng.Intn(len(all))]}
	}
	if t := s.findPlayer(p.room, sel); t != nil {
		return []*Peer{t}
	}
	return nil
}

func parseCoord(arg string, base float64) (float64, bool) {
	if strings.HasPrefix(arg, "~") {
		if arg == "~" {
			return base, true
		}
		v, err := strconv.ParseFloat(arg[1:], 64)
		return base + v, err == nil
	}
	v, err := strconv.ParseFloat(arg, 64)
	return v, err == nil
}

var effectNames = map[string]string{"speed": "速度", "swiftness": "速度", "slowness": "缓慢", "strength": "力量", "jump_boost": "跳跃提升", "regeneration": "生命恢复", "resistance": "抗性提升", "fire_resistance": "抗火", "water_breathing": "水下呼吸", "night_vision": "夜视", "poison": "中毒", "wither": "凋零", "hunger": "饥饿", "saturation": "饱和", "absorption": "伤害吸收", "instant_health": "瞬间治疗", "instant_damage": "瞬间伤害", "haste": "急迫", "mining_fatigue": "挖掘疲劳", "slow_falling": "缓降", "weakness": "虚弱"}

// extCommand handles the vanilla-style commands. ok=false means unknown.
func (s *Server) extCommand(p *Peer, name string, parts []string) (handled bool, success bool) {
	if h, ok := s.handleFullCommand(p, name, parts); h {
		return h, ok
	}
	r := p.room
	args := parts[1:]
	owner := s.canGameCommand(p, "server.admin")
	needOwner := func() bool {
		if !owner {
			p.sys("这个命令需要 OP 权限。")
			return false
		}
		return true
	}
	survivalOnly := func() bool {
		if !r.usesSurvival() {
			p.sys("这个命令只能在生存世界中使用。")
			return false
		}
		return true
	}
	switch name {
	case "gamemode", "gm":
		if !s.canGameCommand(p, "server.gamemode") {
			p.sys("切换游戏模式需要 OP 或 server.gamemode 权限。")
			return true, false
		}
		if !survivalOnly() {
			return true, false
		}
		if len(args) < 1 || len(args) > 2 || gameModes[strings.ToLower(args[0])] == "" {
			p.sys("用法：/gamemode survival|creative|adventure|spectator [玩家]")
			return true, false
		}
		gm := gameModes[strings.ToLower(args[0])]
		sel := ""
		if len(args) > 1 {
			sel = args[1]
		}
		ts := s.targets(p, sel)
		if len(ts) == 0 {
			p.sys("找不到该玩家。")
			return true, false
		}
		for _, t := range ts {
			if !s.setPlayerGameMode(t, gm, s.commandActor(p), s.commandReason()) {
				return true, false
			}
		}
		return true, true
	case "difficulty":
		if len(args) == 0 {
			p.sys("当前难度：" + map[string]string{"peaceful": "和平", "easy": "简单", "normal": "普通", "hard": "困难"}[s.difficulty(r)])
			return true, true
		}
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		d := map[string]string{"peaceful": "peaceful", "p": "peaceful", "0": "peaceful", "easy": "easy", "e": "easy", "1": "easy", "normal": "normal", "n": "normal", "2": "normal", "hard": "hard", "h": "hard", "3": "hard"}[strings.ToLower(args[0])]
		if d == "" {
			p.sys("用法：/difficulty peaceful|easy|normal|hard")
			return true, false
		}
		if r.hardcore() && d != "hard" {
			p.sys("极限模式的难度固定为困难。")
			return true, false
		}
		if !s.setDifficulty(r, p, d, "command") {
			return true, false
		}
		broadcastWorld(r, map[string]string{"t": "sys", "m": "难度已设为" + map[string]string{"peaceful": "和平", "easy": "简单", "normal": "普通", "hard": "困难"}[d] + "。"}, nil)
		return true, true
	case "gamerule":
		if len(args) == 0 {
			keys := make([]string, 0, len(gameRuleDefaults))
			for k := range gameRuleDefaults {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := []string{}
			for _, k := range keys {
				out = append(out, fmt.Sprintf("%s=%v", k, s.gameRule(r, k)))
			}
			out = append(out, fmt.Sprintf("randomTickSpeed=%d", s.randomTickSpeed(r)))
			p.sys(limitText("游戏规则："+strings.Join(out, "，"), 240))
			return true, true
		}
		if args[0] == "randomTickSpeed" {
			if len(args) == 1 {
				p.sys(fmt.Sprintf("randomTickSpeed = %d", s.randomTickSpeed(r)))
				return true, true
			}
			if !needOwner() {
				return true, false
			}
			val, err := strconv.Atoi(args[1])
			if err != nil || val < 0 || val > maxRandomTickSpeed {
				p.sys("值必须是 0 到 4096 之间的整数。")
				return true, false
			}
			if !s.setRandomTickSpeed(r, p, val, "command") {
				return true, false
			}
			p.sys(fmt.Sprintf("游戏规则 randomTickSpeed 已设为 %d。", val))
			return true, true
		}
		if _, ok := gameRuleDefaults[args[0]]; !ok {
			p.sys("未知的游戏规则：" + args[0])
			return true, false
		}
		if len(args) == 1 {
			p.sys(fmt.Sprintf("%s = %v", args[0], s.gameRule(r, args[0])))
			return true, true
		}
		if !needOwner() {
			return true, false
		}
		v, err := strconv.ParseBool(args[1])
		if err != nil {
			p.sys("值必须是 true 或 false。")
			return true, false
		}
		if !s.setRule(r, p, args[0], v, "command") {
			return true, false
		}
		p.sys(fmt.Sprintf("游戏规则 %s 已设为 %v。", args[0], v))
		return true, true
	case "weather":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		if len(args) < 1 {
			p.sys("用法：/weather clear|rain|thunder [秒数]")
			return true, false
		}
		dur := 0
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 && n <= 1000000 {
				dur = n * 20
			}
		}
		switch args[0] {
		case "clear":
			if !s.setWeather(r, false, false, max(dur, 12000+s.rng.Intn(24000))) {
				return true, false
			}
		case "rain":
			if !s.setWeather(r, true, false, dur) {
				return true, false
			}
		case "thunder":
			if !s.setWeather(r, true, true, dur) {
				return true, false
			}
		default:
			p.sys("用法：/weather clear|rain|thunder [秒数]")
			return true, false
		}
		p.sys("天气已更新。")
		return true, true
	case "tp", "teleport":
		if !needOwner() {
			return true, false
		}
		switch len(args) {
		case 1:
			t := s.findPlayer(r, args[0])
			if t == nil {
				p.sys("找不到该玩家。")
				return true, false
			}
			if t.room != p.room {
				return true, !p.state.Dead && s.moveToDimensionWithContext(p, t.room, t.state.X, t.state.Y, t.state.Z, true, p, s.commandReason())
			}
			return true, s.teleport(p, t.state.X, t.state.Y, t.state.Z, "command")
		case 2:
			a, b := s.findPlayer(r, args[0]), s.findPlayer(r, args[1])
			if a == nil || b == nil {
				p.sys("找不到该玩家。")
				return true, false
			}
			if a.room != b.room {
				return true, !a.state.Dead && s.moveToDimensionWithContext(a, b.room, b.state.X, b.state.Y, b.state.Z, true, p, s.commandReason())
			}
			return true, s.teleport(a, b.state.X, b.state.Y, b.state.Z, "command")
		case 3, 4:
			t := p
			off := 0
			if len(args) == 4 {
				t = s.findPlayer(r, args[0])
				off = 1
				if t == nil {
					p.sys("找不到该玩家。")
					return true, false
				}
			}
			x, ok1 := parseCoord(args[off], t.state.X)
			y, ok2 := parseCoord(args[off+1], t.state.Y)
			z, ok3 := parseCoord(args[off+2], t.state.Z)
			if !ok1 || !ok2 || !ok3 {
				p.sys("坐标格式错误。")
				return true, false
			}
			if !s.teleport(t, x, y, z, "command") {
				p.sys("目标位置超出世界范围。")
				return true, false
			}
			return true, true
		}
		p.sys("用法：/tp <玩家> 或 /tp [玩家] <x> <y> <z>")
		return true, false
	case "give":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		if len(args) < 1 {
			p.sys("用法：/give [玩家] <物品> [数量]")
			return true, false
		}
		t := p
		if len(args) >= 2 {
			if other := s.findPlayer(r, args[0]); other != nil {
				t = other
				args = args[1:]
			}
		}
		id := s.itemByCommandName(args[0])
		if id <= 0 {
			p.sys("未知物品：" + args[0])
			return true, false
		}
		count := 1
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 && n <= 2304 {
				count = n
			}
		}
		left := count
		for left > 0 {
			n := min(left, max(1, s.rules.max(id)))
			v := stack(id, n, 0)
			if rest := s.add(t.state.Inv, v); rest != nil {
				s.dropFromPlayer(t, rest)
			}
			left -= n
		}
		s.inventory(t, 0, 0)
		p.sys(fmt.Sprintf("已给予 %s %d 个 %s。", t.name, count, s.rules.byID[id].Name))
		return true, true
	case "kill":
		if len(args) > 0 && !owner {
			p.sys("需要房主权限。")
			return true, false
		}
		sel := ""
		if len(args) > 0 {
			sel = args[0]
		}
		for _, t := range s.targets(p, sel) {
			if !t.room.usesSurvival() {
				continue
			}
			gm := s.gameMode(t)
			if gm == "creative" || gm == "spectator" {
				t.ext().GameMode = "survival-kill"
			}
			s.killPlayer(t)
			if t.state.Ext != nil && t.state.Ext.GameMode == "survival-kill" {
				t.state.Ext.GameMode = gm
			}
		}
		return true, true
	case "xp", "experience":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		if len(args) < 1 {
			p.sys("用法：/xp <数量>[L] [玩家]")
			return true, false
		}
		t := p
		if len(args) > 1 {
			if t = s.findPlayer(r, args[1]); t == nil {
				p.sys("找不到该玩家。")
				return true, false
			}
		}
		raw := strings.ToLower(args[0])
		levels := strings.HasSuffix(raw, "l")
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "l"))
		if err != nil || n < -100000 || n > 100000 {
			p.sys("数量格式错误。")
			return true, false
		}
		if levels {
			e := t.ext()
			e.XPLevel = max(0, e.XPLevel+n)
			if e.XPLevel == 0 {
				e.XPPoints = 0
			}
			s.sendXP(t)
		} else {
			s.giveXP(t, n)
		}
		p.sys("经验已更新。")
		return true, true
	case "effect":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		if len(args) >= 1 && args[0] == "clear" {
			t := p
			if len(args) > 1 {
				if t = s.findPlayer(r, args[1]); t == nil {
					p.sys("找不到该玩家。")
					return true, false
				}
			}
			if !s.clearEffects(t, "command") {
				return true, false
			}
			p.sys("状态效果已清除。")
			return true, true
		}
		if len(args) < 1 {
			p.sys("用法：/effect <效果> [秒数] [等级] 或 /effect clear")
			return true, false
		}
		eff := strings.TrimPrefix(strings.ToLower(args[0]), "minecraft:")
		if eff == "swiftness" {
			eff = "speed"
		}
		if effectNames[eff] == "" {
			p.sys("未知效果。可用：speed、slowness、strength、regeneration、fire_resistance、water_breathing、night_vision、poison、wither、hunger、saturation、absorption、instant_health、instant_damage、jump_boost、resistance、slow_falling、weakness")
			return true, false
		}
		secs, amp := 30, 0
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 && n <= 100000 {
				secs = n
			}
		}
		if len(args) > 2 {
			if n, err := strconv.Atoi(args[2]); err == nil && n >= 0 && n < 10 {
				amp = n
			}
		}
		if !s.addEffect(p, eff, secs*20, amp) {
			return true, false
		}
		p.sys("已获得效果：" + effectNames[eff] + "。")
		return true, true
	case "spawnpoint":
		if !survivalOnly() {
			return true, false
		}
		e := p.ext()
		e.SpawnX, e.SpawnY, e.SpawnZ = int(p.state.X), int(p.state.Y), int(p.state.Z)
		e.SpawnSet = false
		p.sys("重生点仍为世界出生点；睡床可以设置重生点。")
		return true, true
	case "seed":
		p.sys(fmt.Sprintf("种子：%d", seed(r.seedID())))
		return true, true
	case "worldinfo":
		p.sys(fmt.Sprintf("世界宽度 %d 格，维度 %s，难度 %s，极限 %v。", r.width(), r.dimName(), s.difficulty(r), r.hardcore()))
		return true, true
	}
	return false, false
}

// killPlayer deals lethal damage regardless of armor.
func (s *Server) killPlayer(p *Peer) {
	if p.state.Dead {
		return
	}
	s.damagePlayer(p, p.state.Health+p.ext().Absorb+1000, "被杀死了。", &api.DamageContext{Cause: "kill"})
}

// itemByCommandName resolves /give names (english keys or Chinese names).
func (s *Server) itemByCommandName(name string) int {
	n := strings.TrimPrefix(strings.ToLower(name), "minecraft:")
	if id, err := strconv.Atoi(n); err == nil {
		if _, ok := s.rules.byID[id]; ok {
			return id
		}
	}
	for _, it := range s.rules.Items {
		if strings.EqualFold(strings.ReplaceAll(it.Name, " ", "_"), n) || strings.EqualFold(it.Name, name) || it.Key == n {
			return it.ID
		}
	}
	aliases := map[string]string{"redstone": "Spark Dust", "glowstone": "Lumen Rock", "netherrack": "Underrock", "soul_sand": "Grave Sand", "end_stone": "Void Stone", "ender_pearl": "Blink Pearl", "ender_eye": "Seeker Eye", "eye_of_ender": "Seeker Eye", "blaze_rod": "Cinder Rod", "blaze_powder": "Cinder Dust", "nether_wart": "Under Wart", "nether_bricks": "Under Bricks", "redstone_ore": "Spark Ore", "redstone_block": "Block of Spark", "end_portal_frame": "Void Gate Frame"}
	if a, ok := aliases[n]; ok {
		return s.rules.byName[a]
	}
	return 0
}
