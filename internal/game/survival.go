package game

import (
	"encoding/binary"
	"github.com/gorilla/websocket"
	"math"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"time"
)

func (s *Server) survivalInfo(r *Room) map[string]any {
	info := map[string]any{"seed": seed(r.seedID()), "w": r.width(), "gen": r.gen(), "dim": r.dimName(), "spawn": s.engine.spawn(r), "time": r.root().Time, "grace": 0, "hardcore": r.hardcore(), "digAck": true, "placeAck": true, "placeInventoryAck": true}
	if r.dimName() == "end" {
		info["spawn"] = endSpawn(r)
	}
	return info
}
func (s *Server) stream(p *Peer) {
	if p.room.Infinite {
		s.streamInfinite(p)
		return
	}
	cn := p.room.cn()
	cx, cz := int(p.state.X)/16, int(p.state.Z)/16
	radius := p.rd + 2
	for ci := range p.known {
		if abs(ci%cn-cx) > 15 || abs(ci/cn-cz) > 15 {
			delete(p.known, ci)
		}
	}
	var needed []int
	want := map[int]bool{}
	for z := max(0, cz-radius); z <= min(cn-1, cz+radius); z++ {
		for x := max(0, cx-radius); x <= min(cn-1, cx+radius); x++ {
			ci := z*cn + x
			if !p.known[ci] {
				needed = append(needed, ci)
				want[ci] = true
			}
		}
	}
	if len(needed) > 0 {
		grouped := map[int][]byte{}
		for key, b := range p.room.Edits {
			x, y, z := coords(key)
			ci := (z/16)*cn + x/16
			if !want[ci] {
				continue
			}
			idx := ((z&15)*16+(x&15))*128 + y
			grouped[ci] = appendChunkRecord(grouped[ci], p, idx, b, 128)
		}
		dim := p.room.dimIndex()
		out := []byte{chunkFrame(p), dim, 0, 0}
		count := 0
		flush := func() {
			if count > 0 {
				binary.LittleEndian.PutUint16(out[2:], uint16(count))
				p.enqueue(websocket.BinaryMessage, out)
			}
			out = []byte{chunkFrame(p), dim, 0, 0}
			count = 0
		}
		for _, ci := range needed {
			d := grouped[ci]
			if len(out)+6+len(d) > 60000 {
				flush()
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(ci))
			out = binary.LittleEndian.AppendUint16(out, uint16(len(d)/chunkRecordSize(p)))
			out = append(out, d...)
			count++
			p.known[ci] = true
		}
		flush()
	}
	s.syncDropsFor(p)
	s.syncMobsFor(p)
	s.syncRedstoneFor(p)
	s.syncTNTFor(p)
	s.syncMechanicsFor(p)
	s.syncTransportFor(p)
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func (s *Server) broadcastChunk(r *Room, key uint64, b int) {
	s.broadcastChunkFlags(r, key, b, false)
}
func (s *Server) reachable(p *Peer, m Message) bool {
	if !p.room.inside(m.X, m.Z) || m.Y < 1 || m.Y >= 128 || m.F < 0 || m.F > 5 {
		return false
	}
	cfg := s.anticheatConfig()
	reach := cfg.ReachDistance
	if !cfg.Enabled {
		reach = 64.0
	}
	st := p.state
	dx, dy, dz := float64(m.X)+0.5-st.X, float64(m.Y)+0.5-st.Y-1.62, float64(m.Z)+0.5-st.Z
	return dx*dx+dy*dy+dz*dz <= reach*reach
}
func (s *Server) survivalMessage(p *Peer, m Message) {
	st := p.state
	r := p.room
	if m.T == "sel" && m.Q > 0 && m.Q <= 2147483647 && m.I >= 0 && m.I < 9 {
		p.inventorySelQ = m.Q
		revision := p.inventoryRevision
		defer func() {
			if p.inventoryRevision == revision {
				s.inventory(p, 0, 0)
			}
		}()
	}
	if ack := s.placementAck(p, m); ack != nil {
		defer ack()
	}
	// Opt-in reconciliation, including silent refusals and plugin cancellation.
	// It is a state response, never a successful block/event notification.
	if m.T == "dig" && m.S == 2 && m.Q > 0 && m.Q <= 2147483647 && s.reachable(p, m) {
		defer func() {
			p.json(map[string]any{"t": "digack", "q": m.Q, "x": m.X, "y": m.Y, "z": m.Z, "b": s.block(r, m.X, m.Y, m.Z), "epoch": p.epoch, "dim": r.dimName()})
		}()
	}
	if m.T == "respawn" {
		if !st.Dead {
			return
		}
		if !s.allowed(api.PlayerRespawning, r, p, nil) {
			return
		}
		s.closeWindow(p)
		if s.respawnPlayer(p) {
			s.emit(api.PlayerRespawned, p.room, p, map[string]any{"reason": "respawn"})
		}
		return
	}
	if st.Dead {
		return
	}
	if s.runMessageHooks(p, m) {
		return
	}
	switch m.T {
	case "sel":
		if m.I >= 0 && m.I < 9 {
			data := map[string]any{"slot": m.I, "previous": st.Inv.Selected, "stack": itemSnapshot(st.Inv.Slots[m.I]), "previousStack": itemSnapshot(s.heldItem(p))}
			if !s.allowed(api.ItemSelecting, r, p, data) {
				s.inventory(p, 0, 0)
				return
			}
			st.Inv.Selected = m.I
			s.emit(api.ItemSelected, r, p, data)
			p.mining = nil
			p.useAt = time.Time{}
			r.Dirty = true
			s.inventory(p, 0, 0)
			s.broadcastPeerEquipment(p)
		}
	case "dig":
		if !s.reachable(p, m) {
			p.sys("目标太远，无法采集。")
			return
		}
		if m.S == 0 {
			for _, h := range digStartHooks {
				if h(s, p, m) {
					return
				}
			}
		}
		b := s.block(r, m.X, m.Y, m.Z)
		held := 0
		if v := st.Inv.Slots[st.Inv.Selected]; v != nil {
			held = v.ID
		}
		rule := s.rules.mine(b, held)
		if m.S == 1 {
			p.mining = nil
			return
		}
		if b == 0 {
			return
		}
		if rule.Ticks < 0 {
			p.sys("这个方块无法采集。")
			return
		}
		if r.Lock != 0 && !p.owner {
			p.sys("只有房主可以建造。")
			return
		}
		if m.S == 0 {
			p.mining = &digState{X: m.X, Y: m.Y, Z: m.Z, Block: b, Held: held, At: time.Now()}
			return
		}
		mine := p.mining
		if m.S != 2 || mine == nil || mine.X != m.X || mine.Y != m.Y || mine.Z != m.Z || mine.Block != b || mine.Held != held {
			p.sys("请重新开始采集。")
			return
		}
		if time.Since(mine.At) < time.Duration(float64(rule.Ticks)*0.7*s.digScale(p, b))*50*time.Millisecond {
			p.sys("采集尚未完成。")
			return
		}
		if c := r.Containers[blockKey(m.X, m.Y, m.Z)]; c != nil && !s.keepsContents(b) && c.Kind != "dispenser" && c.Kind != "dropper" {
			for _, v := range c.Slots {
				if v != nil {
					p.sys("请先取出容器里的物品。")
					return
				}
			}
		}
		drops := s.enchDrops(p, b, s.dropsAt(r, blockKey(m.X, m.Y, m.Z), b, held))
		key := blockKey(m.X, m.Y, m.Z)
		mechanicBreak := mechanicKinds[b] != "" || isRailBlock(b)
		if mechanicBreak && s.gameRule(r, "doTileDrops") {
			if v := mechanicSnapshot(r, key); v != nil && v.Record != nil {
				drops = append(drops, copyStack(v.Record))
			}
			if c := r.Containers[key]; c != nil {
				for _, v := range c.Slots {
					if v != nil {
						drops = append(drops, copyStack(v))
					}
				}
			}
		}
		var mechanicBreakData map[string]any
		allowedBreak := false
		if mechanicBreak {
			mechanicBreakData = s.mechanicBlockChangeData(r, p, key, 0, "mechanic_break")
			mechanicBreakData["contents"] = cloneStacks(drops)
			allowedBreak = s.allowed(api.BlockBreaking, r, p, mechanicBreakData) && s.mechanicBlockBefore(r, p, mechanicBreakData)
		} else {
			allowedBreak = s.allowBlock(p, key, 0)
		}
		if !allowedBreak {
			p.mining = nil
			s.broadcastChunk(r, key, b)
			return
		}
		toolChanged := rule.Wear > 0 && held > 0 && s.rules.byID[held].Durability > 0
		invData := map[string]any{"reason": "mine.tool", "slot": st.Inv.Selected, "wear": rule.Wear}
		var mechanicWearAfter *Stack
		var mechanicWearData map[string]any
		mechanicWearBefore := itemSnapshot(s.heldItem(p))
		if mechanicBreak && toolChanged {
			var ok bool
			mechanicWearAfter, mechanicWearData, ok = s.prepareTransportWear(p, s.heldItem(p), rule.Wear, "mechanic_mine", map[string]any{"target": map[string]any{"kind": "block", "x": m.X, "y": m.Y, "z": m.Z}})
			if !ok {
				p.mining = nil
				return
			}
			toolChanged = !reflect.DeepEqual(mechanicWearBefore, itemSnapshot(mechanicWearAfter))
			invData = map[string]any{"reason": "mechanic_mine", "actor": p.sid, "source": "player", "target": map[string]any{"kind": "block", "x": m.X, "y": m.Y, "z": m.Z}, "slot": st.Inv.Selected, "stack": mechanicWearBefore.Clone(), "before": mechanicWearBefore.Clone(), "after": itemSnapshot(mechanicWearAfter)}
		}
		if toolChanged && !s.allowed(api.InventoryChanging, r, p, invData) {
			p.mining = nil
			return
		}
		nextDropID := r.NextDropID
		prepared, err := s.prepareDropsWithContext(r, p, drops, float64(m.X)+.5, float64(m.Y)+.35, float64(m.Z)+.5, "block_break", nil, mechanicBreak || s.keepsContents(b))
		if err != nil {
			r.NextDropID = nextDropID
			p.sys(err.Error())
			p.mining = nil
			return
		}
		changes, err := s.prepareTorchChanges(r, p, key, 0, len(prepared))
		if err != nil {
			r.NextDropID = nextDropID

			p.sys(err.Error())
			p.mining = nil
			return
		}
		for other := range r.Peers {
			if other.window != nil && (other.window.Key == key || other.window.Key2 == key) {
				s.closeWindow(other)
				other.json(map[string]any{"t": "wclose", "w": other.nextWindow})
			}
		}
		if mechanicBreak && !reflect.DeepEqual(mechanicWearBefore, itemSnapshot(s.heldItem(p))) {
			r.NextDropID = nextDropID
			p.mining = nil
			return
		}
		if mechanicBreak && mechanicWearData != nil {
			st.Inv.Slots[st.Inv.Selected] = copyStack(mechanicWearAfter)
		}
		delete(r.Containers, key)
		for _, d := range prepared {
			r.Drops[d.ID] = d
		}
		if mechanicBreak {
			mechanicBreakData["before"] = mechanicWearBefore.Clone()
			mechanicBreakData["after"] = itemSnapshot(s.heldItem(p))
			s.commitPlayerBlock(p, key, 0, mechanicBreakData)
		} else {
			s.edit(p, key, 0)
		}
		s.applyTorchChanges(r, p, changes)
		s.runBreakHooks(p, m.X, m.Y, m.Z, b)
		if !mechanicBreak {
			s.wear(p, rule.Wear)
		} else if mechanicWearData != nil {
			s.emit(api.ItemWorn, r, p, mechanicWearData)
		}
		if toolChanged {
			s.emit(api.InventoryChanged, r, p, invData)
		}
		s.publishDrops(r, p, prepared, "block_break")
		st.Exhaustion += 0.005
		p.mining = nil
		s.inventory(p, 0, 0)
	case "place", "wopen":
		if !s.reachable(p, m) {
			p.sys("目标太远，无法交互。")
			return
		}
		target := s.block(r, m.X, m.Y, m.Z)
		name := baseName(s.rules.Names[target])
		key := blockKey(m.X, m.Y, m.Z)
		if m.T == "place" && s.runPlaceHooks(p, m, target) {
			return
		}
		held := st.Inv.Slots[st.Inv.Selected]
		isSign := held != nil && (held.ID == 383 || s.rules.byID[held.ID].Name == "Oak Sign")
		if p.flags&1 == 0 && !(m.T == "place" && name == "Chest" && isSign) {
			kind := map[string]string{"Crafting Table": "table", "Chest": "chest", "Furnace": "furnace"}[name]
			if kind != "" {
				s.openWindow(p, key, kind, m.Q)
				return
			}
		}
		if m.T == "wopen" {
			p.json(map[string]any{"t": "wopened", "q": m.Q, "w": 0})
			return
		}
		if r.Lock != 0 && !p.owner {
			p.sys("只有房主可以建造。")
			return
		}
		held = st.Inv.Slots[st.Inv.Selected]
		if held == nil {
			return
		}
		item, ok := s.rules.byID[held.ID]
		if !ok || item.PlacesID <= 0 || item.PlacesID >= len(s.rules.Names) {
			p.sys("这个物品不能作为方块放置。")
			return
		}
		// Multi-part blocks need additional rules; refuse them instead of consuming items.
		if item.Name == "Bed" || item.Name == "Oak Door" {
			p.sys("基础生存模式暂不支持放置床和门。")
			return
		}
		offsets := [][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
		at := offsets[m.F]
		x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
		placed := item.PlacesID
		if placed == torchBlock {
			placed = torchForFace(m.F)
			if s.rules.Kinds[target] == 5 && !isTorch(target) && name != "Fire" {
				x, y, z = m.X, m.Y, m.Z
				placed = torchBlock
			}
		}
		if !r.inside(x, z) || y < 1 || y >= 128 {
			return
		}
		if placed == 0 || !s.torchSupported(r, blockKey(x, y, z), placed) {
			p.sys("火把需要放在完整方块的顶面或侧面。")
			return
		}
		old := s.block(r, x, y, z)
		if old != 0 && !isFluid(old) && !(isTorch(placed) && s.rules.Kinds[old] == 5 && !isTorch(old) && baseName(s.rules.Names[old]) != "Fire") {
			return
		}
		if isTorch(placed) && isFluid(old) {
			return
		}
		for other := range r.Peers {
			a := other.state
			if a.Dead {
				continue
			}
			if !isTorch(placed) && float64(x+1) > a.X-0.3 && float64(x) < a.X+0.3 && float64(z+1) > a.Z-0.3 && float64(z) < a.Z+0.3 && float64(y+1) > a.Y && float64(y) < a.Y+1.8 {
				p.sys("不能在玩家身体里放置方块。")
				return
			}
		}
		if placed == blkShulkerBox {
			if !s.placePortableBox(p, x, y, z, held) {
				s.inventory(p, 0, 0)
			}
			return
		}
		before := itemSnapshot(held)
		after := copyStack(held)
		count := 0
		if !s.isCreative(p) {
			after = sized(held, held.Count-1)
			count = 1
		}
		invData := s.blockData(r, blockKey(x, y, z), placed)
		invData["reason"] = "place"
		invData["actor"] = p.sid
		invData["source"] = "player"
		invData["target"] = map[string]any{"kind": "block", "x": x, "y": y, "z": z}
		invData["item"] = held.ID
		invData["count"] = 1
		invData["consumed"] = count
		invData["slot"] = st.Inv.Selected
		invData["face"] = m.F
		invData["stack"] = before.Clone()
		invData["before"] = before.Clone()
		invData["after"] = itemSnapshot(after)
		invData["placedOnX"] = m.X
		invData["placedOnY"] = m.Y
		invData["placedOnZ"] = m.Z
		invData["placedOnBlock"] = target
		placedOnName := ""
		if target >= 0 && target < len(s.rules.Names) {
			placedOnName = baseName(s.rules.Names[target])
		}
		placedOnKey := s.registryBlockKey(target)
		if placedOnKey == "" {
			placedOnKey = placedOnName
		}
		invData["placedOnBlockName"] = placedOnName
		invData["placedOnBlockKey"] = placedOnKey
		if !s.allowBlock(p, blockKey(x, y, z), placed, invData) {
			s.broadcastChunk(r, blockKey(x, y, z), old)
			s.inventory(p, 0, 0)
			return
		}
		if !s.allowed(api.InventoryChanging, r, p, invData) {
			s.inventory(p, 0, 0)
			return
		}
		changes, err := s.prepareTorchChanges(r, p, blockKey(x, y, z), placed, 0)
		if err != nil {
			p.sys(err.Error())
			return
		}
		m.PlacementStack = sized(held, 1)
		if !reflect.DeepEqual(before, itemSnapshot(s.heldItem(p))) {
			s.inventory(p, 0, 0)
			return
		}
		st.Inv.Slots[st.Inv.Selected] = after
		s.commitPlayerBlock(p, blockKey(x, y, z), placed, invData)
		s.applyTorchChanges(r, p, changes)
		s.afterPlace(p, x, y, z, placed, m)
		s.emit(api.InventoryChanged, r, p, invData)
		s.inventory(p, 0, 0)
	case "click":
		s.click(p, m)
	case "wclose":
		if m.W == p.windowID() {
			s.closeWindow(p)
			s.inventory(p, 0, 0)
		}
	case "drop":
		p.sys("基础生存模式暂不支持丢弃物品，物品已保留。")
		p.json(map[string]any{"t": "dropack", "d": m.D, "ids": []int{}})
		s.inventory(p, 0, 0)
	case "use":
		held := st.Inv.Slots[st.Inv.Selected]
		if held == nil {
			return
		}
		it := s.rules.byID[held.ID]
		if slot := s.equipmentSlot(held); slot >= 0 && m.S == 0 {
			s.equipHeld(p, slot)
			return
		}
		if it.Food == nil || st.Food >= 20 && !it.Food.Always {
			return
		}
		if m.S == 0 {
			p.useAt = time.Now()
			p.useSlot = st.Inv.Selected
		} else if m.S == 1 {
			p.useAt = time.Time{}
		} else if m.S == 2 && !p.useAt.IsZero() && p.useSlot == st.Inv.Selected && time.Since(p.useAt) >= 1500*time.Millisecond {
			data := map[string]any{"item": held.ID, "count": 1, "slot": st.Inv.Selected, "stack": itemSnapshot(held)}
			if !s.allowed(api.ItemConsuming, r, p, data) {
				p.useAt = time.Time{}
				return
			}
			data["reason"] = "consume"
			if !s.allowed(api.InventoryChanging, r, p, data) {
				p.useAt = time.Time{}
				return
			}
			st.Food = min(20, st.Food+it.Food.Hunger)
			st.Saturation = min(float64(st.Food), st.Saturation+it.Food.Saturation)
			st.Inv.Slots[st.Inv.Selected] = sized(held, held.Count-1)
			p.useAt = time.Time{}
			r.Dirty = true
			s.emit(api.ItemConsumed, r, p, data)
			s.emit(api.InventoryChanged, r, p, data)
			p.json(map[string]any{"t": "itemfx", "k": "eat", "i": held.ID})
			s.inventory(p, 0, 0)
			s.health(p)
		}
	case "attack":
		if time.Since(p.attackAt) < s.attackCooldown(p) {
			return
		}
		for other := range r.Peers {
			if other.id != m.P || other == p || other.state == nil || other.state.Dead {
				continue
			}
			dx, dy, dz := other.state.X-st.X, other.state.Y-st.Y, other.state.Z-st.Z
			reach := s.anticheatConfig().AttackReach
			if dx*dx+dy*dy+dz*dz > reach*reach || !s.lineClear(r, [3]float64{st.X, st.Y + 1.62, st.Z}, [3]float64{other.state.X, other.state.Y + 1, other.state.Z}) {
				return
			}

			// 暴击判定：客户端命中时计算的暴击提示
			isCrit := m.Sound == "crit"
			baseAmount := s.attributes(p)[api.AttackDamage]
			if isCrit {
				baseAmount *= 1.5
			}

			ctx := s.playerAttack(p, playerTarget(other), baseAmount)
			if ctx != nil && s.damagePlayer(other, ctx.Amount, "被玩家击败了。", ctx) {
				p.attackAt = time.Now()
				s.wear(p, 1)
				s.inventory(p, 0, 0)
				s.emit(api.PlayerAttacked, r, p, damageData(ctx, "player_attack"))

				// 1. 击退（Knockback）冲量计算
				kdx := other.state.X - st.X
				kdz := other.state.Z - st.Z
				dist := math.Hypot(kdx, kdz)
				if dist < 0.001 {
					yaw := st.Yaw
					kdx = -math.Sin(yaw)
					kdz = -math.Cos(yaw)
					dist = 1.0
				}
				dirX := kdx / dist
				dirZ := kdz / dist

				// 基础击退 0.45，疾跑攻击 +0.45，击退附魔每级 +0.45
				strength := 0.45
				if m.Sp == 1 {
					strength += 0.45
				}
				if knockLvl := enchOf(s.heldItem(p), "knockback"); knockLvl > 0 {
					strength += float64(knockLvl) * 0.45
				}

				// 向受击者下发带有物理击退冲量的 hurt 数据包
				other.json(map[string]any{
					"t":  "hurt",
					"f":  true,
					"kb": [][]float64{{strength, -dirX, -dirZ}},
				})

				appliedDmg := ctx.AppliedAmount
				if appliedDmg <= 0 {
					appliedDmg = ctx.Amount
				}

				headY := other.state.Y + 1.8

				// 2. 全房间广播受击发红（phurt）
				broadcastWorld(r, map[string]any{
					"t":        "phurt",
					"id":       other.id,
					"attacker": p.id,
					"damage":   appliedDmg,
					"crit":     isCrit,
					"x":        other.state.X,
					"y":        headY,
					"z":        other.state.Z,
				}, nil)

				// 3. 全房间广播头顶网游伤害飘字（dmg）
				broadcastWorld(r, map[string]any{
					"t":          "dmg",
					"targetType": "player",
					"id":         other.id,
					"amount":     appliedDmg,
					"crit":       isCrit,
					"x":          other.state.X,
					"y":          headY + 0.25,
					"z":          other.state.Z,
				}, nil)

				// 4. 广播战斗打击音效
				soundType := "strong"
				if isCrit {
					soundType = "crit"
				} else if m.Sp == 1 {
					soundType = "knockback"
				}
				soundAt(r, "entity.player.attack."+soundType, other.state.X, headY, other.state.Z, nil)
				if m.Sp == 1 || strength > 0.45 {
					soundAt(r, "entity.player.attack.knockback", other.state.X, headY, other.state.Z, nil)
				}
				soundAt(r, "entity.player.hurt", other.state.X, headY, other.state.Z, nil)
			}
			break
		}
	default:
		// Ambient or unrecognized client packet: silently ignore instead of spamming player chat.
	}
}
func (s *Server) health(p *Peer) {
	st := p.state
	msg := map[string]any{"t": "hp", "max": s.maxHealth(p), "attrs": s.attributes(p), "h": st.Health, "f": st.Food, "s": st.Saturation, "a": p.air, "dd": st.Dead, "sp": false, "fi": false}
	s.healthExtra(p, msg)
	p.json(msg)
}
func (s *Server) deathMessage(p *Peer) {
	p.json(s.deathInfo(p))
}
func (s *Server) life(p *Peer) {
	st := p.state
	if st.Dead {
		return
	}
	if s.lifeExt(p) {
		return
	}
	if st.Y < -20 {
		s.damage(p, 20, "掉出了世界。")
		return
	}
	x, y, z := int(math.Floor(st.X)), int(math.Floor(st.Y)), int(math.Floor(st.Z))
	feet := s.block(p.room, x, y, z)
	headY := st.Y + 1.8
	headBlockY := int(math.Floor(headY))
	headBlock := s.block(p.room, x, headBlockY, z)
	headInWater := isWater(headBlock)
	if headInWater {
		aboveWater := isWater(s.block(p.room, x, headBlockY+1, z))
		surfaceY := float64(headBlockY) + 0.875
		if aboveWater {
			surfaceY = float64(headBlockY) + 1.0
		}
		if headY >= surfaceY {
			headInWater = false
		}
	}
	if (isLava(feet) || isLava(headBlock)) && !s.hasEffect(p, "fire_resistance") {
		s.damage(p, 4, "掉入了熔岩。")
		s.ignite(p, 300)
	}
	oldAir := p.air
	if headInWater && !s.hasEffect(p, "water_breathing") && s.gameMode(p) != "creative" && s.gameMode(p) != "spectator" {
		p.air = max(0, p.air-20)
		if p.air == 0 {
			s.damage(p, 2, "溺水了。")
			soundAt(p.room, "entity.player.hurt_drown", st.X, st.Y+1.5, st.Z, nil)
		}
	} else {
		p.air = min(300, p.air+100)
	}
	p.ext().Air = p.air
	if p.air != oldAir {
		s.health(p)
	}
	below := s.block(p.room, x, int(math.Floor(st.Y-0.05)), z)
	solid := below >= 0 && below < len(s.rules.Kinds) && (s.rules.Kinds[below] == 1 || s.rules.Kinds[below] == 2 || s.rules.Kinds[below] == 3)
	p.peakY = max(p.peakY, st.Y)
	if solid && !isWater(feet) && !s.softLanding(p) {
		fall := p.peakY - st.Y
		if fall > 3 {
			s.damage(p, math.Floor(fall-3), "摔落了。")
		}
		p.peakY = st.Y
	}
	if isWater(feet) || isClimbable(feet) {
		p.peakY = st.Y
	}
	for st.Exhaustion >= 4 {
		st.Exhaustion -= 4
		if st.Saturation > 0 {
			st.Saturation = max(0, st.Saturation-1)
		} else {
			st.Food = max(0, st.Food-1)
		}
	}
	if time.Since(p.lastHealth) >= 4*time.Second {
		p.lastHealth = time.Now()
		if st.Food >= 18 && st.Health < s.maxHealth(p) {
			if s.heal(p, 1, "regeneration") {
				st.Exhaustion += 6
			}
		} else if st.Food == 0 && st.Health > 1 {
			s.damage(p, 1, "饿倒了。")
		}
	}
	p.room.Dirty = true
	s.health(p)
}
