package game

import (
	"errors"
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Player-to-Player Face-to-Face Trade System
// ---------------------------------------------------------------------------

type TradeInvite struct {
	FromID    uint16
	FromName  string
	ToID      uint16
	ToName    string
	CreatedAt time.Time
}

type TradeParticipant struct {
	Peer      *Peer      `json:"-"`
	Items     [8]*Stack  `json:"items"`
	Money     float64    `json:"money"`
	Confirmed bool       `json:"confirmed"` // Phase 1: Lock / Confirm offer
	Accepted  bool       `json:"accepted"`  // Phase 2: Final agreement
}

type TradeSession struct {
	ID        string           `json:"id"`
	P1        TradeParticipant `json:"p1"`
	P2        TradeParticipant `json:"p2"`
	WorldID   string           `json:"worldId"`
	CreatedAt time.Time        `json:"createdAt"`
	mu        sync.Mutex
}

var (
	tradeManagerMu sync.Mutex
	tradeInvites   = map[string]*TradeInvite{} // key: "toID:fromID" or "toID"
	activeTrades   = map[*Peer]*TradeSession{}
)

func init() {
	// Register WebSocket message handlers for trading
	onMessage("trade_offer_item", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeOfferItem(p, m)
		return true
	})
	onMessage("trade_remove_item", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeRemoveItem(p, m)
		return true
	})
	onMessage("trade_offer_money", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeOfferMoney(p, m)
		return true
	})
	onMessage("trade_confirm", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeConfirm(p)
		return true
	})
	onMessage("trade_unconfirm", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeUnconfirm(p)
		return true
	})
	onMessage("trade_accept", func(s *Server, p *Peer, m Message) bool {
		s.handleTradeAccept(p)
		return true
	})
	onMessage("trade_cancel", func(s *Server, p *Peer, m Message) bool {
		s.cancelTrade(p, "玩家取消了交易")
		return true
	})
}

// inviteKey formats the invite lookup key.
func inviteKey(toID uint16) string {
	return strconv.Itoa(int(toID))
}

// getActiveTrade returns the active trade session for a peer, if any.
func (s *Server) getActiveTrade(p *Peer) *TradeSession {
	tradeManagerMu.Lock()
	defer tradeManagerMu.Unlock()
	return activeTrades[p]
}

// sendTradeInvite initiates a trade invitation from sender to target.
func (s *Server) sendTradeInvite(from, to *Peer) error {
	if from == nil || to == nil {
		return errors.New("玩家不存在")
	}
	if from == to || from.id == to.id {
		return errors.New("不能与自己发起交易")
	}
	if from.room != to.room {
		return errors.New("目标玩家不在同一世界/房间中")
	}
	if from.state == nil || to.state == nil || from.state.Dead || to.state.Dead {
		return errors.New("玩家当前状态无法进行交易")
	}

	tradeManagerMu.Lock()
	defer tradeManagerMu.Unlock()

	if activeTrades[from] != nil {
		return errors.New("您当前已在交易中，请先结束现有交易")
	}
	if activeTrades[to] != nil {
		return fmt.Errorf("玩家 %s 当前正在与他人交易中", to.name)
	}

	// Emit TradeRequesting (cancellable)
	reqData := map[string]any{
		"fromId":   from.id,
		"fromName": from.name,
		"toId":     to.id,
		"toName":   to.name,
	}
	e := s.emit(api.TradeRequesting, from.room, from, reqData)
	if e.Cancelled {
		reason := e.Reason
		if reason == "" {
			reason = "交易邀请已被插件阻止"
		}
		return errors.New(reason)
	}

	// Register invite
	invite := &TradeInvite{
		FromID:    from.id,
		FromName:  from.name,
		ToID:      to.id,
		ToName:    to.name,
		CreatedAt: time.Now(),
	}
	tradeInvites[inviteKey(to.id)] = invite

	s.emit(api.TradeRequested, from.room, from, reqData)

	from.sys(fmt.Sprintf("已向玩家 %s 发送交易邀请（60秒内有效）...", to.name))
	to.sys(fmt.Sprintf("玩家 %s 邀请你进行交易！输入 /trade accept 同意，或 /trade deny 拒绝。", from.name))

	// Send an interactive prompt packet to target client
	to.json(map[string]any{
		"t":        "trade_invited",
		"fromId":   from.id,
		"fromName": from.name,
	})

	return nil
}

