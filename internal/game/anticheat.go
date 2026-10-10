package game

import (
	"math"
	"time"
)

// ---------------------------------------------------------------------------
// 服务端运动模拟与反作弊系统 (anticheat.go):
// 1. 速度检测 (Speed Check):
//    - 校验水平移动速度，防止变速齿轮、瞬移与超速疾跑 (Teleport/Speed Hack)
//    - 动态根据玩家状态（疾跑、潜行、跳跃、药水效果、水阻）计算允许上限
// 2. 穿墙与相交检测 (Noclip/Phase Check):
//    - 采用步进采样检测移动路径上的坚硬实体方块，防止穿墙透视/瞬移穿墙
// 3. 滞空与飞行检测 (Fly Hack Check):
//    - 生存模式下无飞行特权时，检测玩家是否脱离地面支撑持续悬空或升空
//    - 连续悬空无重力下落判定为非法飞行并回退拉回 (Setback)
// 4. 超距交互与攻击反作弊 (Reach Check):
//    - 限制挖掘方块交互距离 (<= 6.5 格)
//    - 限制近战攻击实体距离 (<= 4.5 格)
// ---------------------------------------------------------------------------

type PlayerMovementState struct {
	LastX, LastY, LastZ float64
	LastTime            time.Time
	AirTicks            int
	ViolationLevel      float64
	HasLastPos          bool
}

var movementStates = map[uint16]*PlayerMovementState{}

func getMovementState(p *Peer) *PlayerMovementState {
	st := movementStates[p.id]
	if st == nil {
		st = &PlayerMovementState{
			LastX:    p.state.X,
			LastY:    p.state.Y,
			LastZ:    p.state.Z,
			LastTime: time.Now(),
		}
		movementStates[p.id] = st
	}
	return st
}

// 检查方块是否为坚固阻挡方块 (不可穿过)
func (s *Server) isSolidBlockForPlayer(r *Room, x, y, z int) bool {
	if y < 0 || y >= 128 || !r.inside(x, z) {
		return false
	}
	b := s.block(r, x, y, z)
	k := s.rules.Kinds[b]
	// kind 1 (solid), kind 2, kind 3
	return k == 1 || k == 2 || k == 3
}

// 检查玩家包围盒在此坐标是否卡在方块内
func (s *Server) playerCollidesWithWorld(r *Room, px, py, pz float64) bool {
	// 玩家宽度约 0.6 (半径 0.3)，高度 1.8
	const rX = 0.28
	const rZ = 0.28
	const hY = 1.75

	minX := int(math.Floor(px - rX))
	maxX := int(math.Floor(px + rX))
	minY := int(math.Floor(py + 0.1))
	maxY := int(math.Floor(py + hY))
	minZ := int(math.Floor(pz - rZ))
	maxZ := int(math.Floor(pz + rZ))

	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if s.isSolidBlockForPlayer(r, x, y, z) {
					return true
				}
			}
		}
	}
	return false
}

// 检查从 A 到 B 的路径是否穿墙
func (s *Server) checkPlayerPathPhase(r *Room, fromX, fromY, fromZ, toX, toY, toZ float64) bool {
	dist := math.Sqrt((toX-fromX)*(toX-fromX) + (toY-fromY)*(toY-fromY) + (toZ-fromZ)*(toZ-fromZ))
	steps := max(1, int(math.Ceil(dist*4)))
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		cx := fromX + (toX-fromX)*t
		cy := fromY + (toY-fromY)*t
		cz := fromZ + (toZ-fromZ)*t
		if s.playerCollidesWithWorld(r, cx, cy, cz) {
			return true
		}
	}
	return false
}

// 检查玩家脚下周围是否有立足支撑
func (s *Server) playerHasGroundSupport(r *Room, px, py, pz float64) bool {
	belowY := int(math.Floor(py - 0.1))
	if belowY < 0 {
		return true
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx := int(math.Floor(px + float64(dx)*0.3))
			bz := int(math.Floor(pz + float64(dz)*0.3))
			b := s.block(r, bx, belowY, bz)
			if s.isSolidBlockForPlayer(r, bx, belowY, bz) || isFluid(b) {
				return true
			}
			// 梯子攀爬
			curB := s.block(r, bx, int(math.Floor(py)), bz)
			if s.rules.byName["Ladder"] > 0 && curB == s.rules.byName["Ladder"] {
				return true
			}
		}
	}
	return false
}

