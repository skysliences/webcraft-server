package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Off-Hand & Shield Blocking System (shield_offhand.go)
// - Off-hand slot: Slot 40 in player inventory
// - Quick swap: 'F' key swaps main-hand active slot and off-hand slot
// - Shield blocking: right-click举盾格挡，180°扇面格挡近战和远程伤害，耐久损耗，反弹箭矢与击退攻击者
// - Off-hand usage: 副手手持火把/方块时右键快捷放置，手持食物时快捷进食
// ---------------------------------------------------------------------------

const offhandSlot = 40

var peerBlocking = map[*Peer]bool{}

func init() {
	// 1. Handle shield blocking state update from client
	onMessage("block", func(s *Server, p *Peer, m Message) bool {
		blocking := m.M > 0 || m.State || m.V > 0
		peerBlocking[p] = blocking
		// Broadcast to other peers in room for visual shield raising animation
		s.broadcastOthers(p, map[string]any{
			"t":        "block",
			"id":       p.id,
			"blocking": blocking,
		})
		s.broadcastPeerEquipment(p)
		return true
	})

	// 2. Handle 'F' key off-hand swap
	onMessage("swap_offhand", func(s *Server, p *Peer, m Message) bool {
		if p.state == nil || p.state.Inv == nil || p.state.Dead {
			return true
		}
		inv := p.state.Inv
		sel := inv.Selected
		if sel < 0 || sel > 8 {
			return true
		}
		// Swap mainhand active slot with offhand slot 40
		mainItem := inv.Slots[sel]
		offItem := inv.Slots[offhandSlot]
		inv.Slots[sel] = offItem
		inv.Slots[offhandSlot] = mainItem

		p.room.Dirty = true
		s.inventory(p, 0, 0)
		s.broadcastPeerEquipment(p)
		soundAt(p.room, "item.armor.equip_generic", p.state.X, p.state.Y, p.state.Z, nil)
		return true
	})
}

// Reports whether peer has shield equipped in either main-hand or off-hand
func (s *Server) hasShieldEquipped(p *Peer) (bool, int) {
	if p == nil || p.state == nil || p.state.Inv == nil {
		return false, -1
	}
	inv := p.state.Inv
	if main := s.heldItem(p); main != nil {
		if s.rules.byID[main.ID].Key == "shield" {
			return true, inv.Selected
		}
	}
	if off := inv.Slots[offhandSlot]; off != nil {
		if s.rules.byID[off.ID].Key == "shield" {
			return true, offhandSlot
		}
	}
	return false, -1
}

// Checks if player successfully blocks incoming damage with shield
func (s *Server) isShieldBlocking(p *Peer, ctx *api.DamageContext) bool {
	if p == nil || p.state == nil || p.state.Inv == nil {
		return false
	}
	if !peerBlocking[p] {
		return false
	}
	hasShield, _ := s.hasShieldEquipped(p)
	if !hasShield {
		return false
	}
	// Cannot block non-directional environmental damage
	if ctx.Cause == "void" || ctx.Cause == "drowning" || ctx.Cause == "starvation" || ctx.Cause == "suffocation" {
		return false
	}
	// Directional 180-degree frontal arc check
	if ctx.Attacker != nil {
		dx := ctx.Attacker.X - p.state.X
		dz := ctx.Attacker.Z - p.state.Z
		dist := math.Hypot(dx, dz)
		if dist > 0.001 {
			dx /= dist
			dz /= dist
			// Player facing look vector: yaw 0 is looking towards +z
			lookX := math.Sin(p.state.Yaw)
			lookZ := math.Cos(p.state.Yaw)
			dot := dx*lookX + dz*lookZ
			if dot < 0 {
				return false // Attacker is behind the player
			}
		}
	}
	return true
}

// Applies shield block effects: 100% damage blocked, shield wear, attacker knockback, sound
func (s *Server) applyShieldBlock(p *Peer, ctx *api.DamageContext) {
	hasShield, slot := s.hasShieldEquipped(p)
	if !hasShield || slot < 0 {
		return
	}
	sh := p.state.Inv.Slots[slot]
	if sh != nil {
		// Wear shield durability
		wear := max(1, int(ctx.RawAmount))
		sh.Damage += wear
		if it := s.rules.byID[sh.ID]; it.Durability > 0 && sh.Damage >= it.Durability {
			p.state.Inv.Slots[slot] = nil
			soundAt(p.room, "item.shield.break", p.state.X, p.state.Y, p.state.Z, nil)
		} else {
			soundAt(p.room, "item.shield.block", p.state.X, p.state.Y, p.state.Z, nil)
		}
		s.inventory(p, 0, 0)
	}

	// Knockback attacker
	if ctx.Attacker != nil && ctx.Attacker.Kind == "entity" {
		if mob := p.room.Mobs[ctx.Attacker.EntityID]; mob != nil {
			mob.walkMS = 0
			// Push mob away in direction of player look
			mob.X += math.Sin(p.state.Yaw) * 0.8
			mob.Z += math.Cos(p.state.Yaw) * 0.8
			mob.fleeMS = 300
		}
	}
}

// Helper to broadcast a JSON message to all peers in room except self
func (s *Server) broadcastOthers(self *Peer, msg map[string]any) {
	for p := range self.room.Peers {
		if p != self {
			p.json(msg)
		}
	}
}
