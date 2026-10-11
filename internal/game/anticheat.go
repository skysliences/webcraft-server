package game

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
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
//    - 限制挖掘方块交互距离 (默认 <= 6.5 格，可配置)
//    - 限制近战攻击实体距离 (默认 <= 4.5 格，可配置)
// ---------------------------------------------------------------------------

// AnticheatConfig 反作弊可配置参数
type AnticheatConfig struct {
	Enabled         bool    `json:"enabled"`         // 反作弊总开关（默认 true）
	ReachDistance   float64 `json:"reachDistance"`   // 方块交互与挖掘距离（默认 6.5 格）
	AttackReach     float64 `json:"attackReach"`     // 实体近战攻击距离（默认 4.5 格）
	MaxSpeed        float64 `json:"maxSpeed"`        // 基准移动速度上限（默认 8.5 m/s）
	SpeedTolerance  float64 `json:"speedTolerance"`  // 速度/瞬移容差倍率（默认 1.8）
	MaxTeleportDist float64 `json:"maxTeleportDist"` // 单次移动最大瞬移距离（默认 10.0 格）
	NoclipCheck     bool    `json:"noclipCheck"`     // 穿墙/相交检测开关（默认 true）
	FlyCheck        bool    `json:"flyCheck"`        // 飞行/滞空检测开关（默认 true）
	MaxAirTicks     int     `json:"maxAirTicks"`     // 允许连续滞空而不触发 Fly 违规的最大 tick 数（默认 25）
}

func DefaultAnticheatConfig() AnticheatConfig {
	return AnticheatConfig{
		Enabled:         false, // 暂时默认关闭反作弊
		ReachDistance:   6.5,
		AttackReach:     4.5,
		MaxSpeed:        8.5,
		SpeedTolerance:  1.8,
		MaxTeleportDist: 10.0,
		NoclipCheck:     false, // 暂时默认关闭
		FlyCheck:        false, // 暂时默认关闭
		MaxAirTicks:     25,
	}
}

func (s *Server) anticheatConfig() AnticheatConfig {
	s.anticheatMu.RLock()
	defer s.anticheatMu.RUnlock()
	return s.anticheat
}

func (s *Server) setAnticheatConfig(cfg AnticheatConfig) {
	s.anticheatMu.Lock()
	defer s.anticheatMu.Unlock()
	s.anticheat = cfg
}

func (s *Server) initAnticheat() {
	cfg := s.config.Anticheat
	def := DefaultAnticheatConfig()
	if cfg.ReachDistance <= 0 && cfg.AttackReach <= 0 && cfg.MaxSpeed <= 0 {
		cfg = def
	} else {
		if cfg.ReachDistance <= 0 {
			cfg.ReachDistance = def.ReachDistance
		}
		if cfg.AttackReach <= 0 {
			cfg.AttackReach = def.AttackReach
		}
		if cfg.MaxSpeed <= 0 {
			cfg.MaxSpeed = def.MaxSpeed
		}
		if cfg.SpeedTolerance <= 0 {
			cfg.SpeedTolerance = def.SpeedTolerance
		}
		if cfg.MaxTeleportDist <= 0 {
			cfg.MaxTeleportDist = def.MaxTeleportDist
		}
		if cfg.MaxAirTicks <= 0 {
			cfg.MaxAirTicks = def.MaxAirTicks
		}
	}
	// 支持从 DATA_DIR/anticheat.json 读取覆盖
	if s.config.DataDir != "" {
		p := filepath.Join(s.config.DataDir, "anticheat.json")
		if data, err := os.ReadFile(p); err == nil {
			var fileCfg AnticheatConfig
			if err := json.Unmarshal(data, &fileCfg); err == nil {
				if fileCfg.ReachDistance > 0 {
					cfg.ReachDistance = fileCfg.ReachDistance
				}
				if fileCfg.AttackReach > 0 {
					cfg.AttackReach = fileCfg.AttackReach
				}
				if fileCfg.MaxSpeed > 0 {
					cfg.MaxSpeed = fileCfg.MaxSpeed
				}
				if fileCfg.SpeedTolerance > 0 {
					cfg.SpeedTolerance = fileCfg.SpeedTolerance
				}
				if fileCfg.MaxTeleportDist > 0 {
					cfg.MaxTeleportDist = fileCfg.MaxTeleportDist
				}
				if fileCfg.MaxAirTicks > 0 {
					cfg.MaxAirTicks = fileCfg.MaxAirTicks
				}
				cfg.Enabled = fileCfg.Enabled
				cfg.NoclipCheck = fileCfg.NoclipCheck
				cfg.FlyCheck = fileCfg.FlyCheck
			}
		}
	}
	s.setAnticheatConfig(cfg)
}

