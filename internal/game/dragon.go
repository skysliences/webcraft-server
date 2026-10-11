package game

import (
	"math"
)

// ---------------------------------------------------------------------------
// Ender Dragon Boss Fight & Elytra Flight System (dragon.go)
// 1. End Crystals: Obsidian pillars topped with healing crystals, explode on hit
// 2. Ender Dragon AI: Circling, perching, diving charge, dragon breath, BossBar
// 3. Victory & Gateway: Dragon death XP shower, dragon egg, exit portal activation
// 4. Elytra Gliding & Firework Propulsion: Pitch-guided air gliding & rocket boost
// ---------------------------------------------------------------------------

type EndCrystal struct {
	ID    uint32  `json:"id"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Alive bool    `json:"alive"`
}

type DragonFight struct {
	Active       bool          `json:"active"`
	DragonID     uint32        `json:"dragonId"`
	Health       float64       `json:"health"`
	MaxHealth    float64       `json:"maxHealth"`
	X            float64       `json:"x"`
	Y            float64       `json:"y"`
	Z            float64       `json:"z"`
	Yaw          float64       `json:"yaw"`
	Pitch        float64       `json:"pitch"`
	VX           float64       `json:"vx"`
	VY           float64       `json:"vy"`
	VZ           float64       `json:"vz"`
	Angle        float64       `json:"angle"`
	State        string        `json:"state"` // "circle", "perch", "dive"
	StateTicks   int           `json:"stateTicks"`
	Crystals     []*EndCrystal `json:"crystals"`
	DyingTicks   int           `json:"dyingTicks"`
	Defeated     bool          `json:"defeated"`
}

var endFights = map[*Room]*DragonFight{}

func getDragonFight(r *Room) *DragonFight {
	df := endFights[r]
	if df == nil {
		df = &DragonFight{
			MaxHealth: 200,
			Health:    200,
			State:     "circle",
		}
		endFights[r] = df
	}
	return df
}

func init() {
	// Register ender_dragon and end_crystal mob specs
	mobSpecs["ender_dragon"] = mobSpec{
		Kind:    23,
		Health:  200,
		Speed:   2.5,
		Width:   12.0,
		Height:  5.0,
		Hostile: true,
		Drops:   []string{}, // 龙蛋由 buildExitPortal 生成于柱顶，不在空中作为普通掉落物析出
	}

	mobSpecs["end_crystal"] = mobSpec{
		Kind:    22,
		Health:  1,
		Speed:   0,
		Width:   0.8,
		Height:  1.0,
		Hostile: false,
		Drops:   []string{},
	}

	// Room tick hook for Dragon AI and Crystal Healing
	onRoomTick(func(s *Server, r *Room) {
		if r.dimName() != "end" {
			return
		}
		df := getDragonFight(r)
		if df.Defeated {
			return
		}

		// Initialize dragon and crystals if not active
		if !df.Active {
			s.initDragonFight(r, df)
		}

		s.tickDragonFight(r, df)
	})

	// Firework Rocket Elytra boost
	onMessage("elytra_boost", func(s *Server, p *Peer, m Message) bool {
		if p.state == nil || p.state.Dead {
			return true
		}
		inv := p.state.Inv
		// Check if wearing Elytra in chest slot 38
		chest := inv.Slots[38]
		if chest == nil || s.rules.byID[chest.ID].Key != "elytra" {
			return true
		}
		// Consume 1 firework rocket or gunpowder in hand
		held := s.heldItem(p)
		if held != nil && (s.rules.byID[held.ID].Key == "firework_rocket" || s.rules.byID[held.ID].Key == "gunpowder") {
			held.Count--
			if held.Count <= 0 {
				inv.Slots[inv.Selected] = nil
			}
			s.inventory(p, 0, 0)
			// Boost forward velocity
			p.json(map[string]any{
				"t":     "boost",
				"speed": 1.6,
			})
			soundAt(p.room, "entity.firework_rocket.launch", p.state.X, p.state.Y, p.state.Z, nil)
		}
		return true
	})
}

// Initializes the End fight: generates obsidian pillars with crystals and spawns dragon
func (s *Server) initDragonFight(r *Room, df *DragonFight) {
	df.Active = true
	df.MaxHealth = 200
	df.State = "circle"
	centerX := float64(r.width() / 2)
	centerZ := float64(r.width() / 2)
	df.X = centerX
	df.Y = 80
	df.Z = centerZ

	// 1. 严格检查并清理现存末影龙：末地世界中至多仅允许存在一条末影龙（优先保留玩家正在攻击的残血龙）
	var activeDragon *Mob
	for _, m := range r.Mobs {
		if m != nil && m.Type == "ender_dragon" {
			if activeDragon == nil || m.Health < activeDragon.Health || (m.Health == activeDragon.Health && m.ID < activeDragon.ID) {
				if activeDragon != nil {
					delete(r.Mobs, activeDragon.ID)
					r.Dirty = true
				}
				activeDragon = m
			} else {
				// 清除多余重复的末影龙
				delete(r.Mobs, m.ID)
				r.Dirty = true
			}
		}
	}

	if activeDragon != nil {
		df.DragonID = activeDragon.ID
		df.Health = activeDragon.Health
		df.X = activeDragon.X
		df.Y = activeDragon.Y
		df.Z = activeDragon.Z
	} else if !df.Defeated {
		dMob, _ := s.spawnMob(r, nil, "ender_dragon", centerX, 80, centerZ, "dragon_spawn")
		if dMob != nil {
			df.DragonID = dMob.ID
			df.Health = dMob.Health
		}
	}

	// 2. 检查或生成 10 根黑曜石柱与末影水晶（按柱子坐标去重，每根柱顶至多保留 1 个水晶）
	df.Crystals = nil
	for i := 0; i < 10; i++ {
		ang := float64(i) * (math.Pi * 2 / 10.0)
		px := int(centerX + math.Cos(ang)*42)
		pz := int(centerZ + math.Sin(ang)*42)
		height := 65 + (i%5)*6 // pillar height 65..90

		// 生成黑曜石柱
		for y := 50; y <= height; y++ {
			for dx := -2; dx <= 2; dx++ {
				for dz := -2; dz <= 2; dz++ {
					if dx*dx+dz*dz <= 5 {
						s.setBlock(r, px+dx, y, pz+dz, 24, setQuiet) // 24 = Obsidian
					}
				}
			}
		}
		// 基岩柱顶
		s.setBlock(r, px, height, pz, 12, setQuiet) // 12 = Bedrock

		topX := float64(px) + 0.5
		topY := float64(height) + 1.0
		topZ := float64(pz) + 0.5

		// 检查该柱顶是否已存在水晶，清理多余重复水晶
		var pillarCrystal *Mob
		for _, m := range r.Mobs {
			if m != nil && m.Type == "end_crystal" {
				if math.Hypot(m.X-topX, m.Z-topZ) < 2.0 && math.Abs(m.Y-topY) < 3.0 {
					if pillarCrystal == nil {
						pillarCrystal = m
					} else {
						delete(r.Mobs, m.ID)
						r.Dirty = true
					}
				}
			}
		}

		var cid uint32
		if pillarCrystal != nil {
			cid = pillarCrystal.ID
		} else if !df.Defeated {
			cMob, _ := s.spawnMob(r, nil, "end_crystal", topX, topY, topZ, "crystal_spawn")
			if cMob != nil {
				cid = cMob.ID
			}
		}

		if cid != 0 {
			df.Crystals = append(df.Crystals, &EndCrystal{
				ID:    cid,
				X:     topX,
				Y:     topY,
				Z:     topZ,
				Alive: true,
			})
		}
	}
}

// Ticks Ender Dragon AI and Crystal Healing
func (s *Server) tickDragonFight(r *Room, df *DragonFight) {
	centerX := float64(r.width() / 2)
	centerZ := float64(r.width() / 2)

	// 1. 严格保证末地全局至多仅有 1 条末影龙，清除任何多余的幽灵/重复龙
	var activeDragon *Mob
	for _, m := range r.Mobs {
		if m != nil && m.Type == "ender_dragon" {
			if activeDragon == nil {
				activeDragon = m
			} else if df.DragonID != 0 && m.ID == df.DragonID {
				delete(r.Mobs, activeDragon.ID)
				r.Dirty = true
				activeDragon = m
			} else if df.DragonID == 0 && (m.Health < activeDragon.Health || (m.Health == activeDragon.Health && m.ID < activeDragon.ID)) {
				delete(r.Mobs, activeDragon.ID)
				r.Dirty = true
				activeDragon = m
			} else {
				delete(r.Mobs, m.ID)
				r.Dirty = true
			}
		}
	}

	if df.Defeated {
		if activeDragon != nil {
			delete(r.Mobs, activeDragon.ID)
			r.Dirty = true
			for p := range r.Peers {
				s.syncMobsFor(p)
			}
		}
		return
	}

	if activeDragon != nil {
		df.DragonID = activeDragon.ID
	}

	dragonMob := r.Mobs[df.DragonID]
	if dragonMob != nil {
		if df.Health <= 0 || df.DyingTicks > 0 {
			dragonMob.Health = 0
		} else {
			dragonMob.X = df.X
			dragonMob.Y = df.Y
			dragonMob.Z = df.Z
			dragonMob.Yaw = df.Yaw
			df.Health = dragonMob.Health
		}
	} else if df.Active && !df.Defeated && df.Health > 0 && df.DyingTicks == 0 {
		// 严防实体脱钩：若血量尚未真正归零且未在死亡阶段，仅在没有任何末影龙时生成一条
		if activeDragon == nil {
			newMob, _ := s.spawnMob(r, nil, "ender_dragon", df.X, df.Y, df.Z, "dragon_respawn")
			if newMob != nil {
				newMob.Health = df.Health
				df.DragonID = newMob.ID
				dragonMob = newMob
			}
		}
	}

	// Broadcast Boss Bar to all players in End dimension
	for p := range r.Peers {
		p.json(map[string]any{
			"t":       "bossbar",
			"name":    "末影龙",
			"percent": math.Max(0, df.Health/df.MaxHealth),
			"visible": !df.Defeated,
		})
	}

	// 2. Dying sequence (死亡动画流：120 ticks = 6 秒)
	if df.Health <= 0 || df.DyingTicks > 0 {
		df.Health = 0
		df.DyingTicks++
		if df.DyingTicks == 1 {
			soundAt(r, "entity.ender_dragon.death", df.X, df.Y, df.Z, nil)
		}
		// 死亡时缓缓盘旋升向中央
		df.X += (centerX - df.X) * 0.05
		df.Y += (75.0 - df.Y) * 0.05
		df.Z += (centerZ - df.Z) * 0.05
		df.Yaw = math.Remainder(df.Yaw+0.05, math.Pi*2)
		if dragonMob != nil {
			dragonMob.X = df.X
			dragonMob.Y = df.Y
			dragonMob.Z = df.Z
			dragonMob.Yaw = df.Yaw
			dragonMob.Health = 0
		}
		// Spawn massive XP shower
		if df.DyingTicks%5 == 0 && df.DyingTicks <= 100 {
			s.spawnOrbs(r, df.X+(s.rng.Float64()-0.5)*8, df.Y+(s.rng.Float64()-0.5)*4, df.Z+(s.rng.Float64()-0.5)*8, 50)
		}
		if df.DyingTicks >= 120 {
			df.Defeated = true
			if dragonMob != nil {
				delete(r.Mobs, dragonMob.ID)
				for peer := range r.Peers {
					s.syncMobsFor(peer)
				}
			}
			// Activate Exit Portal and place Dragon Egg (210)
			portal := [3]int{int(centerX), 49, int(centerZ)}
			s.buildExitPortal(r, portal, true)

			// Spawn End Gateway to outer islands with Elytra
			s.setBlock(r, int(centerX)+35, 75, int(centerZ), blkPortalX, setQuiet)
			soundAt(r, "block.end_portal.spawn", centerX, 55, centerZ, nil)
		}
		return
	}

	// 1. Crystal Healing & Destruction check
	for _, c := range df.Crystals {
		if !c.Alive {
			continue
		}
		// Check if crystal mob died
		if c.ID != 0 && r.Mobs[c.ID] == nil {
			c.Alive = false
			s.explode(r, c.X, c.Y, c.Z, 3.0, true)
			// Damage dragon if close
			if math.Hypot(df.X-c.X, df.Z-c.Z) < 32 && dragonMob != nil {
				s.damageMob(r, nil, dragonMob, 10, "crystal_explosion")
				df.Health = dragonMob.Health
			}
			continue
		}

		dist := math.Hypot(df.X-c.X, df.Z-c.Z)
		if dist < 32 && dragonMob != nil {
			if dragonMob.Health < df.MaxHealth {
				dragonMob.Health = math.Min(df.MaxHealth, dragonMob.Health+0.15)
				df.Health = dragonMob.Health
			}
		}
	}

	// 2. Flight path state machine
	df.StateTicks++
	switch df.State {
	case "circle":
		df.Angle = math.Remainder(df.Angle+0.02, math.Pi*2)
		targetX := centerX + math.Cos(df.Angle)*45
		targetZ := centerZ + math.Sin(df.Angle)*45
		targetY := 75.0 + math.Sin(df.Angle*2)*8

		df.X += (targetX - df.X) * 0.05
		df.Y += (targetY - df.Y) * 0.05
		df.Z += (targetZ - df.Z) * 0.05
		df.Yaw = math.Remainder(df.Angle+math.Pi/2, math.Pi*2)

		// Periodically transition to dive charge (every 300 ticks)
		if df.StateTicks > 300 {
			df.State = "dive"
			df.StateTicks = 0
			soundAt(r, "entity.ender_dragon.growl", df.X, df.Y, df.Z, nil)
		}

	case "dive":
		// Find closest player to charge at
		var closest *Peer
		minD := 99999.0
		for p := range r.Peers {
			if p.state != nil && !p.state.Dead {
				d := math.Hypot(p.state.X-df.X, p.state.Z-df.Z)
				if d < minD {
					minD = d
					closest = p
				}
			}
		}
		if closest != nil {
			tx, ty, tz := closest.state.X, closest.state.Y+2, closest.state.Z
			dx, dz := tx-df.X, tz-df.Z
			df.X += dx * 0.08
			df.Y += (ty - df.Y) * 0.08
			df.Z += dz * 0.08
			df.Yaw = math.Remainder(math.Atan2(-dx, -dz), math.Pi*2)

			// Hit player if within 4 blocks
			if minD < 4 && math.Abs(df.Y-closest.state.Y) < 4 {
				s.damage(closest, 10, "受到了末影龙的撞击。")
				closest.state.Y += 1.5 // Knock player into air
				df.State = "circle"
				df.StateTicks = 0
			}
		}
		if df.StateTicks > 120 {
			df.State = "circle"
			df.StateTicks = 0
		}
	}
}
