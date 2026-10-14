package game

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Residence 领地核心数据结构
type Residence struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	OwnerID   string                     `json:"ownerId"`   // 拥有者固定账号 ID (AccountID / SID)
	OwnerName string                     `json:"ownerName"` // 拥有者玩家名
	WorldID   string                     `json:"worldId"`   // 所在世界 ID
	Center    [3]int                     `json:"center"`    // 中心坐标 [X, Y, Z]
	Size      int                        `json:"size"`      // 边长尺寸（10, 50, 100）
	MinPos    [3]int                     `json:"minPos"`    // 最小立方体边界 [minX, minY, minZ]
	MaxPos    [3]int                     `json:"maxPos"`    // 最大立方体边界 [maxX, maxY, maxZ]
	Cost      float64                    `json:"cost"`      // 购买价格（Size * Size * PricePerBlock）
	Flags     map[string]bool            `json:"flags"`     // 全局标志：build, enter, monsters, pvp, container
	Players   map[string]map[string]bool `json:"players"`   // 指定玩家权限：playerName -> flag -> bool
	EnterMsg  string                     `json:"enterMsg"`  // 进入提示
	LeaveMsg  string                     `json:"leaveMsg"`  // 离开提示
	CreatedAt int64                      `json:"createdAt"` // 创建时间
}

func (r *Residence) Contains(world string, x, y, z int) bool {
	if r.WorldID != "" && world != "" && r.WorldID != world {
		return false
	}
	return x >= r.MinPos[0] && x <= r.MaxPos[0] &&
		y >= r.MinPos[1] && y <= r.MaxPos[1] &&
		z >= r.MinPos[2] && z <= r.MaxPos[2]
}

func (r *Residence) Overlaps(other *Residence) bool {
	if r.WorldID != "" && other.WorldID != "" && r.WorldID != other.WorldID {
		return false
	}
	return r.MinPos[0] <= other.MaxPos[0] && r.MaxPos[0] >= other.MinPos[0] &&
		r.MinPos[1] <= other.MaxPos[1] && r.MaxPos[1] >= other.MinPos[1] &&
		r.MinPos[2] <= other.MaxPos[2] && r.MaxPos[2] >= other.MinPos[2]
}

type ResidenceManager struct {
	mu            sync.RWMutex
	server        *Server
	dataFile      string
	residences    map[string]*Residence // resID -> Residence
	pricePerBlock float64
	refundRate    float64
	maxPerUser    int
	warnMu        sync.Mutex
	lastWarn      map[string]time.Time
}

var (
	globalResidenceManager     *ResidenceManager
	registerResidenceHooksOnce sync.Once
)

func initResidenceManager(s *Server, dataDir string) *ResidenceManager {
	mgr := &ResidenceManager{
		server:        s,
		dataFile:      filepath.Join(dataDir, "residences.json"),
		residences:    make(map[string]*Residence),
		pricePerBlock: 10.0,
		refundRate:    0.8,
		maxPerUser:    5,
		lastWarn:      make(map[string]time.Time),
	}
	mgr.load()
	globalResidenceManager = mgr
	registerResidenceHooksOnce.Do(registerResidenceHooks)
	return mgr
}

func (m *ResidenceManager) shouldWarn(playerKey, tag string, cooldown time.Duration) bool {
	if playerKey == "" {
		playerKey = "anon"
	}
	key := playerKey + ":" + tag
	now := time.Now()
	m.warnMu.Lock()
	defer m.warnMu.Unlock()
	if last, ok := m.lastWarn[key]; ok && now.Sub(last) < cooldown {
		return false
	}
	m.lastWarn[key] = now
	return true
}

func (m *ResidenceManager) load() {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := os.ReadFile(m.dataFile)
	if err != nil {
		return
	}
	var list []*Residence
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, r := range list {
			if r != nil && r.ID != "" {
				if r.Flags == nil {
					r.Flags = make(map[string]bool)
				}
				if r.Players == nil {
					r.Players = make(map[string]map[string]bool)
				}
				m.residences[r.ID] = r
			}
		}
		log.Printf("[ResidenceManager] 已成功从磁盘载入 %d 个领地。", len(m.residences))
	}
}

func (m *ResidenceManager) save() error {
	m.mu.RLock()
	list := make([]*Residence, 0, len(m.residences))
	for _, r := range m.residences {
		list = append(list, r)
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(m.dataFile), 0755)
	return os.WriteFile(m.dataFile, data, 0644)
}

func (m *ResidenceManager) FindAt(world string, x, y, z int) *Residence {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.residences {
		if r.Contains(world, x, y, z) {
			return r
		}
	}
	return nil
}

func (m *ResidenceManager) FindByName(name string) *Residence {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.residences {
		if strings.EqualFold(r.Name, name) {
			return r
		}
	}
	return nil
}

func (m *ResidenceManager) FindByID(id string) *Residence {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.residences[id]
}

func (m *ResidenceManager) isOwner(r *Residence, p *Peer) bool {
	if r == nil || p == nil {
		return false
	}
	if p.sid != "" && r.OwnerID != "" && r.OwnerID == p.sid {
		return true
	}
	if r.OwnerName != "" && p.name != "" && strings.EqualFold(r.OwnerName, p.name) {
		return true
	}
	return false
}

func (m *ResidenceManager) canManage(r *Residence, p *Peer) bool {
	if m.isOwner(r, p) {
		return true
	}
	if p != nil && m.server != nil && m.server.isOperator(p.sid) {
		return true
	}
	return false
}

