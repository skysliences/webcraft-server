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
)

// ---------------------------------------------------------------------------
// 商店系统 (Shop System)
// 支持商品价格配置、分类管理、购买与出售、经济系统无缝联动与自定义 GUI
// ---------------------------------------------------------------------------

type ShopCategory struct {
	ID   string `json:"id"`             // 分类ID: blocks, ores, farming, tools, redstone, misc
	Name string `json:"name"`           // 中文分类名称
	Icon string `json:"icon,omitempty"` // 材质图标
}

type ShopItem struct {
	ID             string  `json:"id"`                       // 商品唯一key，如 "diamond"
	ItemID         int     `json:"itemId"`                   // 游戏物品数字ID，如 264
	Name           string  `json:"name"`                     // 中文显示名，如 "钻石"
	Category       string  `json:"category"`                 // 分类ID
	BuyPrice       float64 `json:"buyPrice"`                 // 购买单价，<=0 表示不开放购买
	SellPrice      float64 `json:"sellPrice"`                // 出售回收单价，<=0 表示不开放回收
	Icon           string  `json:"icon,omitempty"`           // 材质名称或图标key
	Description    string  `json:"description,omitempty"`    // 描述介绍
	MaxBuyPerTrade int     `json:"maxBuyPerTrade,omitempty"` // 单次最大购买数，默认64
}

type ShopConfig struct {
	Title          string         `json:"title"`          // 商店界面标题
	CurrencySymbol string         `json:"currencySymbol"` // 货币符号，默认 "$"
	Categories     []ShopCategory `json:"categories"`     // 分类列表
	Items          []ShopItem     `json:"items"`          // 商品列表
}

type ShopManager struct {
	mu     sync.RWMutex
	server *Server
	config ShopConfig
	byID   map[string]*ShopItem
	byItem map[int]*ShopItem
}

var (
	globalShop *ShopManager
	shopOnce   sync.Once
)

// standardShopItemIDs 提供官方标准商品与核心物品/方块的精准映射
var standardShopItemIDs = map[string]int{
	"diamond":         318, // 钻石 (Diamond)
	"emerald":         38,  // 绿宝石方块 (Block of Emerald)
	"gold_ingot":      316, // 金锭 (Gold Ingot)
	"iron_ingot":      315, // 铁锭 (Iron Ingot)
	"coal":            311, // 煤炭 (Coal)
	"lapis_lazuli":    319, // 青金石 (Lapis Lazuli)
	"stone":           1,   // 石头 (Stone)
	"cobblestone":     4,   // 圆石 (Cobblestone)
	"dirt":            3,   // 泥土 (Dirt)
	"oak_log":         6,   // 橡木原木 (Oak Log)
	"oak_planks":      5,   // 橡木木板 (Oak Planks)
	"glass":           10,  // 玻璃 (Glass)
	"bricks":          11,  // 红砖块 (Bricks)
	"obsidian":        24,  // 黑曜石 (Obsidian)
	"cooked_beef":     341, // 牛排 (Steak)
	"cooked_porkchop": 343, // 熟猪排 (Cooked Porkchop)
	"bread":           339, // 面包 (Bread)
	"golden_apple":    338, // 金苹果 (Golden Apple)
	"wheat":           336, // 小麦 (Wheat)
	"carrot":          350, // 胡萝卜 (Carrot)
	"potato":          351, // 马铃薯 (Potato)
	"diamond_sword":   276, // 钻石剑 (Diamond Sword)
	"diamond_pickaxe": 278, // 钻石镐 (Diamond Pickaxe)
	"iron_sword":      266, // 铁剑 (Iron Sword)
	"iron_pickaxe":    268, // 铁镐 (Iron Pickaxe)
	"bow":             301, // 弓 (Bow)
	"arrow":           302, // 箭矢 (Arrow)
	"shield":          303, // 盾牌 (Shield)
	"redstone_dust":   320, // 红石粉 (Spark Dust)
	"piston":          358, // 活塞 (Piston)
	"sticky_piston":   359, // 粘性活塞 (Sticky Piston)
	"tnt":             27,  // TNT
	"repeater":        356, // 红石中继器 (Redstone Repeater)
	"hopper":          385, // 漏斗 (Hopper)
	"torch":           82,  // 火把 (Torch)
	"water_bucket":    307, // 水桶 (Water Bucket)
	"lava_bucket":     308, // 岩浆桶 (Lava Bucket)
	"bone":            325, // 骨头 (Bone)
	"ender_pearl":     334, // 末影珍珠 (Blink Pearl)
}

