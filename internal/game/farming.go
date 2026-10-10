package game

import (
	api "mcraft/backend/pkg/plugin"
	"strings"
)

// ---------------------------------------------------------------------------
// 农业系统：
// 1. 锄地：草方块/泥土转耕地 (145 Farmland)，附近 4 格有水转湿润 (146 Farmland (Wet))
// 2. 播种：小麦种子(150-157)、胡萝卜(158-161)、马铃薯(162-164, 170) 种植在耕地上
// 3. 生长与随机刻：作物分阶段成长，成熟后阻挡继续生长
// 4. 骨粉：催熟作物至成熟，催熟树苗(147-149)长成大树
// 5. 树木生成：橡树、白桦树、云杉树
// 6. 耕地退化与踩踏：上方有实体方块或长期缺水时退化为泥土
// ---------------------------------------------------------------------------

const (
	blkDirt          = 3
	blkGrassBlock    = 2
	blkFarmlandDry   = 145
	blkFarmlandWet   = 146
	blkWheatMin      = 150
	blkWheatMax      = 157 // Mature
	blkCarrotMin     = 158
	blkCarrotMax     = 161 // Mature
	blkPotatoMin     = 162
	blkPotatoMid     = 164
	blkPotatoMax     = 170 // Mature
	blkOakSapling    = 147
	blkBirchSapling  = 148
	blkSpruceSapling = 149
	blkOakLog        = 6
	blkOakLeaves     = 7
	blkBirchLog      = 16
	blkBirchLeaves   = 17
	blkSpruceLog     = 72
	blkSpruceLeaves  = 73
	itemWheatSeeds   = 335
	itemWheat        = 336
	itemCarrot       = 350
	itemPotato       = 351
	itemPoisonPotato = 353
	itemBoneMeal     = 326
)

func isFarmland(b int) bool {
	return b == blkFarmlandDry || b == blkFarmlandWet
}

func isCrop(b int) bool {
	return (b >= blkWheatMin && b <= blkWheatMax) ||
		(b >= blkCarrotMin && b <= blkCarrotMax) ||
		(b >= blkPotatoMin && b <= blkPotatoMid) || b == blkPotatoMax
}

func isSapling(b int) bool {
	return b == blkOakSapling || b == blkBirchSapling || b == blkSpruceSapling
}

