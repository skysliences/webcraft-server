package game

import (
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func TestChestLockLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, owner := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	owner.name = "Steve"
	owner.sid = "owner_steve_123"

	thief := dropTestPlayer(r, 2, 10.5, 61, 10.5)
	thief.name = "Alex"
	thief.sid = "thief_alex_456"
	r.Peers[thief] = true

	chestX, chestY, chestZ := 10, 60, 10
	chestKey := blockKey(chestX, chestY, chestZ)
	s.setBlock(r, chestX, chestY, chestZ, s.id("Chest"), 0)
	r.Containers[chestKey] = &Container{Kind: "chest", Slots: make(Stacks, 27)}

	signX, signY, signZ := 10, 60, 11
	signKey := blockKey(signX, signY, signZ)
	s.setBlock(r, signX, signY, signZ, blkSign, 0)

	// 1. 模拟插件收到木牌贴箱子后弹出设置密码 UI
	// 验证 applyAction("custom_ui") 正常推送到玩家
	setPwdUI := &api.CustomUIDefinition{
		ID:      "chest_lock:set_password",
		Title:   "设置箱子密码",
		Content: "输入密码",
		Inputs: []api.CustomUIInput{
			{ID: "password", Label: "密码", Type: "password"},
		},
		Buttons: []api.CustomUIButton{
			{ID: "confirm", Text: "确认", Type: "submit"},
		},
		Data: map[string]any{
			"chestX": chestX, "chestY": chestY, "chestZ": chestZ,
			"signX": signX, "signY": signY, "signZ": signZ,
		},
	}
	s.applyAction(api.Action{
		Type:     "custom_ui",
		WorldID:  r.ID,
		PlayerID: owner.id,
		CustomUI: setPwdUI,
	})

	// 2. 模拟玩家提交密码，插件执行 write_sign
	s.applyAction(api.Action{
		Type:    "write_sign",
		WorldID: r.ID,
		X:       float64(signX),
		Y:       float64(signY),
		Z:       float64(signZ),
		Lines:   []string{"[私有密码箱]", "Steve", "右键箱子输入", "密码解锁"},
	})

	signSnap := mechanicSnapshot(r, signKey)
	if !reflect.DeepEqual(signSnap.Lines, [4]string{"[私有密码箱]", "Steve", "右键箱子输入", "密码解锁"}) {
		t.Fatalf("expected sign updated to locked state, got %+v", signSnap.Lines)
	}

	// 3. 验证通过 open_container 动作授权打开箱子
	if owner.window != nil {
		t.Fatal("expected owner window to initially be nil")
	}
	s.applyAction(api.Action{
		Type:     "open_container",
		WorldID:  r.ID,
		PlayerID: owner.id,
		X:        float64(chestX),
		Y:        float64(chestY),
		Z:        float64(chestZ),
	})
	if owner.window == nil {
		t.Fatal("expected owner window opened via open_container action")
	}
	if owner.window.Key != chestKey {
		t.Fatalf("expected opened window key %v, got %v", chestKey, owner.window.Key)
	}
}

func TestSignPlacementOnChestEventMetadata(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, owner := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	owner.name = "Steve"
	owner.sid = "owner_steve_123"

	// 箱子位于 (15, 61, 25)，下方为 Y=60 的泥土，四周 Y=61 为空气
	chestX, chestY, chestZ := 15, 61, 25
	chestKey := blockKey(chestX, chestY, chestZ)
	s.setBlock(r, chestX, chestY, chestZ, s.id("Chest"), 0)
	r.Containers[chestKey] = &Container{Kind: "chest", Slots: make(Stacks, 27)}

	// 监听 BlockPlaced 事件
	var captured *api.Event
	s.testEventHook = func(e *api.Event) {
		if e.Name == api.BlockPlaced {
			captured = e
		}
	}

	// 玩家手持 Oak Sign 对箱子东侧 (F=5: +X) 放置木牌
	owner.state.Inv.Selected = 0
	owner.state.Inv.Slots[0] = &Stack{ID: 383, Count: 1} // Oak Sign
	owner.state.X = 15.5
	owner.state.Y = 61
	owner.state.Z = 25.5

	handled := s.handleMechanicUse(owner, Message{T: "place", X: chestX, Y: chestY, Z: chestZ, F: 5}, nil, false)
	if !handled {
		t.Fatal("expected handleMechanicUse to handle sign placement on chest")
	}

	if captured == nil {
		t.Fatal("expected BlockPlaced event to be emitted")
	}

	// 核心断言：placedOnZ 必须精确为 25，绝不能回退为 0
	if pz := int(captured.Number("placedOnZ")); pz != 25 {
		t.Fatalf("expected placedOnZ to be 25, got %d", pz)
	}
	if px := int(captured.Number("placedOnX")); px != 15 {
		t.Fatalf("expected placedOnX to be 15, got %d", px)
	}
	if py := int(captured.Number("placedOnY")); py != 61 {
		t.Fatalf("expected placedOnY to be 61, got %d", py)
	}
	if pb := captured.String("placedOnBlockKey"); pb != "Chest" {
		t.Fatalf("expected placedOnBlockKey to be Chest, got %s", pb)
	}
}

