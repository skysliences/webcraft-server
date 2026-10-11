package game

import (
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func TestDoubleChestFindPair(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r

	chestID := s.id("Chest")

	// 1. 单个箱子
	x, y, z := 10, 60, 10
	s.setBlock(r, x, y, z, chestID, 0)
	k1, k2, isDouble := s.findDoubleChestPair(r, x, y, z)
	if isDouble || k2 != 0 {
		t.Fatalf("expected single chest, got double chest pair (%v, %v)", k1, k2)
	}

	// 2. 两个水平相邻箱子 (X+1)
	s.setBlock(r, x+1, y, z, chestID, 0)
	k1, k2, isDouble = s.findDoubleChestPair(r, x, y, z)
	if !isDouble {
		t.Fatalf("expected horizontal double chest pair, got false")
	}
	expected1 := blockKey(x, y, z)
	expected2 := blockKey(x+1, y, z)
	if expected1 > expected2 {
		expected1, expected2 = expected2, expected1
	}
	if k1 != expected1 || k2 != expected2 {
		t.Fatalf("expected keys (%v, %v), got (%v, %v)", expected1, expected2, k1, k2)
	}

	// 从第二个箱子查询，应该得到完全一致的配对
	k1b, k2b, isDoubleB := s.findDoubleChestPair(r, x+1, y, z)
	if !isDoubleB || k1b != k1 || k2b != k2 {
		t.Fatalf("expected symmetrical double chest keys, got (%v, %v)", k1b, k2b)
	}

	// 3. 垂直叠放的箱子不应合并为大箱子
	s.setBlock(r, x+1, y, z, 0, 0) // 拆除 X+1
	s.setBlock(r, x, y+1, z, chestID, 0)
	_, _, isDoubleVert := s.findDoubleChestPair(r, x, y, z)
	if isDoubleVert {
		t.Fatalf("vertical chests must not form a double chest")
	}

	// 4. 对角线箱子不应合并为大箱子
	s.setBlock(r, x, y+1, z, 0, 0) // 拆除 Y+1
	s.setBlock(r, x+1, y, z+1, chestID, 0)
	_, _, isDoubleDiag := s.findDoubleChestPair(r, x, y, z)
	if isDoubleDiag {
		t.Fatalf("diagonal chests must not form a double chest")
	}
}

func TestDoubleChestWindowAndSlotsSync(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r

	p2 := dropTestPlayer(r, 2, 10.5, 61, 10.5)
	r.Peers[p2] = true

	chestID := s.id("Chest")
	x1, y1, z1 := 20, 60, 20
	x2, y2, z2 := 20, 60, 21
	key1 := blockKey(x1, y1, z1)
	key2 := blockKey(x2, y2, z2)
	s.setBlock(r, x1, y1, z1, chestID, 0)
	s.setBlock(r, x2, y2, z2, chestID, 0)

	// 初始化两个独立的单箱子容器，各自放入不同的测试物品
	itemA := customStack(264, 5) // 钻石 x5
	itemB := customStack(265, 8) // 铁锭 x8
	r.Containers[key1] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[key1].Slots[0] = itemA
	r.Containers[key2] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[key2].Slots[0] = itemB

	// 1. 玩家 1 打开大箱子（从 key1 点击）
	s.openWindow(p1, key1, "chest", 1)
	if p1.window == nil {
		t.Fatal("expected p1 window to open")
	}
	if p1.window.Kind != "large_chest" {
		t.Fatalf("expected window kind 'large_chest', got '%s'", p1.window.Kind)
	}
	if len(p1.window.Slots) != 54 {
		t.Fatalf("expected 54 slots for large chest, got %d", len(p1.window.Slots))
	}

	// 验证前半部分 27 格与后半部分 27 格分别映射两个单箱
	kMin, kMax := key1, key2
	if kMin > kMax {
		kMin, kMax = kMax, kMin
	}
	if p1.window.Key != kMin || p1.window.Key2 != kMax {
		t.Fatalf("expected window keys (%v, %v), got (%v, %v)", kMin, kMax, p1.window.Key, p1.window.Key2)
	}

	// 2. 玩家 2 也打开大箱子（从 key2 点击）
	s.openWindow(p2, key2, "chest", 2)
	if p2.window == nil || p2.window.Kind != "large_chest" || len(p2.window.Slots) != 54 {
		t.Fatal("expected p2 window to open as 54-slot large chest")
	}

	// 3. 玩家 1 修改前半部分槽位 (slot 46 对应 window slot 0)
	newDiamond := customStack(264, 10)
	p1.put(46, newDiamond)
	if r.Containers[p1.window.Key].Slots[0].Count != 10 {
		t.Fatalf("expected container key1 slot 0 updated to 10, got %v", r.Containers[p1.window.Key].Slots[0])
	}
	// 验证玩家 2 实时同步
	if p2.window.Slots[0] == nil || p2.window.Slots[0].Count != 10 {
		t.Fatalf("expected p2 window slot 0 synced to 10, got %v", p2.window.Slots[0])
	}

	// 4. 玩家 2 修改后半部分槽位 (slot 46+27 = 73 对应 window slot 27)
	newIron := customStack(265, 16)
	p2.put(73, newIron)
	if r.Containers[p1.window.Key2].Slots[0].Count != 16 {
		t.Fatalf("expected container key2 slot 0 updated to 16, got %v", r.Containers[p1.window.Key2].Slots[0])
	}
	// 验证玩家 1 实时同步
	if p1.window.Slots[27] == nil || p1.window.Slots[27].Count != 16 {
		t.Fatalf("expected p1 window slot 27 synced to 16, got %v", p1.window.Slots[27])
	}

	// 5. 破坏箱子保护与独立性
	// 箱子 1 中依然有物品，破坏应被拒绝
	s.survivalMessage(p1, Message{T: "dig", S: 0, X: x1, Y: y1, Z: z1, F: 1})
	s.survivalMessage(p1, Message{T: "dig", S: 2, X: x1, Y: y1, Z: z1, F: 1})
	if s.block(r, x1, y1, z1) != chestID {
		t.Fatal("chest with items must not be broken")
	}

	// 取出箱子 1 的物品后拆除
	r.Containers[key1].Slots[0] = nil
	p1.window.Slots[0] = nil
	s.setBlock(r, x1, y1, z1, 0, 0)
	delete(r.Containers, key1)
	for other := range r.Peers {
		if other.window != nil && (other.window.Key == key1 || other.window.Key2 == key1) {
			s.closeWindow(other)
		}
	}
	if p1.window != nil || p2.window != nil {
		t.Fatal("expected both player windows closed after chest 1 removed")
	}
	// 箱子 2 及其物品完好无损
	if r.Containers[key2] == nil || r.Containers[key2].Slots[0] == nil || r.Containers[key2].Slots[0].Count != 16 {
		t.Fatalf("expected chest 2 and its items intact after chest 1 removed, got %+v", r.Containers[key2])
	}
}

func TestSignPlacementOnChestDirect(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r

	chestID := s.id("Chest")
	signItemID := 383
	chestX, chestY, chestZ := 30, 60, 30
	s.setBlock(r, chestX, chestY, chestZ, chestID, 0)
	s.setBlock(r, chestX, chestY, chestZ+1, 0, 0)

	// 玩家手持木牌 (选中的快捷栏槽位放 4 个木牌)
	p.state.Inv.Slots[p.state.Inv.Selected] = customStack(signItemID, 4)

	// 模拟玩家对箱子侧面 (F = 3, Z+1) 右键放置木牌
	m := Message{T: "place", X: chestX, Y: chestY, Z: chestZ, F: 3}
	s.survivalMessage(p, m)

	// 1. 验证没有错误打开箱子窗口
	if p.window != nil {
		t.Fatalf("holding sign and right clicking chest must NOT open window, but window was opened: %+v", p.window)
	}

	// 2. 验证木牌成功放置在箱子侧面 (30, 60, 31)
	signX, signY, signZ := chestX, chestY, chestZ+1
	placedBlock := s.block(r, signX, signY, signZ)
	if placedBlock != blkSign {
		t.Fatalf("expected sign block %d at (%d,%d,%d), got %d", blkSign, signX, signY, signZ, placedBlock)
	}

	// 3. 验证背包消耗了 1 个木牌
	held := p.state.Inv.Slots[p.state.Inv.Selected]
	if held == nil || held.Count != 3 {
		t.Fatalf("expected 3 signs remaining in hand, got %+v", held)
	}

	// 4. 验证木牌数据正确保存
	signKey := blockKey(signX, signY, signZ)
	mech := mechanicSnapshot(r, signKey)
	if mech == nil {
		t.Fatalf("expected mechanic state for placed sign at key %v", signKey)
	}
	if mech.SupportFace != 3 {
		t.Fatalf("expected sign support face 3, got %d", mech.SupportFace)
	}
}

func TestSignPlacementOnChestTop(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r

	chestID := s.id("Chest")
	signItemID := 383
	chestX, chestY, chestZ := 35, 60, 35
	s.setBlock(r, chestX, chestY, chestZ, chestID, 0)
	s.setBlock(r, chestX, chestY+1, chestZ, 0, 0) // 箱子上方为空气

	// 玩家手持木牌 (选中的快捷栏槽位放 2 个木牌)
	p.state.Inv.Slots[p.state.Inv.Selected] = customStack(signItemID, 2)

	// 模拟玩家对箱子顶面 (F = 1, Y+1) 右键放置木牌
	m := Message{T: "place", X: chestX, Y: chestY, Z: chestZ, F: 1}
	s.survivalMessage(p, m)

	// 1. 验证没有错误打开箱子窗口
	if p.window != nil {
		t.Fatalf("holding sign and right clicking chest top must NOT open window, but window was opened: %+v", p.window)
	}

	// 2. 验证木牌成功放置在箱子顶面 (35, 61, 35)
	signX, signY, signZ := chestX, chestY+1, chestZ
	placedBlock := s.block(r, signX, signY, signZ)
	if placedBlock != blkSign {
		t.Fatalf("expected sign block %d on top of chest at (%d,%d,%d), got %d", blkSign, signX, signY, signZ, placedBlock)
	}

	// 3. 验证木牌数据正确保存，支撑面为 1
	signKey := blockKey(signX, signY, signZ)
	mech := mechanicSnapshot(r, signKey)
	if mech == nil {
		t.Fatalf("expected mechanic state for placed sign at key %v", signKey)
	}
	if mech.SupportFace != 1 {
		t.Fatalf("expected sign support face 1, got %d", mech.SupportFace)
	}

	// 4. 验证破坏下方箱子时，上方的木牌自动脱落
	s.survivalMessage(p, Message{T: "dig", S: 0, X: chestX, Y: chestY, Z: chestZ, F: 1})
	if p.mining != nil {
		p.mining.At = time.Now().Add(-time.Minute)
	}
	s.survivalMessage(p, Message{T: "dig", S: 2, X: chestX, Y: chestY, Z: chestZ, F: 1})
	if topBlock := s.block(r, signX, signY, signZ); topBlock != 0 {
		t.Fatalf("expected sign to drop when chest beneath is destroyed, but block is %d", topBlock)
	}
}

func TestDoubleChestLockCoupling(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, stranger := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	stranger.name = "Alex"
	stranger.sid = "stranger_alex_456"

	chestID := s.id("Chest")
	cx1, cy1, cz1 := 40, 60, 40
	cx2, cy2, cz2 := 41, 60, 40
	k1 := blockKey(cx1, cy1, cz1)
	k2 := blockKey(cx2, cy2, cz2)
	s.setBlock(r, cx1, cy1, cz1, chestID, 0)
	s.setBlock(r, cx2, cy2, cz2, chestID, 0)
	r.Containers[k1] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	r.Containers[k2] = &Container{Kind: "chest", Slots: make(Stacks, 27)}

	// 1. 验证 findDoubleChestPair 能够双向识别大箱子
	pk1, pk2, isDouble := s.findDoubleChestPair(r, cx2, cy2, cz2)
	if !isDouble {
		t.Fatal("expected double chest detected from cx2")
	}
	if pk1 != k1 || pk2 != k2 {
		t.Fatalf("expected double chest keys (%v, %v), got (%v, %v)", k1, k2, pk1, pk2)
	}

	// 2. 验证 openWindow 携带 doubleChest 标记与 authorized 标识
	s.openWindow(stranger, k2, "chest", 0, "plugin_authorized")
	if stranger.window == nil {
		t.Fatal("expected stranger window to open via plugin_authorized")
	}
	if stranger.window.Kind != "large_chest" || len(stranger.window.Slots) != 54 {
		t.Fatalf("expected 54-slot large chest window opened, got kind=%s slots=%d", stranger.window.Kind, len(stranger.window.Slots))
	}
	s.closeWindow(stranger)

	// 3. 验证通过 open_container action 打开大箱子
	s.applyAction(api.Action{
		Type:     "open_container",
		WorldID:  r.ID,
		PlayerID: stranger.id,
		X:        float64(cx2),
		Y:        float64(cy2),
		Z:        float64(cz2),
	})
	if stranger.window == nil {
		t.Fatal("expected stranger window to open after authorized open_container action")
	}
	if stranger.window.Kind != "large_chest" || len(stranger.window.Slots) != 54 {
		t.Fatalf("expected 54-slot large chest window opened for authorized user, got kind=%s slots=%d", stranger.window.Kind, len(stranger.window.Slots))
	}
}
