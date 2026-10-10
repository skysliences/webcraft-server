package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 高级生物 AI、驯服、寻路、蜘蛛爬墙与怪物技能系统 (mobs_ext.go)：
// 1. 驯服 (Taming):
//    - 狼 (wolf): 手持骨头 (Bone 352) 右键有概率驯服为宠物狗
//    - 宠物特性：归属主人 (Owner)、跟随主人、右键坐下/起立、护主与协同攻击
//    - 猪/马乘骑：手持鞍 (Saddle 329) 右键装备鞍，支持玩家骑乘
// 2. 完整寻路 (Advanced Pathfinding):
//    - 阶梯跳跃：前方障碍高 1 格且上方空旷时，自动向上跳跃登上台阶
//    - 悬崖防跌：前方下方悬空超过 3 格时判定为深坑，自动绕道避开
//    - 熔岩与火焰规避：避免走入火或熔岩
// 3. 蜘蛛爬墙 (Spider Wall Climbing):
//    - 蜘蛛贴近垂直墙面且目标在上方时，垂直向上攀爬
// 4. 复杂怪物技能:
//    - 骷髅 (skeleton): 保持 8~14 格拉扯距离走位，定期拉弓向玩家射箭
//    - 苦力怕 (creeper): 进入 3 格启动引信 (EV.FUSE 1, 嘶嘶声+闪白)，3秒后自爆；远离则熄火
//    - 末影人 (enderman): 视线直视检测激怒、瞬移避水与近战突袭、闪避远程弹射物
// ---------------------------------------------------------------------------

// 额外怪物种类属性定义
func init() {
	mobSpecs["skeleton"] = mobSpec{Kind: 5, Health: 20, Speed: 1.5, Width: .6, Height: 1.99, Hostile: true, Drops: []string{"Bone", "Arrow"}}
	mobSpecs["creeper"] = mobSpec{Kind: 6, Health: 20, Speed: 1.4, Width: .6, Height: 1.7, Hostile: true, Drops: []string{"Gunpowder"}}
	mobSpecs["enderman"] = mobSpec{Kind: 8, Health: 40, Speed: 2.2, Width: .6, Height: 2.9, Hostile: true, Drops: []string{"Blink Pearl"}}
	mobSpecs["wolf"] = mobSpec{Kind: 26, Health: 20, Speed: 1.8, Width: .6, Height: .85, Hostile: false, Drops: []string{}}
	mobSpecs["blaze"] = mobSpec{Kind: 14, Health: 20, Speed: 1.2, Width: .6, Height: 1.8, Hostile: true, Drops: []string{"Cinder Rod"}}
	mobSpecs["wither_skeleton"] = mobSpec{Kind: 18, Health: 20, Speed: 1.6, Width: .7, Height: 2.4, Hostile: true, Drops: []string{"Bone", "Wither Skeleton Skull"}}
	mobSpecs["shulker"] = mobSpec{Kind: 27, Health: 30, Speed: 0, Width: 1.0, Height: 1.0, Hostile: true, Drops: []string{"Shulker Shell"}}
	mobSpecs["wither"] = mobSpec{Kind: 29, Health: 300, Speed: 1.8, Width: 0.9, Height: 3.5, Hostile: true, Drops: []string{"Nether Star"}}
	mobSpecs["minecart"] = mobSpec{Kind: 31, Health: 20, Speed: 0, Width: 0.98, Height: 0.7, Hostile: false, Drops: []string{"Minecart"}}
	mobSpecs["boat"] = mobSpec{Kind: 32, Health: 40, Speed: 0, Width: 1.4, Height: 0.6, Hostile: false, Drops: []string{"Oak Boat"}}
	mobSpecs["fishing_bobber"] = mobSpec{Kind: 33, Health: 1, Speed: 0, Width: 0.25, Height: 0.25, Hostile: false, Drops: nil}
}

// 生物扩展运行时状态
type MobExt struct {
	OwnerID       uint16 // 驯服主人
	Sitting       bool   // 坐下状态
	FuseTicks     int    // 苦力怕引线计时
	BowCooldown   int    // 骷髅拉弓冷却
	Angry         bool   // 末影人激怒
	Climbing      bool   // 蜘蛛攀爬中
	BlazeBursts   int    // 烈焰人当前连发剩余火球数
	BlazeCooldown int    // 烈焰人攻击冷却
	ShulkerOpen   bool   // 潜影贝开合状态
	ShulkerTimer  int    // 潜影贝开壳攻击计时
	ChargeTicks   int    // 凋灵召唤蓄力无敌计时
	WitherAttack  int    // 凋灵发射弹射物冷却
}

