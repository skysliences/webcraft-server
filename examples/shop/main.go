package main

import (
	"encoding/json"
	"fmt"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type Config struct {
	Title          string  `json:"title"`
	CurrencySymbol string  `json:"currencySymbol"`
	DiscountRate   float64 `json:"discountRate"` // 全局购买折扣，如 0.8 表示 8 折
}

type ShopAudit struct {
	Timestamp int64   `json:"timestamp"`
	Action    string  `json:"action"` // "buy" or "sell"
	Player    string  `json:"player"`
	ItemKey   string  `json:"itemKey"`
	ItemName  string  `json:"itemName"`
	Count     int     `json:"count"`
	Amount    float64 `json:"amount"`
}

func main() {
	p := api.New()

	cfg := Config{
		Title:          "🛒 WebCraft 贸易集市",
		CurrencySymbol: "$",
		DiscountRate:   1.0,
	}

	var mu sync.Mutex
	var auditPath string

	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			_ = json.Unmarshal(c.Config, &cfg)
		}
		if cfg.DiscountRate <= 0 {
			cfg.DiscountRate = 1.0
		}
		if c.DataDir != "" {
			_ = os.MkdirAll(c.DataDir, 0755)
			auditPath = filepath.Join(c.DataDir, "shop_audit.log")
		}
		log.Printf("[shop-plugin] 商店系统扩展插件已启用。当前折扣率: %.2f", cfg.DiscountRate)
		return nil
	}

	recordAudit := func(entry ShopAudit) {
		if auditPath == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		data, _ := json.Marshal(entry)
		_, _ = f.Write(append(data, '\n'))
	}

	// 1. 监听购买前事件：若配置了全局打折率，动态计算折后价
	p.On(api.ShopBuying, api.Subscription{Priority: api.Normal}, func(c *api.Context, e *api.Event) error {
		if cfg.DiscountRate < 1.0 && cfg.DiscountRate > 0 {
			if unitPrice, ok := e.Data["unitPrice"].(float64); ok && unitPrice > 0 {
				newUnitPrice := unitPrice * cfg.DiscountRate
				e.Data["unitPrice"] = newUnitPrice
			}
		}
		return nil
	})

	// 2. 监听购买成功事件：写入交易审计日志
	p.On(api.ShopBought, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		player, _ := e.Data["player"].(string)
		itemKey, _ := e.Data["itemKey"].(string)
		itemName, _ := e.Data["itemName"].(string)
		count := 1
		if cnt, ok := e.Data["count"].(float64); ok {
			count = int(cnt)
		} else if cnt, ok := e.Data["count"].(int); ok {
			count = cnt
		}
		cost, _ := e.Data["actualCost"].(float64)

		recordAudit(ShopAudit{
			Timestamp: time.Now().Unix(),
			Action:    "buy",
			Player:    player,
			ItemKey:   itemKey,
			ItemName:  itemName,
			Count:     count,
			Amount:    cost,
		})
		return nil
	})

	// 3. 监听出售成功事件：写入回收审计日志
	p.On(api.ShopSold, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		player, _ := e.Data["player"].(string)
		itemKey, _ := e.Data["itemKey"].(string)
		itemName, _ := e.Data["itemName"].(string)
		count := 1
		if cnt, ok := e.Data["count"].(float64); ok {
			count = int(cnt)
		} else if cnt, ok := e.Data["count"].(int); ok {
			count = cnt
		}
		income, _ := e.Data["actualIncome"].(float64)

		recordAudit(ShopAudit{
			Timestamp: time.Now().Unix(),
			Action:    "sell",
			Player:    player,
			ItemKey:   itemKey,
			ItemName:  itemName,
			Count:     count,
			Amount:    income,
		})
		return nil
	})

	// 4. 注册 /shopdiscount 管理员快捷指令
	p.Command(api.Command{
		Name:        "shopdiscount",
		Description: "设置全服商店购买折扣率 (例如: /shopdiscount 0.8)",
		Permission:  "shop.admin",
	}, func(c *api.Context, e *api.Event, args []string) error {
		if len(args) == 0 {
			c.Message(e, fmt.Sprintf("§e当前全服商店折扣率为: %.2f (%.0f折)", cfg.DiscountRate, cfg.DiscountRate*10))
			return nil
		}
		rate, err := strconv.ParseFloat(args[0], 64)
		if err != nil || rate <= 0 || rate > 2.0 {
			c.Message(e, "§c请输入有效的折扣数值 (如 0.8 表示 8 折，1.0 表示原价)")
			return nil
		}
		cfg.DiscountRate = rate
		c.Broadcast(e.World.ID, fmt.Sprintf("§6★ [全服公告] 贸易集市当前已开启限时折扣活动：全场商品 %.0f 折！输入 /shop 即可选购！", rate*10))
		return nil
	})

	p.Run()
}
