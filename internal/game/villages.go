package game

// ---------------------------------------------------------------------------
// Villages & Trading System (villages.go)
// 1. Natural Village Generation: Cobblestone well, cottages, paths, chests with loot
// 2. Villager Mob & Professions: Farmer, Librarian, Weaponsmith, Fletcher
// 3. Emerald Trading Economy: Exchange crops/items for emeralds and gear
// ---------------------------------------------------------------------------

type TradeOffer struct {
	InID     int    `json:"inId"`
	InCount  int    `json:"inCount"`
	OutID    int    `json:"outId"`
	OutCount int    `json:"outCount"`
	Name     string `json:"name"`
}

var defaultTrades = []TradeOffer{
	{InID: 336, InCount: 15, OutID: 38, OutCount: 1, Name: "15 小麦 -> 1 绿宝石"},
	{InID: 38, InCount: 1, OutID: 339, OutCount: 6, Name: "1 绿宝石 -> 6 面包"},
	{InID: 38, InCount: 1, OutID: 266, OutCount: 1, Name: "1 绿宝石 -> 1 铁剑"},
	{InID: 38, InCount: 1, OutID: 303, OutCount: 1, Name: "1 绿宝石 -> 1 盾牌"},
	{InID: 38, InCount: 2, OutID: 301, OutCount: 1, Name: "2 绿宝石 -> 1 弓"},
	{InID: 38, InCount: 1, OutID: 302, OutCount: 16, Name: "1 绿宝石 -> 16 箭矢"},
	{InID: 315, InCount: 4, OutID: 38, OutCount: 1, Name: "4 铁锭 -> 1 绿宝石"},
	{InID: 38, InCount: 3, OutID: 320, OutCount: 8, Name: "3 绿宝石 -> 8 红石粉"},
}

func init() {
	// Register villager mob spec
	mobSpecs["villager"] = mobSpec{
		Kind:    25,
		Health:  20,
		Speed:   1.0,
		Width:   0.6,
		Height:  1.95,
		Hostile: false,
		Drops:   []string{"Bread"},
	}

	// Handle trading interactions
	onMessage("trade_open", func(s *Server, p *Peer, m Message) bool {
		mobID := uint32(m.ID) | mobNamespace
		mob := p.room.Mobs[mobID]
		if mob == nil || mob.Type != "villager" {
			return true
		}
		// Send trading list to player
		p.json(map[string]any{
			"t":      "trade_window",
			"mobId":  mob.ID,
			"trades": defaultTrades,
		})
		soundAt(p.room, "entity.villager.ambient", mob.X, mob.Y, mob.Z, nil)
		return true
	})

	onMessage("trade_execute", func(s *Server, p *Peer, m Message) bool {
		if p.state == nil || p.state.Inv == nil || p.state.Dead {
			return true
		}
		idx := m.I
		if idx < 0 || idx >= len(defaultTrades) {
			return true
		}
		trade := defaultTrades[idx]
		inv := p.state.Inv

		// 1. Check if player has input items
		hasCount := 0
		for _, sl := range inv.Slots {
			if sl != nil && sl.ID == trade.InID {
				hasCount += sl.Count
			}
		}
		if hasCount < trade.InCount {
			p.sys("材料不足，无法完成交易。")
			soundAt(p.room, "entity.villager.no", p.state.X, p.state.Y, p.state.Z, nil)
			return true
		}

		// 2. Deduct input items
		remaining := trade.InCount
		for i, sl := range inv.Slots {
			if sl != nil && sl.ID == trade.InID {
				if sl.Count <= remaining {
					remaining -= sl.Count
					inv.Slots[i] = nil
				} else {
					sl.Count -= remaining
					remaining = 0
				}
				if remaining <= 0 {
					break
				}
			}
		}

		// 3. Grant output item
		outStack := &Stack{ID: trade.OutID, Count: trade.OutCount, Damage: 0}
		s.add(inv, outStack)

		p.room.Dirty = true
		s.inventory(p, 0, 0)
		soundAt(p.room, "entity.villager.yes", p.state.X, p.state.Y, p.state.Z, nil)
		return true
	})
}