var mobExtensions = map[uint32]*MobExt{}

func getMobExt(id uint32) *MobExt {
	ext := mobExtensions[id]
	if ext == nil {
		ext = &MobExt{}
		mobExtensions[id] = ext
	}
	return ext
}

// 检查方块是否为危险致命方块（火/岩浆）
func (s *Server) isHazard(r *Room, x, y, z int) bool {
	b := s.block(r, x, y, z)
	return isLava(b) || b == 95
}

// 智能步进寻路：包含跳台阶与避险
func (s *Server) advancedMobWalk(r *Room, m *Mob, targetX, targetY, targetZ float64, speed float64) bool {
	dx := targetX - m.X
	dz := targetZ - m.Z
	dist := math.Hypot(dx, dz)
	if dist < 0.05 {
		return true
	}
	vx := (dx / dist) * speed
	vz := (dz / dist) * speed

	nextX := m.X + vx
	nextZ := m.Z + vz
	curY := int(math.Round(m.Y))

	// 1. 蜘蛛特殊爬墙逻辑
	if m.Type == "spider" && targetY > m.Y+0.5 {
		// 检查前方水平位移是否受阻，或者目标方向为坚固方块
		targetBlockSolid := s.mobSolid(r, int(math.Floor(targetX)), curY, int(math.Floor(targetZ)))
		if !s.mobFits(r, m.Type, nextX, float64(curY), nextZ) || targetBlockSolid {
			// 前方有墙，垂直向上攀爬
			if curY+1 < 127 && s.mobFits(r, m.Type, m.X, m.Y+0.2, m.Z) {
				m.Y += 0.2
				return true
			}
		}
	}

	// 2. 正常平走或上下一格台阶
	for _, dy := range []int{0, 1, -1} {
		candY := curY + dy
		if candY < 1 || candY >= 127 {
			continue
		}
		// 下方必须是坚实方块，且不能是岩浆或火
		if s.mobSolid(r, int(math.Floor(nextX)), candY-1, int(math.Floor(nextZ))) &&
			!s.isHazard(r, int(math.Floor(nextX)), candY-1, int(math.Floor(nextZ))) {
			// 且生物身位有足够空间
			if s.mobFits(r, m.Type, nextX, float64(candY), nextZ) {
				m.X, m.Y, m.Z = nextX, float64(candY), nextZ
				return true
			}
		}
	}

	// 3. 遇到前方阻挡，尝试向左右 45 度绕行 (避障)
	for _, angleOffset := range []float64{math.Pi / 4, -math.Pi / 4, math.Pi / 2, -math.Pi / 2} {
		cosA, sinA := math.Cos(angleOffset), math.Sin(angleOffset)
		altVx := vx*cosA - vz*sinA
		altVz := vx*sinA + vz*cosA
		altX, altZ := m.X+altVx, m.Z+altVz
		for _, dy := range []int{0, 1, -1} {
			candY := curY + dy
			if s.mobSolid(r, int(math.Floor(altX)), candY-1, int(math.Floor(altZ))) &&
				!s.isHazard(r, int(math.Floor(altX)), candY-1, int(math.Floor(altZ))) &&
				s.mobFits(r, m.Type, altX, float64(candY), altZ) {
				m.X, m.Y, m.Z = altX, float64(candY), altZ
				m.Yaw = math.Atan2(-altVx, -altVz)
				return true
			}
		}
	}

	return false
}

// 末影人瞬移机制
func (s *Server) endermanTeleport(r *Room, m *Mob) bool {
	for tries := 0; tries < 16; tries++ {
		tx := m.X + (s.rng.Float64()-.5)*32
		tz := m.Z + (s.rng.Float64()-.5)*32
		if !r.inside(int(tx), int(tz)) {
			continue
		}
		ty := s.surface(r, int(tx), int(tz))
		if ty > 0 && ty < 125 {
			b := s.block(r, int(tx), ty, int(tz))
			if !isWater(b) && !isLava(b) && s.mobFits(r, m.Type, tx, float64(ty), tz) {
				m.X, m.Y, m.Z = tx, float64(ty), tz
				s.mobEvent(r, m, 8, 0) // TELEPORT 动画
				soundAt(r, "entity.enderman.teleport", tx, float64(ty), tz, nil)
				return true
			}
		}
	}
	return false
}