// 服务端运动反作弊核心校验器
func (s *Server) validatePlayerMovement(p *Peer, toX, toY, toZ float64) (valid bool, reason string) {
	r := p.room
	if r == nil || s.isSpectator(p) || r.Mode == "creative" {
		return true, ""
	}

	st := p.state
	mov := getMovementState(p)
	now := time.Now()
	dt := now.Sub(mov.LastTime).Seconds()
	if dt <= 0.001 {
		dt = 0.05
	}
	if dt > 1.0 {
		// 长时间未移动或刚连接，重置基准点
		mov.LastX, mov.LastY, mov.LastZ = toX, toY, toZ
		mov.LastTime = now
		mov.HasLastPos = true
		return true, ""
	}

	fromX, fromY, fromZ := st.X, st.Y, st.Z
	if mov.HasLastPos {
		fromX, fromY, fromZ = mov.LastX, mov.LastY, mov.LastZ
	}

	hDist := math.Hypot(toX-fromX, toZ-fromZ)
	hSpeed := hDist / dt

	// 1. 速度检测 (Speed Check)
	maxAllowedSpeed := 8.5 // 包含疾跑跳跃余量 (7.1m/s) + 网络延迟抖动余量
	if p.state.Ext != nil && p.state.Ext.Effects != nil {
		if spdLvl := p.state.Ext.Effects["speed"]; spdLvl != nil && spdLvl.Amp >= 0 {
			maxAllowedSpeed *= (1.0 + 0.2*float64(spdLvl.Amp+1))
		}
	}
	// 瞬间瞬移或严重超速
	if hDist > 10.0 || hSpeed > maxAllowedSpeed*1.8 {
		return false, "移动速度异常 (Speed/Teleport)"
	}

	// 2. 穿墙检测 (Noclip/Phase Check)
	if s.checkPlayerPathPhase(r, fromX, fromY, fromZ, toX, toY, toZ) {
		return false, "非法穿墙移动 (Noclip/Phase)"
	}

	// 3. 非法飞行/悬空检测 (Fly Hack Check)
	if !s.mayFly(p) {
		onGround := s.playerHasGroundSupport(r, toX, toY, toZ)
		if !onGround {
			mov.AirTicks++
			// 连续在空中超过 1.5 秒 (约 15 个数据帧)，且 Y 轴未表现出正常的重力坠落而是在平移或持续升空
			if mov.AirTicks > 25 && toY >= fromY-0.05 {
				return false, "生存模式非法飞行或滞空 (Fly Hack)"
			}
		} else {
			mov.AirTicks = 0
		}
	} else {
		mov.AirTicks = 0
	}

	// 验证通过，更新基准点
	mov.LastX, mov.LastY, mov.LastZ = toX, toY, toZ
	mov.LastTime = now
	mov.HasLastPos = true
	return true, ""
}

// 检查交互距离 (Reach Check)
func (s *Server) checkInteractionReach(p *Peer, targetX, targetY, targetZ float64, maxDist float64) bool {
	if s.isCreative(p) || s.isSpectator(p) {
		return true
	}
	dx := p.state.X - targetX
	dy := (p.state.Y + 1.62) - targetY
	dz := p.state.Z - targetZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	return dist <= maxDist
}

func init() {
	// 在玩家每次移动时，挂载反作弊校验
	onPeerTick(func(s *Server, p *Peer) {
		if p.closed || p.state == nil || p.state.Dead {
			return
		}
		// 定期更新和清理超期数据
		mov := getMovementState(p)
		if time.Since(mov.LastTime) > 3*time.Second {
			mov.LastX, mov.LastY, mov.LastZ = p.state.X, p.state.Y, p.state.Z
			mov.LastTime = time.Now()
			mov.AirTicks = 0
		}
	})
}