// defaultShopConfig 提供丰富的默认开箱即用商品列表与回收定价
func defaultShopConfig() ShopConfig {
	return ShopConfig{
		Title:          "🛒 WebCraft 贸易集市",
		CurrencySymbol: "$",
		Categories: []ShopCategory{
			{ID: "all", Name: "全部商品", Icon: "chest"},
			{ID: "ores", Name: "💎 矿石宝石", Icon: "diamond"},
			{ID: "blocks", Name: "🧱 建筑材料", Icon: "stone"},
			{ID: "farming", Name: "🥩 农牧食物", Icon: "cooked_beef"},
			{ID: "tools", Name: "⚔️ 工具武器", Icon: "iron_sword"},
			{ID: "redstone", Name: "⚙️ 红石机械", Icon: "redstone"},
			{ID: "misc", Name: "📦 杂项道具", Icon: "torch"},
		},
		Items: []ShopItem{
			// --- 矿石贵重品 (Ores & Gems) ---
			{ID: "diamond", ItemID: 318, Name: "钻石", Category: "ores", BuyPrice: 100.0, SellPrice: 50.0, Icon: "diamond", Description: "极为珍贵的蓝色宝石"},
			{ID: "emerald", ItemID: 38, Name: "绿宝石", Category: "ores", BuyPrice: 80.0, SellPrice: 40.0, Icon: "emerald", Description: "村民贸易常用通用货币"},
			{ID: "gold_ingot", ItemID: 316, Name: "金锭", Category: "ores", BuyPrice: 30.0, SellPrice: 15.0, Icon: "gold_ingot", Description: "闪烁着金黄光芒的贵金属"},
			{ID: "iron_ingot", ItemID: 315, Name: "铁锭", Category: "ores", BuyPrice: 10.0, SellPrice: 5.0, Icon: "iron_ingot", Description: "工业与装备制造的基础支柱"},
			{ID: "coal", ItemID: 311, Name: "煤炭", Category: "ores", BuyPrice: 4.0, SellPrice: 2.0, Icon: "coal", Description: "熔炉冶炼不可或缺的燃料"},
			{ID: "lapis_lazuli", ItemID: 319, Name: "青金石", Category: "ores", BuyPrice: 6.0, SellPrice: 3.0, Icon: "lapis_lazuli", Description: "附魔台施加附魔的必备染料"},

			// --- 建筑方块 (Blocks & Building) ---
			{ID: "stone", ItemID: 1, Name: "石头", Category: "blocks", BuyPrice: 1.0, SellPrice: 0.5, Icon: "stone", Description: "地下开采烧制的基础石料"},
			{ID: "cobblestone", ItemID: 4, Name: "圆石", Category: "blocks", BuyPrice: 0.5, SellPrice: 0.2, Icon: "cobblestone", Description: "粗糙坚硬的开采石料"},
			{ID: "dirt", ItemID: 3, Name: "泥土", Category: "blocks", BuyPrice: 0.5, SellPrice: 0.1, Icon: "dirt", Description: "大地上最平凡肥沃的土壤"},
			{ID: "oak_log", ItemID: 6, Name: "橡木原木", Category: "blocks", BuyPrice: 2.0, SellPrice: 1.0, Icon: "oak_wood", Description: "优质坚实的森林木材"},
			{ID: "oak_planks", ItemID: 5, Name: "橡木木板", Category: "blocks", BuyPrice: 0.8, SellPrice: 0.3, Icon: "oak_planks", Description: "原木加工成的平整木板"},
			{ID: "glass", ItemID: 10, Name: "玻璃", Category: "blocks", BuyPrice: 2.0, SellPrice: 0.8, Icon: "glass", Description: "通透明亮的采光建筑方块"},
			{ID: "bricks", ItemID: 11, Name: "红砖块", Category: "blocks", BuyPrice: 4.0, SellPrice: 1.5, Icon: "brick_block", Description: "粘土烧制规整的高档砖石"},
			{ID: "obsidian", ItemID: 24, Name: "黑曜石", Category: "blocks", BuyPrice: 60.0, SellPrice: 25.0, Icon: "obsidian", Description: "水火交融淬炼而成的至坚岩石"},

			// --- 农牧食物 (Farming & Food) ---
			{ID: "cooked_beef", ItemID: 341, Name: "牛排", Category: "farming", BuyPrice: 8.0, SellPrice: 4.0, Icon: "cooked_beef", Description: "鲜嫩多汁的高营养肉排"},
			{ID: "cooked_porkchop", ItemID: 343, Name: "熟猪排", Category: "farming", BuyPrice: 8.0, SellPrice: 4.0, Icon: "cooked_porkchop", Description: "香气扑鼻的熟猪肉排"},
			{ID: "bread", ItemID: 339, Name: "面包", Category: "farming", BuyPrice: 3.0, SellPrice: 1.5, Icon: "bread", Description: "小麦烘焙的松软口粮"},
			{ID: "golden_apple", ItemID: 338, Name: "金苹果", Category: "farming", BuyPrice: 150.0, SellPrice: 60.0, Icon: "golden_apple", Description: "赋予再生与伤害吸收的奇迹果实"},
			{ID: "wheat", ItemID: 336, Name: "小麦", Category: "farming", BuyPrice: 2.0, SellPrice: 1.0, Icon: "wheat", Description: "金黄的成熟麦穗"},
			{ID: "carrot", ItemID: 350, Name: "胡萝卜", Category: "farming", BuyPrice: 2.0, SellPrice: 1.0, Icon: "carrot", Description: "脆甜可口的农场作物"},
			{ID: "potato", ItemID: 351, Name: "马铃薯", Category: "farming", BuyPrice: 2.0, SellPrice: 1.0, Icon: "potato", Description: "耐储存的高产块茎"},

			// --- 工具装备 (Tools & Combat) ---
			{ID: "diamond_sword", ItemID: 276, Name: "钻石剑", Category: "tools", BuyPrice: 300.0, SellPrice: 100.0, Icon: "diamond_sword", Description: "削铁如泥的终极利刃"},
			{ID: "diamond_pickaxe", ItemID: 278, Name: "钻石镐", Category: "tools", BuyPrice: 350.0, SellPrice: 120.0, Icon: "diamond_pickaxe", Description: "能够开采黑曜石的高级矿镐"},
			{ID: "iron_sword", ItemID: 266, Name: "铁剑", Category: "tools", BuyPrice: 25.0, SellPrice: 10.0, Icon: "iron_sword", Description: "坚固实用的近战武器"},
			{ID: "iron_pickaxe", ItemID: 268, Name: "铁镐", Category: "tools", BuyPrice: 30.0, SellPrice: 12.0, Icon: "iron_pickaxe", Description: "采掘金矿、红石与钻石的利器"},
			{ID: "bow", ItemID: 301, Name: "弓", Category: "tools", BuyPrice: 30.0, SellPrice: 10.0, Icon: "bow", Description: "远程狙击敌人的强弓"},
			{ID: "arrow", ItemID: 302, Name: "箭矢", Category: "tools", BuyPrice: 1.0, SellPrice: 0.3, Icon: "arrow", Description: "弓箭发射的尖锐弹药"},
			{ID: "shield", ItemID: 303, Name: "盾牌", Category: "tools", BuyPrice: 20.0, SellPrice: 8.0, Icon: "shield", Description: "格挡正面物理与远程伤害的坚盾"},

			// --- 红石机械 (Redstone & Mechanics) ---
			{ID: "redstone_dust", ItemID: 320, Name: "红石粉", Category: "redstone", BuyPrice: 5.0, SellPrice: 2.5, Icon: "redstone", Description: "传输电信号的核心能源"},
			{ID: "piston", ItemID: 358, Name: "活塞", Category: "redstone", BuyPrice: 15.0, SellPrice: 6.0, Icon: "piston", Description: "推动方块的机械装置"},
			{ID: "sticky_piston", ItemID: 359, Name: "粘性活塞", Category: "redstone", BuyPrice: 25.0, SellPrice: 10.0, Icon: "sticky_piston", Description: "能够拉回方块的强力活塞"},
			{ID: "tnt", ItemID: 27, Name: "TNT", Category: "redstone", BuyPrice: 50.0, SellPrice: 20.0, Icon: "tnt", Description: "威力巨大的易爆爆炸物"},
			{ID: "repeater", ItemID: 356, Name: "红石中继器", Category: "redstone", BuyPrice: 8.0, SellPrice: 3.0, Icon: "repeater", Description: "延长红石信号并提供延迟"},
			{ID: "hopper", ItemID: 385, Name: "漏斗", Category: "redstone", BuyPrice: 25.0, SellPrice: 10.0, Icon: "hopper", Description: "自动收集与传输物品的漏斗"},

			// --- 杂项道具 (Misc) ---
			{ID: "torch", ItemID: 82, Name: "火把", Category: "misc", BuyPrice: 1.0, SellPrice: 0.2, Icon: "torch", Description: "驱散黑暗并防止怪物生成的照明源"},
			{ID: "water_bucket", ItemID: 307, Name: "水桶", Category: "misc", BuyPrice: 20.0, SellPrice: 8.0, Icon: "water_bucket", Description: "盛装生命之源清水的铁桶"},
			{ID: "lava_bucket", ItemID: 308, Name: "岩浆桶", Category: "misc", BuyPrice: 30.0, SellPrice: 12.0, Icon: "lava_bucket", Description: "盛装高温岩浆的强力燃料桶"},
			{ID: "bone", ItemID: 325, Name: "骨头", Category: "misc", BuyPrice: 3.0, SellPrice: 1.5, Icon: "bone", Description: "骷髅掉落的骨骼，可研磨为骨粉"},
			{ID: "ender_pearl", ItemID: 334, Name: "末影珍珠", Category: "misc", BuyPrice: 80.0, SellPrice: 35.0, Icon: "ender_pearl", Description: "投掷后瞬移至落点的神奇珍珠"},
		},
	}
}

