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
