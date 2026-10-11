package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 高级下界生物、潜影盒背包扩容、凋灵 Boss 与信标系统 (advanced_mobs_and_features.go)
// 1. 烈焰人 (Blaze) & 凋灵骷髅 (Wither Skeleton) AI 与生态
// 2. 潜影贝 (Shulker) & 潜影盒 (Shulker Box) 27 格便携背包序列化与还原
// 3. 凋灵 Boss (Wither) 灵魂沙T型召唤、阶段状态机、凋灵之首弹射物与下界之星
// 4. 信标金字塔 (Beacon Pyramid) 矿石底座检测与全图光环 Buff
// ---------------------------------------------------------------------------

func init() {
	// 挂载方块放置后的特殊方块处理 (潜影盒与凋灵召唤)
	onAfterPlace(func(s *Server, p *Peer, x, y, z, placed int, m Message) {
		name := s.nameOf(placed)
		r := p.room
		key := blockKey(x, y, z)

		// A. 潜影盒放置：恢复内部 27 格物品
		if name == "Shulker Box" {
			held := m.PlacementStack
			if held == nil {
				held = s.heldItem(p)
			} // Internal legacy hook callers.
			if c, ok := s.portableBox(held); ok {
				r.Containers[key] = c
			}
			return
		}

		// B. 凋灵骷髅头颅放置：检测是否摆放成灵魂沙 T 型结构召唤凋灵 Boss
		if name == "Wither Skeleton Skull" || placed == 178 {
			s.checkWitherSpawn(r, p, x, y, z, m.PlacementStack)
		}
	})

	// 挂载房间循环：高级生物 AI 与信标检测
	onRoomTick(func(s *Server, r *Room) {
		if r.Mode != "survival" {
			return
		}

		// 1. 信标金字塔检测与光环赋予 (每 2 秒检测一次)
		if s.tick%20 == 0 {
			s.tickBeacons(r)
		}

		// 2. 下界要塞刷怪笼定期生成烈焰人 (每 10 秒)
		if s.tick%100 == 0 && r.dimName() == "nether" {
			s.tickNetherSpawners(r)
		}

		// 3. 高级生物 AI 更新
		s.tickAdvancedMobs(r)
	})
}

// ---------------------------------------------------------------------------
// 1. 凋灵 Boss 召唤检测 (Wither Spawning)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 2. 下界要塞刷怪笼 (Blaze Spawner)
// ---------------------------------------------------------------------------

func (s *Server) tickNetherSpawners(r *Room) {
	// 扫描已加载编辑中的刷怪笼方块 188
	for key, b := range r.Edits {
		if b != 188 {
			continue
		}
		x, y, z := coords(key)
		// 附近必须有玩家
		hasPlayer := false
		for p := range r.Peers {
			if !s.spawningPlayer(p) {
				continue
			}
			if math.Sqrt(math.Pow(p.state.X-float64(x)-.5, 2)+math.Pow(p.state.Y-float64(y)-.5, 2)+math.Pow(p.state.Z-float64(z)-.5, 2)) < 16 {
				hasPlayer = true
				break
			}
		}
		if !hasPlayer {
			continue
		}
		// 生成 1~2 只烈焰人
		count := 0
		for _, m := range r.Mobs {
			if m.Type == "blaze" && math.Hypot(m.X-float64(x), m.Z-float64(z)) < 16 {
				count++
			}
		}
		if count < 4 {
			sx := float64(x) + (s.rng.Float64()-.5)*4
			sy := float64(y) + 1.0
			sz := float64(z) + (s.rng.Float64()-.5)*4
			_, _ = s.spawnMob(r, nil, "blaze", sx, sy, sz, "spawner")
		}
	}
}

// ---------------------------------------------------------------------------
// 3. 高级生物 AI 更新 (烈焰人、凋灵骷髅、潜影贝、凋灵 Boss)
// ---------------------------------------------------------------------------