// initShopManager 初始化全局商店管理器
func (s *Server) initShopManager() *ShopManager {
	if s == nil {
		return &ShopManager{config: defaultShopConfig()}
	}
	if s.shop == nil {
		mgr := &ShopManager{
			server: s,
			config: defaultShopConfig(),
			byID:   make(map[string]*ShopItem),
			byItem: make(map[int]*ShopItem),
		}
		mgr.loadConfig()
		s.shop = mgr
	}
	return s.shop
}

// loadConfig 从本地配置文件或插件目录加载商品价格
func (m *ShopManager) loadConfig() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 候选配置文件路径 (按优先级查找)
	dataDir := ""
	if m.server != nil {
		dataDir = m.server.config.DataDir
	}
	candidates := []string{
		filepath.Join(dataDir, "shop.json"),
		filepath.Join(dataDir, "shop_config.json"),
		filepath.Join(dataDir, "config", "shop.json"),
		"plugins/shop/config.json",
		"backend/plugins/shop/config.json",
	}

	loaded := false
	var activePath string
	for _, p := range candidates {
		if p == "" || p == "shop.json" {
			continue
		}
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			var cfg ShopConfig
			if err := json.Unmarshal(data, &cfg); err == nil && len(cfg.Items) > 0 {
				m.config = cfg
				loaded = true
				activePath = p
				break
			}
		}
	}

	if !loaded && dataDir != "" {
		// 写入默认配置文件供服主后续自由修改
		defaultPath := filepath.Join(dataDir, "shop.json")
		if err := os.MkdirAll(filepath.Dir(defaultPath), 0755); err == nil {
			if data, err := json.MarshalIndent(m.config, "", "  "); err == nil {
				_ = os.WriteFile(defaultPath, data, 0644)
				activePath = defaultPath
			}
		}
	}

	// 重建快速索引并修正物品 ID
	m.byID = make(map[string]*ShopItem)
	m.byItem = make(map[int]*ShopItem)

	for i := range m.config.Items {
		item := &m.config.Items[i]
		if item.MaxBuyPerTrade <= 0 {
			item.MaxBuyPerTrade = 64
		}
		// 自动对齐核心真实物品ID：如果配置未填或命中旧版错误ID（如 diamond=264 等），自动校准
		if stdID, ok := standardShopItemIDs[item.ID]; ok {
			if item.ItemID <= 0 || item.ItemID == 264 && item.ID == "diamond" ||
				item.ItemID == 266 && item.ID == "gold_ingot" ||
				item.ItemID == 265 && item.ID == "iron_ingot" ||
				item.ItemID == 263 && item.ID == "coal" ||
				item.ItemID == 17 && item.ID == "oak_log" ||
				item.ItemID == 20 && item.ID == "glass" ||
				item.ItemID == 45 && item.ID == "bricks" ||
				item.ItemID == 49 && item.ID == "obsidian" ||
				item.ItemID == 364 && item.ID == "cooked_beef" ||
				item.ItemID == 320 && item.ID == "cooked_porkchop" ||
				item.ItemID == 297 && item.ID == "bread" ||
				item.ItemID == 322 && item.ID == "golden_apple" ||
				item.ItemID == 296 && item.ID == "wheat" ||
				item.ItemID == 391 && item.ID == "carrot" ||
				item.ItemID == 392 && item.ID == "potato" ||
				item.ItemID == 267 && item.ID == "iron_sword" ||
				item.ItemID == 257 && item.ID == "iron_pickaxe" ||
				item.ItemID == 261 && item.ID == "bow" ||
				item.ItemID == 262 && item.ID == "arrow" ||
				item.ItemID == 442 && item.ID == "shield" ||
				item.ItemID == 108 && item.ID == "redstone_dust" ||
				item.ItemID == 168 && item.ID == "piston" ||
				item.ItemID == 169 && item.ID == "sticky_piston" ||
				item.ItemID == 46 && item.ID == "tnt" ||
				item.ItemID == 166 && item.ID == "repeater" ||
				item.ItemID == 193 && item.ID == "hopper" ||
				item.ItemID == 50 && item.ID == "torch" ||
				item.ItemID == 326 && item.ID == "water_bucket" ||
				item.ItemID == 327 && item.ID == "lava_bucket" ||
				item.ItemID == 352 && item.ID == "bone" ||
				item.ItemID == 368 && item.ID == "ender_pearl" ||
				item.ItemID == 351 && item.ID == "lapis_lazuli" ||
				item.ItemID == 388 && item.ID == "emerald" {
				item.ItemID = stdID
			}
		} else if item.ItemID <= 0 && m.server != nil && m.server.rules != nil {
			if id, ok := m.server.rules.byName[item.Name]; ok {
				item.ItemID = id
			}
		}
		m.byID[item.ID] = item
		if item.ItemID > 0 {
			m.byItem[item.ItemID] = item
		}
	}

	log.Printf("[Shop] 商店系统配置加载就绪 (配置源: %s, 载入商品: %d 种, 分类: %d 个)", activePath, len(m.config.Items), len(m.config.Categories))
}

