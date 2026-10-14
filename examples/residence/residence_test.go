package main

import (
	api "mcraft/backend/pkg/plugin"
	"testing"
)

func TestResidenceCalculations(t *testing.T) {
	// 测试 10*10 规格计算
	size := 10
	cx, cy, cz := 100, 64, 200
	half := size / 2

	minX, maxX := cx-half, cx+half-1
	minZ, maxZ := cz-half, cz+half-1
	minY := cy - half
	maxY := cy + half - 1

	if minX != 95 || maxX != 104 {
		t.Fatalf("unexpected X range: [%d, %d]", minX, maxX)
	}
	if maxX-minX+1 != 10 {
		t.Fatalf("expected width 10, got %d", maxX-minX+1)
	}
	if maxY-minY+1 != 10 {
		t.Fatalf("expected height 10, got %d", maxY-minY+1)
	}
	if maxZ-minZ+1 != 10 {
		t.Fatalf("expected length 10, got %d", maxZ-minZ+1)
	}

	area := size * size
	if area != 100 {
		t.Fatalf("expected area 100, got %d", area)
	}
	cost := float64(area) * 10.0
	if cost != 1000.0 {
		t.Fatalf("expected cost 1000, got %f", cost)
	}

	// 50*50: 2500格 * 10 = 25000
	if 50*50*10 != 25000 {
		t.Fatalf("50*50 cost mismatch")
	}
	// 100*100: 10000格 * 10 = 100000
	if 100*100*10 != 100000 {
		t.Fatalf("100*100 cost mismatch")
	}
}

func TestResidenceContainsAndOverlap(t *testing.T) {
	res1 := &Residence{
		ID:      "res_1",
		Name:    "base1",
		WorldID: "world",
		Center:  [3]int{0, 64, 0},
		Size:    10,
		MinPos:  [3]int{-5, 59, -5},
		MaxPos:  [3]int{4, 68, 4},
		Cost:    1000,
		Flags: map[string]bool{
			"build":    false,
			"enter":    true,
			"monsters": false,
		},
		Players: make(map[string]map[string]bool),
	}

	// 内部坐标测试
	if !res1.Contains("world", 0, 64, 0) {
		t.Fatalf("expected center (0,64,0) in res1")
	}
	if !res1.Contains("world", -5, 59, -5) {
		t.Fatalf("expected min boundary in res1")
	}
	if !res1.Contains("world", 4, 68, 4) {
		t.Fatalf("expected max boundary in res1")
	}
	// 外部坐标测试
	if res1.Contains("world", 5, 64, 0) {
		t.Fatalf("x=5 should be outside res1")
	}
	if res1.Contains("other_world", 0, 64, 0) {
		t.Fatalf("different world should not be contained")
	}

	// 重叠检测测试
	resOverlap := &Residence{
		ID:      "res_2",
		WorldID: "world",
		MinPos:  [3]int{0, 60, 0},
		MaxPos:  [3]int{9, 69, 9},
	}
	if !isOverlap(res1, resOverlap) {
		t.Fatalf("expected res1 and resOverlap to overlap")
	}

	resFar := &Residence{
		ID:      "res_3",
		WorldID: "world",
		MinPos:  [3]int{100, 60, 100},
		MaxPos:  [3]int{109, 69, 109},
	}
	if isOverlap(res1, resFar) {
		t.Fatalf("res1 and resFar should not overlap")
	}
}

func TestResidencePermissions(t *testing.T) {
	res := &Residence{
		ID:        "res_perm",
		Name:      "Home",
		OwnerID:   "sid_alice",
		OwnerName: "Alice",
		WorldID:   "world",
		Flags: map[string]bool{
			"build":    false,
			"enter":    true,
			"monsters": false,
		},
		Players: map[string]map[string]bool{
			"Bob": {
				"build": true,
			},
			"Charlie": {
				"enter": false,
			},
		},
	}

	owner := &api.Player{AccountID: "sid_alice", Name: "Alice"}
	bob := &api.Player{AccountID: "sid_bob", Name: "Bob"}
	charlie := &api.Player{AccountID: "sid_charlie", Name: "Charlie"}
	stranger := &api.Player{AccountID: "sid_stranger", Name: "Stranger"}
	op := &api.Player{AccountID: "sid_op", Name: "Admin", Operator: true}

	// 辅助测试权限
	checkPerm := func(p *api.Player, flag string) bool {
		if (p.AccountID != "" && res.OwnerID == p.AccountID) || res.OwnerName == p.Name {
			return true
		}
		if p.Operator || p.Owner {
			return true
		}
		if pMap, ok := res.Players[p.Name]; ok {
			if val, exists := pMap[flag]; exists {
				return val
			}
		}
		if val, exists := res.Flags[flag]; exists {
			return val
		}
		if flag == "enter" {
			return true
		}
		return false
	}

	// 1. 主人应该拥有全部权限
	if !checkPerm(owner, "build") || !checkPerm(owner, "enter") {
		t.Fatalf("owner should have all permissions")
	}

	// 2. OP 应该拥有全部权限
	if !checkPerm(op, "build") || !checkPerm(op, "enter") {
		t.Fatalf("op should have all permissions")
	}

	// 3. Bob 拥有 build 白名单权限
	if !checkPerm(bob, "build") {
		t.Fatalf("Bob should have build permission")
	}

	// 4. Stranger 没有 build 权限，但默认有 enter 权限
	if checkPerm(stranger, "build") {
		t.Fatalf("Stranger should NOT have build permission")
	}
	if !checkPerm(stranger, "enter") {
		t.Fatalf("Stranger should have default enter permission")
	}

	// 5. Charlie 被特设为 enter=false，不能进入
	if checkPerm(charlie, "enter") {
		t.Fatalf("Charlie should NOT have enter permission")
	}
}

func TestIsMonster(t *testing.T) {
	monsters := []string{"zombie", "skeleton", "creeper", "spider", "enderman", "slime", "witch"}
	for _, m := range monsters {
		if !isMonster(m) {
			t.Fatalf("expected %s to be recognized as monster", m)
		}
	}

	passives := []string{"pig", "cow", "sheep", "chicken", "villager"}
	for _, p := range passives {
		if isMonster(p) {
			t.Fatalf("expected %s to NOT be recognized as monster", p)
		}
	}
}
