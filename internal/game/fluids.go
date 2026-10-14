package game

import (
	"math"
	api "mcraft/backend/pkg/plugin"
)// ---------------------------------------------------------------------------
// 流体动力学与桶交互：
// 1. 水(9)与岩浆(68)流动：水流动等级 96-103，岩浆 104-107
// 2. 流体下落与扩散（水 5 刻扩散一次，岩浆 30 刻扩散一次）
// 3. 水与岩浆接触反应：
//    - 水流遇岩浆源 -> 生成黑曜石 (Obsidian 24)
//    - 水流遇流动岩浆 -> 生成圆石 (Cobblestone 4)
//    - 岩浆流遇水 -> 生成石头 (Stone 1)
// 4. 桶交互：
//    - 空桶(306)对着水源/岩浆源右键 -> 获得水桶(307)/岩浆桶(308)，移除源方块
//    - 水桶/岩浆桶右键方块面 -> 放置水源/岩浆源，退还空桶（创造模式不消耗）
//    - 空桶对奶牛右键 -> 挤奶，获得牛奶桶(309)
// ---------------------------------------------------------------------------

const (
	blkWaterSource   = 9
	blkLavaSource    = 68
	blkWaterFlowMin  = 96
	blkWaterFlowMax  = 102
	blkWaterFalling  = 103
	blkLavaFlowMin   = 104
	blkLavaFlowMax   = 106
	blkLavaFalling   = 107
	blkCobblestone   = 4
	blkStone         = 1
)

func waterLevel(b int) int {
	if b == blkWaterSource {
		return 0
	}
	if b >= blkWaterFlowMin && b <= blkWaterFlowMax {
		return b - blkWaterFlowMin + 1
	}
	return -1
}

func lavaLevel(b int) int {
	if b == blkLavaSource {
		return 0
	}
	if b >= blkLavaFlowMin && b <= blkLavaFlowMax {
		return (b-blkLavaFlowMin+1) * 2
	}
	return -1
}

func (s *Server) fluidReaction(r *Room, x, y, z int) bool {
	b := s.block(r, x, y, z)
	if isWater(b) {
		for _, d := range faceOffsets {
			nb := s.block(r, x+d[0], y+d[1], z+d[2])
			if nb == blkLavaSource {
				s.setBlock(r, x+d[0], y+d[1], z+d[2], blkObsidian, 0)
				soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				return true
			} else if isLava(nb) {
				s.setBlock(r, x+d[0], y+d[1], z+d[2], blkCobblestone, 0)
				soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				return true
			}
		}
	} else if isLava(b) {
		for _, d := range faceOffsets {
			nb := s.block(r, x+d[0], y+d[1], z+d[2])
			if isWater(nb) {
				if b == blkLavaSource {
					s.setBlock(r, x, y, z, blkObsidian, 0)
				} else {
					s.setBlock(r, x, y, z, blkCobblestone, 0)
				}
				soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				return true
			}
		}
	}
	return false
}

var fluidSchedules = map[*Room]map[uint64]int{}

func getFluidSchedule(r *Room) map[uint64]int {
	m := fluidSchedules[r]
	if m == nil {
		m = map[uint64]int{}
		fluidSchedules[r] = m
	}
	return m
}

func (s *Server) scheduleFluid(r *Room, x, y, z int) {
	if !r.inside(x, z) || y < 1 || y >= 127 {
		return
	}
	b := s.block(r, x, y, z)
	if !isFluid(b) {
		return
	}
	delay := 2 // 水流扩散间隔 2 刻 (200ms)
	if !isWater(b) {
		delay = 8 // 主世界岩浆扩散间隔 8 刻 (800ms)
		if r.dimName() == "nether" {
			delay = 3 // 下界岩浆扩散间隔 3 刻 (300ms)
		}
	}
	key := blockKey(x, y, z)
	sched := getFluidSchedule(r)
	if cur, ok := sched[key]; !ok || delay < cur {
		sched[key] = delay
	}
}

func (s *Server) isWashable(b int) bool {
	if b == 0 {
		return true
	}
	name := s.nameOf(b)
	switch name {
	case "Torch", "Redstone Torch", "Redstone Wire", "Redstone Repeater", "Redstone Comparator",
		"Stone Pressure Plate", "Oak Pressure Plate", "Fire", "Wheat Crops", "Carrots", "Potatoes":
		return true
	}
	if b >= 0 && b < len(s.rules.Kinds) && s.rules.Kinds[b] == 5 {
		return true
	}
	return false
}