// Reload 重新加载商店配置
func (m *ShopManager) Reload() (int, error) {
	m.loadConfig()
	return len(m.config.Items), nil
}

// ---------------------------------------------------------------------------
// 玩家交互与网络通信
// ---------------------------------------------------------------------------

// OpenShop 为玩家打开商店界面
func (s *Server) openShop(p *Peer) {
	if p == nil {
		return
	}
	mgr := s.initShopManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	// 确保账户存在并获取最新余额
	_, _ = s.ensureEconomyAccount(p)
	bal, _ := s.getPlayerBalance(p)

	// 计算玩家背包中当前拥有各个在售物品的持有数量
	inventoryCounts := make(map[int]int)
	if p.state != nil && p.state.Inv != nil {
		for i := 0; i < 36; i++ {
			stk := p.state.Inv.Slots[i]
			if stk != nil && stk.ID > 0 && stk.Count > 0 {
				inventoryCounts[stk.ID] += stk.Count
			}
		}
	}

	// 组织下发数据包
	msg := map[string]any{
		"t":              "shop_window",
		"title":          mgr.config.Title,
		"currencySymbol": mgr.config.CurrencySymbol,
		"balance":        bal,
		"categories":     mgr.config.Categories,
		"items":          mgr.config.Items,
		"inventory":      inventoryCounts,
	}
	p.json(msg)
}