func (m *ResidenceManager) HasPermission(r *Residence, p *Peer, flag string) bool {
	if r == nil || p == nil {
		return true
	}
	// 1. 领地主人拥有全部权限
	if m.isOwner(r, p) {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 2. 玩家专属白名单（忽略大小写匹配玩家名或账号 SID）
	if r.Players != nil {
		for k, pMap := range r.Players {
			if (p.name != "" && strings.EqualFold(k, p.name)) || (p.sid != "" && strings.EqualFold(k, p.sid)) {
				if val, exists := pMap[flag]; exists {
					return val
				}
				// 若拥有 build 权限，默认允许开启容器
				if flag == "container" {
					if buildVal, exists := pMap["build"]; exists && buildVal {
						return true
					}
				}
			}
		}
	}
	// 3. 全局标志
	if r.Flags != nil {
		if val, exists := r.Flags[flag]; exists {
			return val
		}
	}
	// 4. 默认策略：未显式授权时默认禁止
	return false
}

// ejectPlayerOutside 将无权停留在领地内的玩家安全推出到领地边界外侧
func (m *ResidenceManager) ejectPlayerOutside(p *Peer, res *Residence) {
	if p == nil || p.state == nil || p.room == nil || res == nil {
		return
	}
	px, pz := p.state.X, p.state.Z
	minX, maxX := float64(res.MinPos[0]), float64(res.MaxPos[0]+1)
	minZ, maxZ := float64(res.MinPos[2]), float64(res.MaxPos[2]+1)

	dWest := math.Abs(px - minX)
	dEast := math.Abs(maxX - px)
	dNorth := math.Abs(pz - minZ)
	dSouth := math.Abs(maxZ - pz)

	outX, outZ := px, pz
	minDist := dWest
	outX = minX - 1.5

	if dEast < minDist {
		minDist = dEast
		outX, outZ = maxX+1.5, pz
	}
	if dNorth < minDist {
		minDist = dNorth
		outX, outZ = px, minZ-1.5
	}
	if dSouth < minDist {
		outX, outZ = px, maxZ+1.5
	}

	outY := p.state.Y
	if m.server != nil {
		if sy := m.server.surface(p.room, int(math.Floor(outX)), int(math.Floor(outZ))); sy > 0 {
			outY = float64(sy)
		}
	}
	p.state.X = outX
	p.state.Y = outY
	p.state.Z = outZ
	p.peakY = outY
	p.mining = nil
}

// ejectUnauthorizedPlayers 检查领地内所有在线玩家，将无进入权限的玩家推出领地外
func (m *ResidenceManager) ejectUnauthorizedPlayers(res *Residence) {
	if m.server == nil || res == nil {
		return
	}
	for _, r := range m.server.allRooms() {
		if res.WorldID != "" && r.ID != res.WorldID {
			continue
		}
		for p := range r.Peers {
			if p == nil || p.state == nil || p.closed {
				continue
			}
			px, py, pz := int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z))
			if (res.Contains(r.ID, px, py, pz) || res.Contains(r.ID, px, py+1, pz)) && !m.HasPermission(res, p, "enter") {
				m.ejectPlayerOutside(p, res)
				m.server.correctPosition(p)
				p.sys(fmt.Sprintf("§c[领地保护] 你没有领地【%s】的进入权限，已被移出领地范围！", res.Name))
			}
		}
	}
}