func (s *Server) nearWater(r *Room, x, y, z int) bool {
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			for dy := 0; dy <= 1; dy++ {
				if r.inside(x+dx, z+dz) && y+dy >= 0 && y+dy < 128 {
					if isWater(s.block(r, x+dx, y+dy, z+dz)) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s *Server) growCrop(r *Room, x, y, z, b int, instantMature bool) bool {
	next := nextCropBlock(b, instantMature)
	if next == 0 || s.block(r, x, y, z) != b {
		return false
	}
	data := map[string]any{"actor": "server", "source": "world", "reason": "crop_growth", "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "from": b, "to": next}
	if !s.allowed(api.CropGrowing, r, nil, data) || !s.setBlocksWithContext(r, [][4]int{{x, y, z, next}}, setQuiet, "crop_growth", nil, data) {
		return false
	}
	s.emit(api.CropGrown, r, nil, data)
	return true
}

func (s *Server) growTree(r *Room, x, y, z, sapling int) bool {
	if !isSapling(sapling) || s.block(r, x, y, z) != sapling {
		return false
	}
	edits := s.treeGrowthEdits(r, x, y, z, sapling)
	if len(edits) == 0 {
		return false
	}
	data := map[string]any{"actor": "server", "source": "world", "reason": "tree_growth", "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "from": sapling, "to": s.rules.byName[map[int]string{blkOakSapling: "Oak Log", blkBirchSapling: "Birch Log", blkSpruceSapling: "Spruce Log"}[sapling]], "changes": append([][4]int(nil), edits...)}
	if !s.allowed(api.CropGrowing, r, nil, data) || !s.setBlocksWithContext(r, edits, setQuiet, "tree_growth", nil, data) {
		return false
	}
	s.emit(api.CropGrown, r, nil, data)
	soundAt(r, "block.grass.place", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
	return true
}

func init() {
	// 1. 锄地、播种与骨粉使用 (onPlace)
	onPlace(1, func(s *Server, p *Peer, m Message, target int) bool {
		r := p.room
		if r.Mode != "survival" {
			return false
		}
		v := s.heldItem(p)
		if v == nil {
			return false
		}
		it := s.rules.byID[v.ID]
		tName := s.fullName(target)

		// 锄地
		if it.Tool != nil && it.Tool.Type == "hoe" {
			if m.F != 0 && (strings.HasPrefix(tName, "Grass Block") || strings.HasPrefix(tName, "Dirt")) {
				above := s.block(r, m.X, m.Y+1, m.Z)
				if above == 0 {
					tillData := map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "target": target, "tool": itemSnapshot(v)}
					if !s.allowed(api.FarmingTilling, r, p, tillData) {
						return true
					}
					farmland := blkFarmlandDry
					if s.nearWater(r, m.X, m.Y, m.Z) {
						farmland = blkFarmlandWet
					}
					s.setBlock(r, m.X, m.Y, m.Z, farmland, setQuiet)
					s.wear(p, 1)
					soundAt(r, "item.hoe.till", float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
					s.inventory(p, 0, 0)
					s.emit(api.FarmingTilled, r, p, map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "farmland": farmland})
					return true
				}
			}
		}

		// Bone meal shares the protected growth transaction with dispensers.
		if v.ID == itemBoneMeal {
			s.useFertilizer(p, m)
			return true
		}

		// 播种 (种子、胡萝卜、马铃薯点在耕地面上)
		if isFarmland(target) && m.F == 1 {
			above := s.block(r, m.X, m.Y+1, m.Z)
			if above == 0 {
				cropBlock := 0
				switch v.ID {
				case itemWheatSeeds:
					cropBlock = blkWheatMin
				case itemCarrot:
					cropBlock = blkCarrotMin
				case itemPotato:
					cropBlock = blkPotatoMin
				}
				if cropBlock != 0 {
					s.setBlock(r, m.X, m.Y+1, m.Z, cropBlock, setQuiet)
					s.consumeHeld(p, 1)
					soundAt(r, "item.crop.plant", float64(m.X)+.5, float64(m.Y)+1.5, float64(m.Z)+.5, nil)
					s.inventory(p, 0, 0)
					return true
				}
			}
		}

		return false
	})

	// 2. 作物附魔采集与常规掉落 hook (farming_drops)
	// 成熟小麦掉小麦+种子，胡萝卜掉1-4胡萝卜，马铃薯掉1-4马铃薯(偶有毒马铃薯)
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		if p == nil || p.room.Mode != "survival" {
			return
		}
		var drops Stacks
		switch old {
		case blkWheatMax:
			drops = append(drops, stack(itemWheat, 1, 0), stack(itemWheatSeeds, 1+s.rng.Intn(3), 0))
		case blkCarrotMax:
			drops = append(drops, stack(itemCarrot, 1+s.rng.Intn(4), 0))
		case blkPotatoMax:
			drops = append(drops, stack(itemPotato, 1+s.rng.Intn(4), 0))
			if s.rng.Intn(50) == 0 {
				drops = append(drops, stack(itemPoisonPotato, 1, 0))
			}
		default:
			if isCrop(old) {
				// 未成熟被破坏，掉原种子
				switch {
				case old >= blkWheatMin && old < blkWheatMax:
					drops = append(drops, stack(itemWheatSeeds, 1, 0))
				case old >= blkCarrotMin && old < blkCarrotMax:
					drops = append(drops, stack(itemCarrot, 1, 0))
				case (old >= blkPotatoMin && old <= blkPotatoMid):
					drops = append(drops, stack(itemPotato, 1, 0))
				}
			}
		}
		if len(drops) > 0 {
			prepared, err := s.prepareDrops(p.room, p, drops, float64(x)+.5, float64(y)+.3, float64(z)+.5, "crop_harvest")
			if err == nil {
				s.publishDrops(p.room, p, prepared, "crop_harvest")
				s.emit(api.CropHarvested, p.room, p, map[string]any{"x": x, "y": y, "z": z, "block": old})
			}
		}
	})

	// 3. 随机刻更新与方块更新 (onBlockUpdate)
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if r.Mode != "survival" {
			return
		}
		// 耕地水分与上方方块检测
		if isFarmland(b) {
			above := s.block(r, x, y+1, z)
			if s.solidAt(r, x, y+1, z) {
				// 被压实，退化为泥土
				s.setBlock(r, x, y, z, blkDirt, setQuiet)
				return
			}
			hasWater := s.nearWater(r, x, y, z)
			if hasWater && b == blkFarmlandDry {
				s.setBlock(r, x, y, z, blkFarmlandWet, setQuiet)
			} else if !hasWater && b == blkFarmlandWet {
				s.setBlock(r, x, y, z, blkFarmlandDry, setQuiet)
			} else if !hasWater && above == 0 && s.rng.Intn(100) == 0 {
				// 长期无水且上方无作物，退化为泥土
				s.setBlock(r, x, y, z, blkDirt, setQuiet)
			}
			return
		}

		// 作物下方检测：若下方不再是耕地，作物掉落
		if isCrop(b) {
			below := s.block(r, x, y-1, z)
			if !isFarmland(below) {
				s.setBlock(r, x, y, z, 0, 0)
			}
		}
	})

	// 4. 定期生长随机刻 (onRoomTick)
	onRoomTick(func(s *Server, r *Room) {
		if r.Mode != "survival" || len(r.Peers) == 0 {
			return
		}
		// 每秒在玩家附近抽取若干方块进行随机刻生长
		if r.rt().tickCount%10 != 0 {
			return
		}
		for p := range r.Peers {
			if p.state == nil || p.state.Dead {
				continue
			}
			px, py, pz := int(p.state.X), int(p.state.Y), int(p.state.Z)
			for i := 0; i < 3; i++ {
				rx := px + s.rng.Intn(32) - 16
				rz := pz + s.rng.Intn(32) - 16
				ry := py + s.rng.Intn(16) - 8
				if !r.inside(rx, rz) || ry < 1 || ry >= 127 {
					continue
				}
				b := s.block(r, rx, ry, rz)
				if isCrop(b) {
					s.growCrop(r, rx, ry, rz, b, false)
				} else if isSapling(b) {
					if s.rng.Intn(7) == 0 {
						s.growTree(r, rx, ry, rz, b)
					}
				}
			}
		}
	})
}
