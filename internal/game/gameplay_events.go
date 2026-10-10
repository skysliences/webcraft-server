package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

func (s *Server) setDifficulty(r *Room, p *Peer, value, reason string) bool {
	if value != "peaceful" && value != "easy" && value != "normal" && value != "hard" || r.hardcore() && value != "hard" {
		return false
	}
	data := map[string]any{"previous": s.difficulty(r), "difficulty": value, "reason": reason}
	if !s.allowed(api.WorldDifficultyChanging, r, p, data) {
		return false
	}
	r.ext().Difficulty = value
	r.root().Dirty = true
	s.emit(api.WorldDifficultyChanged, r, p, data)
	return true
}
func (s *Server) setRule(r *Room, p *Peer, name string, value bool, reason string) bool {
	if _, ok := gameRuleDefaults[name]; !ok {
		return false
	}
	data := map[string]any{"rule": name, "previous": s.gameRule(r, name), "value": value, "reason": reason}
	if !s.allowed(api.WorldRuleChanging, r, p, data) {
		return false
	}
	e := r.ext()
	if e.Rules == nil {
		e.Rules = map[string]bool{}
	}
	e.Rules[name] = value
	r.root().Dirty = true
	s.emit(api.WorldRuleChanged, r, p, data)
	return true
}
func (s *Server) removeEffects(p *Peer, names []string, reason string, commit ...func() bool) bool {
	if p.state.Ext == nil {
		if len(commit) > 0 {
			return commit[0]()
		}
		return true
	}
	e := p.state.Ext
	var data []map[string]any
	for _, name := range names {
		fx := e.Effects[name]
		if fx == nil {
			continue
		}
		v := map[string]any{"effect": name, "ticks": fx.Ticks, "amplifier": fx.Amp, "reason": reason}
		if !s.allowed(api.PlayerEffectRemoving, p.room, p, v) {
			return false
		}
		data = append(data, v)
	}
	if len(commit) > 0 && !commit[0]() {
		return false
	}
	for _, v := range data {
		name := v["effect"].(string)
		delete(e.Effects, name)
		if name == "absorption" {
			e.Absorb = 0
		}
		s.emit(api.PlayerEffectRemoved, p.room, p, v)
	}
	p.room.Dirty = true
	s.health(p)
	return true
}
func (s *Server) clearEffects(p *Peer, reason string, commit ...func() bool) bool {
	var names []string
	if p.state.Ext != nil {
		for name := range p.state.Ext.Effects {
			names = append(names, name)
		}
	}
	return s.removeEffects(p, names, reason, commit...)
}

type experienceChange struct {
	Next      PlayerExt
	Data      map[string]any
	LevelData map[string]any
}

func (s *Server) prepareExperience(p *Peer, amount int, levels bool, reason string, contexts ...map[string]any) *experienceChange {
	if p == nil || p.state == nil || !p.room.usesSurvival() || amount < -1000000 || amount > 1000000 {
		return nil
	}
	old := p.ext()
	next := *old
	data := map[string]any{"amount": amount, "levelBefore": old.XPLevel, "pointsBefore": old.XPPoints, "totalBefore": old.XPTotal, "reason": reason}
	for _, context := range contexts {
		for key, value := range context {
			if _, protected := data[key]; !protected {
				data[key] = value
			}
		}
	}
	if levels {
		next.XPLevel = max(0, next.XPLevel-amount)
		next.XPPoints = min(next.XPPoints, xpForLevel(next.XPLevel)-1)
		if next.XPLevel == 0 {
			next.XPPoints = 0
		}
	} else {
		e := s.emit(api.PlayerExperienceChanging, p.room, p, data)
		n := e.Number("amount")
		if e.Cancelled || !number(n, -1000000, 1000000) || n != math.Trunc(n) {
			return nil
		}
		amount = int(n)
		data["amount"] = amount
		if amount > 0 {
			next.XPTotal += amount
		}
		next.XPPoints += amount
		for next.XPPoints >= xpForLevel(next.XPLevel) && next.XPLevel < 10000 {
			next.XPPoints -= xpForLevel(next.XPLevel)
			next.XPLevel++
		}
		for next.XPPoints < 0 {
			if next.XPLevel == 0 {
				next.XPPoints = 0
				break
			}
			next.XPLevel--
			next.XPPoints += xpForLevel(next.XPLevel)
		}
	}
	data["levelAfter"], data["pointsAfter"], data["totalAfter"] = next.XPLevel, next.XPPoints, next.XPTotal
	v := &experienceChange{Next: next, Data: data}
	if next.XPLevel != old.XPLevel {
		v.LevelData = map[string]any{"previous": old.XPLevel, "level": next.XPLevel, "reason": reason}
		for _, context := range contexts {
			for key, value := range context {
				if _, protected := v.LevelData[key]; !protected {
					v.LevelData[key] = value
				}
			}
		}
		if !s.allowed(api.PlayerLevelChanging, p.room, p, v.LevelData) {
			return nil
		}
	}
	return v
}
func (s *Server) commitExperience(p *Peer, c *experienceChange) {
	e := p.ext()
	e.XPLevel, e.XPPoints, e.XPTotal = c.Next.XPLevel, c.Next.XPPoints, c.Next.XPTotal
	p.room.Dirty = true
	s.sendXP(p)
	if c.LevelData != nil {
		s.emit(api.PlayerLevelChanged, p.room, p, c.LevelData)
	}
	if c.Data["reason"] != "enchant_cost" {
		s.emit(api.PlayerExperienceChanged, p.room, p, c.Data)
	}
}
func projectileData(pr *Projectile, reason string) map[string]any {
	var origin, attacker *api.Combatant
	if pr.Shooter != nil {
		first, second := *pr.Shooter, *pr.Shooter
		origin, attacker = &first, &second
	}
	return map[string]any{"entityId": pr.ID, "type": pr.Type, "x": pr.X, "y": pr.Y, "z": pr.Z, "vx": pr.VX, "vy": pr.VY, "vz": pr.VZ, "damage": pr.Damage, "actor": pr.Actor, "source": pr.Source, "origin": origin, "attacker": attacker, "target": map[string]any{"kind": "projectile", "entityId": pr.ID}, "stack": pr.Weapon.Clone(), "before": pr.Weapon.Clone(), "after": pr.Weapon.Clone(), "weapon": pr.Weapon.Clone(), "reason": reason}
}