// acceptTradeInvite accepts an incoming trade invite and opens the trade panel for both.
func (s *Server) acceptTradeInvite(to *Peer, senderName string) error {
	if to == nil || to.state == nil || to.room == nil {
		return errors.New("玩家状态异常")
	}

	tradeManagerMu.Lock()
	invite, ok := tradeInvites[inviteKey(to.id)]
	if !ok || time.Since(invite.CreatedAt) > 60*time.Second {
		delete(tradeInvites, inviteKey(to.id))
		tradeManagerMu.Unlock()
		return errors.New("没有收到交易邀请或邀请已过期")
	}

	var from *Peer
	for p := range to.room.Peers {
		if p.id == invite.FromID && !p.closed {
			from = p
			break
		}
	}
	if from == nil || from.room != to.room {
		delete(tradeInvites, inviteKey(to.id))
		tradeManagerMu.Unlock()
		return errors.New("发起交易的玩家已离线或离开当前世界")
	}

	if activeTrades[from] != nil || activeTrades[to] != nil {
		delete(tradeInvites, inviteKey(to.id))
		tradeManagerMu.Unlock()
		return errors.New("一方已在其他交易中")
	}

	delete(tradeInvites, inviteKey(to.id))

	// Create TradeSession
	sessionID := fmt.Sprintf("trade_%d_%d_%d", from.id, to.id, time.Now().UnixNano())
	session := &TradeSession{
		ID: sessionID,
		P1: TradeParticipant{
			Peer: from,
		},
		P2: TradeParticipant{
			Peer: to,
		},
		WorldID:   to.room.root().ID,
		CreatedAt: time.Now(),
	}

	activeTrades[from] = session
	activeTrades[to] = session
	tradeManagerMu.Unlock()

	// Ensure accounts exist in SQLite
	s.ensureEconomyAccount(from)
	s.ensureEconomyAccount(to)

	// Emit TradeStarted event
	startData := map[string]any{
		"tradeId": sessionID,
		"p1Id":    from.id,
		"p1Name":  from.name,
		"p2Id":    to.id,
		"p2Name":  to.name,
	}
	s.emit(api.TradeStarted, to.room, to, startData)

	from.sys(fmt.Sprintf("玩家 %s 同意了交易，交易视窗已打开。", to.name))
	to.sys(fmt.Sprintf("你接受了 %s 的交易邀请，交易视窗已打开。", from.name))

	// Open Trade UI on both clients
	s.broadcastTradeUpdate(session, true)

	return nil
}

// denyTradeInvite rejects an incoming trade invite.
func (s *Server) denyTradeInvite(to *Peer) error {
	tradeManagerMu.Lock()
	invite, ok := tradeInvites[inviteKey(to.id)]
	if !ok || time.Since(invite.CreatedAt) > 60*time.Second {
		delete(tradeInvites, inviteKey(to.id))
		tradeManagerMu.Unlock()
		return errors.New("没有待处理的交易邀请")
	}
	delete(tradeInvites, inviteKey(to.id))
	tradeManagerMu.Unlock()

	to.sys("已拒绝交易邀请。")
	for p := range to.room.Peers {
		if p.id == invite.FromID && !p.closed {
			p.sys(fmt.Sprintf("玩家 %s 拒绝了您的交易邀请。", to.name))
			break
		}
	}
	return nil
}