func (s *Server) tickAdvancedMobs(r *Room) {
	for _, id := range sortedMobIDs(r) {
		m := r.Mobs[id]
		if m == nil || m.Health <= 0 || !s.mobActive(r, m) {
			continue
		}
		ext := getMobExt(m.ID)

		// 寻找最近玩家
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

		// A. 烈焰人 (Blaze)
		if m.Type == "blaze" {
			// 水/雨受伤害
			bx, by, bz := int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
			if isWater(s.block(r, bx, by, bz)) || (s.raining(r) && s.highestBlock(r, bx, bz) <= by) {
				s.damageMob(r, nil, m, 2, "water")
				continue
			}
			if target != nil && minDist <= 20 {
				m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
				// 悬浮在目标上方 1.5~2.5 格
				targetY := target.state.Y + 2.0
				if m.Y < targetY {
					m.Y += 0.15
				} else if m.Y > targetY+1.0 {
					m.Y -= 0.1
				}

				// 拉扯距离
				if minDist < 6 {
					s.advancedMobWalk(r, m, m.X-(target.state.X-m.X), m.Y, m.Z-(target.state.Z-m.Z), 0.12)
				} else if minDist > 12 {
					s.advancedMobWalk(r, m, target.state.X, m.Y, target.state.Z, 0.15)
				}

				ext.BlazeCooldown = max(0, ext.BlazeCooldown-1)
				// 发射三连发火球
				if ext.BlazeCooldown == 0 && ext.BlazeBursts == 0 {
					ext.BlazeBursts = 3
					ext.BlazeCooldown = 40 // 4 秒一轮
				}
				if ext.BlazeBursts > 0 && s.tick%5 == 0 {
					ext.BlazeBursts--
					// 发射 small_fireball
					dirX := target.state.X - m.X
					dirY := (target.state.Y + 1.0) - (m.Y + 1.0)
					dirZ := target.state.Z - m.Z
					dLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
					if dLen > 0.001 {
						speed := 1.4
						pr := &Projectile{
							Type:    projSmallFireball,
							Damage:  5.0,
							Gravity: 0.0,
							Drag:    1.0,
							Shooter: mobTarget(m),
						}
						pr.Kind = 20 // ENT_KIND: small_fireball
						pr.X, pr.Y, pr.Z = m.X, m.Y+1.0, m.Z
						pr.VX = (dirX / dLen) * speed
						pr.VY = (dirY / dLen) * speed
						pr.VZ = (dirZ / dLen) * speed
						s.spawnProjectile(r, pr)
						soundAt(r, "entity.blaze.shoot", m.X, m.Y+1.0, m.Z, nil)
					}
				}
			}
			continue
		}

		// B. 凋灵骷髅 (Wither Skeleton)
		if m.Type == "wither_skeleton" {
			if target != nil {
				m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
				s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.22)
				// 攻击范围 1.8 格
				if minDist < 1.8 && math.Abs(target.state.Y-m.Y) < 1.8 && m.attackMS <= 0 {
					s.damagePlayer(target, 4.0, "被凋灵骷髅刺杀了。", &api.DamageContext{
						Cause:    "entity_attack",
						Attacker: mobTarget(m),
					})
					s.addEffect(target, "wither", 200, 0) // 10秒凋灵效果
					soundAt(r, "entity.wither_skeleton.step", m.X, m.Y, m.Z, nil)
					m.attackMS = 1500 // 1.5 秒攻速
				}
			}
			continue
		}

		// C. 潜影贝 (Shulker)
		if m.Type == "shulker" {
			ext.ShulkerTimer++
			// 每 4 秒开合一次外壳
			if ext.ShulkerTimer >= 40 {
				ext.ShulkerTimer = 0
				ext.ShulkerOpen = !ext.ShulkerOpen
				if ext.ShulkerOpen && target != nil && minDist <= 24 {
					// 张开外壳并发射追踪飞弹
					dirX := target.state.X - m.X
					dirY := (target.state.Y + 0.8) - (m.Y + 0.5)
					dirZ := target.state.Z - m.Z
					dLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
					if dLen > 0.001 {
						speed := 0.75
						pr := &Projectile{
							Type:    projShulkerBullet,
							Damage:  4.0,
							Gravity: 0.0,
							Drag:    1.0,
							Shooter: mobTarget(m),
						}
						pr.Kind = 28 // ENT_KIND: shulker_bullet
						pr.X, pr.Y, pr.Z = m.X, m.Y+0.5, m.Z
						pr.VX = (dirX / dLen) * speed
						pr.VY = (dirY / dLen) * speed
						pr.VZ = (dirZ / dLen) * speed
						s.spawnProjectile(r, pr)
						soundAt(r, "entity.shulker.shoot", m.X, m.Y+0.5, m.Z, nil)
					}
				}
			}
			continue
		}

		// D. 凋灵 Boss (The Wither)
		if m.Type == "wither" {
			// 1. 蓄力无敌充能
			if ext.ChargeTicks > 0 {
				ext.ChargeTicks--
				s.mobEvent(r, m, 1, 0)
				if ext.ChargeTicks == 0 {
					// 充能结束大爆炸
					s.explode(r, m.X, m.Y+1.5, m.Z, 5.0, true)
					soundAt(r, "entity.wither.ambient", m.X, m.Y+1.5, m.Z, nil)
				}
				continue
			}

			if target == nil {
				continue
			}

			m.Yaw = math.Atan2(-(target.state.X - m.X), -(target.state.Z - m.Z))
			ext.WitherAttack = max(0, ext.WitherAttack-1)

			// 2. 阶段二：半血狂暴 (Health <= 150)
			isEnraged := m.Health <= 150
			if isEnraged {
				// 降至低空贴身冲撞
				targetY := target.state.Y + 0.5
				if m.Y > targetY {
					m.Y -= 0.15
				}
				s.advancedMobWalk(r, m, target.state.X, target.state.Y, target.state.Z, 0.28)
				if minDist < 2.0 && m.attackMS <= 0 {
					s.damagePlayer(target, 10.0, "被狂暴的凋灵撕碎了。", &api.DamageContext{
						Cause:    "entity_attack",
						Attacker: mobTarget(m),
					})
					s.addEffect(target, "wither", 200, 1)
					m.attackMS = 1000
				}
			} else {
				// 阶段一：高空盘旋飞行
				targetY := target.state.Y + 4.5
				if m.Y < targetY {
					m.Y += 0.2
				} else if m.Y > targetY+1.0 {
					m.Y -= 0.1
				}
				// 绕圈盘旋
				angle := float64(s.tick) * 0.05
				orbitX := target.state.X + math.Cos(angle)*10
				orbitZ := target.state.Z + math.Sin(angle)*10
				s.advancedMobWalk(r, m, orbitX, m.Y, orbitZ, 0.20)
			}

			// 发射凋灵之首炸弹 (Wither Skull)
			if ext.WitherAttack == 0 && minDist <= 32 {
				dirX := target.state.X - m.X
				dirY := (target.state.Y + 0.8) - (m.Y + 1.8)
				dirZ := target.state.Z - m.Z
				dLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
				if dLen > 0.001 {
					speed := 1.6
					pr := &Projectile{
						Type:    projWitherSkull,
						Damage:  8.0,
						Gravity: 0.0,
						Drag:    1.0,
						Shooter: mobTarget(m),
					}
					pr.Kind = 30 // ENT_KIND: wither_skull
					pr.X, pr.Y, pr.Z = m.X, m.Y+1.8, m.Z
					pr.VX = (dirX / dLen) * speed
					pr.VY = (dirY / dLen) * speed
					pr.VZ = (dirZ / dLen) * speed
					s.spawnProjectile(r, pr)
					soundAt(r, "entity.wither.shoot", m.X, m.Y+1.8, m.Z, nil)
				}
				ext.WitherAttack = 25 // 2.5 秒一发
				if isEnraged {
					ext.WitherAttack = 15 // 狂暴时 1.5 秒一发
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 4. 信标金字塔与光环效果 (Beacon Pyramid & Buffs)
// ---------------------------------------------------------------------------

// 检查方块是否为信标矿石底座有效方块 (铁块 35、金块 36、钻石块 37、绿宝石块 38)
func isMineralBlock(b int) bool {
	return b == 35 || b == 36 || b == 37 || b == 38
}

func (s *Server) tickBeacons(r *Room) {
	for key, b := range r.Edits {
		if b != 177 && s.nameOf(b) != "Beacon" {
			continue
		}
		x, y, z := coords(key)

		// 1. 顶部必须露天无遮挡
		if !s.skyOpen(r, x, y+1, z) {
			continue
		}

		// 2. 检测金字塔层数 (1~4 层)
		level := 0
		for l := 1; l <= 4; l++ {
			by := y - l
			if by < 1 {
				break
			}
			complete := true
			for ox := -l; ox <= l; ox++ {
				for oz := -l; oz <= l; oz++ {
					if !isMineralBlock(s.block(r, x+ox, by, z+oz)) {
						complete = false
						break
					}
				}
				if !complete {
					break
				}
			}
			if complete {
				level = l
			} else {
				break
			}
		}

		if level == 0 {
			continue
		}

		// 3. 为范围内的玩家赋予强效增益
		buffRange := float64(20 + level*10)
		buffTicks := 180 // 9 秒持续时间 (每 2 秒刷新一次)

		for p := range r.Peers {
			if p.state == nil || p.state.Dead || s.isSpectator(p) {
				continue
			}
			dist := math.Hypot(p.state.X-float64(x), p.state.Z-float64(z))
			if dist <= buffRange && math.Abs(p.state.Y-float64(y)) <= 64 {
				// Level 1: 速度与急迫
				s.addEffect(p, "speed", buffTicks, 0)
				s.addEffect(p, "haste", buffTicks, 0)

				// Level 2: 抗性提升与跳跃提升
				if level >= 2 {
					s.addEffect(p, "resistance", buffTicks, 0)
					s.addEffect(p, "jump_boost", buffTicks, 0)
				}
				// Level 3: 力量
				if level >= 3 {
					s.addEffect(p, "strength", buffTicks, 0)
				}
				// Level 4: 生命恢复与更强效果
				if level >= 4 {
					s.addEffect(p, "regeneration", buffTicks, 0)
					s.addEffect(p, "haste", buffTicks, 1) // 急迫 II
					s.addEffect(p, "speed", buffTicks, 1) // 速度 II
				}
			}
		}
	}
}