// Generates village structures in plains chunks
func (s *Server) maybeGenerateVillage(r *Room, c ChunkCoord, set func(x, y, z int, b byte), id func(string) byte) {
	if r.dimName() != "overworld" {
		return
	}
	sd := seed(r.seedID())
	regionX := floorDiv(c.X, 8)
	regionZ := floorDiv(c.Z, 8)
	vHash := terrainHash(sd+777, regionX, 0, regionZ)
	villageChunkX := regionX*8 + int((vHash)%6) + 1
	villageChunkZ := regionZ*8 + int((vHash>>8)%6) + 1

	if c.X != villageChunkX || c.Z != villageChunkZ {
		return
	}

	cx := c.X*16 + 8
	cz := c.Z*16 + 8
	baseY, biome := terrainColumn(sd, cx, cz)
	if biome == "ocean" || baseY < 54 || baseY > 85 {
		return
	}

	cobble := id("Cobblestone")
	planks := id("Oak Planks")
	glass := id("Glass")
	log := id("Oak Log")
	fence := id("Oak Fence")
	torch := id("Torch")
	doorLower := id("Oak Door")
	doorUpper := byte(118) // Oak Door (Upper)
	bedFoot := byte(132)   // Bed
	bedHead := byte(136)   // Bed (Head)
	craftTable := id("Crafting Table")
	water := id("Water")
	gravel := id("Gravel")
	if gravel == 0 {
		gravel = cobble
	}

	// 辅助地基填充：向下延伸圆石直到自然地表，避免建筑在斜坡悬空
	fillFoundation := func(x, y, z int, block byte) {
		for dy := y; dy >= max(1, y-4); dy-- {
			set(x, dy, z, block)
		}
	}

	// 1. Central Village Well at (cx, baseY, cz) [x: 6..10, z: 6..10]
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			fillFoundation(cx+dx, baseY, cz+dz, cobble)
			if dx == -2 || dx == 2 || dz == -2 || dz == 2 {
				set(cx+dx, baseY+1, cz+dz, cobble)
			} else {
				set(cx+dx, baseY, cz+dz, water)
				set(cx+dx, baseY-1, cz+dz, water)
			}
		}
	}
	// 井立柱与屋顶
	set(cx-2, baseY+2, cz-2, fence)
	set(cx-2, baseY+3, cz-2, fence)
	set(cx+2, baseY+2, cz-2, fence)
	set(cx+2, baseY+3, cz-2, fence)
	set(cx-2, baseY+2, cz+2, fence)
	set(cx-2, baseY+3, cz+2, fence)
	set(cx+2, baseY+2, cz+2, fence)
	set(cx+2, baseY+3, cz+2, fence)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			set(cx+dx, baseY+4, cz+dz, cobble)
		}
	}

	// 2. Cottage 1 (Plains House) at (cx+4, baseY, cz-3) [x: 10..14, z: 3..7] - 完全在区块内
	hx, hz := cx+4, cz-3
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			fillFoundation(hx+dx, baseY, hz+dz, cobble)
			for dy := 1; dy <= 3; dy++ {
				if dx == -2 || dx == 2 || dz == -2 || dz == 2 {
					if dy == 2 && (dx == 0 || dz == 0) {
						set(hx+dx, baseY+dy, hz+dz, glass) // 窗户
					} else {
						set(hx+dx, baseY+dy, hz+dz, planks)
					}
				} else {
					set(hx+dx, baseY+dy, hz+dz, 0) // 内部空间清空
				}
			}
			set(hx+dx, baseY+4, hz+dz, log) // 屋顶
		}
	}
	// 门口朝西 (完整双格门: 下半部+上半部)
	set(hx-2, baseY+1, hz, doorLower)
	set(hx-2, baseY+2, hz, doorUpper)
	// 室内家具 (完整两格床: 脚部+头部)
	set(hx, baseY+1, hz-1, bedFoot)
	set(hx+1, baseY+1, hz-1, bedHead)
	set(hx+1, baseY+1, hz+1, craftTable)
	set(hx, baseY+2, hz+1, torch)

	// 3. Cottage 2 (Blacksmith / Storage) at (cx-4, baseY, cz-3) [x: 2..6, z: 3..7] - 完全在区块内
	bx, bz := cx-4, cz-3
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			fillFoundation(bx+dx, baseY, bz+dz, cobble)
			for dy := 1; dy <= 3; dy++ {
				if dx == -2 || dx == 2 || dz == -2 || dz == 2 {
					set(bx+dx, baseY+dy, bz+dz, cobble)
				} else {
					set(bx+dx, baseY+dy, bz+dz, 0)
				}
			}
			set(bx+dx, baseY+4, bz+dz, cobble)
		}
	}
	// 门口朝东 (完整双格门)
	set(bx+2, baseY+1, bz, doorLower)
	set(bx+2, baseY+2, bz, doorUpper)
	set(bx-1, baseY+1, bz-1, id("Furnace"))
	set(bx-1, baseY+1, bz+1, id("Chest"))

	// 4. 村庄农田 (Farmland & Crops) at (cx, baseY, cz+4) [x: 6..10, z: 10..14] - 完全在区块内
	fx, fz := cx, cz+4
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			fillFoundation(fx+dx, baseY, fz+dz, cobble)
			if dx == -2 || dx == 2 || dz == -2 || dz == 2 {
				set(fx+dx, baseY+1, fz+dz, log) // 围边
			} else if dx == 0 && dz == 0 {
				set(fx+dx, baseY+1, fz+dz, water) // 灌溉水源
			} else {
				set(fx+dx, baseY+1, fz+dz, 146) // 湿润耕地 (Farmland Wet)
				set(fx+dx, baseY+2, fz+dz, 157) // 成熟小麦 (Wheat Crops Mature)
			}
		}
	}

	// 5. 铺设连接水井、民居与铁匠铺的砂砾道路
	for dx := -3; dx <= 3; dx++ {
		set(cx+dx, baseY, cz, gravel)
	}
	for dz := -1; dz <= 3; dz++ {
		set(cx, baseY, cz+dz, gravel)
	}

	// 只有活动房间才注册宝箱内容和排队生成村民，避免 worker 生成时 nil panic
	if r.Containers != nil {
		chestKey := blockKey(bx-1, baseY+1, bz+1)
		chestSlots := make(Stacks, 27)
		chestSlots[0] = &Stack{ID: 315, Count: 3} // Iron Ingot
		chestSlots[1] = &Stack{ID: 339, Count: 8} // Bread
		chestSlots[2] = &Stack{ID: 38, Count: 2}  // Emerald
		chestSlots[3] = &Stack{ID: 337, Count: 4} // Apples
		chestSlots[4] = &Stack{ID: 24, Count: 3}  // Obsidian
		r.Containers[chestKey] = &Container{
			Kind:  "chest",
			Slots: chestSlots,
		}

		s.pendingVillageSpawns = append(s.pendingVillageSpawns,
			villageSpawn{r, float64(cx) + 1.5, float64(baseY) + 1, float64(cz) + 1.5},
			villageSpawn{r, float64(hx), float64(baseY) + 1, float64(hz)})
	}
}

type villageSpawn struct {
	Room    *Room
	X, Y, Z float64
}

func (s *Server) spawnPendingVillagers() {
	pending := s.pendingVillageSpawns
	s.pendingVillageSpawns = nil
	for _, v := range pending {
		if v.Room != nil && s.rooms[v.Room.root().ID] == v.Room.root() {
			// Structure anchors may be on water or an integer cell boundary.
			// Select nearby footing before the ordinary protected spawn path.
			if x, y, z, ok := s.villageSpawnPosition(v); ok {
				s.spawnMob(v.Room, nil, "villager", x, y, z, "village")
			}
		}
	}
}