// broadcastTradeUpdate sends current trade state to both participants.
func (s *Server) broadcastTradeUpdate(sess *TradeSession, isOpening bool) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	p1, p2 := sess.P1.Peer, sess.P2.Peer
	if p1 == nil || p2 == nil {
		sess.mu.Unlock()
		return
	}

	bothConfirmed := sess.P1.Confirmed && sess.P2.Confirmed
	p1Money := sess.P1.Money
	p1Confirmed := sess.P1.Confirmed
	p1Accepted := sess.P1.Accepted
	p2Money := sess.P2.Money
	p2Confirmed := sess.P2.Confirmed
	p2Accepted := sess.P2.Accepted

	// Snapshot items for JSON transmission
	p1Items := make([]map[string]any, 8)
	for i, stk := range sess.P1.Items {
		if stk != nil {
			name := ""
			if rule, ok := s.rules.byID[stk.ID]; ok {
				name = rule.Name
			}
			p1Items[i] = map[string]any{
				"id":    stk.ID,
				"count": stk.Count,
				"name":  name,
				"glint": len(stk.Ench) > 0 || (stk.Meta != nil && (len(stk.Meta.Attributes) > 0 || stk.Meta.CustomData != nil)),
			}
		}
	}
	p2Items := make([]map[string]any, 8)
	for i, stk := range sess.P2.Items {
		if stk != nil {
			name := ""
			if rule, ok := s.rules.byID[stk.ID]; ok {
				name = rule.Name
			}
			p2Items[i] = map[string]any{
				"id":    stk.ID,
				"count": stk.Count,
				"name":  name,
				"glint": len(stk.Ench) > 0 || (stk.Meta != nil && (len(stk.Meta.Attributes) > 0 || stk.Meta.CustomData != nil)),
			}
		}
	}
	sess.mu.Unlock()

	p1Bal, _ := s.getPlayerBalance(p1)
	p2Bal, _ := s.getPlayerBalance(p2)

	msgType := "trade_update"
	if isOpening {
		msgType = "trade_open"
	}

	// Payload for Player 1 (P1 is self, P2 is partner)
	p1.json(map[string]any{
		"t":              msgType,
		"id":             sess.ID,
		"myName":         p1.name,
		"myBalance":      p1Bal,
		"myMoney":        p1Money,
		"myItems":        p1Items,
		"myConfirmed":    p1Confirmed,
		"myAccepted":     p1Accepted,
		"theirName":      p2.name,
		"theirMoney":     p2Money,
		"theirItems":     p2Items,
		"theirConfirmed": p2Confirmed,
		"theirAccepted":  p2Accepted,
		"bothConfirmed":  bothConfirmed,
	})

	// Payload for Player 2 (P2 is self, P1 is partner)
	p2.json(map[string]any{
		"t":              msgType,
		"id":             sess.ID,
		"myName":         p2.name,
		"myBalance":      p2Bal,
		"myMoney":        p2Money,
		"myItems":        p2Items,
		"myConfirmed":    p2Confirmed,
		"myAccepted":     p2Accepted,
		"theirName":      p1.name,
		"theirMoney":     p1Money,
		"theirItems":     p1Items,
		"theirConfirmed": p1Confirmed,
		"theirAccepted":  p1Accepted,
		"bothConfirmed":  bothConfirmed,
	})
}

// handleTradeOfferItem places an item from player's inventory into a trade slot.
func (s *Server) handleTradeOfferItem(p *Peer, m Message) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}
	invSlot := m.I // player inventory slot 0..35
	tradeSlot := m.S // trade slot 0..7
	if invSlot < 0 || invSlot >= 36 || tradeSlot < 0 || tradeSlot >= 8 {
		return
	}
	if p.state == nil || p.state.Inv == nil {
		return
	}

	invStack := p.state.Inv.Slots[invSlot]
	if invStack == nil || invStack.Count <= 0 {
		return
	}

	count := m.N
	if count <= 0 || count > invStack.Count {
		count = invStack.Count
	}

	sess.mu.Lock()
	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}

	// If slot already occupied, return old item to inventory first
	if old := part.Items[tradeSlot]; old != nil {
		p.state.Inv.Slots[invSlot] = s.add(p.state.Inv, old)
		part.Items[tradeSlot] = nil
	}

	// Transfer item from inventory to trade slot
	offerItem := copyStack(invStack)
	offerItem.Count = count
	part.Items[tradeSlot] = offerItem

	if invStack.Count == count {
		p.state.Inv.Slots[invSlot] = nil
	} else {
		invStack.Count -= count
	}

	// ANTI-SCAM: ANY item modification forcibly resets confirmations for BOTH users!
	sess.P1.Confirmed = false
	sess.P1.Accepted = false
	sess.P2.Confirmed = false
	sess.P2.Accepted = false
	sess.mu.Unlock()

	s.inventory(p, 0, 0)
	s.broadcastTradeUpdate(sess, false)
}

