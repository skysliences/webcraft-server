package game

import (
	"fmt"
	"math"
	"testing"
)

func TestEconomyDB(t *testing.T) {
	tempDir := t.TempDir()
	db, err := initEconomyDB(tempDir)
	if err != nil {
		t.Fatalf("initEconomyDB failed: %v", err)
	}
	defer db.Close()

	// 1. EnsureAccount & Initial Balance
	acc1, err := db.EnsureAccount("user_1", "Alice")
	if err != nil {
		t.Fatalf("EnsureAccount user_1 failed: %v", err)
	}
	if math.Abs(acc1.Balance-defaultInitialBalance) > 1e-4 {
		t.Fatalf("expected initial balance %.2f, got %.2f", defaultInitialBalance, acc1.Balance)
	}

	_, err = db.EnsureAccount("user_2", "Bob")
	if err != nil {
		t.Fatalf("EnsureAccount user_2 failed: %v", err)
	}

	// 2. Deposit & Withdraw
	bal1, err := db.Deposit("user_1", "Alice", 500, "quest_reward")
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}
	if math.Abs(bal1-1500.0) > 1e-4 {
		t.Fatalf("expected balance 1500, got %.2f", bal1)
	}

	bal1, err = db.Withdraw("user_1", 200, "buy_item")
	if err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}
	if math.Abs(bal1-1300.0) > 1e-4 {
		t.Fatalf("expected balance 1300, got %.2f", bal1)
	}

	// 3. Insufficient balance withdraw
	_, err = db.Withdraw("user_1", 99999, "overdraw")
	if err == nil {
		t.Fatalf("expected error for insufficient funds, got nil")
	}

	// 4. Transfer
	fromBal, toBal, err := db.Transfer("user_1", "user_2", "Alice", "Bob", 300, "player_pay")
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}
	if math.Abs(fromBal-1000.0) > 1e-4 {
		t.Fatalf("expected from balance 1000, got %.2f", fromBal)
	}
	if math.Abs(toBal-1300.0) > 1e-4 {
		t.Fatalf("expected to balance 1300, got %.2f", toBal)
	}
}

func TestEconomyCommandsAndPay(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p1.name = "Alice"
	p1.sid = "alice_001"

	p2 := dropTestPlayer(r, 2, 10, 64, 10)
	p2.name = "Bob"
	p2.sid = "bob_002"
	r.Peers[p2] = true

	// Ensure accounts
	bal1, err := s.ensureEconomyAccount(p1)
	if err != nil {
		t.Fatalf("ensureEconomyAccount p1 failed: %v", err)
	}
	bal2, err := s.ensureEconomyAccount(p2)
	if err != nil {
		t.Fatalf("ensureEconomyAccount p2 failed: %v", err)
	}
	if bal1 != 1000 || bal2 != 1000 {
		t.Fatalf("expected 1000 balance, got %v, %v", bal1, bal2)
	}

	// 1. Balance command
	handled, ok := s.handleFullCommand(p1, "balance", []string{"balance"})
	if !handled || !ok {
		t.Fatalf("/balance command failed")
	}

	handled, ok = s.handleFullCommand(p1, "balance", []string{"balance", "Bob"})
	if !handled || !ok {
		t.Fatalf("/balance Bob command failed")
	}

	// 2. Pay command
	handled, ok = s.handleFullCommand(p1, "pay", []string{"pay", "Bob", "250"})
	if !handled || !ok {
		t.Fatalf("/pay Bob 250 failed")
	}

	b1, _ := s.getPlayerBalance(p1)
	b2, _ := s.getPlayerBalance(p2)
	if math.Abs(b1-750.0) > 1e-4 || math.Abs(b2-1250.0) > 1e-4 {
		t.Fatalf("balances after pay mismatch: p1=%.2f, p2=%.2f", b1, b2)
	}

	// 3. Pay with insufficient funds
	handled, ok = s.handleFullCommand(p1, "pay", []string{"pay", "Bob", "99999"})
	if !handled || ok {
		t.Fatalf("expected /pay with insufficient funds to fail")
	}

	// 4. Eco command (OP only)
	if s.ops.Settings.Operators == nil {
		s.ops.Settings.Operators = map[string]bool{}
	}
	s.ops.Settings.Operators[p1.sid] = true
	handled, ok = s.handleFullCommand(p1, "eco", []string{"eco", "give", "Bob", "500"})
	if !handled || !ok {
		t.Fatalf("/eco give failed")
	}
	b2, _ = s.getPlayerBalance(p2)
	if math.Abs(b2-1750.0) > 1e-4 {
		t.Fatalf("expected Bob balance 1750, got %.2f", b2)
	}
}

