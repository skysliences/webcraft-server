package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 火焰、打火石与 TNT 系统：
// 1. 打火石 (Flint and Steel 305)：在目标方块对应面放置火焰 (Fire 95)，消耗耐久
// 2. 灭火：挖掘开始阶段(dig s=0)探测相邻火焰，若存在则同步移除并播放灭火声
// 3. 火焰蔓延与自然熄灭：
//    - 雨天室外火焰迅速熄灭
//    - 火焰向相邻可燃方块蔓延（木板、树叶、木栅栏、羊毛、书架、TNT）
// 4. TNT (27)：
//    - 受火焰、打火石右键或红石充能激活
//    - 延时 4 秒（40 个 100ms 刻）爆炸
//    - 爆炸范围 4 格：摧毁非坚固方块，对周围玩家/生物造成伤害与击退，连锁引爆相邻 TNT
// ---------------------------------------------------------------------------

const (
	itemFlintAndSteel = 305
	blkFlintFire      = 95
	blkExplosiveTNT   = 27
)

var flammability = map[int]int{
	blkOakLeaves:    60,
	blkBirchLeaves:  60,
	blkSpruceLeaves: 60,
	blkOakLog:       5,
	blkBirchLog:     5,
	blkSpruceLog:    5,
	blkExplosiveTNT: 100,
}

func (s *Server) isFlammable(b int) bool {
	if flammability[b] > 0 {
		return true
	}
	name := s.fullName(b)
	return name == "Oak Planks" || name == "Bookshelf" || name == "White Wool" ||
		name == "Oak Fence" || name == "Oak Fence Gate" || name == "Short Grass"
}

// 引爆 TNT
func (s *Server) igniteTNT(r *Room, x, y, z int) {
	s.primeTNT(r, nil, x, y, z, defaultTNTFuse, "redstone", nil)
}

// 执行通用爆炸逻辑
func (s *Server) explode(r *Room, ex, ey, ez float64, radius float64, breakBlocks bool, sources ...[3]int) bool {
	return s.explodeAttributed(r, ex, ey, ez, radius, breakBlocks, nil, sources...)
}
func (s *Server) explodeAttributed(r *Room, ex, ey, ez float64, radius float64, breakBlocks bool, origin *TNTState, sources ...[3]int) bool {
	previousSource := s.pluginSource
	if origin != nil {
		s.pluginSource = origin.Source
		defer func() { s.pluginSource = previousSource }()
	} else {
		origin = s.ignitionOrigin(nil, "explosion")
	}
	data := map[string]any{"x": ex, "y": ey, "z": ez, "radius": radius, "breakBlocks": breakBlocks, "reason": "explosion", "actor": origin.Actor, "source": origin.Source, "stack": origin.Stack.Clone(), "before": origin.Stack.Clone(), "after": origin.Stack.Clone(), "attacker": cloneTNTState(origin).Attacker, "target": map[string]any{"kind": "explosion", "x": ex, "y": ey, "z": ez}, "ignitionReason": origin.Reason}
	e := s.emit(api.ExplosionCreating, r, nil, data)
	n := e.Number("radius")
	if e.Cancelled || !number(n, .1, 16) {
		return false
	}
	radius = n
	data["radius"] = radius
	if len(sources) > 0 {
		at := sources[0]
		k := blockKey(at[0], at[1], at[2])
		if !s.setBlocksWithCommit(r, [][4]int{{at[0], at[1], at[2], 0}}, setQuiet, "tnt_explosion", func() { delete(r.blockExt().Blocks, k) }) {
			return false
		}
		s.broadcastTNT(r, k, false, origin)
	}
	defer s.emit(api.ExplosionCreated, r, nil, data)
	s.tntSound(r, "entity.generic.explode", ex, ey, ez)

	// 1. 伤害周围玩家与生物
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) {
			continue
		}
		dist := math.Hypot(p.state.X-ex, p.state.Z-ez)
		dist = math.Hypot(dist, p.state.Y-ey)
		if dist <= radius {
			dmg := math.Max(1, (1-dist/radius)*radius*4)
			s.damagePlayer(p, dmg, "爆炸了。", &api.DamageContext{Cause: "explosion", Actor: origin.Actor, Source: origin.Source, Attacker: cloneTNTState(origin).Attacker, Weapon: origin.Stack.Clone()})
		}
	}

	for _, m := range r.Mobs {
		dist := math.Hypot(m.X-ex, m.Z-ez)
		dist = math.Hypot(dist, m.Y-ey)
		if dist <= radius {
			dmg := math.Max(1, (1-dist/radius)*radius*4)
			s.damageMobSource(r, nil, m, dmg, "explosion", &api.DamageContext{Cause: "explosion", Actor: origin.Actor, Source: origin.Source, Attacker: cloneTNTState(origin).Attacker, Weapon: origin.Stack.Clone()})
		}
	}

	if !breakBlocks {
		return true
	}

	// 2. 摧毁周围非防爆方块
	ir := int(math.Ceil(radius))
	ix, iy, iz := int(math.Floor(ex)), int(math.Floor(ey)), int(math.Floor(ez))
	for dx := -ir; dx <= ir; dx++ {
		for dz := -ir; dz <= ir; dz++ {
			for dy := -ir; dy <= ir; dy++ {
				d := math.Sqrt(float64(dx*dx + dy*dy + dz*dz))
				if d > radius {
					continue
				}
				bx, by, bz := ix+dx, iy+dy, iz+dz
				if !r.inside(bx, bz) || by <= 0 || by >= 127 {
					continue
				}
				b := s.block(r, bx, by, bz)
				if b == 0 || b == blkBedrock || b == blkObsidian {
					continue
				}
				if b == blkExplosiveTNT {
					// 连锁引爆
					s.primeTNT(r, nil, bx, by, bz, 10, "explosion_chain", origin)
					continue
				}
				// 破坏并产生掉落物 (几率掉落)
				var drops Stacks
				if s.gameRule(r, "doTileDrops") && s.rng.Float64() < .3 {
					drops = s.drops(b, 0)
				}
				s.explosionBlock(r, bx, by, bz, b, drops, origin)
			}
		}
	}
	return true
}

