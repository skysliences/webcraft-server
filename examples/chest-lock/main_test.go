package main

import (
	api "mcraft/backend/pkg/plugin"
	"testing"
)

func TestPosKey(t *testing.T) {
	if k := posKey(12, 64, 38); k != "12,64,38" {
		t.Fatalf("unexpected posKey: %s", k)
	}
	if k := posKey(-5, 0, -100); k != "-5,0,-100" {
		t.Fatalf("unexpected posKey: %s", k)
	}
}

func TestAdjacentDoubleChestLockLookup(t *testing.T) {
	locks := map[string]*LockRecord{
		posKey(10, 60, 20): {
			ChestKey: posKey(10, 60, 20),
			ChestPos: [3]int{10, 60, 20},
			Password: "mypassword",
		},
	}

	// 1. 直接命中主箱
	if rec := locks[posKey(10, 60, 20)]; rec == nil || rec.Password != "mypassword" {
		t.Fatal("direct chest lock not found")
	}

	// 2. 模拟点击相邻副箱 (11, 60, 20)
	adjX, adjY, adjZ := 11, 60, 20
	var found *LockRecord
	for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		if r := locks[posKey(adjX+d[0], adjY, adjZ+d[1])]; r != nil {
			found = r
			break
		}
	}
	if found == nil {
		t.Fatal("expected adjacent double chest lock record to be found")
	}
	if found.Password != "mypassword" {
		t.Fatalf("unexpected password: %s", found.Password)
	}
}

func TestAutomationCannotBypassLockedChest(t *testing.T) {
	locks := map[string]*LockRecord{"10,60,10": {WorldID: "world"}}
	for _, field := range []string{"origin", "target"} {
		for _, dx := range []int{0, 1, -1} {
			e := &api.Event{World: &api.World{ID: "world"}, Data: map[string]any{field: map[string]any{"kind": "container", "type": "chest", "x": float64(10 + dx), "y": 60., "z": 10.}}}
			if !lockedAutomation(e, locks) {
				t.Fatal("locked chest/paired half bypassed")
			}
			e.World.ID = "other"
			if lockedAutomation(e, locks) {
				t.Fatal("other world denied")
			}
			e.World.ID = "world"
			e.Data[field].(map[string]any)["type"] = "hopper"
			if lockedAutomation(e, locks) {
				t.Fatal("unrelated hopper denied")
			}
		}
	}
}