func TestTradeSystemFlow(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p1.name = "TraderOne"
	p1.sid = "p1_sid"

	p2 := dropTestPlayer(r, 2, 10, 64, 10)
	p2.name = "TraderTwo"
	p2.sid = "p2_sid"
	r.Peers[p2] = true

	_, _ = s.ensureEconomyAccount(p1)
	_, _ = s.ensureEconomyAccount(p2)

	diamondID := s.id("Diamond")
	if diamondID == 0 {
		diamondID = 264
	}
	goldID := s.id("Gold Ingot")
	if goldID == 0 {
		goldID = 266
	}
	p1.state.Inv.Slots[0] = &Stack{ID: diamondID, Count: 10}
	p2.state.Inv.Slots[0] = &Stack{ID: goldID, Count: 20}

	// 1. 发起交易邀请
	handled, ok := s.handleFullCommand(p1, "trade", []string{"trade", fmt.Sprintf("%d", p2.id)})
	if !handled || !ok {
		t.Fatalf("/trade <id> failed")
	}

	// 2. 接受邀请
	handled, ok = s.handleFullCommand(p2, "trade", []string{"trade", "accept"})
	if !handled || !ok {
		t.Fatalf("/trade accept failed")
	}

	sess := s.getActiveTrade(p1)
	if sess == nil {
		t.Fatalf("expected active trade session for p1")
	}
	if s.getActiveTrade(p2) != sess {
		t.Fatalf("p1 and p2 should share the same trade session")
	}

	// 3. P1 放入 5 块钻石到交易槽 0
	s.handleTradeOfferItem(p1, Message{I: 0, S: 0, N: 5})
	if sess.P1.Items[0] == nil || sess.P1.Items[0].Count != 5 {
		t.Fatalf("p1 trade slot 0 item mismatch: %+v", sess.P1.Items[0])
	}
	if p1.state.Inv.Slots[0].Count != 5 {
		t.Fatalf("p1 inventory slot 0 should have 5 left, got %d", p1.state.Inv.Slots[0].Count)
	}

	// 4. P1 出资金币 $300
	s.handleTradeOfferMoney(p1, Message{Amount: 300})
	if sess.P1.Money != 300 {
		t.Fatalf("expected P1 money offer 300, got %.2f", sess.P1.Money)
	}

	// P1 尝试出资超出余额 $99999 (应被拒绝，保持 300)
	s.handleTradeOfferMoney(p1, Message{Amount: 99999})
	if sess.P1.Money != 300 {
		t.Fatalf("excess money offer should be rejected, got %.2f", sess.P1.Money)
	}

	// 5. P2 放入 10 块金锭到交易槽 0，出资 $100
	s.handleTradeOfferItem(p2, Message{I: 0, S: 0, N: 10})
	s.handleTradeOfferMoney(p2, Message{Amount: 100})

	// 6. 双方点击确认锁定
	s.handleTradeConfirm(p1)
	if !sess.P1.Confirmed {
		t.Fatalf("expected P1 confirmed")
	}
	if sess.P2.Confirmed {
		t.Fatalf("P2 should not be confirmed yet")
	}

	s.handleTradeConfirm(p2)
	if !sess.P1.Confirmed || !sess.P2.Confirmed {
		t.Fatalf("both participants should be confirmed")
	}

	// 7. 测试防欺诈（Anti-Scam）：双方锁定后，若任何一方修改出资，双重锁立即解除
	s.handleTradeOfferMoney(p1, Message{Amount: 250})
	if sess.P1.Confirmed || sess.P2.Confirmed {
		t.Fatalf("modifying offer must reset both players' confirmed status")
	}

	// 重新锁定
	s.handleTradeConfirm(p1)
	s.handleTradeConfirm(p2)

	// 8. 双方点击同意交易 (Phase 2)
	s.handleTradeAccept(p1)
	if !sess.P1.Accepted {
		t.Fatalf("P1 should be accepted")
	}
	// 尚未完成
	if s.getActiveTrade(p1) == nil {
		t.Fatalf("trade should still be active until both accept")
	}

	s.handleTradeAccept(p2)

	// 9. 交易完成，Session 应当已关闭
	if s.getActiveTrade(p1) != nil || s.getActiveTrade(p2) != nil {
		t.Fatalf("trade session should be closed after mutual accept")
	}

	// 验证金币变化：P1 出资 250 收到 100 -> 净出 150，初始 1000 -> 850
	// P2 出资 100 收到 250 -> 净入 150，初始 1000 -> 1150
	b1, _ := s.getPlayerBalance(p1)
	b2, _ := s.getPlayerBalance(p2)
	if math.Abs(b1-850.0) > 1e-4 {
		t.Fatalf("expected P1 balance 850, got %.2f", b1)
	}
	if math.Abs(b2-1150.0) > 1e-4 {
		t.Fatalf("expected P2 balance 1150, got %.2f", b2)
	}

	// 验证物品交换：P1 背包中应该收到了 P2 的 10 块金锭
	hasGold := false
	for _, st := range p1.state.Inv.Slots {
		if st != nil && st.ID == goldID && st.Count == 10 {
			hasGold = true
			break
		}
	}
	if !hasGold {
		t.Fatalf("P1 should have received 10 gold ingots")
	}

	// P2 背包中应该收到了 P1 的 5 块钻石
	hasDiamond := false
	for _, st := range p2.state.Inv.Slots {
		if st != nil && st.ID == diamondID && st.Count == 5 {
			hasDiamond = true
			break
		}
	}
	if !hasDiamond {
		t.Fatalf("P2 should have received 5 diamonds")
	}
}