// BuyItem 购买商品业务逻辑
func (s *Server) buyShopItem(p *Peer, itemKey string, count int) {
	if p == nil || count <= 0 {
		return
	}
	mgr := s.initShopManager()
	mgr.mu.RLock()
	item, exists := mgr.byID[itemKey]
	// 兼容按数字 ID 购买
	if !exists {
		if num, err := strconv.Atoi(itemKey); err == nil {
			item = mgr.byItem[num]
		}
	}
	mgr.mu.RUnlock()

	if item == nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "该商品不存在或已下架！"})
		return
	}
	if item.BuyPrice <= 0 {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": fmt.Sprintf("商品【%s】暂不开放购买！", item.Name)})
		return
	}
	if count > 6400 {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "单次购买数量超出上限 (最大 6400)！"})
		return
	}

	// 1. 发送 shop.buy.before 事件，插件可拦截、修改数量或调整单价
	unitPrice := item.BuyPrice
	evtData := map[string]any{
		"itemId":    item.ItemID,
		"itemKey":   item.ID,
		"itemName":  item.Name,
		"count":     count,
		"unitPrice": unitPrice,
		"price":     unitPrice * float64(count),
		"player":    p.name,
		"playerId":  p.id,
	}
	evt := s.emit(api.ShopBuying, p.room, p, evtData)
	if evt.Cancelled {
		reason := evt.Reason
		if reason == "" {
			reason = "购买请求被系统插件拦截取消！"
		}
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": reason})
		return
	}

	// 获取可能被插件修改的数量与单价
	if newCount, ok := evt.Data["count"].(int); ok && newCount > 0 {
		count = newCount
	} else if newCountFloat, ok := evt.Data["count"].(float64); ok && newCountFloat > 0 {
		count = int(newCountFloat)
	}
	if newPrice, ok := evt.Data["unitPrice"].(float64); ok && newPrice >= 0 {
		unitPrice = newPrice
	}

	totalCost := math.Round(unitPrice*float64(count)*100) / 100

	// 2. 检查玩家金币余额
	bal, err := s.getPlayerBalance(p)
	if err != nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "经济系统暂未就绪，请稍后重试！"})
		return
	}
	if bal < totalCost {
		p.json(map[string]any{
			"t":       "shop_result",
			"ok":      false,
			"message": fmt.Sprintf("金币不足！需要 $%.2f，当前钱包余额仅有 $%.2f", totalCost, bal),
			"balance": bal,
		})
		return
	}

	// 3. 经济扣款
	accID := p.sid
	if accID == "" {
		accID = fmt.Sprintf("p_%d", p.id)
	}
	newBal, err := s.economyDB.Withdraw(accID, totalCost, fmt.Sprintf("商店购买: %s ×%d", item.Name, count))
	if err != nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "扣款失败: " + err.Error()})
		return
	}

	// 4. 给予玩家物品
	itemToGive := stack(item.ItemID, count, 0)
	leftover := s.add(p.state.Inv, itemToGive)
	if leftover != nil && leftover.Count > 0 {
		// 背包放不下的部分存入恢复队列并在玩家身边生成掉落物，防止吞物品
		p.state.Recovery = append(p.state.Recovery, leftover)
		s.dropFromPlayer(p, leftover)
		p.sys(fmt.Sprintf("§e[商店提示] 背包已满，多余的 %d 个【%s】已掉落在脚边！", leftover.Count, item.Name))
	}

	// 同步背包与客户端数据
	s.inventory(p, 0, 0)

	// 5. 触发 shop.bought 完成事件
	evtData["newBalance"] = newBal
	evtData["actualCost"] = totalCost
	s.emit(api.ShopBought, p.room, p, evtData)

	// 6. 音效与成功响应
	soundAt(p.room, "entity.player.levelup", p.state.X, p.state.Y, p.state.Z, nil)
	p.sys(fmt.Sprintf("§a[商店] 成功购买 §f%s ×%d§a，共花费 §e$%.2f§a！剩余余额: §e$%.2f", item.Name, count, totalCost, newBal))

	p.json(map[string]any{
		"t":         "shop_result",
		"ok":        true,
		"action":    "buy",
		"message":   fmt.Sprintf("成功购买 %s ×%d！", item.Name, count),
		"balance":   newBal,
		"itemId":    item.ItemID,
		"itemKey":   item.ID,
		"bought":    count,
		"totalCost": totalCost,
	})
}