// CheckEvent 在服务端事件分发时同步执行领地权限与保护规则拦截
func (m *ResidenceManager) CheckEvent(r *Room, p *Peer, e *api.Event) {
	if m == nil || e == nil || e.Cancelled {
		return
	}
	m.mu.RLock()
	hasAny := len(m.residences) > 0
	m.mu.RUnlock()
	if !hasAny {
		return
	}

	worldID := ""
	if r != nil {
		worldID = r.ID
	} else if e.World != nil {
		worldID = e.World.ID
	}

	// 若调用方传入的 p 为 nil，但事件中记录了发起动作的玩家 actor，则自动解析对应在线玩家
	resolvedFromActor := false
	if p == nil && r != nil {
		actor := e.String("actor")
		if actor != "" && e.String("source") == "player" {
			for peer := range r.Peers {
				if peer.sid == actor {
					p = peer
					resolvedFromActor = true
					break
				}
			}
		}
	}

	cancelWithNotice := func(reason string, silent bool) {
		if silent {
			e.Data["silentCancel"] = true
		} else if resolvedFromActor && p != nil {
			p.sys(limitText(reason, 240))
		}
		e.Cancellable = true
		e.Cancelled = true
		e.Reason = reason
	}

	switch e.Name {
	// 1. 建筑、破坏、方块改造与道具放置保护 (build 权限)
	case api.BlockBreaking, api.BlockPlacing,
		api.BucketFilling, api.BucketEmptying, api.BottleFilling,
		api.FarmingTilling, api.CropGrowing,
		api.MechanicChanging, api.SignChanging, api.RedstoneComponentChanging,
		api.TNTPriming, api.ItemSpawnEggUsing,
		api.VehiclePlacing, api.VehicleDestroying,
		api.AnimalShearing, api.EntityShearing, api.AnimalBreeding:

		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))
		if tgt, ok := e.Data["target"].(map[string]any); ok {
			if tx, okX := tgt["x"].(int); okX {
				x = tx
			} else if tx, okX := tgt["x"].(float64); okX {
				x = int(math.Floor(tx))
			}
			if ty, okY := tgt["y"].(int); okY {
				y = ty
			} else if ty, okY := tgt["y"].(float64); okY {
				y = int(math.Floor(ty))
			}
			if tz, okZ := tgt["z"].(int); okZ {
				z = tz
			} else if tz, okZ := tgt["z"].(float64); okZ {
				z = int(math.Floor(tz))
			}
		}

		res := m.FindAt(worldID, x, y, z)
		if res == nil && e.Name == api.BucketFilling && e.Bool("milk") && p != nil && p.state != nil {
			res = m.FindAt(worldID, int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z)))
		}
		if res == nil {
			return
		}

		// 防爆保护：禁止爆炸破坏领地内任何方块
		if e.Name == api.BlockBreaking {
			reason := e.String("reason")
			if reason == "explosion" || reason == "tnt_explosion" {
				cancelWithNotice("领地防爆保护", true)
				return
			}
		}

		// 非玩家触发的自然更新（如红石信号衰减、自然生长等）不作玩家权限拦截
		if p == nil {
			return
		}

		if !m.HasPermission(res, p, "build") {
			playerKey := p.sid
			if playerKey == "" {
				playerKey = p.name
			}
			silent := !m.shouldWarn(playerKey, "build_"+res.ID, 1500*time.Millisecond)
			cancelWithNotice(fmt.Sprintf("§c[领地保护] 你没有领地【%s】（主人: %s）的建筑与破坏权限！", res.Name, res.OwnerName), silent)
		}

	// 2. 门、栅栏门与床交互保护
	case api.DoorToggling, api.PlayerSleeping:
		if p == nil {
			return
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))
		res := m.FindAt(worldID, x, y, z)
		if res != nil && !m.HasPermission(res, p, "build") && !m.HasPermission(res, p, "container") {
			cancelWithNotice(fmt.Sprintf("§c[领地保护] 你没有领地【%s】的设施使用权限！", res.Name), false)
		}

	// 3. 爆炸生成保护
	case api.ExplosionCreating:
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))
		if res := m.FindAt(worldID, x, y, z); res != nil {
			cancelWithNotice("领地内禁止爆炸", true)
		}

	// 4. 容器开启保护 (container 权限)
	case api.InventoryOpening:
		if p == nil {
			return
		}
		kind := e.String("kind")
		if kind == "ender" {
			return
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))
		res := m.FindAt(worldID, x, y, z)
		if res == nil && e.Bool("doubleChest") {
			res = m.FindAt(worldID, int(math.Floor(e.Number("chest2X"))), int(math.Floor(e.Number("chest2Y"))), int(math.Floor(e.Number("chest2Z"))))
		}
		if res != nil && !m.HasPermission(res, p, "container") && !m.HasPermission(res, p, "build") {
			cancelWithNotice(fmt.Sprintf("§c[领地保护] 你没有开启领地【%s】（主人: %s）容器的权限！", res.Name, res.OwnerName), false)
		}

	// 5. 玩家移动、传送与载具进入保护 (enter 权限)
	case api.PlayerMoving, api.PlayerTeleporting, api.VehicleEntering, api.VehicleMoving:
		if p == nil || p.state == nil {
			return
		}
		toX := int(math.Floor(e.Number("x")))
		toY := int(math.Floor(e.Number("y")))
		toZ := int(math.Floor(e.Number("z")))
		fromX := int(math.Floor(e.Number("fromX")))
		fromY := int(math.Floor(e.Number("fromY")))
		fromZ := int(math.Floor(e.Number("fromZ")))
		if e.Name == api.VehicleEntering {
			if tgt, ok := e.Data["target"].(map[string]any); ok {
				if tx, okX := tgt["x"].(float64); okX {
					toX = int(math.Floor(tx))
				}
				if ty, okY := tgt["y"].(float64); okY {
					toY = int(math.Floor(ty))
				}
				if tz, okZ := tgt["z"].(float64); okZ {
					toZ = int(math.Floor(tz))
				}
			}
			fromX, fromY, fromZ = int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z))
		}

		toRes := m.FindAt(worldID, toX, toY, toZ)
		if toRes == nil {
			toRes = m.FindAt(worldID, toX, toY+1, toZ)
		}
		fromRes := m.FindAt(worldID, fromX, fromY, fromZ)
		if fromRes == nil {
			fromRes = m.FindAt(worldID, fromX, fromY+1, fromZ)
		}

		if toRes != nil && !m.HasPermission(toRes, p, "enter") {
			playerKey := p.sid
			if playerKey == "" {
				playerKey = p.name
			}
			silent := e.Name == api.PlayerMoving && !m.shouldWarn(playerKey, "enter_"+toRes.ID, 2*time.Second)
			if fromRes == toRes {
				// 玩家当前坐标已在领地内部但无权限，立即将其推出到领地边界之外
				m.ejectPlayerOutside(p, toRes)
			}
			cancelWithNotice(fmt.Sprintf("§c[领地保护] 你没有进入领地【%s】（主人: %s）的权限！", toRes.Name, toRes.OwnerName), silent)
			return
		}

		// 正常进出领地时的欢迎/离开提示
		if (e.Name == api.PlayerMoving || e.Name == api.PlayerTeleporting) && fromRes != toRes {
			playerKey := p.sid
			if playerKey == "" {
				playerKey = p.name
			}
			if fromRes != nil && fromRes.LeaveMsg != "" && m.shouldWarn(playerKey, "leave_"+fromRes.ID, 2*time.Second) {
				p.sys("§e[领地] " + fromRes.LeaveMsg)
			}
			if toRes != nil && toRes.EnterMsg != "" && m.shouldWarn(playerKey, "welcome_"+toRes.ID, 2*time.Second) {
				p.sys("§a[领地] " + toRes.EnterMsg)
			}
		}

	// 6. PVP 与领地内友善生物保护
	case api.PlayerAttacking:
		if p == nil || p.state == nil || e.Damage == nil || e.Damage.Target == nil {
			return
		}
		tgt := e.Damage.Target
		attRes := m.FindAt(worldID, int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z)))
		tgtRes := m.FindAt(worldID, int(math.Floor(tgt.X)), int(math.Floor(tgt.Y)), int(math.Floor(tgt.Z)))

		if tgt.Kind == "player" {
			if attRes != nil && !attRes.Flags["pvp"] {
				cancelWithNotice(fmt.Sprintf("§c[领地保护] 领地【%s】内禁止 PVP 战斗！", attRes.Name), false)
				return
			}
			if tgtRes != nil && !tgtRes.Flags["pvp"] {
				cancelWithNotice(fmt.Sprintf("§c[领地保护] 目标所在的领地【%s】内禁止 PVP 战斗！", tgtRes.Name), false)
				return
			}
		} else if tgt.Kind == "entity" && tgtRes != nil {
			if !mobSpecs[tgt.EntityType].Hostile && !m.HasPermission(tgtRes, p, "build") {
				cancelWithNotice(fmt.Sprintf("§c[领地保护] 你没有权限在领地【%s】内攻击非敌对生物！", tgtRes.Name), false)
				return
			}
		}

	case api.PlayerDamaging:
		if p == nil || p.state == nil || e.Damage == nil || e.Damage.Attacker == nil {
			return
		}
		att := e.Damage.Attacker
		tgtRes := m.FindAt(worldID, int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z)))
		attRes := m.FindAt(worldID, int(math.Floor(att.X)), int(math.Floor(att.Y)), int(math.Floor(att.Z)))
		if att.Kind == "player" {
			if (tgtRes != nil && !tgtRes.Flags["pvp"]) || (attRes != nil && !attRes.Flags["pvp"]) {
				cancelWithNotice("§c[领地保护] 领地内禁止 PVP 伤害！", true)
				return
			}
		} else if att.Kind == "entity" && mobSpecs[att.EntityType].Hostile {
			if tgtRes != nil && !tgtRes.Flags["monsters"] {
				cancelWithNotice("领地防怪保护", true)
				return
			}
		}

	// 7. 怪物生成与怪物攻击保护 (monsters 标志)
	case api.EntitySpawning:
		typ := e.String("type")
		if mobSpecs[typ].Hostile || e.String("category") == "monster" {
			x := int(math.Floor(e.Number("x")))
			y := int(math.Floor(e.Number("y")))
			z := int(math.Floor(e.Number("z")))
			if res := m.FindAt(worldID, x, y, z); res != nil && !res.Flags["monsters"] {
				cancelWithNotice("领地内禁止刷新怪物", true)
			}
		}

	case api.EntityAttacking:
		if p != nil && p.state != nil {
			if res := m.FindAt(worldID, int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z))); res != nil && !res.Flags["monsters"] {
				cancelWithNotice("领地内禁止怪物攻击", true)
			}
		}
	}
}

