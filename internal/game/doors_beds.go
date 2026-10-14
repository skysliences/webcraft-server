package game

import (
	"math"
	"strings"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 门、床、栅栏门与睡眠逻辑：
// 1. 门：两格高（下半部/上半部），朝向根据玩家 yaw，右键开/关，上下联动；红石激活开门
// 2. 栅栏门：四向开闭，右键开/关，红石开门
// 3. 床：两格长（脚部/头部），右键睡觉，设置出生点，全员跳夜；下界/末地爆炸
// 4. 起床：wake 消息唤醒玩家
// ---------------------------------------------------------------------------

const (
	blkDoorBase       = 110 // Oak Door
	blkDoorSouth      = 111
	blkDoorWest       = 112
	blkDoorEast       = 113
	blkDoorOpenBase   = 114
	blkDoorUpper      = 118
	blkDoorUpperRight = 144

	blkGateBase     = 120 // Oak Fence Gate
	blkGateOpenBase = 124

	blkBedBase = 132 // Bed
	blkBedHead = 136 // Bed (Head)
)

func isDoor(b int) bool {
	return b >= 110 && b <= 118 || b == 144
}

func isFenceGate(b int) bool {
	return b >= 120 && b <= 127
}

func isBed(b int) bool {
	return b >= 132 && b <= 139
}

// 门的方向：0=North(Z-), 1=South(Z+), 2=West(X-), 3=East(X+)
func playerFacing(yaw float64) int {
	y := math.Mod(yaw*180/math.Pi, 360)
	if y < 0 {
		y += 360
	}
	switch {
	case y >= 315 || y < 45:
		return 1 // South
	case y >= 45 && y < 135:
		return 2 // West
	case y >= 135 && y < 225:
		return 0 // North
	default:
		return 3 // East
	}
}

// 门下半部对应的开/关切换
func toggleDoorBlock(b int) int {
	switch b {
	case 110:
		return 114
	case 111:
		return 115
	case 112:
		return 116
	case 113:
		return 117
	case 114:
		return 110
	case 115:
		return 111
	case 116:
		return 112
	case 117:
		return 113
	}
	return b
}

func toggleGateBlock(b int) int {
	if b >= 120 && b <= 123 {
		return b + 4
	}
	if b >= 124 && b <= 127 {
		return b - 4
	}
	return b
}

// 检查是否全员入睡跳夜
func checkSleepNightSkip(r *Room, s *Server) {
	if r.Mode != "survival" || r.dimName() != "overworld" || len(r.Peers) == 0 {
		return
	}
	if !isNight(r.root().Time) {
		return
	}
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) {
			continue
		}
		if p.ext().SpawnSet && p.flags&4 == 0 {
			// 有人还没睡着
			return
		}
	}
	// 全部睡着，推进到黎明
	newTime := (r.root().Time/24000 + 1) * 24000
	s.setWorldTime(r, nil, newTime, "sleep")
	s.emit(api.WorldNightSkipped, r, nil, map[string]any{"time": newTime})
	for p := range r.Peers {
		p.flags &^= 4
		p.json(map[string]any{"t": "sleep", "on": 0})
	}
}

