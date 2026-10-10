package game

import (
	api "mcraft/backend/pkg/plugin"
)

const (
	defaultRandomTickSpeed = 3
	maxRandomTickSpeed     = 4096
	blkSnowBlock           = 14
	blkCactus              = 70
	blkSugarCane           = 71
)

// randomTickSpeed returns the effective randomTickSpeed for the room.
func (s *Server) randomTickSpeed(r *Room) int {
	if r == nil {
		return defaultRandomTickSpeed
	}
	root := r.root()
	if root.Ext != nil && root.Ext.RandomTickSpeed != nil {
		return *root.Ext.RandomTickSpeed
	}
	return defaultRandomTickSpeed
}

// setRandomTickSpeed updates randomTickSpeed, emitting protection events.
func (s *Server) setRandomTickSpeed(r *Room, p *Peer, speed int, reason string) bool {
	if r == nil || speed < 0 || speed > maxRandomTickSpeed {
		return false
	}
	root := r.root()
	if root.Ext == nil {
		root.Ext = &RoomExt{}
	}
	prev := s.randomTickSpeed(root)
	if prev == speed {
		return true
	}
	data := map[string]any{
		"rule":     "randomTickSpeed",
		"previous": prev,
		"value":    speed,
		"reason":   reason,
		"target":   root.ID,
	}
	if p != nil {
		data["actor"] = p.sid
	} else {
		data["actor"] = s.commandActor(nil)
	}
	if !s.allowed(api.WorldRuleChanging, root, p, data) {
		return false
	}
	val := speed
	root.Ext.RandomTickSpeed = &val
	root.Dirty = true
	s.emit(api.WorldRuleChanged, root, p, data)
	return true
}

// tickRandomBlocks runs unified random ticks across loaded chunk areas around players.
func (s *Server) tickRandomBlocks(r *Room) {
	if r == nil || len(r.Peers) == 0 {
		return
	}
	speed := s.randomTickSpeed(r)
	if speed <= 0 {
		return
	}

	// Collect unique chunk coords around active online players (radius 3 chunks)
	chunks := map[ChunkCoord]bool{}
	for p := range r.Peers {
		if p.state == nil || p.state.Dead {
			continue
		}
		cx, cz := int(p.state.X)/16, int(p.state.Z)/16
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				cc := ChunkCoord{X: cx + dx, Z: cz + dz}
				if r.Infinite || (r.inside(cc.X*16, cc.Z*16) && r.inside(cc.X*16+15, cc.Z*16+15)) {
					chunks[cc] = true
				}
			}
		}
	}

	if len(chunks) == 0 {
		return
	}

	// For each active chunk, sample speed * 3 random block positions across heights
	for c := range chunks {
		samples := speed * 3
		for i := 0; i < samples; i++ {
			rx := c.X*16 + s.rng.Intn(16)
			rz := c.Z*16 + s.rng.Intn(16)
			ry := 1 + s.rng.Intn(126)
			if !r.inside(rx, rz) {
				continue
			}
			b := s.block(r, rx, ry, rz)
			if b == 0 {
				continue
			}
			s.handleRandomTick(r, rx, ry, rz, b)
		}
	}
}