// SendResidenceWindow 向客户端下发领地完整 GUI 数据包
func (m *ResidenceManager) SendResidenceWindow(p *Peer) {
	if p == nil || p.state == nil {
		return
	}
	bal, _ := m.server.ensureEconomyAccount(p)
	worldID := ""
	if p.room != nil {
		worldID = p.room.ID
	}

	px := int(math.Floor(p.state.X))
	py := int(math.Floor(p.state.Y))
	pz := int(math.Floor(p.state.Z))

	currentRes := m.FindAt(worldID, px, py, pz)
	currentResID := ""
	if currentRes != nil {
		currentResID = currentRes.ID
	}

	m.mu.RLock()
	list := make([]*Residence, 0, len(m.residences))
	for _, r := range m.residences {
		// 返回属于该世界或与玩家相关的领地
		if r.WorldID == worldID || (p.sid != "" && r.OwnerID == p.sid) || strings.EqualFold(r.OwnerName, p.name) {
			list = append(list, r)
		}
	}
	m.mu.RUnlock()

	p.json(map[string]any{
		"t":             "residence_window",
		"balance":       bal,
		"pricePerBlock": m.pricePerBlock,
		"refundRate":    m.refundRate,
		"playerPos":     []int{px, py, pz},
		"currentResId":  currentResID,
		"residences":    list,
	})
}

