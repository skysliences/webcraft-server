package game

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHungerStarvationDeath(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	_, p := dropTestWorld(s)

	// 1. 设置玩家生命值为 2，饱食度为 0
	p.state.Health = 2
	p.state.Food = 0
	p.state.Saturation = 0
	p.state.Exhaustion = 0
	p.state.Dead = false
	p.lastHealth = time.Now().Add(-5 * time.Second)

	// 第一次 life 触发饥饿扣血，血量由 2 变为 1
	s.life(p)

	if p.state.Health != 1 {
		t.Fatalf("expected health to drop from 2 to 1, got %f", p.state.Health)
	}
	if p.state.Dead {
		t.Fatalf("player should still be alive with 1 health")
	}

	// 2. 再次触发饥饿扣血：生命值由 1 降为 0，应当触发死亡
	p.lastHealth = time.Now().Add(-5 * time.Second)
	s.life(p)

	if p.state.Health != 0 {
		t.Fatalf("expected health to drop to 0, got %f", p.state.Health)
	}
	if !p.state.Dead {
		t.Fatalf("player must be marked dead when hunger drops health to 0")
	}

	// 验证收到的网络消息中包含死亡消息和 pdead 倒下广播
	var receivedDead bool
	var receivedPDead bool
	for len(p.send) > 0 {
		pkt := <-p.send
		var msg map[string]any
		if err := json.Unmarshal(pkt.body, &msg); err == nil {
			if msg["t"] == "dead" && msg["score"] != nil {
				receivedDead = true
			}
			if msg["t"] == "pdead" {
				receivedPDead = true
			}
		}
	}
	if !receivedDead {
		t.Fatalf("player did not receive death message packet")
	}
	if !receivedPDead {
		t.Fatalf("world did not broadcast pdead packet")
	}

	// 3. 测试重生流程
	if !s.respawnPlayer(p) {
		t.Fatalf("respawnPlayer failed")
	}
	if p.state.Dead {
		t.Fatalf("player should not be dead after respawn")
	}
	if p.state.Health != 20 {
		t.Fatalf("player health should be restored to 20 after respawn, got %f", p.state.Health)
	}
	if p.state.Food != 20 {
		t.Fatalf("player food should be restored to 20 after respawn, got %d", p.state.Food)
	}
}

func TestHealthZeroFailsafeDeath(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	_, p := dropTestWorld(s)

	// 模拟玩家生命值变为 0 但 Dead 仍为 false 的异常状态
	p.state.Health = 0
	p.state.Dead = false

	// 调用 life(p)，应当兜底结算死亡
	s.life(p)

	if !p.state.Dead {
		t.Fatalf("failsafe: player with health <= 0 must be marked dead in life()")
	}
}

func TestEasyDifficultyHungerCapsAtTen(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := dropTestWorld(s)
	r.root().ext().Difficulty = "easy"

	p.state.Health = 10
	p.state.Food = 0
	p.state.Dead = false
	p.lastHealth = time.Now().Add(-5 * time.Second)

	s.life(p)

	// easy 难度下生命值在 10 时不应再扣血
	if p.state.Health != 10 {
		t.Fatalf("easy difficulty: health should cap at 10, got %f", p.state.Health)
	}
	if p.state.Dead {
		t.Fatalf("easy difficulty: player should not die of hunger at 10 health")
	}
}
