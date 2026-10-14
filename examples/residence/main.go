package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config 领地插件配置文件
type Config struct {
	PricePerBlock float64 `json:"pricePerBlock"` // 领地平面每格单价，默认 10.0
	RefundRate    float64 `json:"refundRate"`    // 删除领地退款比例，默认 0.8 (80%)
	MaxResPerUser int     `json:"maxResPerUser"` // 每个玩家最大拥有的领地数量，默认 5
	AllowOpBypass bool    `json:"allowOpBypass"` // 管理员是否绕过领地限制，默认 true
}

// Residence 领地完整数据结构
type Residence struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	OwnerID   string                     `json:"ownerId"`   // 拥有者固定账号 ID (AccountID / SID)
	OwnerName string                     `json:"ownerName"` // 拥有者游戏名
	WorldID   string                     `json:"worldId"`   // 所在世界
	Center    [3]int                     `json:"center"`    // 中心坐标 [X, Y, Z]
	Size      int                        `json:"size"`      // 边长尺寸（10, 50, 100 等）
	MinPos    [3]int                     `json:"minPos"`    // 最小立方体边界 [minX, minY, minZ]
	MaxPos    [3]int                     `json:"maxPos"`    // 最大立方体边界 [maxX, maxY, maxZ]
	Cost      float64                    `json:"cost"`      // 购买价格（Size * Size * PricePerBlock）
	Flags     map[string]bool            `json:"flags"`     // 全局标志：build, enter, monsters, pvp, container
	Players   map[string]map[string]bool `json:"players"`   // 指定玩家权限：playerName -> flag -> bool
	EnterMsg  string                     `json:"enterMsg"`  // 进入提示
	LeaveMsg  string                     `json:"leaveMsg"`  // 离开提示
	CreatedAt int64                      `json:"createdAt"` // 创建时间
}

// Contains 检查某世界中的坐标是否落在本领地内
func (r *Residence) Contains(world string, x, y, z int) bool {
	if r.WorldID != "" && r.WorldID != world {
		return false
	}
	return x >= r.MinPos[0] && x <= r.MaxPos[0] &&
		y >= r.MinPos[1] && y <= r.MaxPos[1] &&
		z >= r.MinPos[2] && z <= r.MaxPos[2]
}

// IsOverlap 检查两个领地是否在空间上产生立体包围盒重叠
func isOverlap(a, b *Residence) bool {
	if a.WorldID != b.WorldID {
		return false
	}
	return a.MinPos[0] <= b.MaxPos[0] && a.MaxPos[0] >= b.MinPos[0] &&
		a.MinPos[1] <= b.MaxPos[1] && a.MaxPos[1] >= b.MinPos[1] &&
		a.MinPos[2] <= b.MaxPos[2] && a.MaxPos[2] >= b.MinPos[2]
}

// isMonster 检查实体类型是否属于敌对怪物
func isMonster(kind string) bool {
	switch strings.ToLower(kind) {
	case "zombie", "skeleton", "creeper", "spider", "enderman",
		"witch", "slime", "magma_cube", "ghast", "blaze", "zombified_piglin",
		"piglin", "hoglin", "drowned", "husk", "stray", "phantom":
		return true
	default:
		return false
	}
}