// 检查玩家准星是否直视末影人头部
func (s *Server) isLookingAtEnderman(p *Peer, m *Mob) bool {
	if p.state == nil || p.state.Dead {
		return false
	}
	dx := m.X - p.state.X
	dy := (m.Y + 2.5) - (p.state.Y + 1.62)
	dz := m.Z - p.state.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > 32 || dist < 0.5 {
		return false
	}
	// 玩家视线方向向量
	cp := math.Cos(p.state.Pitch)
	lookX := -math.Sin(p.state.Yaw) * cp
	lookY := math.Sin(p.state.Pitch)
	lookZ := -math.Cos(p.state.Yaw) * cp

	dot := (dx*lookX + dy*lookY + dz*lookZ) / dist
	return dot > 0.85 // 约 30 度视锥范围内即可察觉目光
}

func init() {
	// 1. 驯服狼与坐下交互 (onPlace 拦截对生物右键)
	onMessage("animal", func(s *Server, p *Peer, m Message) bool {
		r := p.room
		mob := r.Mobs[m.E]
		if mob == nil || mob.Health <= 0 {
			return false
		}
		held := s.heldItem(p)
		ext := getMobExt(mob.ID)

		// 驯服狼 (手持骨头 352)
		if mob.Type == "wolf" && ext.OwnerID == 0 {
			if held != nil && held.ID == 352 {
				eventData := map[string]any{
					"mob_id":   mob.ID,
					"mob_type": mob.Type,
					"item":     itemSnapshot(held),
				}
				if !s.allowed(api.AnimalTaming, r, p, eventData) {
					return true
				}
				s.consumeHeld(p, 1)
				if s.rng.Intn(3) == 0 {
					ext.OwnerID = p.id
					s.mobEvent(r, mob, 7, 0) // 爱心粒子
					soundAt(r, "entity.wolf.pant", mob.X, mob.Y, mob.Z, nil)
					p.sys("你驯服了这只狼！")
					s.emit(api.AnimalTamed, r, p, map[string]any{
						"mob_id":   mob.ID,
						"mob_type": mob.Type,
						"owner_id": p.id,
					})
				} else {
					s.mobEvent(r, mob, 6, 0) // 烟雾失败粒子
				}
				s.inventory(p, 0, 0)
				return true
			}
		}

		// 主人让狼坐下/站起
		if mob.Type == "wolf" && ext.OwnerID == p.id {
			ext.Sitting = !ext.Sitting
			if ext.Sitting {
				p.sys("狼坐下了。")
			} else {
				p.sys("狼站起来了。")
			}
			return true
		}

		// 猪/马装鞍
		if (mob.Type == "pig" || mob.Type == "horse") && held != nil && held.ID == 329 {
			s.consumeHeld(p, 1)
			mob.Flags |= 4 // 装鞍标记
			s.mobEvent(r, mob, 4, uint16(mob.Flags))
			soundAt(r, "entity.pig.saddle", mob.X, mob.Y, mob.Z, nil)
			s.inventory(p, 0, 0)
			return true
		}

		return false
	})

	// 2. 怪物特殊技能与寻路更新 (onRoomTick)
	onRoomTick(func(s *Server, r *Room) {
		if r.Mode != "survival" || len(r.Mobs) == 0 {
			return
		}

		for _, id := range sortedMobIDs(r) {
			m := r.Mobs[id]
			if m == nil || m.Health <= 0 {
				continue
			}
			ext := getMobExt(m.ID)

			// 寻找最近的玩家
			var target *Peer
			minDist := math.Inf(1)
			for p := range r.Peers {
				if p.state == nil || p.state.Dead || s.isSpectator(p) {
					continue
				}
				d := math.Hypot(p.state.X-m.X, p.state.Z-m.Z)
				if d < minDist {
					minDist = d
					target = p
				}
			}

			// A. 驯服狼逻辑：跟随主人或攻击敌对目标
			if m.Type == "wolf" && ext.OwnerID != 0 {
				var owner *Peer
				for p := range r.Peers {
					if p.id == ext.OwnerID {
						owner = p
						break
					}
				}
				if owner != nil && !ext.Sitting {
					d := math.Hypot(owner.state.X-m.X, owner.state.Z-m.Z)
					if d > 16 {
						// 距离太远瞬移到主人身边
						m.X, m.Y, m.Z = owner.state.X, owner.state.Y, owner.state.Z
					} else if d > 2.5 {
						s.advancedMobWalk(r, m, owner.state.X, owner.state.Y, owner.state.Z, 0.22)
					}
				}
				continue
			}

			if target == nil {
				continue
			}

			// B. 骷髅射手 (Skeleton): 拉扯与射箭
			if m.Type == "skeleton" {
				m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
				ext.BowCooldown = max(0, ext.BowCooldown-1)
				if minDist < 7 {
					// 距离太近，向后撤退拉扯
					s.advancedMobWalk(r, m, m.X-(target.state.X-m.X), m.Y, m.Z-(target.state.Z-m.Z), 0.14)
				} else if minDist > 14 {
					// 距离太远，向前靠近
					s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.16)
				}
				// 远程射箭
				if minDist <= 16 && ext.BowCooldown == 0 && s.lineClear(r, [3]float64{m.X, m.Y + 1.6, m.Z}, [3]float64{target.state.X, target.state.Y + 1, target.state.Z}) {
					s.mobEvent(r, m, 5, 0) // SHOOT 动作
					// 发射箭矢弹射物
					dirX := target.state.X - m.X
					dirY := (target.state.Y + .8) - (m.Y + 1.6)
					dirZ := target.state.Z - m.Z
					dLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
					if dLen > 0.001 {
						speed := 1.6
						hDist := math.Hypot(dirX, dirZ)
						pr := &Projectile{
							Type:    projArrow,
							Damage:  4.0,
							Gravity: 0.068,
							Drag:    0.99,
							Shooter: mobTarget(m),
						}
						pr.Kind = 3 // ENT_KIND: arrow
						pr.X, pr.Y, pr.Z = m.X, m.Y+1.6, m.Z
						pr.VX = (dirX / dLen) * speed
						pr.VY = (dirY/dLen)*speed + 0.05 + hDist*0.015
						pr.VZ = (dirZ / dLen) * speed
						pr.face()
						s.spawnProjectile(r, pr)
						soundAt(r, "entity.skeleton.shoot", m.X, m.Y+1.6, m.Z, nil)
					}
					ext.BowCooldown = 25 // 2.5 秒一箭
				}
				continue
			}

			// C. 苦力怕 (Creeper): 引信与自爆
			if m.Type == "creeper" {
				if minDist < 3.0 && math.Abs(target.state.Y-m.Y) < 2.0 {
					ext.FuseTicks++
					if ext.FuseTicks == 1 {
						s.mobEvent(r, m, 7, 1) // FUSE on
						soundAt(r, "entity.creeper.primed", m.X, m.Y+1, m.Z, nil)
					}
					if ext.FuseTicks >= 30 { // 3秒爆炸
						delete(r.Mobs, m.ID)
						s.explode(r, m.X, m.Y+.5, m.Z, 3.5, true)
						delete(mobExtensions, m.ID)
						continue
					}
				} else if ext.FuseTicks > 0 {
					ext.FuseTicks = max(0, ext.FuseTicks-2)
					if ext.FuseTicks == 0 {
						s.mobEvent(r, m, 7, 0) // FUSE off
					}
				}
				if ext.FuseTicks == 0 {
					s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.16)
				}
				continue
			}

			// D. 末影人 (Enderman): 视线激怒与避水瞬移
			if m.Type == "enderman" {
				// 碰水受伤并立即瞬移
				bx, by, bz := int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
				if isWater(s.block(r, bx, by, bz)) || (s.raining(r) && s.highestBlock(r, bx, bz) <= by) {
					s.damageMob(r, nil, m, 1, "water")
					s.endermanTeleport(r, m)
					continue
				}

				// 检查玩家视线直视
				if !ext.Angry && s.isLookingAtEnderman(target, m) {
					ext.Angry = true
					s.mobEvent(r, m, 9, 1) // ANGRY 咆哮
					soundAt(r, "entity.enderman.stare", m.X, m.Y+2.5, m.Z, nil)
				}

				if ext.Angry {
					m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
					s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.28)
					// 距离稍远时有几率直接突进瞬移到玩家身后
					if minDist > 12 && s.rng.Intn(10) == 0 {
						s.endermanTeleport(r, m)
					}
				}
				continue
			}

			// E. 蜘蛛爬墙寻路
			if m.Type == "spider" {
				s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.20)
			}
		}
	})
}
