package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"testing"
)

func TestShopOpenAndConfig(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	_ = r

	mgr := s.initShopManager()
	if mgr == nil {
		t.Fatal("shop manager must not be nil")
	}
	if len(mgr.config.Items) == 0 {
		t.Fatal("shop items should have default config")
	}

	// 模拟 /shop 指令
	handled, send := s.handleShopCommand(p, []string{})
	if !handled || !send {
		t.Fatal("/shop command should be handled")
	}
}

func TestShopBuyItem(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	_ = r

	accID := p.sid
	if accID == "" {
		accID = "p_1"
		p.sid = accID
	}

	// 1. 确保初始金币为 0
	_, _ = s.economyDB.SetBalance(accID, p.name, 0, "test")

	// 尝试购买钻石 (单价 100) -> 应该因为金币不足而失败
	s.buyShopItem(p, "diamond", 1)

	// 验证背包中没有钻石 (264)
	for _, stk := range p.state.Inv.Slots[:36] {
		if stk != nil && stk.ID == 264 {
			t.Fatal("金币不足时不应该获得钻石")
		}
	}

	// 2. 给玩家充值 250 金币
	_, _ = s.economyDB.SetBalance(accID, p.name, 250.0, "test deposit")

	// 购买 2 个钻石 (总价 200)
	s.buyShopItem(p, "diamond", 2)

	// 验证扣款后的余额为 50.0
	bal, err := s.getPlayerBalance(p)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 50.0 {
		t.Fatalf("购买后余额不正确，期望 50.0，实际 %.2f", bal)
	}

	// 验证背包中有 2 个钻石
	diamondCount := 0
	for _, stk := range p.state.Inv.Slots[:36] {
		if stk != nil && stk.ID == 264 {
			diamondCount += stk.Count
		}
	}
	if diamondCount != 2 {
		t.Fatalf("购买后背包钻石数量不正确，期望 2，实际 %d", diamondCount)
	}
}

func TestShopSellItem(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	_ = r

	accID := p.sid
	if accID == "" {
		accID = "p_1"
		p.sid = accID
	}
	_, _ = s.economyDB.SetBalance(accID, p.name, 10.0, "test")

	// 1. 背包中无物品时出售失败
	s.sellShopItem(p, "coal", 5)
	bal, _ := s.getPlayerBalance(p)
	if bal != 10.0 {
		t.Fatalf("背包无物品时余额不应改变，实际 %.2f", bal)
	}

	// 2. 放入 10 块煤炭 (ID 263, 默认回收单价 2.0)
	p.state.Inv.Slots[0] = stack(263, 10, 0)

	// 出售 4 块煤炭 (回收收益 4 * 2.0 = 8.0)
	s.sellShopItem(p, "coal", 4)

	// 验证背包剩余 6 块煤炭
	if p.state.Inv.Slots[0] == nil || p.state.Inv.Slots[0].Count != 6 {
		t.Fatalf("出售后背包中剩余物品不正确: %+v", p.state.Inv.Slots[0])
	}

	// 验证余额增加至 18.0 (10 + 8)
	bal, _ = s.getPlayerBalance(p)
	if bal != 18.0 {
		t.Fatalf("出售后余额不正确，期望 18.0，实际 %.2f", bal)
	}
}

func TestShopSellAllAndHand(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	_ = r

	accID := p.sid
	if accID == "" {
		accID = "p_1"
		p.sid = accID
	}
	_, _ = s.economyDB.SetBalance(accID, p.name, 0.0, "test")

	// 放入 2 个钻石 (ID 264, 回收价 50.0/个 -> 100.0) 和 10 个铁锭 (ID 265, 回收价 5.0/个 -> 50.0)
	p.state.Inv.Slots[0] = stack(264, 2, 0)
	p.state.Inv.Slots[1] = stack(265, 10, 0)
	p.state.Inv.Selected = 0

	// 1. 测试 /shop sell hand (手持钻石出售 1 个 -> 50.0)
	s.handleShopCommand(p, []string{"sell", "hand", "1"})
	if p.state.Inv.Slots[0].Count != 1 {
		t.Fatalf("手持出售后钻石应剩 1 个，实际: %d", p.state.Inv.Slots[0].Count)
	}
	bal, _ := s.getPlayerBalance(p)
	if bal != 50.0 {
		t.Fatalf("手持出售后余额应为 50.0，实际: %.2f", bal)
	}

	// 2. 测试 /shop sell all (一键出售剩余 1 个钻石 + 10 个铁锭 -> 50 + 50 = 100)
	s.handleShopCommand(p, []string{"sell", "all"})
	if p.state.Inv.Slots[0] != nil || p.state.Inv.Slots[1] != nil {
		t.Fatal("一键出售后可回收槽位应已被清空")
	}
	bal, _ = s.getPlayerBalance(p)
	if bal != 150.0 {
		t.Fatalf("一键出售后总余额应为 150.0，实际: %.2f", bal)
	}
}

func TestShopCustomConfigReload(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	customConfig := ShopConfig{
		Title:          "VIP 专享折扣商城",
		CurrencySymbol: "¥",
		Categories: []ShopCategory{
			{ID: "special", Name: "特惠专区", Icon: "gold_ingot"},
		},
		Items: []ShopItem{
			{ID: "super_diamond", ItemID: 264, Name: "极品钻石", Category: "special", BuyPrice: 9.9, SellPrice: 8.8},
		},
	}
	cfgData, _ := json.MarshalIndent(customConfig, "", "  ")
	configFile := filepath.Join(tempDir, "shop.json")
	if err := os.WriteFile(configFile, cfgData, 0644); err != nil {
		t.Fatal(err)
	}

	mgr := s.initShopManager()
	n, err := mgr.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("期望重载 1 种商品，实际: %d", n)
	}
	if mgr.config.Title != "VIP 专享折扣商城" {
		t.Fatalf("重载后的标题不正确: %s", mgr.config.Title)
	}
	item := mgr.byID["super_diamond"]
	if item == nil || item.BuyPrice != 9.9 || item.SellPrice != 8.8 {
		t.Fatalf("商品自定义价格不正确: %+v", item)
	}
}

func TestShopPluginCancelEvent(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	_ = r

	accID := p.sid
	if accID == "" {
		accID = "p_1"
		p.sid = accID
	}
	_, _ = s.economyDB.SetBalance(accID, p.name, 1000.0, "test")

	// 模拟插件监听并取消 shop.buy.before
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.ShopBuying {
			e.Cancellable = true
			e.Cancel("商城正在维护中，暂停购买！")
		}
	}

	s.buyShopItem(p, "diamond", 1)

	// 验证未扣款且未给物品
	bal, _ := s.getPlayerBalance(p)
	if bal != 1000.0 {
		t.Fatalf("插件取消购买时余额不应变动，实际: %.2f", bal)
	}
	for _, stk := range p.state.Inv.Slots[:36] {
		if stk != nil && stk.ID == 264 {
			t.Fatal("插件取消购买时不应发放物品")
		}
	}
}