// SellItem 出售回收商品业务逻辑
func (s *Server) sellShopItem(p *Peer, itemKey string, count int) {
	if p == nil || count <= 0 {
		return
	}
	mgr := s.initShopManager()
	mgr.mu.RLock()
	item, exists := mgr.byID[itemKey]
	// 兼容按数字 ID 出售
	if !exists {
		if num, err := strconv.Atoi(itemKey); err == nil {
			item = mgr.byItem[num]
		}
	}
	mgr.mu.RUnlock()

	if item == nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "该商品暂无回收渠道！"})
		return
	}
	if item.SellPrice <= 0 {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": fmt.Sprintf("商品【%s】暂未开放回收！", item.Name)})
		return
	}

	// 1. 统计玩家背包中该物品的实际库存
	if p.state == nil || p.state.Inv == nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "背包数据异常！"})
		return
	}

	availableCount := 0
	for i := 0; i < 36; i++ {
		stk := p.state.Inv.Slots[i]
		if stk != nil && stk.ID == item.ItemID {
			availableCount += stk.Count
		}
	}

	if availableCount < count {
		p.json(map[string]any{
			"t":       "shop_result",
			"ok":      false,
			"message": fmt.Sprintf("背包中【%s】数量不足！需要 %d 个，当前拥有 %d 个", item.Name, count, availableCount),
		})
		return
	}

	// 2. 触发 shop.sell.before 事件，插件可拦截调整
	unitPrice := item.SellPrice
	evtData := map[string]any{
		"itemId":    item.ItemID,
		"itemKey":   item.ID,
		"itemName":  item.Name,
		"count":     count,
		"unitPrice": unitPrice,
		"price":     unitPrice * float64(count),
		"player":    p.name,
		"playerId":  p.id,
	}
	evt := s.emit(api.ShopSelling, p.room, p, evtData)
	if evt.Cancelled {
		reason := evt.Reason
		if reason == "" {
			reason = "出售操作被系统插件拦截取消！"
		}
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": reason})
		return
	}

	if newCount, ok := evt.Data["count"].(int); ok && newCount > 0 && newCount <= availableCount {
		count = newCount
	}
	if newPrice, ok := evt.Data["unitPrice"].(float64); ok && newPrice >= 0 {
		unitPrice = newPrice
	}

	totalIncome := math.Round(unitPrice*float64(count)*100) / 100

	// 3. 从背包中按序扣除物品
	remainingToRemove := count
	for i := 0; i < 36 && remainingToRemove > 0; i++ {
		stk := p.state.Inv.Slots[i]
		if stk != nil && stk.ID == item.ItemID {
			if stk.Count <= remainingToRemove {
				remainingToRemove -= stk.Count
				p.state.Inv.Slots[i] = nil
			} else {
				stk.Count -= remainingToRemove
				remainingToRemove = 0
			}
		}
	}

	// 刷新背包
	s.inventory(p, 0, 0)

	// 4. 存入金币收益
	accID := p.sid
	if accID == "" {
		accID = fmt.Sprintf("p_%d", p.id)
	}
	newBal, err := s.economyDB.Deposit(accID, p.name, totalIncome, fmt.Sprintf("商店出售: %s ×%d", item.Name, count))
	if err != nil {
		p.json(map[string]any{"t": "shop_result", "ok": false, "message": "金币发放异常: " + err.Error()})
		return
	}

	// 5. 触发 shop.sold 事件
	evtData["newBalance"] = newBal
	evtData["actualIncome"] = totalIncome
	s.emit(api.ShopSold, p.room, p, evtData)

	// 6. 音效与成功响应
	soundAt(p.room, "entity.player.levelup", p.state.X, p.state.Y, p.state.Z, nil)
	p.sys(fmt.Sprintf("§a[商店] 成功出售 §f%s ×%d§a，获得金币 §e+$%.2f§a！最新余额: §e$%.2f", item.Name, count, totalIncome, newBal))

	p.json(map[string]any{
		"t":           "shop_result",
		"ok":          true,
		"action":      "sell",
		"message":     fmt.Sprintf("成功出售 %s ×%d！获得 $%.2f", item.Name, count, totalIncome),
		"balance":     newBal,
		"itemId":      item.ItemID,
		"itemKey":     item.ID,
		"sold":        count,
		"totalIncome": totalIncome,
	})
}