func (s *Server) canFlowInto(r *Room, x, y, z int, water bool) bool {
	if !r.inside(x, z) || y < 1 || y >= 127 {
		return false
	}
	b := s.block(r, x, y, z)
	if b == 0 || s.isWashable(b) {
		return true
	}
	if water && isLava(b) {
		return true
	}
	if !water && isWater(b) {
		return true
	}
	return false
}

func (s *Server) canFlowDown(r *Room, x, y, z int, water bool) bool {
	return s.canFlowInto(r, x, y-1, z, water)
}

// 检查 (x, y, z) 下方是否有阻止流体继续向下的实体地面支撑
func (s *Server) hasSolidSupport(r *Room, x, y, z int, water bool) bool {
	if y <= 1 {
		return true
	}
	below := s.block(r, x, y-1, z)
	if below == 0 || s.isWashable(below) {
		return false
	}
	if water && (isWater(below) || isLava(below)) {
		return false
	}
	if !water && (isLava(below) || isWater(below)) {
		return false
	}
	return true
}

func (s *Server) canPathThrough(r *Room, x, y, z int, water bool) bool {
	if !r.inside(x, z) || y < 1 || y >= 127 {
		return false
	}
	b := s.block(r, x, y, z)
	if b == 0 || s.isWashable(b) {
		return true
	}
	if water && isWater(b) {
		return true
	}
	if !water && isLava(b) {
		return true
	}
	return false
}

// 计算从 (x, y, z) 沿某一方向出发到达最近落差边缘的最短距离 (BFS, 最多 4 步)
func (s *Server) distanceToDrop(r *Room, x, y, z int, dx, dz int, water bool) int {
	type node struct {
		x, z int
		dist int
	}
	startNX, startNZ := x+dx, z+dz
	if !s.canPathThrough(r, startNX, y, startNZ, water) {
		return 999
	}
	if s.canFlowDown(r, startNX, y, startNZ, water) {
		return 1
	}

	queue := []node{{x: startNX, z: startNZ, dist: 1}}
	visited := map[uint64]bool{blockKey(startNX, y, startNZ): true, blockKey(x, y, z): true}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.dist >= 4 {
			continue
		}
		for _, step := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, nz := cur.x+step[0], cur.z+step[1]
			k := blockKey(nx, y, nz)
			if visited[k] {
				continue
			}
			visited[k] = true
			if !s.canPathThrough(r, nx, y, nz, water) {
				continue
			}
			if s.canFlowDown(r, nx, y, nz, water) {
				return cur.dist + 1
			}
			queue = append(queue, node{x: nx, z: nz, dist: cur.dist + 1})
		}
	}
	return 999
}

func (s *Server) residenceAllowsFluidFlow(r *Room, fx, fy, fz, tx, ty, tz int) bool {
	if s.residence == nil || r == nil {
		return true
	}
	toRes := s.residence.FindAt(r.ID, tx, ty, tz)
	if toRes == nil {
		return true
	}
	fromRes := s.residence.FindAt(r.ID, fx, fy, fz)
	return fromRes == toRes
}

