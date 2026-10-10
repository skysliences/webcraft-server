package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"time"
)

func (s *Server) heldItem(p *Peer) *Stack {
	if p == nil || p.state == nil || p.state.Inv == nil {
		return nil
	}
	return p.state.Inv.Slots[p.state.Inv.Selected]
}
func (s *Server) attributes(p *Peer) map[string]float64 {
	base := map[string]float64{api.AttackDamage: 1, api.AttackSpeed: 1 / .6, api.Armor: 0, api.MaxHealth: 20}
	if p.state.Inv == nil {
		return base
	}
	if v := s.heldItem(p); v != nil {
		if a := s.rules.byID[v.ID].Attack; a != nil {
			base[api.AttackDamage] = a.Damage
		}
	}
	for _, v := range p.state.Inv.Slots[36:40] {
		if v != nil {
			if a := s.rules.byID[v.ID].Armor; a != nil {
				base[api.Armor] += float64(a.Points)
			}
		}
	}
	mods := map[string][]api.AttributeModifier{}
	seen := map[string]bool{}
	for _, slot := range []int{p.state.Inv.Selected, 40, 36, 37, 38, 39} {
		v := p.state.Inv.Slots[slot]
		if v == nil || v.Meta == nil {
			continue
		}
		active := map[int]string{40: "offhand", 36: "feet", 37: "legs", 38: "chest", 39: "head"}[slot]
		if active == "" {
			active = "mainhand"
		}
		for _, a := range v.Meta.Attributes {
			if a.Slot != active && a.Slot != "any" && !(a.Slot == "armor" && slot >= 36 && slot <= 39) {
				continue
			}
			key := a.Attribute + "/" + a.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			mods[a.Attribute] = append(mods[a.Attribute], a)
		}
	}
	for attr, list := range mods {
		v := base[attr]
		for _, a := range list {
			if a.Operation == api.Add {
				v += a.Amount
			}
		}
		withAdd := v
		for _, a := range list {
			if a.Operation == api.MultiplyBase {
				v += withAdd * a.Amount
			}
		}
		for _, a := range list {
			if a.Operation == api.MultiplyTotal {
				v *= 1 + a.Amount
			}
		}
		base[attr] = v
	}
	base[api.AttackDamage] += s.attackBonusExt(p)
	base[api.AttackDamage] = max(0, min(1000, base[api.AttackDamage]))
	base[api.AttackSpeed] = max(.1, min(20, base[api.AttackSpeed]))
	base[api.Armor] = max(0, min(30, base[api.Armor]))
	base[api.MaxHealth] = max(1, min(200, base[api.MaxHealth]))
	return base
}
func (s *Server) maxHealth(p *Peer) float64 { return s.attributes(p)[api.MaxHealth] }
func (s *Server) attackCooldown(p *Peer) time.Duration {
	return time.Duration(float64(time.Second) / s.attributes(p)[api.AttackSpeed])
}

func (s *Server) syncAttributes(p *Peer) {
	attrs := s.attributes(p)
	if p.state.Health > attrs[api.MaxHealth] {
		p.state.Health = attrs[api.MaxHealth]
		p.room.Dirty = true
	}
	if !reflect.DeepEqual(p.attributes, attrs) {
		before := p.attributes
		p.attributes = attrs
		s.emit(api.PlayerAttributesChanged, p.room, p, map[string]any{"before": before, "after": attrs})
	}
}
func (s *Server) heal(p *Peer, amount float64, reason string, commit ...func() bool) bool {
	if p == nil || p.state.Dead || !number(amount, 0, 1000) || amount == 0 || p.state.Health >= s.maxHealth(p) {
		return false
	}
	before := p.state.Health
	e := s.emit(api.PlayerHealing, p.room, p, map[string]any{"amount": amount, "reason": reason, "healthBefore": before, "maxHealth": s.maxHealth(p)})
	amount = e.Number("amount")
	if e.Cancelled || !number(amount, 0, 1000) || amount == 0 {
		return false
	}
	if len(commit) > 0 && !commit[0]() {
		return false
	}
	p.state.Health = math.Min(s.maxHealth(p), before+amount)
	p.room.Dirty = true
	s.emit(api.PlayerHealed, p.room, p, map[string]any{"amount": p.state.Health - before, "requestedAmount": amount, "reason": reason, "healthBefore": before, "healthAfter": p.state.Health})
	s.health(p)
	return true
}