// handleTradeRemoveItem removes an item from trade slot back to player inventory.
func (s *Server) handleTradeRemoveItem(p *Peer, m Message) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}

	tradeSlot := m.S
	if tradeSlot < 0 || tradeSlot >= 8 || p.state == nil || p.state.Inv == nil {
		return
	}

	sess.mu.Lock()
	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}

	item := part.Items[tradeSlot]
	if item == nil {
		sess.mu.Unlock()
		return
	}
	part.Items[tradeSlot] = nil

	// Return to inventory
	if left := s.add(p.state.Inv, item); left != nil {
		p.state.Recovery = append(p.state.Recovery, left)
	}

	// ANTI-SCAM: Reset confirmations for BOTH users!
	sess.P1.Confirmed = false
	sess.P1.Accepted = false
	sess.P2.Confirmed = false
	sess.P2.Accepted = false
	sess.mu.Unlock()

	s.inventory(p, 0, 0)
	s.broadcastTradeUpdate(sess, false)
}

// handleTradeOfferMoney sets the money offer for a player after verifying sufficient funds.
func (s *Server) handleTradeOfferMoney(p *Peer, m Message) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}
	amount := m.Amount
	if amount == 0 && m.Forward > 0 {
		amount = m.Forward
	}
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		p.sys("金额必须大于或等于 0。")
		return
	}
	amount = math.Round(amount*100) / 100

	bal, err := s.getPlayerBalance(p)
	if err != nil {
		p.sys("获取余额失败：" + err.Error())
		return
	}
	if amount > bal {
		p.sys(fmt.Sprintf("您的金币不足！当前余额为 $%.2f，无法出资 $%.2f。", bal, amount))
		return
	}

	sess.mu.Lock()
	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}
	part.Money = amount

	// ANTI-SCAM: Reset confirmations
	sess.P1.Confirmed = false
	sess.P1.Accepted = false
	sess.P2.Confirmed = false
	sess.P2.Accepted = false
	sess.mu.Unlock()

	s.broadcastTradeUpdate(sess, false)
}

// handleTradeConfirm sets player confirmation (Phase 1).
func (s *Server) handleTradeConfirm(p *Peer) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}
	sess.mu.Lock()

	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}
	part.Confirmed = true

	// Check if both confirmed
	bothConfirmed := sess.P1.Confirmed && sess.P2.Confirmed
	sess.mu.Unlock()

	if bothConfirmed {
		sess.P1.Peer.sys("双方均已确认交易内容！请点击【同意交易】完成交换。")
		sess.P2.Peer.sys("双方均已确认交易内容！请点击【同意交易】完成交换。")
	}

	s.broadcastTradeUpdate(sess, false)
}

// handleTradeUnconfirm unlocks player offer.
func (s *Server) handleTradeUnconfirm(p *Peer) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}
	part.Confirmed = false
	part.Accepted = false
	sess.mu.Unlock()

	s.broadcastTradeUpdate(sess, false)
}

// handleTradeAccept marks player acceptance (Phase 2) and finalizes if both accepted.
func (s *Server) handleTradeAccept(p *Peer) {
	sess := s.getActiveTrade(p)
	if sess == nil {
		return
	}
	sess.mu.Lock()

	// Both must be confirmed first!
	if !sess.P1.Confirmed || !sess.P2.Confirmed {
		sess.mu.Unlock()
		p.sys("双方必须先锁定确认交易内容后，方可同意交易。")
		return
	}

	part := &sess.P1
	if p == sess.P2.Peer {
		part = &sess.P2
	}
	part.Accepted = true

	bothAccepted := sess.P1.Accepted && sess.P2.Accepted
	sess.mu.Unlock()

	if bothAccepted {
		// Finalize trade atomically!
		s.finalizeTrade(sess)
	} else {
		s.broadcastTradeUpdate(sess, false)
		p.sys("已同意交易，等待对方同意...")
	}
}