// handleRandomTick executes game mechanics for a selected block.
func (s *Server) handleRandomTick(r *Room, x, y, z, b int) {
	// 1. 农作物与树苗生长 (Crops & Saplings)
	if isCrop(b) {
		// 作物生长：检查上方有光照或耕地湿润
		if s.rng.Intn(4) == 0 {
			s.growCrop(r, x, y, z, b, false)
		}
		return
	}
	if isSapling(b) {
		if s.rng.Intn(8) == 0 {
			s.growTree(r, x, y, z, b)
		}
		return
	}

	// 2. 甘蔗生长 (Sugar Cane)
	if b == blkSugarCane {
		if y+1 < 128 && s.block(r, x, y+1, z) == 0 {
			h := 1
			for y-h >= 1 && s.block(r, x, y-h, z) == blkSugarCane {
				h++
			}
			if h < 3 && s.rng.Intn(4) == 0 {
				s.setBlock(r, x, y+1, z, blkSugarCane, setQuiet)
			}
		}
		return
	}

	// 3. 仙人掌生长 (Cactus)
	cactusID := s.id("Cactus")
	if cactusID > 0 && b == cactusID {
		h := 1
		for y-h >= 1 && s.block(r, x, y-h, z) == cactusID {
			h++
		}
		if h < 3 && y+1 < 128 && s.block(r, x, y+1, z) == 0 {
			blocked := false
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				if s.solidAt(r, x+d[0], y+1, z+d[1]) {
					blocked = true
					break
				}
			}
			if !blocked && s.rng.Intn(4) == 0 {
				s.setBlock(r, x, y+1, z, cactusID, setQuiet)
			}
		}
		return
	}

	// 4. 耕地水分与退化 (Farmland)
	if isFarmland(b) {
		above := s.block(r, x, y+1, z)
		if s.solidAt(r, x, y+1, z) {
			s.setBlock(r, x, y, z, blkDirt, setQuiet)
			return
		}
		hasWater := s.nearWater(r, x, y, z)
		if hasWater && b == blkFarmlandDry {
			s.setBlock(r, x, y, z, blkFarmlandWet, setQuiet)
		} else if !hasWater && b == blkFarmlandWet {
			s.setBlock(r, x, y, z, blkFarmlandDry, setQuiet)
		} else if !hasWater && above == 0 && s.rng.Intn(8) == 0 {
			s.setBlock(r, x, y, z, blkDirt, setQuiet)
		}
		return
	}

	// 5. 树叶自然衰败 (Leaves decay)
	if s.leafBlock(b) && r.usesSurvival() {
		k := blockKey(x, y, z)
		if !s.persistentLeaf(r, k, b) {
			supported, complete := s.leafSupport(r, x, y, z)
			if complete && !supported {
				drops := s.leafDecayDrops(b)
				s.decayLeafWithDrops(r, k, b, drops)
			}
		}
		return
	}

	// 6. 冰雪消融 (Snow melting)
	if b == blkSnowBlock {
		// 附近有热源（火把、熔岩、火焰）或者白天露天阳光照射
		hasHeat := false
		for dx := -2; dx <= 2 && !hasHeat; dx++ {
			for dz := -2; dz <= 2 && !hasHeat; dz++ {
				for dy := -1; dy <= 2 && !hasHeat; dy++ {
					nb := s.block(r, x+dx, y+dy, z+dz)
					if nb == blkFlintFire || isLava(nb) || (nb >= 82 && nb <= 86) { // Torches
						hasHeat = true
					}
				}
			}
		}
		if hasHeat || (s.skyOpen(r, x, y+1, z) && !isNight(r.Time) && !s.raining(r) && s.rng.Intn(16) == 0) {
			s.setBlock(r, x, y, z, 0, setQuiet)
		}
		return
	}

	// 7. 火焰蔓延与自然熄灭 (Fire Spread & Extinguish)
	if b == blkFlintFire && s.gameRule(r, "doFireTick") {
		origin := s.fireOrigin(r, blockKey(x, y, z))
		// 下雨天熄灭室外火焰
		if s.raining(r) && s.highestBlock(r, x, z) <= y {
			s.setBlock(r, x, y, z, 0, 0)
			soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
			return
		}
		// 检查相邻 TNT
		for _, off := range faceOffsets {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if s.block(r, nx, ny, nz) == blkExplosiveTNT {
				s.primeTNT(r, nil, nx, ny, nz, defaultTNTFuse, "fire", origin)
			}
		}
		// 蔓延
		if s.rng.Intn(4) == 0 {
			below := s.block(r, x, y-1, z)
			if s.isFlammable(below) && below != blkExplosiveTNT && s.rng.Intn(3) == 0 {
				if s.setBlock(r, x, y-1, z, blkFlintFire, 0) {
					s.inheritFire(r, blockKey(x, y-1, z), origin)
				}
			}
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
		// 自然烧尽熄灭
		if s.rng.Intn(8) == 0 {
			s.setBlock(r, x, y, z, 0, 0)
		}
		return
	}
}

func init() {
	// 注册 roomTick 钩子，统一触发 RandomTick 循环
	onRoomTick(func(s *Server, r *Room) {
		s.tickRandomBlocks(r)
	})
}