// handleShopCommand 统一处理 /shop 系列聊天指令
func (s *Server) handleShopCommand(p *Peer, args []string) (bool, bool) {
	if p == nil {
		return true, true
	}
	if len(args) == 0 {
		s.openShop(p)
		return true, true
	}

	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "open", "gui":
		s.openShop(p)
		return true, true

	case "buy":
		if len(args) < 2 {
			p.sys("§e用法: /shop buy <商品名称或ID> [数量]")
			return true, true
		}
		itemKey := args[1]
		count := 1
		if len(args) >= 3 {
			if c, err := strconv.Atoi(args[2]); err == nil && c > 0 {
				count = c
			}
		}
		s.buyShopItem(p, itemKey, count)
		return true, true

	case "sell":
		if len(args) < 2 {
			p.sys("§e用法: /shop sell <商品名称或ID|hand|all> [数量]")
			return true, true
		}
		target := strings.ToLower(args[1])
		if target == "hand" {
			// 出售当前手持物品
			if p.state == nil || p.state.Inv == nil {
				p.sys("§c背包为空！")
				return true, true
			}
			sel := p.state.Inv.Selected
			held := p.state.Inv.Slots[sel]
			if held == nil || held.ID <= 0 || held.Count <= 0 {
				p.sys("§c你手中没有拿着任何物品！")
				return true, true
			}
			count := held.Count
			if len(args) >= 3 {
				if c, err := strconv.Atoi(args[2]); err == nil && c > 0 && c < count {
					count = c
				}
			}
			s.sellShopItem(p, strconv.Itoa(held.ID), count)
			return true, true
		} else if target == "all" {
			// 一键出售背包中所有可回收物品
			s.sellAllShopItems(p)
			return true, true
		}

		itemKey := args[1]
		count := 1
		if len(args) >= 3 {
			if c, err := strconv.Atoi(args[2]); err == nil && c > 0 {
				count = c
			}
		}
		s.sellShopItem(p, itemKey, count)
		return true, true

	case "reload":
		// 管理员重载商店配置
		if !s.isOperator(p.name) {
			p.sys("§c只有管理员才能重载商店配置！")
			return true, true
		}
		mgr := s.initShopManager()
		n, err := mgr.Reload()
		if err != nil {
			p.sys("§c重载商店配置失败: " + err.Error())
		} else {
			p.sys(fmt.Sprintf("§a商店配置重载成功！共加载 %d 种商品。", n))
		}
		return true, true

	default:
		s.openShop(p)
		return true, true
	}
}

