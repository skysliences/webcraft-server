package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

func playerTarget(p *Peer) *api.Combatant {
	return &api.Combatant{Kind: "player", PlayerID: p.id, Name: p.name, X: p.state.X, Y: p.state.Y, Z: p.state.Z, Health: p.state.Health}
}
func mobTarget(m *Mob) *api.Combatant {
	return &api.Combatant{Kind: "entity", EntityID: m.ID, EntityType: m.Type, X: m.X, Y: m.Y, Z: m.Z, Health: m.Health}
}
func (s *Server) playerAttack(p *Peer, target *api.Combatant, amount float64) *api.DamageContext {
	ctx := &api.DamageContext{Cause: "player_attack", Attacker: playerTarget(p), Target: target, Weapon: itemSnapshot(s.heldItem(p)), RawAmount: amount, Amount: amount, HealthBefore: target.Health, HealthAfter: target.Health}
	e := s.emit(api.PlayerAttacking, p.room, p, damageData(ctx, "player_attack"))
	amount = e.Number("amount")
	if e.Cancelled || !number(amount, 0, 1000) || amount == 0 {
		return nil
	}
	ctx.RawAmount, ctx.Amount = amount, amount
	return ctx
}
func damageData(ctx *api.DamageContext, reason string) map[string]any {
	snapshot := *ctx
	return map[string]any{"amount": ctx.Amount, "reason": reason, "cause": ctx.Cause, "damage": &snapshot, "health": ctx.HealthAfter, "healthBefore": ctx.HealthBefore, "healthAfter": ctx.HealthAfter, "appliedAmount": ctx.AppliedAmount}
}
func damageCause(reason string) string {
	switch reason {
	case "掉出了世界。":
		return "void"
	case "掉入了熔岩。":
		return "lava"
	case "溺水了。":
		return "drowning"
	case "摔落了。":
		return "fall"
	case "饿倒了。":
		return "starvation"
	case "sunlight":
		return "sunlight"
	case "fall":
		return "fall"
	default:
		return "environment"
	}
}
func (s *Server) damage(p *Peer, amount float64, reason string) {
	s.damagePlayer(p, amount, reason, &api.DamageContext{Cause: damageCause(reason)})
}
func (s *Server) damagePlayer(p *Peer, amount float64, reason string, ctx *api.DamageContext, commit ...func() bool) bool {
	if p == nil || p.state.Dead || !number(amount, 0, 1000) || amount == 0 {
		return false
	}
	if !s.vulnerable(p, ctx) {
		return false
	}
	if s.isShieldBlocking(p, ctx) {
		s.applyShieldBlock(p, ctx)
		return false
	}
	ctx.Target = playerTarget(p)
	ctx.HealthBefore, ctx.HealthAfter = p.state.Health, p.state.Health
	if ctx.RawAmount == 0 {
		ctx.RawAmount = amount
	}
	if ctx.Cause == "player_attack" || ctx.Cause == "mob_attack" || ctx.Cause == "projectile" || ctx.Cause == "explosion" {
		ctx.Armor = s.attributes(p)[api.Armor]
		amount *= 1 - min(20, ctx.Armor)*.04
	}
	amount = s.protectionScale(p, amount, ctx)
	amount = s.scaleDamage(p, amount, ctx)
	if amount <= 0 {
		return false
	}
	ctx.Amount = amount
	if p.state.Ext != nil {
		ctx.AbsorptionBefore = p.state.Ext.Absorb
		ctx.AbsorptionAfter = ctx.AbsorptionBefore
	}
	e := s.emit(api.PlayerDamaging, p.room, p, damageData(ctx, reason))
	amount = e.Number("amount")
	if e.Cancelled || !number(amount, 0, 1000) || amount == 0 {
		return false
	}
	ctx.Amount = amount
	absorbed := 0.0
	if p.state.Ext != nil {
		ctx.AbsorptionBefore = p.state.Ext.Absorb
		absorbed = math.Min(ctx.AbsorptionBefore, amount)
	}
	actual := amount - absorbed
	var death *api.Event
	if actual >= p.state.Health {
		data := damageData(ctx, reason)
		data["message"] = p.name + reason
		death = s.emit(api.PlayerDying, p.room, p, data)
		if death.Cancelled {
			return false
		}
	}
	// Coupled actions (such as drinking) commit only after damage and death
	// protection succeeds, before death processes the resulting inventory.
	if len(commit) > 0 && !commit[0]() {
		return false
	}
	if p.state.Ext != nil {
		p.state.Ext.Absorb -= absorbed
		ctx.AbsorptionAfter = p.state.Ext.Absorb
	}
	ctx.AbsorbedAmount = absorbed
	p.state.Health = max(0, p.state.Health-actual)
	ctx.HealthAfter = p.state.Health
	ctx.AppliedAmount = ctx.HealthBefore - ctx.HealthAfter
	s.emit(api.PlayerDamaged, p.room, p, damageData(ctx, reason))
	if ctx == nil || ctx.Cause != "player_attack" {
		p.json(map[string]any{"t": "hurt", "f": true})
		if ctx != nil && ctx.AppliedAmount > 0 {
			broadcastWorld(p.room, map[string]any{
				"t":          "dmg",
				"targetType": "player",
				"id":         p.id,
				"amount":     ctx.AppliedAmount,
				"crit":       false,
				"x":          p.state.X,
				"y":          p.state.Y + 2.1,
				"z":          p.state.Z,
			}, nil)
		}
	}
	if p.state.Health <= 0 {
		p.state.Dead = true
		p.mining = nil
		s.closeWindow(p)
		s.onDeathExt(p)
		s.deathMessage(p)
		message := limitText(death.String("message"), 240)
		broadcast(p.room, map[string]any{"t": "dead", "id": p.id, "m": message}, p)
		broadcastWorld(p.room, map[string]any{"t": "pdead", "id": p.id, "m": message}, nil)
		data := damageData(ctx, reason)
		data["message"] = message
		s.emit(api.PlayerDead, p.room, p, data)
	}
	p.room.Dirty = true
	s.health(p)
	return true
}