// CreateResidence 购买并创建领地
func (m *ResidenceManager) CreateResidence(p *Peer, name string, size int) (bool, string) {
	if p == nil || p.state == nil || p.room == nil {
		return false, "玩家状态异常"
	}
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 16 {
		return false, "领地名称长度必须在 1 到 16 个字符之间"
	}
	if strings.ContainsAny(name, " /\\:*?\"<>|") {
		return false, "领地名称不能包含特殊符号或空格"
	}
	if size != 10 && size != 50 && size != 100 {
		return false, "领地规格仅支持 10*10、50*50 或 100*100"
	}

	if m.FindByName(name) != nil {
		return false, fmt.Sprintf("领地名称【%s】已被占用，请更换其他名称", name)
	}

	// 检查玩家拥有领地数量上限
	m.mu.RLock()
	userResCount := 0
	for _, r := range m.residences {
		if (p.sid != "" && r.OwnerID == p.sid) || strings.EqualFold(r.OwnerName, p.name) {
			userResCount++
		}
	}
	m.mu.RUnlock()

	if userResCount >= m.maxPerUser && !m.server.isOperator(p.sid) {
		return false, fmt.Sprintf("你已拥有 %d 个领地，已达上限（最大 %d 个）", userResCount, m.maxPerUser)
	}

	area := size * size
	cost := float64(area) * m.pricePerBlock

	bal, err := m.server.ensureEconomyAccount(p)
	if err != nil {
		return false, "获取钱包账户失败"
	}
	if bal < cost {
		return false, fmt.Sprintf("金币不足！需要 $%.2f，当前余额 $%.2f", cost, bal)
	}

	cx := int(math.Floor(p.state.X))
	cy := int(math.Floor(p.state.Y))
	cz := int(math.Floor(p.state.Z))
	half := size / 2

	minX, maxX := cx-half, cx+half-1
	minZ, maxZ := cz-half, cz+half-1
	minY := int(math.Max(0, float64(cy-half)))
	maxY := int(math.Min(127, float64(cy+half-1)))

	newRes := &Residence{
		ID:        fmt.Sprintf("res_%d_%s", time.Now().Unix(), name),
		Name:      name,
		OwnerID:   p.sid,
		OwnerName: p.name,
		WorldID:   p.room.ID,
		Center:    [3]int{cx, cy, cz},
		Size:      size,
		MinPos:    [3]int{minX, minY, minZ},
		MaxPos:    [3]int{maxX, maxY, maxZ},
		Cost:      cost,
		Flags: map[string]bool{
			"build":     false, // 默认禁止外人建筑和破坏
			"enter":     false, // 默认禁止未授权玩家进入
			"monsters":  false, // 默认禁止刷怪
			"pvp":       false, // 默认禁止打斗
			"container": false, // 默认禁止外人翻箱
		},
		Players:   make(map[string]map[string]bool),
		EnterMsg:  fmt.Sprintf("欢迎来到领地【%s】（主人: %s）。", name, p.name),
		LeaveMsg:  fmt.Sprintf("你已离开领地【%s】。", name),
		CreatedAt: time.Now().Unix(),
	}

	// 重叠检测
	m.mu.RLock()
	for _, r := range m.residences {
		if newRes.Overlaps(r) {
			m.mu.RUnlock()
			return false, fmt.Sprintf("选定范围与已有领地【%s】（主人: %s）重叠！", r.Name, r.OwnerName)
		}
	}
	m.mu.RUnlock()

	// 触发插件 before 事件
	createData := map[string]any{
		"resId":      newRes.ID,
		"name":       newRes.Name,
		"size":       newRes.Size,
		"cost":       newRes.Cost,
		"center":     newRes.Center,
		"minPos":     newRes.MinPos,
		"maxPos":     newRes.MaxPos,
		"actor":      p.sid,
		"playerName": p.name,
	}
	if !m.server.allowed(api.ResidenceCreating, p.room, p, createData) {
		return false, "领地创建请求已被插件阻止"
	}
	if newCost, ok := createData["cost"].(float64); ok && newCost >= 0 {
		cost = newCost
		newRes.Cost = newCost
	}

	// 扣款
	newBal, err := m.server.economyDB.Withdraw(p.sid, cost, fmt.Sprintf("购买领地 %s (%d*%d)", name, size, size))
	if err != nil {
		return false, "扣款失败: " + err.Error()
	}
	p.json(map[string]any{"t": "economy_balance", "balance": newBal})

	m.mu.Lock()
	m.residences[newRes.ID] = newRes
	m.mu.Unlock()
	_ = m.save()

	// 将领地内未授权的其他玩家安全移出领地
	m.ejectUnauthorizedPlayers(newRes)

	// 触发创建完成事件
	m.server.emit(api.ResidenceCreated, p.room, p, createData)

	return true, fmt.Sprintf("🎉 成功购买并创建领地【%s】！已扣除 $%.2f 金币。", name, cost)
}

// SetFlag 设置领地全局标志
func (m *ResidenceManager) SetFlag(p *Peer, resID, flag string, val bool) (bool, string) {
	res := m.FindByID(resID)
	if res == nil {
		res = m.FindByName(resID)
	}
	if res == nil {
		return false, "领地不存在"
	}
	if !m.canManage(res, p) {
		return false, "你不是领地主人，无权修改其标志"
	}

	flag = strings.ToLower(flag)
	switch flag {
	case "monsters", "monster", "mob", "mobs":
		flag = "monsters"
	case "enter", "move":
		flag = "enter"
	case "build", "place", "break":
		flag = "build"
	case "pvp":
		flag = "pvp"
	case "container", "chest":
		flag = "container"
	default:
		return false, "不支持的标志类型（可选: build, enter, container, monsters, pvp）"
	}

	flagData := map[string]any{
		"resId":  res.ID,
		"flag":   flag,
		"value":  val,
		"actor":  p.sid,
		"player": p.name,
	}
	if !m.server.allowed(api.ResidenceFlagChanging, p.room, p, flagData) {
		return false, "修改被插件阻止"
	}
	if newVal, ok := flagData["value"].(bool); ok {
		val = newVal
	}

	m.mu.Lock()
	if res.Flags == nil {
		res.Flags = make(map[string]bool)
	}
	res.Flags[flag] = val
	m.mu.Unlock()
	_ = m.save()

	if flag == "enter" && !val {
		m.ejectUnauthorizedPlayers(res)
	}

	m.server.emit(api.ResidenceFlagChanged, p.room, p, flagData)

	stateStr := "已开启"
	if !val {
		stateStr = "已关闭"
	}
	return true, fmt.Sprintf("领地【%s】的标志【%s】%s。", res.Name, flag, stateStr)
}

