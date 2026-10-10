package game

import (
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 动物喂食繁殖与剪羊毛：
// 1. 喂养繁殖：
//    - 牛/羊：小麦 (Wheat 336)
//    - 猪：胡萝卜 (Carrot 350)
//    - 鸡：小麦种子 (Wheat Seeds 335)
//    - 消耗对应食物，生成幼崽 (Mob.Flags 1=BABY)
// 2. 剪羊毛：
//    - 羊 + 剪刀 (Shears 304)：设置 SHEARED 标记 (Flags |= 2)，掉落 1-3 个白羊毛 (White Wool 47)
// ---------------------------------------------------------------------------

const (
	mobFlagBaby    byte = 1
	mobFlagSheared byte = 2
	itemWhiteWool       = 47
)

func init() {
	onMessage("animal", func(s *Server, p *Peer, m Message) bool {
		r := p.room
		if r.Mode != "survival" {
			return true
		}
		mob := r.Mobs[m.E]
		if mob == nil || mob.Health <= 0 {
			return true
		}
		held := s.heldItem(p)
		if held == nil {
			return true
		}

		// Both player and dispenser shearing prepare protection, drops and wear
		// before the sheep or either inventory is changed.
		if mob.Type == "sheep" && held.ID == 304 {
			s.shearAnimal(p, mob)
			return true
		}

		// 2. 喂食繁殖
		canBreed := false
		switch mob.Type {
		case "cow", "sheep":
			canBreed = held.ID == itemWheat
		case "pig":
			canBreed = held.ID == itemCarrot
		case "chicken":
			canBreed = held.ID == itemWheatSeeds
		}

		if canBreed && (mob.Flags&mobFlagBaby == 0) {
			if !s.allowed(api.AnimalBreeding, r, p, map[string]any{"mob_id": mob.ID, "mob_type": mob.Type, "item": itemSnapshot(held)}) {
				return true
			}
			s.consumeHeld(p, 1)
			s.mobEvent(r, mob, 10, 0) // EAT 动作
			soundAt(r, "entity."+mob.Type+".ambient", mob.X, mob.Y, mob.Z, nil)

			// 几率生成幼崽
			var babyID uint32
			if baby, err := s.spawnMob(r, p, mob.Type, mob.X+(s.rng.Float64()-.5), mob.Y, mob.Z+(s.rng.Float64()-.5), "breed"); err == nil {
				baby.Flags |= mobFlagBaby
				babyID = baby.ID
				s.mobEvent(r, baby, 4, uint16(baby.Flags))
				s.spawnOrbs(r, mob.X, mob.Y+.5, mob.Z, 1+s.rng.Intn(5))
			}
			s.inventory(p, 0, 0)
			s.emit(api.AnimalBred, r, p, map[string]any{
				"mob_id":   mob.ID,
				"mob_type": mob.Type,
				"baby_id":  babyID,
			})
			return true
		}

		return true
	})
}
