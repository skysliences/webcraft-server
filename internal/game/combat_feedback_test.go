package game

import (
	"encoding/json"
	"math"
	"testing"
)

func TestPlayerAttackKnockbackAndFeedback(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p1.name = "Attacker"
	p1.sid = "attacker_sid"
	p1.state.X = 10.0
	p1.state.Y = 61.0
	p1.state.Z = 10.0

	p2 := dropTestPlayer(r, 2, 11.5, 61.0, 10.0) // 位于攻击者正东方向 1.5 格
	p2.name = "Target"
	p2.sid = "target_sid"
	p2.state.Health = 20.0
	r.Peers[p2] = true

	// 模拟玩家1手持钻石剑
	p1.state.Inv.Slots[0] = stack(s.rules.byName["Diamond Sword"], 1, 0)
	p1.state.Inv.Selected = 0

	// 模拟攻击者向目标发起疾跑攻击
	msg := Message{
		T:     "attack",
		P:     p2.id,
		Sp:    1,        // 疾跑攻击
		Sound: "crit",   // 暴击
	}

	s.survivalMessage(p1, msg)

	// 1. 验证目标受击扣血
	if p2.state.Health >= 20.0 {
		t.Fatalf("target should have taken damage, current health: %v", p2.state.Health)
	}

	// 2. 检查发给 target (p2) 的数据包，必须包含带击退冲量的 hurt
	var hurtPacket map[string]any
	for len(p2.send) > 0 {
		pkt := <-p2.send
		var parsed map[string]any
		if err := json.Unmarshal(pkt.body, &parsed); err == nil {
			if parsed["t"] == "hurt" {
				hurtPacket = parsed
				break
			}
		}
	}

	if hurtPacket == nil {
		t.Fatal("target did not receive hurt packet")
	}

	kbRaw, ok := hurtPacket["kb"].([]any)
	if !ok || len(kbRaw) == 0 {
		t.Fatalf("hurt packet missing kb knockback vector: %+v", hurtPacket)
	}
	kbFirst, ok := kbRaw[0].([]any)
	if !ok || len(kbFirst) < 3 {
		t.Fatalf("invalid kb entry format: %+v", kbRaw)
	}

	strength := kbFirst[0].(float64)
	dirX := kbFirst[1].(float64)
	dirZ := kbFirst[2].(float64)

	// 基础 0.45 + 疾跑 0.45 = 0.90
	if math.Abs(strength-0.90) > 1e-4 {
		t.Fatalf("expected knockback strength 0.90, got %v", strength)
	}

	// 目标位于 (11.5, 10.0)，攻击者在 (10.0, 10.0)，击退远离攻击者方向为 +X
	// 由于客户端公式是 st[3]/2 - (rx/d)*k，所以下发的 rx 为 -1.0，rz 为 0.0
	if math.Abs(dirX-(-1.0)) > 1e-3 || math.Abs(dirZ) > 1e-3 {
		t.Fatalf("expected knockback direction rx=-1.0, rz=0.0, got rx=%v, rz=%v", dirX, dirZ)
	}

	// 3. 检查攻击者 (p1) 收到广播的 phurt 和 dmg 伤害飘字数据包
	var phurtPacket map[string]any
	var dmgPacket map[string]any

	for len(p1.send) > 0 {
		pkt := <-p1.send
		var parsed map[string]any
		if err := json.Unmarshal(pkt.body, &parsed); err == nil {
			if parsed["t"] == "phurt" {
				phurtPacket = parsed
			} else if parsed["t"] == "dmg" {
				dmgPacket = parsed
			}
		}
	}

	if phurtPacket == nil {
		t.Fatal("attacker did not receive phurt broadcast")
	}
	if phurtPacket["id"].(float64) != float64(p2.id) {
		t.Fatalf("phurt packet target id mismatch: %+v", phurtPacket)
	}

	if dmgPacket == nil {
		t.Fatal("attacker did not receive dmg floating numbers broadcast")
	}
	if dmgPacket["id"].(float64) != float64(p2.id) || dmgPacket["targetType"] != "player" {
		t.Fatalf("dmg packet mismatch: %+v", dmgPacket)
	}
	amount, ok := dmgPacket["amount"].(float64)
	if !ok || amount <= 0 {
		t.Fatalf("dmg amount must be positive, got: %v", amount)
	}
	if dmgPacket["crit"] != true {
		t.Fatalf("expected crit=true in dmg packet, got: %+v", dmgPacket)
	}
}
