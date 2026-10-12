package game

import (
	"errors"
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Economy System Integration (SQLite-backed)
// ---------------------------------------------------------------------------

// ensureEconomyAccount makes sure the player has an entry in SQLite with starting balance.
func (s *Server) ensureEconomyAccount(p *Peer) (float64, error) {
	if s == nil || s.economyDB == nil || p == nil {
		return 0, errors.New("economy not ready")
	}
	accID := p.sid
	if accID == "" {
		accID = fmt.Sprintf("p_%d", p.id)
	}
	acc, err := s.economyDB.EnsureAccount(accID, p.name)
	if err != nil {
		return 0, err
	}
	return acc.Balance, nil
}

// getEconomyBalance returns the balance of an account.
func (s *Server) getEconomyBalance(accountID string) (float64, error) {
	if s == nil || s.economyDB == nil {
		return 0, errors.New("economy not ready")
	}
	return s.economyDB.GetBalance(accountID)
}

// getPlayerBalance returns the balance of a peer.
func (s *Server) getPlayerBalance(p *Peer) (float64, error) {
	if p == nil {
		return 0, errors.New("peer is nil")
	}
	accID := p.sid
	if accID == "" {
		accID = fmt.Sprintf("p_%d", p.id)
	}
	return s.getEconomyBalance(accID)
}

// findPeerByName finds an online peer in any room by player name or ID.
func (s *Server) findPeerByName(query string) *Peer {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	// Try numeric player ID first
	if num, err := strconv.Atoi(query); err == nil && num > 0 && num <= 65535 {
		targetID := uint16(num)
		for _, r := range s.allRooms() {
			for p := range r.Peers {
				if p.id == targetID && !p.closed {
					return p
				}
			}
		}
	}

	// Try player name (exact then case-insensitive)
	lower := strings.ToLower(query)
	var matched *Peer
	for _, r := range s.allRooms() {
		for p := range r.Peers {
			if p.closed {
				continue
			}
			if p.name == query {
				return p
			}
			if strings.ToLower(p.name) == lower {
				matched = p
			}
		}
	}
	return matched
}

// payPlayer executes a player-to-player transfer with plugin event lifecycle.
func (s *Server) payPlayer(from, to *Peer, amount float64) (float64, float64, error) {
	if from == nil || to == nil {
		return 0, 0, errors.New("玩家不存在或不在线")
	}
	if from == to || from.id == to.id {
		return 0, 0, errors.New("不能向自己转账")
	}
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, 0, errors.New("转账金额必须大于 0")
	}
	// Round to 2 decimal places
	amount = math.Round(amount*100) / 100
	if amount <= 0 {
		return 0, 0, errors.New("转账金额过小（最小单位 0.01）")
	}

	r := from.room
	fromID := from.sid
	if fromID == "" {
		fromID = fmt.Sprintf("p_%d", from.id)
	}
	toID := to.sid
	if toID == "" {
		toID = fmt.Sprintf("p_%d", to.id)
	}

	// 1. Emit EconomyPaying (cancellable, mutable "amount")
	payData := map[string]any{
		"fromId":      from.id,
		"fromAccount": fromID,
		"fromName":    from.name,
		"toId":        to.id,
		"toAccount":   toID,
		"toName":      to.name,
		"amount":      amount,
	}
	e := s.emit(api.EconomyPaying, r, from, payData)
	if e.Cancelled {
		reason := e.Reason
		if reason == "" {
			reason = "转账已被插件策略拦截"
		}
		return 0, 0, errors.New(reason)
	}
	// Check modified amount
	if newAmt, ok := e.Data["amount"].(float64); ok && newAmt > 0 {
		amount = math.Round(newAmt*100) / 100
	}

	// 2. Perform atomic SQLite transfer
	fromBal, toBal, err := s.economyDB.Transfer(fromID, toID, from.name, to.name, amount, fmt.Sprintf("玩家转账：%s -> %s", from.name, to.name))
	if err != nil {
		return 0, 0, err
	}

	// 3. Emit EconomyPaid event
	payData["fromBalanceAfter"] = fromBal
	payData["toBalanceAfter"] = toBal
	s.emit(api.EconomyPaid, r, from, payData)

	// 4. Notify both players
	from.sys(fmt.Sprintf("成功向 %s 转账 $%.2f，您的当前余额: $%.2f", to.name, amount, fromBal))
	to.sys(fmt.Sprintf("收到来自 %s 的转账 $%.2f，您的当前余额: $%.2f", from.name, amount, toBal))

	return fromBal, toBal, nil
}