func (s *Server) reloadAnticheatConfig() error {
	if s.config.DataDir == "" {
		return nil
	}
	p := filepath.Join(s.config.DataDir, "anticheat.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var fileCfg AnticheatConfig
	if err := json.Unmarshal(data, &fileCfg); err != nil {
		return err
	}
	s.setAnticheatConfig(fileCfg)
	return nil
}

type PlayerMovementState struct {
	LastX, LastY, LastZ float64
	LastTime            time.Time
	AirTicks            int
	ViolationLevel      float64
	HasLastPos          bool
}

var (
	movementStates   = map[uint16]*PlayerMovementState{}
	movementStatesMu sync.Mutex
)

func getMovementState(p *Peer) *PlayerMovementState {
	movementStatesMu.Lock()
	defer movementStatesMu.Unlock()
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

	cfg := s.anticheatConfig()
	if !cfg.Enabled {
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
	maxAllowedSpeed := cfg.MaxSpeed
	if p.state.Ext != nil && p.state.Ext.Effects != nil {
		if spdLvl := p.state.Ext.Effects["speed"]; spdLvl != nil && spdLvl.Amp >= 0 {
			maxAllowedSpeed *= (1.0 + 0.2*float64(spdLvl.Amp+1))
		}
	}
	// 瞬间瞬移或严重超速
	if hDist > cfg.MaxTeleportDist || hSpeed > maxAllowedSpeed*cfg.SpeedTolerance {
		return false, "移动速度异常 (Speed/Teleport)"
	}

	// 2. 穿墙检测 (Noclip/Phase Check)
	if cfg.NoclipCheck && s.checkPlayerPathPhase(r, fromX, fromY, fromZ, toX, toY, toZ) {
		return false, "非法穿墙移动 (Noclip/Phase)"
	}

	// 3. 非法飞行/悬空检测 (Fly Hack Check)
	if cfg.FlyCheck && !s.mayFly(p) {
		onGround := s.playerHasGroundSupport(r, toX, toY, toZ)
		if !onGround {
			mov.AirTicks++
			// 连续在空中超过配置阈值，且 Y 轴未表现出正常的重力坠落而是在平移或持续升空
			if mov.AirTicks > cfg.MaxAirTicks && toY >= fromY-0.05 {
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
	cfg := s.anticheatConfig()
	if !cfg.Enabled {
		return true
	}
	if maxDist <= 0 {
		maxDist = cfg.ReachDistance
	}
	dx := p.state.X - targetX
	dy := (p.state.Y + 1.62) - targetY
	dz := p.state.Z - targetZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	return dist <= maxDist
}

func (s *Server) handleAnticheatCommand(p *Peer, args []string) (bool, bool) {
	if p == nil {
		return true, false
	}
	if !s.canGameCommand(p, "server.admin") {
		p.sys("该指令需要管理员 (OP) 权限。")
		return true, false
	}
	cfg := s.anticheatConfig()
	if len(args) == 0 || args[0] == "status" {
		p.sys(fmt.Sprintf("反作弊配置：启用=%v，挖掘距离=%.1f格，攻击距离=%.1f格，最大速度=%.1fm/s，速度容差=%.1fx，瞬移阈值=%.1f格，穿墙检测=%v，飞行检测=%v，滞空容差=%dticks",
			cfg.Enabled, cfg.ReachDistance, cfg.AttackReach, cfg.MaxSpeed, cfg.SpeedTolerance, cfg.MaxTeleportDist, cfg.NoclipCheck, cfg.FlyCheck, cfg.MaxAirTicks))
		return true, true
	}
	if args[0] == "reload" {
		if err := s.reloadAnticheatConfig(); err != nil {
			p.sys("反作弊配置重载失败：" + err.Error())
			return true, false
		}
		newCfg := s.anticheatConfig()
		p.sys(fmt.Sprintf("反作弊配置已重新加载生效：启用=%v，挖掘距离=%.1f格，最大速度=%.1fm/s", newCfg.Enabled, newCfg.ReachDistance, newCfg.MaxSpeed))
		return true, true
	}
	if args[0] == "off" || args[0] == "disable" || args[0] == "close" {
		cfg.Enabled = false
		s.setAnticheatConfig(cfg)
		p.sys("反作弊系统已关闭 (disabled)。")
		return true, true
	}
	if args[0] == "on" || args[0] == "enable" || args[0] == "open" {
		cfg.Enabled = true
		s.setAnticheatConfig(cfg)
		p.sys("反作弊系统已开启 (enabled)。")
		return true, true
	}
	if args[0] == "toggle" {
		cfg.Enabled = !cfg.Enabled
		s.setAnticheatConfig(cfg)
		p.sys(fmt.Sprintf("反作弊已切换为：%v", cfg.Enabled))
		return true, true
	}
	p.sys("用法：/anticheat [status|reload|toggle|on|off]")
	return true, false
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