func init() {
	// 1. 打火石放置火焰与点燃 TNT (onPlace)
	onPlace(4, func(s *Server, p *Peer, m Message, target int) bool {
		r := p.room
		if r.Mode != "survival" {
			return false
		}
		v := s.heldItem(p)
		if v == nil || v.ID != itemFlintAndSteel {
			return false
		}
		if p.state.Dead || !s.canBuild(p) || !s.reachable(p, m) {
			return true
		}

		// 直接点燃 TNT
		if target == blkExplosiveTNT {
			s.primeTNT(r, p, m.X, m.Y, m.Z, defaultTNTFuse, "flint_and_steel", nil)
			return true
		}

		// 在朝向面上放置火方块
		offsets := faceOffsets
		at := offsets[m.F]
		x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
		if !r.inside(x, z) || y < 1 || y >= 127 {
			return true
		}
		if s.block(r, x, y, z) == 0 {
			if !s.setBlock(r, x, y, z, blkFlintFire, 0) {
				return true
			}
			s.inheritFire(r, blockKey(x, y, z), s.ignitionOrigin(p, "flint_and_steel"))
			soundAt(r, "item.flintandsteel.use", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
			s.wear(p, 1)
			s.inventory(p, 0, 0)
			return true
		}

		return false
	})

	// 2. 挖掘开始阶段灭火 (onDigStart)
	onDigStart(func(s *Server, p *Peer, m Message) bool {
		r := p.room
		offsets := faceOffsets
		at := offsets[m.F]
		x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
		if r.inside(x, z) && y >= 0 && y < 128 {
			if s.block(r, x, y, z) == blkFlintFire {
				if !s.setBlock(r, x, y, z, 0, 0) {
					return true
				}
				soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				return true
			}
		}
		return false
	})

	// 3. 红石激活 TNT (onBlockUpdate)
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if r.Mode != "survival" {
			return
		}
		if b == blkExplosiveTNT {
			if s.primedTNT(r, blockKey(x, y, z)) {
				return
			}
			if s.blockPowered(r, x, y, z) {
				s.igniteTNT(r, x, y, z)
				return
			}
			for _, off := range faceOffsets {
				nx, ny, nz := x+off[0], y+off[1], z+off[2]
				if s.block(r, nx, ny, nz) == blkFlintFire {
					s.primeTNT(r, nil, x, y, z, defaultTNTFuse, "fire", s.fireOrigin(r, blockKey(nx, ny, nz)))
					break
				}
			}
			return
		}

		// 火焰自然熄灭与蔓延
		if b == blkFlintFire {
			origin := s.fireOrigin(r, blockKey(x, y, z))
			for _, off := range faceOffsets {
				nx, ny, nz := x+off[0], y+off[1], z+off[2]
				if s.block(r, nx, ny, nz) == blkExplosiveTNT {
					s.primeTNT(r, nil, nx, ny, nz, defaultTNTFuse, "fire", origin)
				}
			}
			// 下雨天熄灭室外火焰
			if s.raining(r) && s.highestBlock(r, x, z) <= y {
				s.setBlock(r, x, y, z, 0, 0)
				soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				return
			}
			// 随机蔓延与燃烧下方方块
			if s.rng.Intn(4) == 0 {
				below := s.block(r, x, y-1, z)
				if s.isFlammable(below) && below != blkExplosiveTNT && s.rng.Intn(3) == 0 {
					if s.setBlock(r, x, y-1, z, blkFlintFire, 0) {
						s.inheritFire(r, blockKey(x, y-1, z), origin)
					}
				}
				// 蔓延到周围
				for _, d := range faceOffsets {
					nx, ny, nz := x+d[0], y+d[1], z+d[2]
					if r.inside(nx, nz) && ny > 0 && ny < 127 {
						nb := s.block(r, nx, ny, nz)
						if nb == blkExplosiveTNT {
							s.primeTNT(r, nil, nx, ny, nz, defaultTNTFuse, "fire", origin)
						} else if s.isFlammable(nb) && s.block(r, nx, ny+1, nz) == 0 {
							if s.setBlock(r, nx, ny+1, nz, blkFlintFire, 0) {
								s.inheritFire(r, blockKey(nx, ny+1, nz), origin)
							}
						}
					}
				}
			}
			// 火焰本身随时间自然烧尽
			if s.rng.Intn(10) == 0 {
				s.setBlock(r, x, y, z, 0, 0)
			}
		}
	})
}