func main() {
	p := api.New()

	cfg := Config{
		PricePerBlock: 10.0,
		RefundRate:    0.8,
		MaxResPerUser: 5,
		AllowOpBypass: true,
	}

	var mu sync.RWMutex
	residences := make(map[string]*Residence) // resID -> Residence
	var dataPath string

	// 记录玩家当前所处的领地 ID，用于触发进出跨界提示：playerID -> resID
	playerLocations := make(map[uint16]string)

	saveResidences := func() error {
		if dataPath == "" {
			return nil
		}
		list := make([]*Residence, 0, len(residences))
		for _, r := range residences {
			list = append(list, r)
		}
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(dataPath, data, 0644)
	}

	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			_ = json.Unmarshal(c.Config, &cfg)
		}
		if cfg.PricePerBlock <= 0 {
			cfg.PricePerBlock = 10.0
		}
		if cfg.MaxResPerUser <= 0 {
			cfg.MaxResPerUser = 5
		}

		if c.DataDir != "" {
			_ = os.MkdirAll(c.DataDir, 0755)
			dataPath = filepath.Join(c.DataDir, "residences.json")
			if raw, err := os.ReadFile(dataPath); err == nil {
				var list []*Residence
				if err := json.Unmarshal(raw, &list); err == nil {
					mu.Lock()
					for _, r := range list {
						if r != nil && r.ID != "" {
							residences[r.ID] = r
						}
					}
					mu.Unlock()
					log.Printf("[residence] 已成功载入 %d 个领地。", len(residences))
				}
			}
		}
		log.Printf("[residence] 领地系统启用。单价: $%.1f/格, 最大数量: %d", cfg.PricePerBlock, cfg.MaxResPerUser)
		return nil
	}

	// 辅助方法：查找某个坐标所属的领地（如有多个取首个）
	findResidenceAt := func(world string, x, y, z int) *Residence {
		mu.RLock()
		defer mu.RUnlock()
		for _, r := range residences {
			if r.Contains(world, x, y, z) {
				return r
			}
		}
		return nil
	}

	// 辅助方法：按名称精确查找领地
	findResidenceByName := func(name string) *Residence {
		mu.RLock()
		defer mu.RUnlock()
		for _, r := range residences {
			if strings.EqualFold(r.Name, name) {
				return r
			}
		}
		return nil
	}

	// 检查玩家在领地内是否拥有指定 flag 权限
	hasPermission := func(r *Residence, p *api.Player, flag string) bool {
		if r == nil || p == nil {
			return true
		}
		// 1. 领地主人拥有最高权限
		if (p.AccountID != "" && r.OwnerID == p.AccountID) || strings.EqualFold(r.OwnerName, p.Name) {
			return true
		}
		// 2. OP 豁免
		if cfg.AllowOpBypass && (p.Operator || p.Owner) {
			return true
		}
		// 3. 针对该玩家的专属白名单设置
		if pMap, ok := r.Players[p.Name]; ok {
			if val, exists := pMap[flag]; exists {
				return val
			}
		}
		// 4. 领地全局标志
		if val, exists := r.Flags[flag]; exists {
			return val
		}
		// 默认 fallback
		switch flag {
		case "enter", "move":
			return true // 默认允许进入
		default:
			return false // 其余默认禁止
		}
	}

	// ==========================================
	// 1. 核心事件拦截：破坏方块
	// ==========================================
	p.On(api.BlockBreaking, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil || e.Player == nil {
			return nil
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))

		res := findResidenceAt(e.World.ID, x, y, z)
		if res != nil {
			if !hasPermission(res, e.Player, "build") {
				e.Cancel(fmt.Sprintf("⛔ 领地保护：你没有在领地 [%s] 建筑/破坏的权限！", res.Name))
			}
		}
		return nil
	})

	// ==========================================
	// 2. 核心事件拦截：放置方块
	// ==========================================
	p.On(api.BlockPlacing, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil || e.Player == nil {
			return nil
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))

		res := findResidenceAt(e.World.ID, x, y, z)
		if res != nil {
			if !hasPermission(res, e.Player, "build") {
				e.Cancel(fmt.Sprintf("⛔ 领地保护：你没有在领地 [%s] 建筑/放置的权限！", res.Name))
			}
		}
		return nil
	})

	// ==========================================
	// 3. 核心事件拦截：玩家移动与准入控制
	// ==========================================
	p.On(api.PlayerMoving, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil || e.Player == nil {
			return nil
		}
		toX := int(math.Floor(e.Number("x")))
		toY := int(math.Floor(e.Number("y")))
		toZ := int(math.Floor(e.Number("z")))

		resTo := findResidenceAt(e.World.ID, toX, toY, toZ)
		if resTo != nil {
			// 准入检查：若领地禁止访客且没有 enter 权限
			if !hasPermission(resTo, e.Player, "enter") {
				e.Cancel(fmt.Sprintf("⛔ 领地保护：领地 [%s] 属于私有领地，禁止未授权进入！", resTo.Name))
				return nil
			}
		}

		// 跨界进出提示
		lastResID := playerLocations[e.Player.ID]
		currentResID := ""
		if resTo != nil {
			currentResID = resTo.ID
		}

		if lastResID != currentResID {
			playerLocations[e.Player.ID] = currentResID
			// 如果离开了旧领地
			if lastResID != "" {
				mu.RLock()
				oldRes := residences[lastResID]
				mu.RUnlock()
				if oldRes != nil {
					leaveMsg := oldRes.LeaveMsg
					if leaveMsg == "" {
						leaveMsg = fmt.Sprintf("已离开领地 [%s]。", oldRes.Name)
					}
					c.Message(e, "§e"+leaveMsg)
				}
			}
			// 如果进入了新领地
			if resTo != nil {
				enterMsg := resTo.EnterMsg
				if enterMsg == "" {
					enterMsg = fmt.Sprintf("欢迎来到领地 [%s]（主人: %s）。", resTo.Name, resTo.OwnerName)
				}
				c.Message(e, "§a"+enterMsg)
			}
		}
		return nil
	})

	// 玩家离开时清理位置缓存
	p.On(api.PlayerQuit, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Player != nil {
			delete(playerLocations, e.Player.ID)
		}
		return nil
	})

	// ==========================================
	// 4. 核心事件拦截：怪物生成控制 (monsters flag)
	// ==========================================
	p.On(api.EntitySpawning, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil {
			return nil
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))

		res := findResidenceAt(e.World.ID, x, y, z)
		if res != nil {
			// 若领地关闭了怪物生成 (monsters == false)
			allowMonsters := res.Flags["monsters"]
			if !allowMonsters {
				typ := e.String("entityType")
				if typ == "" {
					typ = e.String("mob")
				}
				if isMonster(typ) {
					e.Cancel("领地内已禁止刷新敌对怪物。")
					return nil
				}
			}
		}
		return nil
	})

	// ==========================================
	// 5. 核心事件拦截：容器/箱子打开权限
	// ==========================================
	p.On(api.InventoryOpening, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil || e.Player == nil {
			return nil
		}
		x := int(math.Floor(e.Number("x")))
		y := int(math.Floor(e.Number("y")))
		z := int(math.Floor(e.Number("z")))

		res := findResidenceAt(e.World.ID, x, y, z)
		if res != nil {
			if !hasPermission(res, e.Player, "container") {
				e.Cancel(fmt.Sprintf("⛔ 领地保护：你没有在领地 [%s] 打开容器的权限！", res.Name))
			}
		}
		return nil
	})

	// ==========================================
	// 6. 核心事件拦截：PVP 伤害保护
	// ==========================================
	p.On(api.PlayerDamaging, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.World == nil || e.Player == nil {
			return nil
		}
		// 检查攻击者是否是玩家，受害者是否在领地内
		attackerID := e.String("attacker")
		if attackerID != "" && attackerID != "environment" {
			x := int(math.Floor(e.Player.X))
			y := int(math.Floor(e.Player.Y))
			z := int(math.Floor(e.Player.Z))

			res := findResidenceAt(e.World.ID, x, y, z)
			if res != nil {
				// 若领地未开启 pvp，阻止玩家间互相伤害
				if !res.Flags["pvp"] {
					e.Cancel(fmt.Sprintf("⛔ 领地保护：领地 [%s] 禁止 PVP 决斗！", res.Name))
				}
			}
		}
		return nil
	})

	// ==========================================
	// 7. 指令注册系统：/res (别名 /residence)
	// ==========================================
	p.Command(api.Command{
		Name:        "res",
		Aliases:     []string{"residence", "claim"},
		Description: "领地系统：圈地、权限管理、怪物控制",
		Usage:       "/res [help|select|create|set|pset|info|list|tp|remove]",
	}, func(c *api.Context, e *api.Event, args []string) error {
		if e.Player == nil || e.World == nil {
			return nil
		}

		if len(args) == 0 || args[0] == "help" {
			c.Message(e, "§6===== 🏛️ Residence 领地指令系统 =====")
			c.Message(e, "§b/res select <10|50|100> §7- 预览以脚下为中心的领地范围与费用")
			c.Message(e, "§b/res create <领地名> <10|50|100> §7- 以当前坐标为中心购买领地")
			c.Message(e, "§b/res info [领地名] §7- 查看当前领地或指定领地详情")
			c.Message(e, "§b/res list §7- 查看我的所有领地")
			c.Message(e, "§b/res tp <领地名> §7- 传送到领地中心")
			c.Message(e, "§b/res set <monsters|enter|build|pvp|container> <true|false> §7- 设置领地全局开关")
			c.Message(e, "§b/res pset [领地名] <玩家名> <build|enter|container> <true|false> §7- 设置指定玩家权限")
			c.Message(e, "§b/res message enter|leave <文本> §7- 设置进出提示语")
			c.Message(e, "§b/res remove <领地名> §7- 移除领地并退还 80% 费用")
			c.Message(e, fmt.Sprintf("§7💡 提示：领地每格单价 $%.1f，按平面计算，高度以正方体覆盖。", cfg.PricePerBlock))
			return nil
		}

		sub := strings.ToLower(args[0])
		switch sub {
		case "select", "cost", "preview":
			if len(args) < 2 {
				c.Message(e, "§c用法：/res select <10|50|100>")
				return nil
			}
			size, err := strconv.Atoi(args[1])
			if err != nil || (size != 10 && size != 50 && size != 100) {
				c.Message(e, "§c支持的规格范围：10*10、50*50、100*100。")
				return nil
			}
			cx := int(math.Floor(e.Player.X))
			cy := int(math.Floor(e.Player.Y))
			cz := int(math.Floor(e.Player.Z))
			half := size / 2

			minX, maxX := cx-half, cx+half-1
			minZ, maxZ := cz-half, cz+half-1
			minY := int(math.Max(0, float64(cy-half)))
			maxY := int(math.Min(127, float64(cy+half-1)))

			area := size * size
			cost := float64(area) * cfg.PricePerBlock

			c.Message(e, fmt.Sprintf("§6📐 领地规格预览 [%d*%d]：", size, size))
			c.Message(e, fmt.Sprintf("§7- 中心坐标: §f(%d, %d, %d)", cx, cy, cz))
			c.Message(e, fmt.Sprintf("§7- X 范围: §f%d ~ %d §7(宽度 %d)", minX, maxX, size))
			c.Message(e, fmt.Sprintf("§7- Y 范围: §f%d ~ %d §7(高度 %d, 正方体范围)", minY, maxY, maxY-minY+1))
			c.Message(e, fmt.Sprintf("§7- Z 范围: §f%d ~ %d §7(长度 %d)", minZ, maxZ, size))
			c.Message(e, fmt.Sprintf("§7- 平面面积: §f%d 格", area))
			c.Message(e, fmt.Sprintf("§7- 购买价格: §a$%.2f §7(每格 $%.1f)", cost, cfg.PricePerBlock))
			c.Message(e, fmt.Sprintf("§e确认购买请输入：§b/res create <领地名> %d", size))
			return nil

		case "create", "buy":
			if len(args) < 3 {
				c.Message(e, "§c用法：/res create <领地名> <10|50|100>")
				return nil
			}
			resName := strings.TrimSpace(args[1])
			if len(resName) < 1 || len(resName) > 16 {
				c.Message(e, "§c领地名称长度必须在 1 到 16 个字符之间。")
				return nil
			}
			if strings.ContainsAny(resName, " /\\:*?\"<>|") {
				c.Message(e, "§c领地名称不能包含特殊符号或空格。")
				return nil
			}

			size, err := strconv.Atoi(args[2])
			if err != nil || (size != 10 && size != 50 && size != 100) {
				c.Message(e, "§c当前仅支持规格：10、50 或 100。例如：/res create mybase 10")
				return nil
			}

			// 检查是否已有同名领地
			if findResidenceByName(resName) != nil {
				c.Message(e, fmt.Sprintf("§c领地名称 [%s] 已被占用，请更换其他名称。", resName))
				return nil
			}

			// 检查玩家拥有的领地数量上限
			userResCount := 0
			mu.RLock()
			for _, r := range residences {
				if (e.Player.AccountID != "" && r.OwnerID == e.Player.AccountID) || strings.EqualFold(r.OwnerName, e.Player.Name) {
					userResCount++
				}
			}
			mu.RUnlock()

			if userResCount >= cfg.MaxResPerUser && !e.Player.Operator {
				c.Message(e, fmt.Sprintf("§c你已拥有 %d 个领地，已达上限（最大 %d 个）。", userResCount, cfg.MaxResPerUser))
				return nil
			}

			// 计算价格
			area := size * size
			cost := float64(area) * cfg.PricePerBlock

			// 检查余额
			if e.Player.Balance < cost {
				c.Message(e, fmt.Sprintf("§c余额不足！购买 %d*%d 领地需要 §a$%.2f§c，你当前余额为 §e$%.2f§c。", size, size, cost, e.Player.Balance))
				return nil
			}

			// 计算正方体立方体坐标边界
			cx := int(math.Floor(e.Player.X))
			cy := int(math.Floor(e.Player.Y))
			cz := int(math.Floor(e.Player.Z))
			half := size / 2

			minX, maxX := cx-half, cx+half-1
			minZ, maxZ := cz-half, cz+half-1
			minY := int(math.Max(0, float64(cy-half)))
			maxY := int(math.Min(127, float64(cy+half-1)))

			newRes := &Residence{
				ID:        fmt.Sprintf("res_%d_%s", time.Now().Unix(), resName),
				Name:      resName,
				OwnerID:   e.Player.AccountID,
				OwnerName: e.Player.Name,
				WorldID:   e.World.ID,
				Center:    [3]int{cx, cy, cz},
				Size:      size,
				MinPos:    [3]int{minX, minY, minZ},
				MaxPos:    [3]int{maxX, maxY, maxZ},
				Cost:      cost,
				Flags: map[string]bool{
					"build":     false, // 默认禁止外人建筑
					"enter":     true,  // 默认允许进入
					"monsters":  false, // 默认禁止刷怪
					"pvp":       false, // 默认禁止 PVP
					"container": false, // 默认禁止外人翻箱子
				},
				Players:   make(map[string]map[string]bool),
				EnterMsg:  fmt.Sprintf("欢迎来到领地 [%s]（主人: %s）。", resName, e.Player.Name),
				LeaveMsg:  fmt.Sprintf("你已离开领地 [%s]。", resName),
				CreatedAt: time.Now().Unix(),
			}

			// 检查是否与现有领地重叠
			mu.RLock()
			for _, r := range residences {
				if isOverlap(newRes, r) {
					mu.RUnlock()
					c.Message(e, fmt.Sprintf("§c创建失败：该范围与已有领地 [%s]（主人: %s）重叠！", r.Name, r.OwnerName))
					return nil
				}
			}
			mu.RUnlock()

			// 执行扣款
			c.Withdraw(e, cost, fmt.Sprintf("购买领地 %s (%d*%d)", resName, size, size))

			// 保存领地
			mu.Lock()
			residences[newRes.ID] = newRes
			_ = saveResidences()
			mu.Unlock()

			c.Message(e, fmt.Sprintf("§a🎉 恭喜！领地 [%s] 创建成功！", resName))
			c.Message(e, fmt.Sprintf("§7- 规格: §f%d*%d §7(平面 %d 格, 立方体范围)", size, size, area))
			c.Message(e, fmt.Sprintf("§7- 边界: X:[%d~%d], Y:[%d~%d], Z:[%d~%d]", minX, maxX, minY, maxY, minZ, maxZ))
			c.Message(e, fmt.Sprintf("§7- 扣除金币: §a$%.2f§7，剩余金币: §e$%.2f", cost, e.Player.Balance-cost))
			c.Message(e, "§7- 默认开启保护：禁止怪物刷新、禁止外人建筑与开箱。")
			c.Message(e, "§b输入 /res set monsters true 可允许刷怪；输入 /res pset <玩家名> build true 可授权建筑。")
			return nil

		case "set":
			// 站在领地内修改全局标志：/res set <flag> <true|false>
			if len(args) < 3 {
				c.Message(e, "§c用法：/res set <monsters|enter|build|pvp|container> <true|false>")
				return nil
			}
			flag := strings.ToLower(args[1])
			valStr := strings.ToLower(args[2])
			val := valStr == "true" || valStr == "1" || valStr == "on" || valStr == "yes"

			// 获取玩家当前所在位置的领地
			px := int(math.Floor(e.Player.X))
			py := int(math.Floor(e.Player.Y))
			pz := int(math.Floor(e.Player.Z))
			res := findResidenceAt(e.World.ID, px, py, pz)

			if res == nil {
				c.Message(e, "§c你当前没有站在任何领地内！请进入你的领地再执行此命令。")
				return nil
			}

			// 权限检查：必须是主人或 OP
			isOwner := (e.Player.AccountID != "" && res.OwnerID == e.Player.AccountID) || strings.EqualFold(res.OwnerName, e.Player.Name)
			if !isOwner && !e.Player.Operator {
				c.Message(e, fmt.Sprintf("§c你不是领地 [%s] 的主人，无权修改其标志！", res.Name))
				return nil
			}

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
				c.Message(e, "§c不支持的标志！支持列表：monsters(刷怪), enter(准入), build(建筑), pvp(打斗), container(翻箱)")
				return nil
			}

			mu.Lock()
			res.Flags[flag] = val
			_ = saveResidences()
			mu.Unlock()

			stateStr := "§a允许 (true)"
			if !val {
				stateStr = "§c禁止 (false)"
			}
			c.Message(e, fmt.Sprintf("§a领地 [%s] 的标志 [%s] 已更新为：%s", res.Name, flag, stateStr))
			return nil

		case "pset":
			// 设置指定玩家权限：
			// 形式 A: /res pset <领地名> <玩家名> <flag> <true|false> (4个参数)
			// 形式 B: /res pset <玩家名> <flag> <true|false> (站在领地内，3个参数)
			var targetRes *Residence
			var targetPlayer string
			var flag string
			var val bool

			if len(args) >= 5 {
				resName := args[1]
				targetRes = findResidenceByName(resName)
				if targetRes == nil {
					c.Message(e, fmt.Sprintf("§c未找到领地 [%s]。", resName))
					return nil
				}
				targetPlayer = args[2]
				flag = strings.ToLower(args[3])
				vStr := strings.ToLower(args[4])
				val = vStr == "true" || vStr == "1" || vStr == "on" || vStr == "yes"
			} else if len(args) == 4 {
				px := int(math.Floor(e.Player.X))
				py := int(math.Floor(e.Player.Y))
				pz := int(math.Floor(e.Player.Z))
				targetRes = findResidenceAt(e.World.ID, px, py, pz)
				if targetRes == nil {
					c.Message(e, "§c你没有站在任何领地内，请使用格式：/res pset <领地名> <玩家名> <flag> <true|false>")
					return nil
				}
				targetPlayer = args[1]
				flag = strings.ToLower(args[2])
				vStr := strings.ToLower(args[3])
				val = vStr == "true" || vStr == "1" || vStr == "on" || vStr == "yes"
			} else {
				c.Message(e, "§c用法：/res pset [领地名] <玩家名> <build|enter|container> <true|false>")
				return nil
			}

			// 检查是否是主人
			isOwner := (e.Player.AccountID != "" && targetRes.OwnerID == e.Player.AccountID) || strings.EqualFold(targetRes.OwnerName, e.Player.Name)
			if !isOwner && !e.Player.Operator {
				c.Message(e, fmt.Sprintf("§c你不是领地 [%s] 的主人，无权设置成员权限！", targetRes.Name))
				return nil
			}

			switch flag {
			case "build", "place", "break":
				flag = "build"
			case "enter", "move":
				flag = "enter"
			case "container", "chest":
				flag = "container"
			default:
				c.Message(e, "§c玩家权限标志仅支持：build(建筑), enter(进入), container(容器)")
				return nil
			}

			mu.Lock()
			if targetRes.Players == nil {
				targetRes.Players = make(map[string]map[string]bool)
			}
			if targetRes.Players[targetPlayer] == nil {
				targetRes.Players[targetPlayer] = make(map[string]bool)
			}
			targetRes.Players[targetPlayer][flag] = val
			_ = saveResidences()
			mu.Unlock()

			stateStr := "§a授予 (true)"
			if !val {
				stateStr = "§c剥夺 (false)"
			}
			c.Message(e, fmt.Sprintf("§a已为玩家 [%s] 在领地 [%s] 中设置 [%s] 权限为：%s", targetPlayer, targetRes.Name, flag, stateStr))
			return nil

		case "info":
			var res *Residence
			if len(args) >= 2 {
				res = findResidenceByName(args[1])
				if res == nil {
					c.Message(e, fmt.Sprintf("§c未找到名为 [%s] 的领地。", args[1]))
					return nil
				}
			} else {
				px := int(math.Floor(e.Player.X))
				py := int(math.Floor(e.Player.Y))
				pz := int(math.Floor(e.Player.Z))
				res = findResidenceAt(e.World.ID, px, py, pz)
				if res == nil {
					c.Message(e, "§c你当前不在任何领地内。查看指定领地请输入：/res info <领地名>")
					return nil
				}
			}

			c.Message(e, fmt.Sprintf("§6===== 🏛️ 领地信息: %s =====", res.Name))
			c.Message(e, fmt.Sprintf("§7- 主人: §f%s", res.OwnerName))
			c.Message(e, fmt.Sprintf("§7- 中心点: §f(%d, %d, %d) §7| 边长: §f%d*%d", res.Center[0], res.Center[1], res.Center[2], res.Size, res.Size))
			c.Message(e, fmt.Sprintf("§7- 边界范围: X:[%d~%d], Y:[%d~%d], Z:[%d~%d]", res.MinPos[0], res.MaxPos[0], res.MinPos[1], res.MaxPos[1], res.MinPos[2], res.MaxPos[2]))
			c.Message(e, fmt.Sprintf("§7- 购买成本: §a$%.2f", res.Cost))

			// 全局标志
			var flagStrs []string
			for k, v := range res.Flags {
				if v {
					flagStrs = append(flagStrs, fmt.Sprintf("§a%s§7", k))
				} else {
					flagStrs = append(flagStrs, fmt.Sprintf("§c%s§7", k))
				}
			}
			sort.Strings(flagStrs)
			c.Message(e, fmt.Sprintf("§7- 全局标志: [%s]", strings.Join(flagStrs, ", ")))

			// 玩家权限列表
			if len(res.Players) > 0 {
				var pStrs []string
				for pName, pFlags := range res.Players {
					var pf []string
					for fk, fv := range pFlags {
						if fv {
							pf = append(pf, "+"+fk)
						} else {
							pf = append(pf, "-"+fk)
						}
					}
					pStrs = append(pStrs, fmt.Sprintf("%s(%s)", pName, strings.Join(pf, ",")))
				}
				c.Message(e, fmt.Sprintf("§7- 成员白名单: §f%s", strings.Join(pStrs, "; ")))
			} else {
				c.Message(e, "§7- 成员白名单: §8(暂无指定玩家)")
			}
			return nil

		case "list":
			var myRes []*Residence
			mu.RLock()
			for _, r := range residences {
				if (e.Player.AccountID != "" && r.OwnerID == e.Player.AccountID) || strings.EqualFold(r.OwnerName, e.Player.Name) {
					myRes = append(myRes, r)
				}
			}
			mu.RUnlock()

			if len(myRes) == 0 {
				c.Message(e, "§7你当前还没有拥有任何领地。输入 §b/res select 10 §7可开始选购。")
				return nil
			}

			c.Message(e, fmt.Sprintf("§6===== 我的领地列表 (%d/%d) =====", len(myRes), cfg.MaxResPerUser))
			for i, r := range myRes {
				c.Message(e, fmt.Sprintf("§b%d. [%s] §7- 规格: §f%d*%d §7| 中心: §f(%d, %d, %d) §7| 费用: §a$%.1f",
					i+1, r.Name, r.Size, r.Size, r.Center[0], r.Center[1], r.Center[2], r.Cost))
			}
			c.Message(e, "§7提示：输入 §b/res tp <领地名> §7可传送到领地。")
			return nil

		case "tp":
			if len(args) < 2 {
				c.Message(e, "§c用法：/res tp <领地名>")
				return nil
			}
			res := findResidenceByName(args[1])
			if res == nil {
				c.Message(e, fmt.Sprintf("§c未找到领地 [%s]。", args[1]))
				return nil
			}
			// 准入检查
			if !hasPermission(res, e.Player, "enter") {
				c.Message(e, fmt.Sprintf("§c你没有进入领地 [%s] 的权限！", res.Name))
				return nil
			}
			// 传送至中心点上方安全高度
			targetX := float64(res.Center[0]) + 0.5
			targetY := float64(res.Center[1]) + 0.5
			targetZ := float64(res.Center[2]) + 0.5
			c.Teleport(e, targetX, targetY, targetZ)
			c.Message(e, fmt.Sprintf("§a已传送至领地 [%s] 的中心点！", res.Name))
			return nil

		case "message", "msg":
			// /res message enter/leave <文本>
			if len(args) < 3 {
				c.Message(e, "§c用法：/res message <enter|leave> <提示内容>")
				return nil
			}
			kind := strings.ToLower(args[1])
			text := strings.Join(args[2:], " ")

			px := int(math.Floor(e.Player.X))
			py := int(math.Floor(e.Player.Y))
			pz := int(math.Floor(e.Player.Z))
			res := findResidenceAt(e.World.ID, px, py, pz)
			if res == nil {
				c.Message(e, "§c你必须站在自己的领地内执行此命令！")
				return nil
			}
			isOwner := (e.Player.AccountID != "" && res.OwnerID == e.Player.AccountID) || strings.EqualFold(res.OwnerName, e.Player.Name)
			if !isOwner && !e.Player.Operator {
				c.Message(e, "§c你不是领地主人，无权修改提示语！")
				return nil
			}

			mu.Lock()
			if kind == "enter" {
				res.EnterMsg = text
			} else {
				res.LeaveMsg = text
			}
			_ = saveResidences()
			mu.Unlock()
			c.Message(e, fmt.Sprintf("§a已更新领地 [%s] 的 %s 提示语！", res.Name, kind))
			return nil

		case "remove", "delete":
			if len(args) < 2 {
				c.Message(e, "§c用法：/res remove <领地名>")
				return nil
			}
			resName := args[1]
			res := findResidenceByName(resName)
			if res == nil {
				c.Message(e, fmt.Sprintf("§c未找到领地 [%s]。", resName))
				return nil
			}

			isOwner := (e.Player.AccountID != "" && res.OwnerID == e.Player.AccountID) || strings.EqualFold(res.OwnerName, e.Player.Name)
			if !isOwner && !e.Player.Operator {
				c.Message(e, fmt.Sprintf("§c你不是领地 [%s] 的主人，无权删除该领地！", res.Name))
				return nil
			}

			refund := res.Cost * cfg.RefundRate
			if refund > 0 {
				c.Deposit(e, refund, fmt.Sprintf("删除领地 %s 退款", res.Name))
			}

			mu.Lock()
			delete(residences, res.ID)
			_ = saveResidences()
			mu.Unlock()

			c.Message(e, fmt.Sprintf("§e领地 [%s] 已成功删除并释放土地！已返还 %.0f%% 购地款项: §a$%.2f§e。", res.Name, cfg.RefundRate*100, refund))
			return nil

		default:
			c.Message(e, fmt.Sprintf("§c未知子命令 [%s]，输入 /res help 查看帮助。", args[0]))
			return nil
		}
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
