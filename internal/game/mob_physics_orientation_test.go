package game

import (
	"math"
	"testing"
)

func TestMobCannotClimbLeavesAsStairs(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)

	// 在地面 y=60 上生成一只牛，坐标 (10.5, 60, 10.5)
	m, err := s.spawnMob(r, p, "cow", 10.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}

	// 旁边 (11, 60, 10) 放置树叶方块 (Oak Leaves, ID 7, kind 2)
	r.Edits[blockKey(11, 60, 10)] = 7

	// 尝试向 x+1 方向走
	walked := s.walkMob(r, m, 0.5, 0)
	// 因为上方是树叶，不应踩着树叶爬上 y=61
	if walked && m.Y > 60.1 {
		t.Fatalf("mob should not step up onto tree leaves, got Y=%f", m.Y)
	}
}

func TestMobGravityNegativeCoordinates(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	r.Infinite = true

	// 清空 (-11, -11) 处的垂直柱体空间，并在 y=64 放置站立地面
	for y := 60; y < 128; y++ {
		r.Edits[blockKey(-11, y, -11)] = 0
	}
	r.Edits[blockKey(-11, 64, -11)] = 1
	m, err := s.spawnMob(r, p, "sheep", -10.5, 65, -10.5, "test")
	if err != nil {
		t.Fatal(err)
	}

	// 破坏 y=64 的方块，使其悬空
	r.Edits[blockKey(-11, 64, -11)] = 0
	// 并在下方 y=59 放置地面
	r.Edits[blockKey(-11, 59, -11)] = 1

	// 运行一次 mobsTick 物理模拟
	s.mobsTick(r)

	// 羊必须下落，不能悬浮在 y=65
	if m.Y >= 65 {
		t.Fatalf("mob at negative coordinates must fall under gravity, got Y=%f", m.Y)
	}
}

func TestAdvancedMobWalkFacesMovementDirection(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)

	// 在 (10.5, 60, 10.5) 生成苦力怕
	m, err := s.spawnMob(r, p, "creeper", 10.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}

	// 地面铺设实心方块
	for x := 10; x <= 15; x++ {
		for z := 10; z <= 15; z++ {
			r.Edits[blockKey(x, 59, z)] = 1
		}
	}

	// 目标在 (14.5, 60, 10.5)（向 +X 方向）
	s.advancedMobWalk(r, m, 14.5, 60, 10.5, 0.2)

	// 往 +X 方向走时，yaw 应该对应 -pi/2
	expectedYaw := math.Atan2(-1.0, 0.0)
	if math.Abs(m.Yaw-expectedYaw) > 0.01 {
		t.Fatalf("expected creeper yaw %f facing +X, got %f", expectedYaw, m.Yaw)
	}
}

func TestValidMobNormalizesUnwrappedYaw(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)

	m, err := s.spawnMob(r, p, "ender_dragon", 64, 80, 64, "test")
	if err != nil {
		t.Fatal(err)
	}

	// 模拟长期巡航后未折叠的 Yaw (如 256.91 rad)
	m.Yaw = 256.9107963268517
	if !validMob(m) {
		t.Fatalf("validMob should normalize unwrapped yaw and return true, got false")
	}
	if m.Yaw < -math.Pi*2 || m.Yaw > math.Pi*2 {
		t.Fatalf("m.Yaw was not normalized within [-2pi, 2pi], got %f", m.Yaw)
	}
}