func (s *Server) spreadFluid(r *Room, x, y, z, b int) {
	if !r.inside(x, z) || y < 1 || y >= 127 {
		return
	}
	flowData := map[string]any{"x": x, "y": y, "z": z, "fluid": b}
	if !s.allowed(api.FluidFlowing, r, nil, flowData) {
		return
	}
	if s.fluidReaction(r, x, y, z) {
		return
	}

	water := isWater(b)
	below := s.block(r, x, y-1, z)

	// 1. 优先向下流动 (瀑布流体)
	if below == 0 || s.isWashable(below) || (water && isLava(below)) || (!water && isWater(below)) {
		if !s.residenceAllowsFluidFlow(r, x, y, z, x, y-1, z) {
			return
		}
		if water && isLava(below) {
			s.setBlock(r, x, y-1, z, blkCobblestone, 0)
			soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y-1)+.5, float64(z)+.5, nil)
			return
		} else if !water && isWater(below) {
			s.setBlock(r, x, y-1, z, blkStone, 0)
			soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y-1)+.5, float64(z)+.5, nil)
			return
		}
		nextBelow := blkWaterFalling
		if !water {
			nextBelow = blkLavaFalling
		}
		if below != nextBelow {
			if s.isWashable(below) && below != 0 {
				if drops := s.drops(below, 0); len(drops) > 0 {
					if prepared, err := s.prepareDrops(r, nil, drops, float64(x)+.5, float64(y-1)+.5, float64(z)+.5, "wash"); err == nil {
						s.publishDrops(r, nil, prepared, "wash")
					}
				}
			}
			s.setBlock(r, x, y-1, z, nextBelow, 0)
			s.scheduleFluid(r, x, y-1, z)
			s.emit(api.FluidFlowed, r, nil, flowData)
		}
		return
	}

	// 如果下方是悬空/流体（例如垂直落水或下方的流动水），绝不在空中向水平方向横向扩散！
	if !s.hasSolidSupport(r, x, y, z, water) {
		return
	}

	// 2. 下方遇实心方块阻挡时，向水平四周逐级扩散
	curLvl := 0
	maxSpread := 7
	flowBase := blkWaterFlowMin
	if water {
		curLvl = waterLevel(b)
	} else {
		curLvl = lavaLevel(b)
		maxSpread = 4
		flowBase = blkLavaFlowMin
		if r.dimName() == "nether" {
			maxSpread = 7
		}
	}

	if curLvl >= 0 && curLvl < maxSpread {
		nextBlock := flowBase + curLvl
		if !water && r.dimName() != "nether" {
			nextBlock = flowBase + (curLvl / 2)
		}

		// 落差优先寻路 (Slope / Drop Seeking)
		directions := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
		distMap := make([]int, 4)
		minDist := 999
		for i, d := range directions {
			dist := s.distanceToDrop(r, x, y, z, d[0], d[1], water)
			distMap[i] = dist
			if dist < minDist {
				minDist = dist
			}
		}

		for i, d := range directions {
			// 如果附近 4 格内有落差边缘，水流只朝最近的落差点方向流去，绝不向远离边缘的内侧漫延
			if minDist <= 4 && distMap[i] > minDist {
				continue
			}

			nx, nz := x+d[0], z+d[1]
			if !r.inside(nx, nz) || !s.residenceAllowsFluidFlow(r, x, y, z, nx, y, nz) {
				continue
			}
			target := s.block(r, nx, y, nz)
			if target == 0 || s.isWashable(target) {
				if s.isWashable(target) && target != 0 {
					if drops := s.drops(target, 0); len(drops) > 0 {
						if prepared, err := s.prepareDrops(r, nil, drops, float64(nx)+.5, float64(y)+.5, float64(nz)+.5, "wash"); err == nil {
							s.publishDrops(r, nil, prepared, "wash")
						}
					}
				}

				// 如果目标格到达了边缘（下方是空气落差），直接以垂直流形态进入并立即向下调度
				placedBlock := nextBlock
				if s.canFlowDown(r, nx, y, nz, water) {
					placedBlock = blkWaterFalling
					if !water {
						placedBlock = blkLavaFalling
					}
				}

				s.setBlock(r, nx, y, nz, placedBlock, 0)
				s.scheduleFluid(r, nx, y, nz)
			} else if (water && isLava(target)) || (!water && isWater(target)) {
				s.fluidReaction(r, nx, y, nz)
			}
		}
	}
}

func (s *Server) hasFluidSource(r *Room, x, y, z, b int) bool {
	return s.traceFluidSource(r, x, y, z, isWater(b), make(map[uint64]bool))
}

func (s *Server) traceFluidSource(r *Room, x, y, z int, water bool, visited map[uint64]bool) bool {
	b := s.block(r, x, y, z)
	if (water && b == blkWaterSource) || (!water && b == blkLavaSource) {
		return true
	}
	k := blockKey(x, y, z)
	if visited[k] {
		return false
	}
	visited[k] = true

	up := s.block(r, x, y+1, z)
	if (water && isWater(up)) || (!water && isLava(up)) {
		if s.traceFluidSource(r, x, y+1, z, water, visited) {
			return true
		}
	}

	falling := (water && b == blkWaterFalling) || (!water && b == blkLavaFalling)
	curLvl := -1
	if water {
		curLvl = waterLevel(b)
	} else {
		curLvl = lavaLevel(b)
	}

	if curLvl <= 0 && !falling {
		return false
	}

	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, nz := x+d[0], z+d[1]
		nb := s.block(r, nx, y, nz)
		nlvl := -1
		if water && isWater(nb) {
			nlvl = waterLevel(nb)
		} else if !water && isLava(nb) {
			nlvl = lavaLevel(nb)
		}
		if falling {
			if (water && isWater(nb)) || (!water && isLava(nb)) {
				if s.traceFluidSource(r, nx, y, nz, water, visited) {
					return true
				}
			}
		} else {
			if nlvl >= 0 && nlvl < curLvl {
				if s.traceFluidSource(r, nx, y, nz, water, visited) {
					return true
				}
			}
		}
	}
	return false
}