// SetPlayerPermission 设置成员特定权限
func (m *ResidenceManager) SetPlayerPermission(p *Peer, resID, targetPlayer, flag string, val bool) (bool, string) {
	res := m.FindByID(resID)
	if res == nil {
		res = m.FindByName(resID)
	}
	if res == nil {
		return false, "领地不存在"
	}
	if !m.canManage(res, p) {
		return false, "你不是领地主人，无权设置成员权限"
	}
	targetPlayer = strings.TrimSpace(targetPlayer)
	if targetPlayer == "" {
		return false, "玩家名不能为空"
	}

	flag = strings.ToLower(flag)
	switch flag {
	case "build", "place", "break":
		flag = "build"
	case "enter", "move":
		flag = "enter"
	case "container", "chest":
		flag = "container"
	default:
		return false, "成员权限仅支持: build(建筑), enter(准入), container(容器)"
	}

	m.mu.Lock()
	if res.Players == nil {
		res.Players = make(map[string]map[string]bool)
	}
	actualKey := targetPlayer
	for k := range res.Players {
		if strings.EqualFold(k, targetPlayer) {
			actualKey = k
			break
		}
	}
	if res.Players[actualKey] == nil {
		res.Players[actualKey] = make(map[string]bool)
	}
	res.Players[actualKey][flag] = val
	m.mu.Unlock()
	_ = m.save()

	if flag == "enter" && !val {
		m.ejectUnauthorizedPlayers(res)
	}

	stateStr := "授予"
	if !val {
		stateStr = "剥夺"
	}
	return true, fmt.Sprintf("已成功为玩家【%s】在领地【%s】中%s【%s】权限。", actualKey, res.Name, stateStr, flag)
}

// RemovePlayerPermission 移除成员在领地中的全部特权
func (m *ResidenceManager) RemovePlayerPermission(p *Peer, resID, targetPlayer string) (bool, string) {
	res := m.FindByID(resID)
	if res == nil {
		res = m.FindByName(resID)
	}
	if res == nil {
		return false, "领地不存在"
	}
	if !m.canManage(res, p) {
		return false, "你不是领地主人，无权管理成员权限"
	}
	m.mu.Lock()
	if res.Players != nil {
		for k := range res.Players {
			if strings.EqualFold(k, targetPlayer) {
				delete(res.Players, k)
			}
		}
	}
	m.mu.Unlock()
	_ = m.save()
	m.ejectUnauthorizedPlayers(res)
	return true, fmt.Sprintf("已移除玩家【%s】在领地【%s】中的所有自定义权限。", targetPlayer, res.Name)
}

// TeleportToResidence 传送到领地中心点上方
func (m *ResidenceManager) TeleportToResidence(p *Peer, resID string) (bool, string) {
	res := m.FindByID(resID)
	if res == nil {
		res = m.FindByName(resID)
	}
	if res == nil {
		return false, "领地不存在"
	}
	if !m.HasPermission(res, p, "enter") {
		return false, fmt.Sprintf("你没有进入领地【%s】的权限", res.Name)
	}
	targetX := float64(res.Center[0]) + 0.5
	targetY := float64(res.Center[1]) + 0.5
	targetZ := float64(res.Center[2]) + 0.5

	if !m.server.teleport(p, targetX, targetY, targetZ, "residence.tp") {
		return false, "传送被取消或目标位置不可达"
	}
	return true, fmt.Sprintf("已传送到领地【%s】中心点！", res.Name)
}

// RemoveResidence 删除领地并返还 80% 款项
func (m *ResidenceManager) RemoveResidence(p *Peer, resID string) (bool, string) {
	res := m.FindByID(resID)
	if res == nil {
		res = m.FindByName(resID)
	}
	if res == nil {
		return false, "领地不存在"
	}
	if !m.canManage(res, p) {
		return false, "你不是领地主人，无权删除该领地"
	}

	refund := res.Cost * m.refundRate
	if refund > 0 && p.sid != "" && m.server.economyDB != nil {
		newBal, err := m.server.economyDB.Deposit(p.sid, p.name, refund, fmt.Sprintf("删除领地 %s 退款", res.Name))
		if err == nil {
			p.json(map[string]any{"t": "economy_balance", "balance": newBal})
		}
	}

	m.mu.Lock()
	delete(m.residences, res.ID)
	m.mu.Unlock()
	_ = m.save()

	removeData := map[string]any{
		"resId":  res.ID,
		"name":   res.Name,
		"refund": refund,
		"actor":  p.sid,
	}
	m.server.emit(api.ResidenceRemoved, p.room, p, removeData)

	return true, fmt.Sprintf("领地【%s】已删除并释放土地！已返还 $%.2f 金币。", res.Name, refund)
}

