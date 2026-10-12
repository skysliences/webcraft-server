package main

import (
	"encoding/json"
	"fmt"
	"log"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	MaxSingleTransfer     float64 `json:"maxSingleTransfer"`
	NotifyTradeCompletion bool    `json:"notifyTradeCompletion"`
}

type AuditLog struct {
	Timestamp int64   `json:"timestamp"`
	Type      string  `json:"type"`
	Actor     string  `json:"actor"`
	Target    string  `json:"target"`
	Amount    float64 `json:"amount"`
	Reason    string  `json:"reason"`
}

func main() {
	p := api.New()

	config := Config{
		MaxSingleTransfer:     100000.0,
		NotifyTradeCompletion: true,
	}

	var mu sync.Mutex
	var auditFile string

	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 && string(c.Config) != "null" {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		if c.DataDir != "" {
			_ = os.MkdirAll(c.DataDir, 0755)
			auditFile = filepath.Join(c.DataDir, "economy_audit.log")
		}
		log.Printf("[economy-audit] enabled. MaxSingleTransfer=%.2f", config.MaxSingleTransfer)
		return nil
	}

	appendAudit := func(entry AuditLog) {
		if auditFile == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		data, _ := json.Marshal(entry)
		_, _ = f.Write(append(data, '\n'))
	}

	// 1. 监听转账前事件：进行限额风控
	p.On(api.EconomyPaying, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		amount := 0.0
		switch val := e.Data["amount"].(type) {
		case float64:
			amount = val
		case json.Number:
			amount, _ = val.Float64()
		}

		if config.MaxSingleTransfer > 0 && amount > config.MaxSingleTransfer {
			e.Cancel(fmt.Sprintf("单笔转账超出最大安全限额 $%.2f", config.MaxSingleTransfer))
			return nil
		}
		return nil
	})

	// 2. 监听转账成功事件：记录审计日志
	p.On(api.EconomyPaid, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		amount := 0.0
		switch val := e.Data["amount"].(type) {
		case float64:
			amount = val
		case json.Number:
			amount, _ = val.Float64()
		}
		actor, _ := e.Data["actor"].(string)
		target, _ := e.Data["target"].(string)

		appendAudit(AuditLog{
			Timestamp: time.Now().Unix(),
			Type:      "transfer",
			Actor:     actor,
			Target:    target,
			Amount:    amount,
			Reason:    "player_pay",
		})
		return nil
	})

	// 3. 监听面对面交易完成事件
	p.On(api.TradeCompleted, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		tradeID, _ := e.Data["tradeId"].(string)
		p1, _ := e.Data["p1"].(string)
		p2, _ := e.Data["p2"].(string)
		log.Printf("[economy-audit] trade completed: %s between %s and %s", tradeID, p1, p2)
		return nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