func init() {
	// 1. 放置门和床 (在 survival.go 默认拒绝前用 onPlace 拦截并成功放置两格)
	onPlace(2, func(s *Server, p *Peer, m Message, target int) bool {
		r := p.room
		// 右键开关门/栅栏门/使用床（优先于放置新门/床，空手或手持任何物品均可交互）
		tName := s.fullName(target)
		if isDoor(target) {
			lowY := m.Y
			if strings.Contains(tName, "Upper") {
				lowY = m.Y - 1
			}
			lowBlock := s.block(r, m.X, lowY, m.Z)
			toggled := toggleDoorBlock(lowBlock)
			if toggled != lowBlock {
				if !s.allowed(api.DoorToggling, r, p, map[string]any{"x": m.X, "y": lowY, "z": m.Z, "block": lowBlock, "next_block": toggled}) {
					return true
				}
				s.setBlock(r, m.X, lowY, m.Z, toggled, setQuiet)
				sound := "block.wooden_door.open"
				if strings.Contains(s.fullName(toggled), "Open") {
					sound = "block.wooden_door.close"
				}
				soundAt(r, sound, float64(m.X)+.5, float64(lowY)+1, float64(m.Z)+.5, nil)
				s.emit(api.DoorToggled, r, p, map[string]any{"x": m.X, "y": lowY, "z": m.Z, "block": toggled})
				return true
			}
		}

		if isFenceGate(target) {
			toggled := toggleGateBlock(target)
			if !s.allowed(api.DoorToggling, r, p, map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "block": target, "next_block": toggled}) {
				return true
			}
			s.setBlock(r, m.X, m.Y, m.Z, toggled, setQuiet)
			sound := "block.fence_gate.open"
			if strings.Contains(s.fullName(toggled), "Open") {
				sound = "block.fence_gate.close"
			}
			soundAt(r, sound, float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
			s.emit(api.DoorToggled, r, p, map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "block": toggled})
			return true
		}

		if isBed(target) {
			// 在下界或末地睡觉会发生剧烈爆炸
			if r.dimName() != "overworld" {
				s.explode(r, float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, 5, true)
				return true
			}
			e := p.ext()
			e.SpawnX, e.SpawnY, e.SpawnZ = m.X, m.Y, m.Z
			e.SpawnSet = true
			p.sys("重生点已设置。")
			if !isNight(r.root().Time) {
				p.sys("只有在夜晚才能睡觉。")
				return true
			}
			if !s.allowed(api.PlayerSleeping, r, p, map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "block": target}) {
				return true
			}
			p.flags |= 4 // 睡觉标记
			p.json(map[string]any{"t": "sleep", "on": 1})
			s.emit(api.PlayerSlept, r, p, map[string]any{"x": m.X, "y": m.Y, "z": m.Z, "block": target})
			checkSleepNightSkip(r, s)
			return true
		}

		v := s.heldItem(p)
		if v == nil {
			return false
		}
		facing := playerFacing(p.state.Yaw)
		dx, dz := 0, 0
		switch facing {
		case 0:
			dz = -1 // North
		case 1:
			dz = 1  // South
		case 2:
			dx = -1 // West
		case 3:
			dx = 1  // East
		}

		// 放置橡木门 (Oak Door 110)
		if v.ID == 110 {
			offsets := faceOffsets
			at := offsets[m.F]
			x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
			if !r.inside(x, z) || y < 1 || y >= 127 {
				return true
			}
			if s.block(r, x, y, z) != 0 || s.block(r, x, y+1, z) != 0 || !s.solidAt(r, x, y-1, z) {
				p.sys("门需要放在完整方块上，且需要两格高空间。")
				return true
			}
			doorLower := blkDoorBase + facing
			if !s.allowBlock(p, blockKey(x, y, z), doorLower) || !s.allowBlock(p, blockKey(x, y+1, z), blkDoorUpper) {
				s.broadcastChunk(r, blockKey(x, y, z), s.block(r, x, y, z))
				s.broadcastChunk(r, blockKey(x, y+1, z), s.block(r, x, y+1, z))
				s.inventory(p, 0, 0)
				return true
			}
			s.setBlock(r, x, y, z, doorLower, setQuiet)
			s.setBlock(r, x, y+1, z, blkDoorUpper, setQuiet)
			s.consumeHeld(p, 1)
			soundAt(r, "block.wooden_door.close", float64(x)+.5, float64(y)+1, float64(z)+.5, nil)
			s.inventory(p, 0, 0)
			return true
		}

		// 放置床 (Bed 132)
		if v.ID == 132 {
			offsets := faceOffsets
			at := offsets[m.F]
			x, y, z := m.X+at[0], m.Y+at[1], m.Z+at[2]
			hx, hz := x+dx, z+dz
			if !r.inside(x, z) || !r.inside(hx, hz) || y < 1 || y >= 127 {
				return true
			}
			if s.block(r, x, y, z) != 0 || s.block(r, hx, y, hz) != 0 || !s.solidAt(r, x, y-1, z) || !s.solidAt(r, hx, y-1, hz) {
				p.sys("床需要平坦的两个完整方块空间。")
				return true
			}
			bedFoot := blkBedBase + facing
			bedHead := blkBedHead + facing
			if !s.allowBlock(p, blockKey(x, y, z), bedFoot) || !s.allowBlock(p, blockKey(hx, y, hz), bedHead) {
				s.broadcastChunk(r, blockKey(x, y, z), s.block(r, x, y, z))
				s.broadcastChunk(r, blockKey(hx, y, hz), s.block(r, hx, y, hz))
				s.inventory(p, 0, 0)
				return true
			}
			s.setBlock(r, x, y, z, bedFoot, setQuiet)
			s.setBlock(r, hx, y, hz, bedHead, setQuiet)
			s.consumeHeld(p, 1)
			soundAt(r, "block.wood.place", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
			s.inventory(p, 0, 0)
			return true
		}

		return false
	})

	// 2. 唤醒消息处理
	onMessage("wake", func(s *Server, p *Peer, m Message) bool {
		if !s.allowed(api.PlayerWaking, p.room, p, map[string]any{}) {
			return true
		}
		p.flags &^= 4
		p.json(map[string]any{"t": "sleep", "on": 0})
		s.emit(api.PlayerWoke, p.room, p, map[string]any{})
		return true
	})

	// 3. 门与床的破坏联动 (破坏其中一半，另一半连带被移除)
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		r := p.room
		if isDoor(old) {
			if strings.Contains(s.fullName(old), "Upper") {
				// 上半部分被破坏，移除下半部分
				if isDoor(s.block(r, x, y-1, z)) {
					s.setBlock(r, x, y-1, z, 0, 0)
				}
			} else {
				// 下半部分被破坏，移除上半部分
				if isDoor(s.block(r, x, y+1, z)) {
					s.setBlock(r, x, y+1, z, 0, 0)
				}
			}
			delete(doorPoweredState, blockKey(x, y, z))
			delete(doorPoweredState, blockKey(x, y-1, z))
			delete(doorPoweredState, blockKey(x, y+1, z))
		}

		if isFenceGate(old) {
			delete(doorPoweredState, blockKey(x, y, z))
		}

		if isBed(old) {
			// 查找相邻相连的另一半床
			for _, d := range faceOffsets {
				nx, nz := x+d[0], z+d[2]
				if isBed(s.block(r, nx, y, nz)) {
					s.setBlock(r, nx, y, nz, 0, 0)
					break
				}
			}
		}
	})

	// 4. 红石触发门开闭 (边沿触发模式，避免误关玩家手动打开的门)
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if !isDoor(b) && !isFenceGate(b) {
			return
		}
		if isDoor(b) {
			lowY := y
			if strings.Contains(s.fullName(b), "Upper") {
				lowY = y - 1
			}
			lowBlock := s.block(r, x, lowY, z)
			if !isDoor(lowBlock) {
				return
			}
			k := blockKey(x, lowY, z)
			powered := s.blockPowered(r, x, lowY, z) || s.blockPowered(r, x, lowY+1, z)
			lastPowered, tracked := doorPoweredState[k]
			// 红石供电状态未改变时，保持当前开闭状态，绝不自动关闭玩家手动开启的门
			if tracked && powered == lastPowered {
				return
			}
			doorPoweredState[k] = powered

			isOpen := strings.Contains(s.fullName(lowBlock), "Open")
			if powered && !isOpen {
				toggled := toggleDoorBlock(lowBlock)
				s.setBlock(r, x, lowY, z, toggled, setQuiet)
				soundAt(r, "block.wooden_door.open", float64(x)+.5, float64(lowY)+1, float64(z)+.5, nil)
			} else if !powered && isOpen && tracked && lastPowered {
				// 仅当先前是由红石充能打开、且现在信号丢失时才自动关闭
				toggled := toggleDoorBlock(lowBlock)
				s.setBlock(r, x, lowY, z, toggled, setQuiet)
				soundAt(r, "block.wooden_door.close", float64(x)+.5, float64(lowY)+1, float64(z)+.5, nil)
			}
		} else if isFenceGate(b) {
			k := blockKey(x, y, z)
			powered := s.blockPowered(r, x, y, z)
			lastPowered, tracked := doorPoweredState[k]
			if tracked && powered == lastPowered {
				return
			}
			doorPoweredState[k] = powered

			isOpen := strings.Contains(s.fullName(b), "Open")
			if powered && !isOpen {
				toggled := toggleGateBlock(b)
				s.setBlock(r, x, y, z, toggled, setQuiet)
			} else if !powered && isOpen && tracked && lastPowered {
				toggled := toggleGateBlock(b)
				s.setBlock(r, x, y, z, toggled, setQuiet)
			}
		}
	})
}

var doorPoweredState = map[uint64]bool{}


var _ = api.ItemMeta{}