// sellAllShopItems 一键出售背包中所有可回收的物品
func (s *Server) sellAllShopItems(p *Peer) {
	if p == nil || p.state == nil || p.state.Inv == nil {
		return
	}
	mgr := s.initShopManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	totalSold := 0
	totalIncome := 0.0

	for i := 0; i < 36; i++ {
		stk := p.state.Inv.Slots[i]
		if stk != nil && stk.ID > 0 && stk.Count > 0 {
			if item, ok := mgr.byItem[stk.ID]; ok && item.SellPrice > 0 {
				income := math.Round(item.SellPrice*float64(stk.Count)*100) / 100
				totalIncome += income
				totalSold += stk.Count
				p.state.Inv.Slots[i] = nil
			}
		}
	}

	if totalSold == 0 {
		p.sys("§e背包中没有可以出售回收的商品！")
		return
	}

	s.inventory(p, 0, 0)

	accID := p.sid
	if accID == "" {
		accID = fmt.Sprintf("p_%d", p.id)
	}
	newBal, err := s.economyDB.Deposit(accID, p.name, totalIncome, fmt.Sprintf("商店一键出售: 共 %d 件物品", totalSold))
	if err != nil {
		p.sys("§c发放金币异常: " + err.Error())
		return
	}

	soundAt(p.room, "entity.player.levelup", p.state.X, p.state.Y, p.state.Z, nil)
	p.sys(fmt.Sprintf("§a[商店] 一键出售完成！共售出 §f%d§a 件物品，获得金币 §e+$%.2f§a！当前余额: §e$%.2f", totalSold, totalIncome, newBal))
	p.json(map[string]any{
		"t":           "shop_result",
		"ok":          true,
		"action":      "sell_all",
		"message":     fmt.Sprintf("一键出售成功！共获得 $%.2f", totalIncome),
		"balance":     newBal,
		"totalIncome": totalIncome,
	})
}

func init() {
	onMessage("shop_open", func(s *Server, p *Peer, m Message) bool {
		s.openShop(p)
		return true
	})
	onMessage("shop_buy", func(s *Server, p *Peer, m Message) bool {
		itemKey := m.Item
		count := m.Count
		if count <= 0 {
			count = 1
		}
		s.buyShopItem(p, itemKey, count)
		return true
	})
	onMessage("shop_sell", func(s *Server, p *Peer, m Message) bool {
		itemKey := m.Item
		count := m.Count
		if count <= 0 {
			count = 1
		}
		s.sellShopItem(p, itemKey, count)
		return true
	})
	onMessage("shop_sell_all", func(s *Server, p *Peer, m Message) bool {
		s.sellAllShopItems(p)
		return true
	})
}