func TestTradeCancelRefund(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p1.name = "Canceller1"
	p1.sid = "c1_sid"

	p2 := dropTestPlayer(r, 2, 10, 64, 10)
	p2.name = "Canceller2"
	p2.sid = "c2_sid"
	r.Peers[p2] = true

	_, _ = s.ensureEconomyAccount(p1)
	_, _ = s.ensureEconomyAccount(p2)

	p1.state.Inv.Slots[0] = &Stack{ID: 1, Count: 32} // 32 石头

	// 发起并接受交易
	s.handleTradeCommand(p1, []string{"trade", fmt.Sprintf("%d", p2.id)})
	s.handleTradeCommand(p2, []string{"trade", "accept"})

	sess := s.getActiveTrade(p1)
	if sess == nil {
		t.Fatalf("trade session not found")
	}

	// P1 放入 20 个石头
	s.handleTradeOfferItem(p1, Message{I: 0, S: 0, N: 20})
	if p1.state.Inv.Slots[0].Count != 12 {
		t.Fatalf("P1 inv should have 12 stones left, got %d", p1.state.Inv.Slots[0].Count)
	}

	// P1 取消交易
	s.cancelTrade(p1, "测试取消")

	if s.getActiveTrade(p1) != nil {
		t.Fatalf("trade session should be cancelled")
	}

	// 检查物品退还：P1 背包第 0 格恢复或退回到背包空槽，总数应为 32
	totalStones := 0
	for _, st := range p1.state.Inv.Slots {
		if st != nil && st.ID == 1 {
			totalStones += st.Count
		}
	}
	if totalStones != 32 {
		t.Fatalf("expected 32 stones refunded, got %d", totalStones)
	}
}
