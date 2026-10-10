package game

import (
	"math"
	"testing"
	"time"
)

func TestBowArrowParabolicTrajectory(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)

	// 准备弓与箭矢
	p.state.Inv.Slots[0] = stack(301, 1, 0) // 弓
	p.state.Inv.Slots[1] = stack(302, 64, 0) // 箭矢
	p.state.Inv.Selected = 0

	// 玩家平视前方 (Yaw=0, Pitch=0)
	p.state.Yaw = 0
	p.state.Pitch = 0
	p.state.X = 10.0
	p.state.Y = 64.0
	p.state.Z = 10.0

	// 模拟开始拉弓 (use: s=0)
	s.runMessageHooks(p, Message{T: "use", S: 0})
	if p.useAt.IsZero() {
		t.Fatalf("expected bow useAt to be set")
	}

	// 模拟蓄力 1.2 秒 (满蓄力)
	p.useAt = time.Now().Add(-1200 * time.Millisecond)

	// 松开射箭 (use: s=1)
	s.runMessageHooks(p, Message{T: "use", S: 1})

	rt := r.rt()
	if len(rt.projectiles) != 1 {
		t.Fatalf("expected 1 arrow projectile spawned, got %d", len(rt.projectiles))
	}

	var arrow *Projectile
	for _, pr := range rt.projectiles {
		arrow = pr
		break
	}

	if arrow.Type != projArrow {
		t.Fatalf("expected projectile type %s, got %s", projArrow, arrow.Type)
	}

	// 1. 验证抛物线初动量与上扬仰角微补偿
	// 平视时 dy = 0, 但应有上抛微补偿使 VY > 0, Pitch > 0
	if arrow.VY <= 0 {
		t.Fatalf("expected positive initial vertical velocity for parabolic arc lofting, got VY=%f", arrow.VY)
	}
	if arrow.Pitch <= 0 {
		t.Fatalf("expected positive initial pitch, got Pitch=%f", arrow.Pitch)
	}
	initialVY := arrow.VY
	startY := arrow.Y

	// 2. 推进物理步进，观察抛物线顶点与下坠
	// 预期在若干 tick 内，Y 轴上升达到抛物线顶点，随后在重力作用下持续下坠
	var peakY float64 = startY
	reachedPeak := false
	pitchBecameNegative := false

	for step := 0; step < 20; step++ {
		s.stepProjectile(r, arrow)
		if arrow.Y > peakY {
			peakY = arrow.Y
		} else if arrow.Y < peakY && !reachedPeak {
			reachedPeak = true
		}
		if arrow.Pitch < 0 {
			pitchBecameNegative = true
		}
	}

	if !reachedPeak {
		t.Fatalf("expected projectile to reach parabolic apex and begin falling, startY=%f, peakY=%f, currentY=%f", startY, peakY, arrow.Y)
	}
	if peakY <= startY {
		t.Fatalf("expected peakY (%f) to be higher than startY (%f)", peakY, startY)
	}
	if !pitchBecameNegative {
		t.Fatalf("expected projectile pitch to become negative on descent along tangent, got pitch=%f", arrow.Pitch)
	}
	if arrow.VY >= initialVY {
		t.Fatalf("expected VY to decrease under gravity, initial=%f, current=%f", initialVY, arrow.VY)
	}

	// 当前位置 Y 必须显著下落（表现出明显的抛物线下坠）
	if arrow.Y >= startY {
		t.Fatalf("expected arrow to fall below start position after 20 steps, got Y=%f", arrow.Y)
	}
}

func TestBowPartialChargeTrajectory(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)

	p.state.Inv.Slots[0] = stack(301, 1, 0)
	p.state.Inv.Slots[1] = stack(302, 64, 0)
	p.state.Inv.Selected = 0
	p.state.Yaw = 0
	p.state.Pitch = 0
	p.state.X = 10.0
	p.state.Y = 64.0
	p.state.Z = 10.0

	// 轻度蓄力 (仅蓄力 200ms)
	s.runMessageHooks(p, Message{T: "use", S: 0})
	p.useAt = time.Now().Add(-200 * time.Millisecond)
	s.runMessageHooks(p, Message{T: "use", S: 1})

	rt := r.rt()
	if len(rt.projectiles) != 1 {
		t.Fatalf("expected 1 projectile spawned, got %d", len(rt.projectiles))
	}

	var arrow *Projectile
	for _, pr := range rt.projectiles {
		arrow = pr
		break
	}

	// 低蓄力初速度应较低 (低于满蓄力的 2.0)，下坠更快
	hSpeed := math.Hypot(arrow.VX, arrow.VZ)
	if hSpeed > 1.8 {
		t.Fatalf("expected lower horizontal speed for partial charge, got %f", hSpeed)
	}
}