func init() {
	// 1. 桶交互 (onPlace 拦截)
	onPlace(3, func(s *Server, p *Peer, m Message, target int) bool {
		r := p.room
		v := s.heldItem(p)
		if v == nil {
			return false
		}

		// 空桶舀取水源/岩浆源
		if v.ID == itemBucket {
			bx, by, bz := m.X, m.Y, m.Z
			b := s.block(r, bx, by, bz)
			if b == blkWaterSource || b == blkLavaSource {
				fillData := map[string]any{"x": bx, "y": by, "z": bz, "fluid": b}
				if !s.allowed(api.BucketFilling, r, p, fillData) {
					return true
				}
				if b == blkWaterSource {
					s.setBlock(r, bx, by, bz, 0, 0)
					for _, d := range faceOffsets {
						s.scheduleFluid(r, bx+d[0], by+d[1], bz+d[2])
					}
					s.replaceHeld(p, stack(itemWaterBucket, 1, 0))
					soundAt(r, "item.bucket.fill", float64(bx)+.5, float64(by)+.5, float64(bz)+.5, nil)
					s.inventory(p, 0, 0)
					s.emit(api.BucketFilled, r, p, fillData)
					return true
				} else if b == blkLavaSource {
					s.setBlock(r, bx, by, bz, 0, 0)
					for _, d := range faceOffsets {
						s.scheduleFluid(r, bx+d[0], by+d[1], bz+d[2])
					}
					s.replaceHeld(p, stack(s.rules.byName["Lava Bucket"], 1, 0))
					soundAt(r, "item.bucket.fill_lava", float64(bx)+.5, float64(by)+.5, float64(bz)+.5, nil)
					s.inventory(p, 0, 0)
					s.emit(api.BucketFilled, r, p, fillData)
					return true
				}
			}
		}

		// 水桶/岩浆桶放置流体源
		if v.ID == itemWaterBucket || v.ID == s.rules.byName["Lava Bucket"] {
			offsets := faceOffsets
			at := offsets[m.F]
			x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
			if !r.inside(x, z) || y < 1 || y >= 127 {
				return true
			}
			old := s.block(r, x, y, z)
			if old == 0 || isFluid(old) || s.isWashable(old) {
				fluidBlock := blkWaterSource
				sound := "item.bucket.empty"
				if v.ID == s.rules.byName["Lava Bucket"] {
					fluidBlock = blkLavaSource
					sound = "item.bucket.empty_lava"
				}
				emptyData := map[string]any{"x": x, "y": y, "z": z, "fluid": fluidBlock}
				if !s.allowed(api.BucketEmptying, r, p, emptyData) {
					return true
				}
				// 下界倒水瞬间蒸发
				if fluidBlock == blkWaterSource && r.dimName() == "nether" {
					soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
					if !s.isCreative(p) {
						s.replaceHeld(p, stack(itemBucket, 1, 0))
					}
					s.inventory(p, 0, 0)
					s.emit(api.BucketEmptied, r, p, emptyData)
					return true
				}
				s.setBlock(r, x, y, z, fluidBlock, 0)
				soundAt(r, sound, float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
				s.scheduleFluid(r, x, y, z) // 启动流体逐步扩散时钟
				if !s.isCreative(p) {
					s.replaceHeld(p, stack(itemBucket, 1, 0))
				}
				s.inventory(p, 0, 0)
				s.emit(api.BucketEmptied, r, p, emptyData)
				return true
			}
		}

		return false
	})

	// 2. 挤奶消息 (milk)
	onMessage("milk", func(s *Server, p *Peer, m Message) bool {
		r := p.room
		mobID := m.E
		if mobID == 0 {
			mobID = uint32(m.ID)
		}
		mob := r.Mobs[mobID]
		if mob != nil && mob.Type == "cow" && mob.Flags&1 == 0 { // 成年奶牛
			held := s.heldItem(p)
			if held != nil && held.ID == itemBucket {
				fillData := map[string]any{"mob": mob.ID, "type": "cow", "milk": true}
				if !s.allowed(api.BucketFilling, r, p, fillData) {
					return true
				}
				s.replaceHeld(p, stack(itemMilkBucket, 1, 0))
				soundAt(r, "entity.cow.milk", mob.X, mob.Y, mob.Z, nil)
				s.inventory(p, 0, 0)
				s.emit(api.BucketFilled, r, p, fillData)
			}
		}
		return true
	})

	// 3. 流体流动计划调度 (onBlockUpdate)
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if isFluid(b) {
			s.scheduleFluid(r, x, y, z)
		}
	})

	// 4. 挖掘与放置时检查四周流体并调度
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		r := p.room
		for _, d := range faceOffsets {
			nx, ny, nz := x+d[0], y+d[1], z+d[2]
			if isFluid(s.block(r, nx, ny, nz)) {
				s.scheduleFluid(r, nx, ny, nz)
			}
		}
	})

	onAfterPlace(func(s *Server, p *Peer, x, y, z, placed int, m Message) {
		r := p.room
		for _, d := range faceOffsets {
			nx, ny, nz := x+d[0], y+d[1], z+d[2]
			if isFluid(s.block(r, nx, ny, nz)) {
				s.scheduleFluid(r, nx, ny, nz)
			}
		}
	})

	// 5. 流体时钟推进与实体推动 (onRoomTick)
	onRoomTick(func(s *Server, r *Room) {
		// A. 流体扩散时钟步进
		sched := getFluidSchedule(r)
		if len(sched) > 0 {
			var ready []uint64
			for k, ticks := range sched {
				if ticks <= 1 {
					ready = append(ready, k)
					delete(sched, k)
				} else {
					sched[k] = ticks - 1
				}
			}
			for _, k := range ready {
				fx, fy, fz := coords(k)
				fb := s.block(r, fx, fy, fz)
				if isFluid(fb) {
					// 退潮检测
					if !s.hasFluidSource(r, fx, fy, fz, fb) {
						s.setBlock(r, fx, fy, fz, 0, 0)
						for _, d := range faceOffsets {
							s.scheduleFluid(r, fx+d[0], fy+d[1], fz+d[2])
						}
					} else {
						s.spreadFluid(r, fx, fy, fz, fb)
					}
				}
			}
		}

		if len(r.Peers) == 0 {
			return
		}
		// 推动掉落物
		for _, d := range r.Drops {
			if d == nil {
				continue
			}
			bx, by, bz := int(math.Floor(d.X)), int(math.Floor(d.Y)), int(math.Floor(d.Z))
			if vx, vz, inFluid := s.fluidFlowVector(r, bx, by, bz); inFluid {
				d.VX += vx * 0.015
				d.VZ += vz * 0.015
				if isWater(s.block(r, bx, by, bz)) && d.VY < 0.04 {
					d.VY += 0.01 // 水中微浮力
				}
			}
		}

		// 推动生物
		for _, m := range r.Mobs {
			if m == nil || m.Health <= 0 {
				continue
			}
			bx, by, bz := int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
			if vx, vz, inFluid := s.fluidFlowVector(r, bx, by, bz); inFluid {
				speed := 0.03
				if !isWater(s.block(r, bx, by, bz)) {
					speed = 0.015
				}
				nx, nz := m.X+vx*speed, m.Z+vz*speed
				if s.mobFits(r, m.Type, nx, m.Y, nz) {
					m.X, m.Z = nx, nz
				}
			}
		}
	})
}

// fluidFlowVector 计算指定位置流体的水平流速向量 (vx, vz)
func (s *Server) fluidFlowVector(r *Room, x, y, z int) (float64, float64, bool) {
	b := s.block(r, x, y, z)
	if !isFluid(b) {
		return 0, 0, false
	}
	water := isWater(b)
	lvl := 0
	if water {
		lvl = waterLevel(b)
	} else {
		lvl = lavaLevel(b)
	}
	if lvl < 0 {
		return 0, 0, true
	}
	var vx, vz float64
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, nz := x+d[0], z+d[1]
		if !r.inside(nx, nz) {
			continue
		}
		nb := s.block(r, nx, y, nz)
		nlvl := -1
		if water && isWater(nb) {
			nlvl = waterLevel(nb)
		} else if !water && isLava(nb) {
			nlvl = lavaLevel(nb)
		}
		if nlvl >= 0 {
			diff := float64(nlvl - lvl)
			vx += float64(d[0]) * diff
			vz += float64(d[1]) * diff
		}
	}
	length := math.Hypot(vx, vz)
	if length > 0.001 {
		vx /= length
		vz /= length
	}
	return vx, vz, true
}