// handleEconomyCommands dispatches /balance, /pay, and /eco commands.
func (s *Server) handleEconomyCommands(p *Peer, name string, parts []string) (bool, bool) {
	switch name {
	case "balance", "bal", "money":
		if len(parts) == 1 {
			// Query own balance
			bal, err := s.ensureEconomyAccount(p)
			if err != nil {
				p.sys("查询余额失败：" + err.Error())
				return true, false
			}
			p.sys(fmt.Sprintf("您的金币余额为：$%.2f", bal))
			return true, true
		}

		// Query target player balance
		targetName := parts[1]
		targetPeer := s.findPeerByName(targetName)
		var targetID, displayName string
		if targetPeer != nil {
			targetID = targetPeer.sid
			if targetID == "" {
				targetID = fmt.Sprintf("p_%d", targetPeer.id)
			}
			displayName = targetPeer.name
		} else {
			// Query from DB by name
			acc, err := s.economyDB.FindAccountByName(targetName)
			if err != nil || acc == nil {
				p.sys(fmt.Sprintf("未找到玩家 %s 的账户。", targetName))
				return true, false
			}
			targetID = acc.AccountID
			displayName = acc.Name
		}

		bal, err := s.getEconomyBalance(targetID)
		if err != nil {
			p.sys("查询余额失败：" + err.Error())
			return true, false
		}
		p.sys(fmt.Sprintf("玩家 %s 的金币余额为：$%.2f", displayName, bal))
		return true, true

	case "pay":
		if len(parts) < 3 {
			p.sys("用法：/pay <玩家名或ID> <金额>")
			return true, false
		}
		target := s.findPeerByName(parts[1])
		if target == nil {
			p.sys(fmt.Sprintf("找不到在线玩家：%s", parts[1]))
			return true, false
		}
		amount, err := strconv.ParseFloat(parts[2], 64)
		if err != nil || amount <= 0 {
			p.sys("转账金额必须为大于 0 的有效数字。")
			return true, false
		}

		_, _, err = s.payPlayer(p, target, amount)
		if err != nil {
			p.sys("转账失败：" + err.Error())
			return true, false
		}
		return true, true

	case "eco", "economy":
		if !s.canGameCommand(p, "server.admin") {
			p.sys("该指令需要管理员 (OP) 权限。")
			return true, false
		}
		if len(parts) < 3 {
			p.sys("用法：/eco <give|take|set|bal> <玩家名> [金额]")
			return true, false
		}
		sub := strings.ToLower(parts[1])
		targetName := parts[2]
		targetPeer := s.findPeerByName(targetName)
		var targetID, displayName string
		if targetPeer != nil {
			targetID = targetPeer.sid
			if targetID == "" {
				targetID = fmt.Sprintf("p_%d", targetPeer.id)
			}
			displayName = targetPeer.name
		} else {
			acc, err := s.economyDB.FindAccountByName(targetName)
			if err != nil || acc == nil {
				p.sys(fmt.Sprintf("未找到玩家 %s 的账户。", targetName))
				return true, false
			}
			targetID = acc.AccountID
			displayName = acc.Name
		}

		switch sub {
		case "bal", "balance":
			bal, err := s.getEconomyBalance(targetID)
			if err != nil {
				p.sys("查询失败：" + err.Error())
				return true, false
			}
			p.sys(fmt.Sprintf("[经济管理] 玩家 %s 当前余额：$%.2f", displayName, bal))
			return true, true

		case "give":
			if len(parts) < 4 {
				p.sys("用法：/eco give <玩家名> <金额>")
				return true, false
			}
			amt, err := strconv.ParseFloat(parts[3], 64)
			if err != nil || amt <= 0 {
				p.sys("金额必须大于 0。")
				return true, false
			}
			newBal, err := s.economyDB.Deposit(targetID, displayName, amt, fmt.Sprintf("管理员 %s 发放金币", p.name))
			if err != nil {
				p.sys("发放金币失败：" + err.Error())
				return true, false
			}
			p.sys(fmt.Sprintf("[经济管理] 已向 %s 发放 $%.2f，新余额: $%.2f", displayName, amt, newBal))
			if targetPeer != nil {
				targetPeer.sys(fmt.Sprintf("管理员向您发放了 $%.2f 金币，当前余额: $%.2f", amt, newBal))
			}
			return true, true

		case "take":
			if len(parts) < 4 {
				p.sys("用法：/eco take <玩家名> <金额>")
				return true, false
			}
			amt, err := strconv.ParseFloat(parts[3], 64)
			if err != nil || amt <= 0 {
				p.sys("金额必须大于 0。")
				return true, false
			}
			newBal, err := s.economyDB.Withdraw(targetID, amt, fmt.Sprintf("管理员 %s 扣除金币", p.name))
			if err != nil {
				p.sys("扣除金币失败：" + err.Error())
				return true, false
			}
			p.sys(fmt.Sprintf("[经济管理] 已从 %s 扣除 $%.2f，新余额: $%.2f", displayName, amt, newBal))
			if targetPeer != nil {
				targetPeer.sys(fmt.Sprintf("管理员扣除了您 $%.2f 金币，当前余额: $%.2f", amt, newBal))
			}
			return true, true

		case "set":
			if len(parts) < 4 {
				p.sys("用法：/eco set <玩家名> <金额>")
				return true, false
			}
			amt, err := strconv.ParseFloat(parts[3], 64)
			if err != nil || amt < 0 {
				p.sys("金额不能为负数。")
				return true, false
			}
			newBal, err := s.economyDB.SetBalance(targetID, displayName, amt, fmt.Sprintf("管理员 %s 设定金币", p.name))
			if err != nil {
				p.sys("设置金币失败：" + err.Error())
				return true, false
			}
			p.sys(fmt.Sprintf("[经济管理] 已将 %s 的金币设置为 $%.2f", displayName, newBal))
			if targetPeer != nil {
				targetPeer.sys(fmt.Sprintf("管理员已将您的金币设置为 $%.2f", newBal))
			}
			return true, true

		default:
			p.sys("未知子指令。用法：/eco <give|take|set|bal> <玩家名> [金额]")
			return true, false
		}
	}
	return false, false
}