// finalizeTrade performs the atomic exchange of items and money between P1 and P2.
func (s *Server) finalizeTrade(sess *TradeSession) {
	tradeManagerMu.Lock()
	delete(activeTrades, sess.P1.Peer)
	delete(activeTrades, sess.P2.Peer)
	tradeManagerMu.Unlock()

	sess.mu.Lock()
	defer sess.mu.Unlock()

	p1, p2 := sess.P1.Peer, sess.P2.Peer
	if p1 == nil || p2 == nil || p1.closed || p2.closed {
		s.refundTradeItems(sess)
		return
	}

	// 1. Re-verify money balances
	p1Bal, _ := s.getPlayerBalance(p1)
	p2Bal, _ := s.getPlayerBalance(p2)
	if sess.P1.Money > p1Bal {
		p1.sys(fmt.Sprintf("交易失败：%s 金币不足！", p1.name))
		p2.sys(fmt.Sprintf("交易失败：%s 金币不足！", p1.name))
		s.refundTradeItems(sess)
		return
	}
	if sess.P2.Money > p2Bal {
		p1.sys(fmt.Sprintf("交易失败：%s 金币不足！", p2.name))
		p2.sys(fmt.Sprintf("交易失败：%s 金币不足！", p2.name))
		s.refundTradeItems(sess)
		return
	}

	// 2. Atomic Money Transfer via SQLite
	p1ID := p1.sid
	if p1ID == "" {
		p1ID = fmt.Sprintf("p_%d", p1.id)
	}
	p2ID := p2.sid
	if p2ID == "" {
		p2ID = fmt.Sprintf("p_%d", p2.id)
	}

	// P1 pays P2
	if sess.P1.Money > 0 {
		_, _, err := s.economyDB.Transfer(p1ID, p2ID, p1.name, p2.name, sess.P1.Money, "玩家交易：出资转出")
		if err != nil {
			p1.sys("资金划转失败，交易已取消：" + err.Error())
			p2.sys("资金划转失败，交易已取消：" + err.Error())
			s.refundTradeItems(sess)
			return
		}
	}
	// P2 pays P1
	if sess.P2.Money > 0 {
		_, _, err := s.economyDB.Transfer(p2ID, p1ID, p2.name, p1.name, sess.P2.Money, "玩家交易：出资转出")
		if err != nil {
			// Rollback P1's payment if P2 fails
			if sess.P1.Money > 0 {
				_, _, _ = s.economyDB.Transfer(p2ID, p1ID, p2.name, p1.name, sess.P1.Money, "玩家交易异常回滚")
			}
			p1.sys("资金划转失败，交易已取消：" + err.Error())
			p2.sys("资金划转失败，交易已取消：" + err.Error())
			s.refundTradeItems(sess)
			return
		}
	}

	// 3. Swap Items: Give P1's offered items to P2, give P2's offered items to P1
	for _, it := range sess.P1.Items {
		if it != nil {
			if left := s.add(p2.state.Inv, it); left != nil {
				p2.state.Recovery = append(p2.state.Recovery, left)
			}
		}
	}
	for _, it := range sess.P2.Items {
		if it != nil {
			if left := s.add(p1.state.Inv, it); left != nil {
				p1.state.Recovery = append(p1.state.Recovery, left)
			}
		}
	}

	// 4. Emit TradeCompleted event
	compData := map[string]any{
		"tradeId": sess.ID,
		"p1Id":    p1.id,
		"p1Name":  p1.name,
		"p1Money": sess.P1.Money,
		"p2Id":    p2.id,
		"p2Name":  p2.name,
		"p2Money": sess.P2.Money,
	}
	s.emit(api.TradeCompleted, p1.room, p1, compData)

	// 5. Notify both players
	p1.sys("★ 交易成功完成！已获得对方物品与金钱。")
	p2.sys("★ 交易成功完成！已获得对方物品与金钱。")

	soundAt(p1.room, "entity.player.levelup", p1.state.X, p1.state.Y, p1.state.Z, nil)

	s.inventory(p1, 0, 0)
	s.inventory(p2, 0, 0)

	// Close UI
	closeMsg := map[string]any{"t": "trade_close", "success": true, "reason": "交易成功！"}
	p1.json(closeMsg)
	p2.json(closeMsg)
}