// 注册网络消息路由钩子（全局只注册一次，内部按 s.residence 动态分发）
func registerResidenceHooks() {
	getMgr := func(s *Server) *ResidenceManager {
		if s != nil && s.residence != nil {
			return s.residence
		}
		return globalResidenceManager
	}

	onMessage("residence_open", func(s *Server, p *Peer, msg Message) bool {
		if mgr := getMgr(s); mgr != nil {
			mgr.SendResidenceWindow(p)
		}
		return true
	})

	onMessage("residence_create", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		name := msg.Name
		if name == "" {
			name = msg.Text
		}
		size := msg.Size
		if size <= 0 {
			size = msg.I
		}
		ok, text := mgr.CreateResidence(p, name, size)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		mgr.SendResidenceWindow(p)
		return true
	})

	onMessage("residence_set_flag", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		ok, text := mgr.SetFlag(p, msg.ResID, msg.Flag, msg.State)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		mgr.SendResidenceWindow(p)
		return true
	})

	onMessage("residence_set_player", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		ok, text := mgr.SetPlayerPermission(p, msg.ResID, msg.TargetPlayer, msg.Flag, msg.State)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		mgr.SendResidenceWindow(p)
		return true
	})

	onMessage("residence_remove_player", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		ok, text := mgr.RemovePlayerPermission(p, msg.ResID, msg.TargetPlayer)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		mgr.SendResidenceWindow(p)
		return true
	})

	onMessage("residence_tp", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		ok, text := mgr.TeleportToResidence(p, msg.ResID)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		return true
	})

	onMessage("residence_remove", func(s *Server, p *Peer, msg Message) bool {
		mgr := getMgr(s)
		if mgr == nil {
			return true
		}
		ok, text := mgr.RemoveResidence(p, msg.ResID)
		p.json(map[string]any{
			"t":       "residence_result",
			"ok":      ok,
			"message": text,
		})
		mgr.SendResidenceWindow(p)
		return true
	})
}

func parseBoolArg(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1", "yes", "y", "on", "allow":
		return true, true
	case "false", "f", "0", "no", "n", "off", "deny":
		return false, true
	default:
		return false, false
	}
}

