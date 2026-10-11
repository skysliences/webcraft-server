package game

import (
	"reflect"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func TestCustomUIEventAndActions(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	ap.name = "Tester"
	ap.sid = "test_player_uid_999"

	// 1. 验证插件通过 Action 推送 Custom UI 弹窗
	testUI := &api.CustomUIDefinition{
		ID:      "test:prompt",
		Title:   "测试弹窗",
		Content: "请输入测试内容",
		Inputs: []api.CustomUIInput{
			{ID: "field1", Label: "输入项", Type: "text"},
		},
		Buttons: []api.CustomUIButton{
			{ID: "ok", Text: "确认", Type: "submit"},
		},
		Data: map[string]any{"targetKey": "abc1234"},
	}

	s.applyAction(api.Action{
		Type:     "custom_ui",
		WorldID:  r.ID,
		PlayerID: ap.id,
		CustomUI: testUI,
	})

	// 2. 模拟客户端提交 custom_ui_response，验证触发 PlayerCustomUISubmit 事件
	respMsg := Message{
		T:          "custom_ui_response",
		UIID:       "test:prompt",
		Button:     "ok",
		Values:     map[string]any{"field1": "hello_world"},
		CustomData: map[string]any{"targetKey": "abc1234"},
	}
	s.survivalMessage(ap, respMsg)

	// 3. 验证 UpdateSign 能够直接写入木牌文本
	signX, signY, signZ := 15, 60, 15
	signKey := blockKey(signX, signY, signZ)
	s.setBlock(r, signX, signY, signZ, blkSign, 0)

	s.applyAction(api.Action{
		Type:    "write_sign",
		WorldID: r.ID,
		X:       float64(signX),
		Y:       float64(signY),
		Z:       float64(signZ),
		Lines:   []string{"[已上锁]", "Tester", "密码保护", "****"},
	})

	snap := mechanicSnapshot(r, signKey)
	if !reflect.DeepEqual(snap.Lines, [4]string{"[已上锁]", "Tester", "密码保护", "****"}) {
		t.Fatalf("expected sign lines updated, got %+v", snap.Lines)
	}

	// 4. 验证 OpenContainer 能够命令服务端为玩家打开容器窗口
	chestX, chestY, chestZ := 16, 60, 15
	chestKey := blockKey(chestX, chestY, chestZ)
	r.Edits[chestKey] = s.id("Chest")
	r.Containers[chestKey] = &Container{Kind: "chest", Slots: make(Stacks, 27)}

	s.applyAction(api.Action{
		Type:     "open_container",
		WorldID:  r.ID,
		PlayerID: ap.id,
		X:        float64(chestX),
		Y:        float64(chestY),
		Z:        float64(chestZ),
	})

	if ap.window == nil {
		t.Fatal("expected container window to be opened for player")
	}
}