// refundTradeItems safely returns all items in trade slots back to their respective owners.
func (s *Server) refundTradeItems(sess *TradeSession) {
	p1, p2 := sess.P1.Peer, sess.P2.Peer
	if p1 != nil && p1.state != nil && p1.state.Inv != nil {
		for i, it := range sess.P1.Items {
			if it != nil {
				if left := s.add(p1.state.Inv, it); left != nil {
					p1.state.Recovery = append(p1.state.Recovery, left)
				}
				sess.P1.Items[i] = nil
			}
		}
		s.inventory(p1, 0, 0)
	}
	if p2 != nil && p2.state != nil && p2.state.Inv != nil {
		for i, it := range sess.P2.Items {
			if it != nil {
				if left := s.add(p2.state.Inv, it); left != nil {
					p2.state.Recovery = append(p2.state.Recovery, left)
				}
				sess.P2.Items[i] = nil
			}
		}
		s.inventory(p2, 0, 0)
	}
}

// cancelTrade terminates an active trade session and refunds items.
func (s *Server) cancelTrade(p *Peer, reason string) {
	tradeManagerMu.Lock()
	sess := activeTrades[p]
	if sess == nil {
		tradeManagerMu.Unlock()
		return
	}
	delete(activeTrades, sess.P1.Peer)
	delete(activeTrades, sess.P2.Peer)
	tradeManagerMu.Unlock()

	sess.mu.Lock()
	defer sess.mu.Unlock()

	s.refundTradeItems(sess)

	p1, p2 := sess.P1.Peer, sess.P2.Peer
	cancelMsg := map[string]any{"t": "trade_close", "reason": reason}

	if p1 != nil && !p1.closed {
		p1.sys("交易已取消，所有物品已安全退回背包。")
		p1.json(cancelMsg)
	}
	if p2 != nil && !p2.closed {
		p2.sys("交易已取消，所有物品已安全退回背包。")
		p2.json(cancelMsg)
	}

	s.emit(api.TradeCancelled, nil, p, map[string]any{"tradeId": sess.ID, "reason": reason})
}

// handleTradeCommand dispatches /trade <player|accept|deny|cancel>.
func (s *Server) handleTradeCommand(p *Peer, parts []string) (bool, bool) {
	if len(parts) < 2 {
		p.sys("用法：/trade <玩家名或ID> 发起交易；/trade accept [玩家名] 同意交易；/trade deny 拒绝；/trade cancel 取消")
		return true, false
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "accept":
		senderName := ""
		if len(parts) >= 3 {
			senderName = parts[2]
		}
		if err := s.acceptTradeInvite(p, senderName); err != nil {
			p.sys("无法接受交易：" + err.Error())
			return true, false
		}
		return true, true

	case "deny", "refuse":
		if err := s.denyTradeInvite(p); err != nil {
			p.sys(err.Error())
			return true, false
		}
		return true, true

	case "cancel":
		if s.getActiveTrade(p) != nil {
			s.cancelTrade(p, "玩家取消了交易")
			return true, true
		}
		// Cancel outgoing invite
		tradeManagerMu.Lock()
		for k, inv := range tradeInvites {
			if inv.FromID == p.id {
				delete(tradeInvites, k)
				p.sys("已取消发出的交易邀请。")
				tradeManagerMu.Unlock()
				return true, true
			}
		}
		tradeManagerMu.Unlock()
		p.sys("当前没有正在进行或发出的交易。")
		return true, false

	default:
		// Target player name or ID
		targetName := parts[1]
		targetPeer := s.findPeerByName(targetName)
		if targetPeer == nil {
			p.sys(fmt.Sprintf("找不到玩家：%s（玩家必须在线且在同一世界中）", targetName))
			return true, false
		}
		if err := s.sendTradeInvite(p, targetPeer); err != nil {
			p.sys("发起交易失败：" + err.Error())
			return true, false
		}
		return true, true
	}
}