// handleResidenceCommand 统一处理 /res, /residence, /claim 系列聊天指令
func (s *Server) handleResidenceCommand(p *Peer, args []string) (bool, bool) {
	if p == nil {
		return true, true
	}
	mgr := s.residence
	if mgr == nil {
		mgr = globalResidenceManager
	}
	if mgr == nil {
		p.sys("§c领地系统暂未就绪！")
		return true, true
	}

	// 容错处理：若传入了主命令名（"res", "residence", "claim"），先将其剥离
	if len(args) > 0 {
		first := strings.ToLower(args[0])
		if first == "res" || first == "residence" || first == "claim" {
			args = args[1:]
		}
	}

	if len(args) == 0 {
		mgr.SendResidenceWindow(p)
		return true, true
	}

	currentRes := func() *Residence {
		if p.state != nil && p.room != nil {
			return mgr.FindAt(p.room.ID, int(math.Floor(p.state.X)), int(math.Floor(p.state.Y)), int(math.Floor(p.state.Z)))
		}
		return nil
	}

	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "open", "gui", "menu":
		mgr.SendResidenceWindow(p)
		return true, true

	case "create":
		if len(args) < 3 {
			p.sys("§e用法: /res create <领地名称> <10|50|100>")
			return true, true
		}
		name := args[1]
		size, err := strconv.Atoi(args[2])
		if err != nil || (size != 10 && size != 50 && size != 100) {
			p.sys("§c领地尺寸必须为 10, 50 或 100")
			return true, true
		}
		ok, text := mgr.CreateResidence(p, name, size)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "set", "flag":
		// 支持 /res set <flag> <true|false> 或 /res set <领地名> <flag> <true|false>
		var targetRes *Residence
		var flagName, valStr string
		if len(args) == 3 {
			targetRes = currentRes()
			flagName, valStr = args[1], args[2]
		} else if len(args) >= 4 {
			targetRes = mgr.FindByID(args[1])
			if targetRes == nil {
				targetRes = mgr.FindByName(args[1])
			}
			flagName, valStr = args[2], args[3]
		} else {
			p.sys("§e用法: /res set [领地名] <build|enter|container|monsters|pvp> <true|false>")
			return true, true
		}
		if targetRes == nil {
			p.sys("§c请站在领地内操作，或指定领地名称: /res set <领地名> <标志> <true|false>")
			return true, true
		}
		val, valid := parseBoolArg(valStr)
		if !valid {
			p.sys("§c状态值必须为 true 或 false")
			return true, true
		}
		ok, text := mgr.SetFlag(p, targetRes.ID, flagName, val)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "pset":
		// 支持 /res pset <玩家名> <build|enter|container> <true|false> 或 /res pset <领地名> <玩家名> <flag> <true|false>
		var targetRes *Residence
		var targetPlayer, flagName, valStr string
		if len(args) == 4 {
			targetRes = currentRes()
			targetPlayer, flagName, valStr = args[1], args[2], args[3]
		} else if len(args) >= 5 {
			targetRes = mgr.FindByID(args[1])
			if targetRes == nil {
				targetRes = mgr.FindByName(args[1])
			}
			targetPlayer, flagName, valStr = args[2], args[3], args[4]
		} else {
			p.sys("§e用法: /res pset [领地名] <玩家名> <build|enter|container> <true|false>")
			return true, true
		}
		if targetRes == nil {
			p.sys("§c请站在领地内操作，或指定领地名称: /res pset <领地名> <玩家名> <权限> <true|false>")
			return true, true
		}
		val, valid := parseBoolArg(valStr)
		if !valid {
			p.sys("§c状态值必须为 true 或 false")
			return true, true
		}
		ok, text := mgr.SetPlayerPermission(p, targetRes.ID, targetPlayer, flagName, val)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "padd", "trust":
		if len(args) < 2 {
			p.sys("§e用法: /res padd <玩家名> (授予当前领地建筑、准入与容器权限)")
			return true, true
		}
		targetRes := currentRes()
		targetPlayer := args[1]
		if len(args) >= 3 {
			if r := mgr.FindByName(args[1]); r != nil {
				targetRes = r
				targetPlayer = args[2]
			}
		}
		if targetRes == nil {
			p.sys("§c请站在领地内执行，或使用 /res padd <领地名> <玩家名>")
			return true, true
		}
		for _, f := range []string{"build", "enter", "container"} {
			if ok, text := mgr.SetPlayerPermission(p, targetRes.ID, targetPlayer, f, true); !ok {
				p.sys("§c" + text)
				return true, true
			}
		}
		p.sys(fmt.Sprintf("§a已授予玩家【%s】在领地【%s】的建筑、进入与容器权限！", targetPlayer, targetRes.Name))
		return true, true

	case "pdel", "untrust":
		if len(args) < 2 {
			p.sys("§e用法: /res pdel <玩家名> (移除玩家在当前领地的所有权限)")
			return true, true
		}
		targetRes := currentRes()
		targetPlayer := args[1]
		if len(args) >= 3 {
			if r := mgr.FindByName(args[1]); r != nil {
				targetRes = r
				targetPlayer = args[2]
			}
		}
		if targetRes == nil {
			p.sys("§c请站在领地内执行，或使用 /res pdel <领地名> <玩家名>")
			return true, true
		}
		ok, text := mgr.RemovePlayerPermission(p, targetRes.ID, targetPlayer)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "remove", "delete":
		var targetRes *Residence
		if len(args) >= 2 {
			target := args[1]
			targetRes = mgr.FindByID(target)
			if targetRes == nil {
				targetRes = mgr.FindByName(target)
			}
		} else {
			targetRes = currentRes()
		}
		if targetRes == nil {
			p.sys("§c未找到指定的领地！用法: /res remove [领地名称或ID]")
			return true, true
		}
		ok, text := mgr.RemoveResidence(p, targetRes.ID)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "tp":
		if len(args) < 2 {
			p.sys("§e用法: /res tp <领地名称或ID>")
			return true, true
		}
		target := args[1]
		targetRes := mgr.FindByID(target)
		if targetRes == nil {
			targetRes = mgr.FindByName(target)
		}
		if targetRes == nil {
			p.sys("§c未找到指定的领地！")
			return true, true
		}
		ok, text := mgr.TeleportToResidence(p, targetRes.ID)
		if ok {
			p.sys("§a" + text)
		} else {
			p.sys("§c" + text)
		}
		return true, true

	case "list":
		mgr.mu.RLock()
		if len(mgr.residences) == 0 {
			mgr.mu.RUnlock()
			p.sys("§e当前世界还没有任何已创建的领地。")
			return true, true
		}
		p.sys("§6=== 领地列表 ===")
		for _, r := range mgr.residences {
			p.sys(fmt.Sprintf("§e• %s §7(主人: %s, 规格: %dx%d, 中心: [%d, %d, %d])", r.Name, r.OwnerName, r.Size, r.Size, r.Center[0], r.Center[1], r.Center[2]))
		}
		mgr.mu.RUnlock()
		return true, true

	case "info":
		res := currentRes()
		if len(args) >= 2 {
			if byName := mgr.FindByName(args[1]); byName != nil {
				res = byName
			}
		}
		if res == nil {
			p.sys("§e你当前不在任何领地内。")
			return true, true
		}
		p.sys(fmt.Sprintf("§6=== 领地【%s】信息 ===", res.Name))
		p.sys(fmt.Sprintf("§7所有者: §f%s", res.OwnerName))
		p.sys(fmt.Sprintf("§7范围: §f%d x %d (三维立方体)", res.Size, res.Size))
		p.sys(fmt.Sprintf("§7坐标: §f[%d, %d, %d] 到 [%d, %d, %d]", res.MinPos[0], res.MinPos[1], res.MinPos[2], res.MaxPos[0], res.MaxPos[1], res.MaxPos[2]))
		p.sys(fmt.Sprintf("§7标志: 怪物=%v, 准入=%v, 建筑=%v, 容器=%v, PVP=%v", res.Flags["monsters"], res.Flags["enter"], res.Flags["build"], res.Flags["container"], res.Flags["pvp"]))
		return true, true

	case "help":
		fallthrough
	default:
		p.sys("§6=== 领地系统 (Residence) 指令 ===")
		p.sys("§e/res §7- 打开领地图形化管理界面 (GUI)")
		p.sys("§e/res create <名称> <10|50|100> §7- 以当前位置为中心购买领地")
		p.sys("§e/res set <build|enter|container|monsters|pvp> <true|false> §7- 设置领地标志")
		p.sys("§e/res pset <玩家名> <build|enter|container> <true|false> §7- 设置玩家权限")
		p.sys("§e/res padd <玩家名> / /res pdel <玩家名> §7- 快捷授权/移除成员")
		p.sys("§e/res remove [名称] §7- 删除领地 (返还 80% 费用)")
		p.sys("§e/res tp <名称> §7- 传送到指定领地")
		p.sys("§e/res info §7- 查看当前所在领地的信息")
		return true, true
	}
}
