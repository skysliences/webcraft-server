package game

import (
	"testing"
)

// 1. 测试槽位过短（如村庄生成或旧存档的 5 格宝箱）自动规整扩容至 27 格
func TestContainerAutoNormalize_ShortSlots(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createFixTestWorld(s)
	chestKey := blockKey(10, 61, 10)
	s.setBlock(r, 10, 61, 10, s.id("Chest"), setQuiet)

	// 模拟村庄铁匠铺宝箱：仅有 5 个物品的旧切片
	r.Containers[chestKey] = &Container{
		Kind: "chest",
		Slots: Stacks{
			{ID: 315, Count: 3}, // Iron Ingot
			{ID: 339, Count: 8}, // Bread
			{ID: 38, Count: 2},  // Emerald
			{ID: 337, Count: 4}, // Apples
			{ID: 24, Count: 3},  // Obsidian
		},
	}
	r.Dirty = false

	// 玩家打开箱子
	s.openWindow(p, chestKey, "chest", 1)

	// 断言：玩家窗口必须成功打开
	if p.window == nil {
		t.Fatalf("expected p.window to be opened, got nil")
	}
	if p.window.Kind != "chest" {
		t.Fatalf("expected window kind 'chest', got %s", p.window.Kind)
	}
	if len(p.window.Slots) != 27 {
		t.Fatalf("expected window slots length 27, got %d", len(p.window.Slots))
	}

	// 断言：底层的容器也已自适应规整为标准 27 格
	c := r.Containers[chestKey]
	if c == nil {
		t.Fatalf("container not found")
	}
	if len(c.Slots) != 27 {
		t.Fatalf("expected container slots to be normalized to 27, got %d", len(c.Slots))
	}
	if !r.Dirty {
		t.Fatalf("expected room to be marked dirty after normalization")
	}

	// 断言：原有 5 个物品完好无损地保存在 0..4 槽位
	if c.Slots[0] == nil || c.Slots[0].ID != 315 || c.Slots[0].Count != 3 {
		t.Fatalf("slot 0 corrupted: %+v", c.Slots[0])
	}
	if c.Slots[1] == nil || c.Slots[1].ID != 339 || c.Slots[1].Count != 8 {
		t.Fatalf("slot 1 corrupted: %+v", c.Slots[1])
	}
	if c.Slots[2] == nil || c.Slots[2].ID != 38 || c.Slots[2].Count != 2 {
		t.Fatalf("slot 2 corrupted: %+v", c.Slots[2])
	}
	if c.Slots[3] == nil || c.Slots[3].ID != 337 || c.Slots[3].Count != 4 {
		t.Fatalf("slot 3 corrupted: %+v", c.Slots[3])
	}
	if c.Slots[4] == nil || c.Slots[4].ID != 24 || c.Slots[4].Count != 3 {
		t.Fatalf("slot 4 corrupted: %+v", c.Slots[4])
	}

	// 剩余槽位必须全部为 nil（空格）
	for i := 5; i < 27; i++ {
		if c.Slots[i] != nil {
			t.Fatalf("expected slot %d to be nil, got %+v", i, c.Slots[i])
		}
	}
}

// 2. 测试 Kind 为空或不匹配时自动修正
func TestContainerAutoNormalize_MismatchKind(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createFixTestWorld(s)
	chestKey := blockKey(11, 61, 10)
	s.setBlock(r, 11, 61, 10, s.id("Chest"), setQuiet)

	// 模拟 Kind 为空或旧类型的容器
	r.Containers[chestKey] = &Container{
		Kind:  "",
		Slots: make(Stacks, 27),
	}
	r.Containers[chestKey].Slots[0] = &Stack{ID: 1, Count: 64}

	s.openWindow(p, chestKey, "chest", 2)

	if p.window == nil {
		t.Fatalf("window should be opened even if container kind was empty")
	}
	if r.Containers[chestKey].Kind != "chest" {
		t.Fatalf("expected kind 'chest', got '%s'", r.Containers[chestKey].Kind)
	}
	if r.Containers[chestKey].Slots[0] == nil || r.Containers[chestKey].Slots[0].ID != 1 {
		t.Fatalf("item in slot 0 should be preserved")
	}
}

// 3. 测试槽位溢出超长时，多余物品被保留或安全掉落，不丢失物品
func TestContainerAutoNormalize_OverflownSlots(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createFixTestWorld(s)
	chestKey := blockKey(12, 61, 10)
	s.setBlock(r, 12, 61, 10, s.id("Chest"), setQuiet)

	// 构造一个有 29 格的容器，第 27 格为 nil，第 28 格有物品
	overflowSlots := make(Stacks, 29)
	overflowSlots[0] = &Stack{ID: 1, Count: 10}
	overflowSlots[28] = &Stack{ID: 315, Count: 5} // 溢出槽位中的物品

	r.Containers[chestKey] = &Container{
		Kind:  "chest",
		Slots: overflowSlots,
	}

	s.openWindow(p, chestKey, "chest", 3)

	c := r.Containers[chestKey]
	if len(c.Slots) != 27 {
		t.Fatalf("expected container slots to be 27, got %d", len(c.Slots))
	}
	// 溢出的铁锭 (315) 应被转移到前 27 格中的空闲槽位
	foundIron := false
	for _, sl := range c.Slots {
		if sl != nil && sl.ID == 315 && sl.Count == 5 {
			foundIron = true
			break
		}
	}
	if !foundIron {
		t.Fatalf("expected overflowed iron ingot to be placed in an empty slot within the first 27 slots")
	}
}

// 4. 测试末影箱槽位平滑扩容
func TestContainerAutoNormalize_EnderChest(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	_, p := createFixTestWorld(s)
	enderKey := blockKey(13, 61, 10)

	// 模拟玩家末影箱只有 3 格
	ext := p.ext()
	ext.EnderChest = Stacks{
		{ID: 264, Count: 10}, // Diamond
	}

	s.openWindow(p, enderKey, "ender", 4)

	if p.window == nil {
		t.Fatalf("ender chest window should open")
	}
	if len(p.window.Slots) != 27 {
		t.Fatalf("expected ender chest window slots to be 27, got %d", len(p.window.Slots))
	}
	if p.window.Slots[0] == nil || p.window.Slots[0].ID != 264 || p.window.Slots[0].Count != 10 {
		t.Fatalf("diamond should be preserved in slot 0")
	}
	if len(ext.EnderChest) != 27 {
		t.Fatalf("player ender chest should be expanded to 27, got %d", len(ext.EnderChest))
	}
}

// 5. 验证村庄生成铁匠铺宝箱默认就是标准的 27 格切片
func TestVillageBlacksmithChestStandardSize(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createFixTestWorld(s)
	r.Infinite = true
	_, targetCoord := findStructureVillage(t, s, r)

	// 生成地形（含村庄生成）
	s.generateTerrain(r, targetCoord)

	// 检查是否有生成的箱子
	var villageChest *Container
	var chestKey uint64
	for k, c := range r.Containers {
		if c.Kind == "chest" {
			villageChest = c
			chestKey = k
			break
		}
	}

	if villageChest == nil {
		t.Fatalf("expected a village blacksmith chest to be generated in r.Containers")
	}
	if len(villageChest.Slots) != 27 {
		t.Fatalf("expected village chest to be initialized with 27 slots, got %d", len(villageChest.Slots))
	}
	// 验证包含铁锭
	if villageChest.Slots[0] == nil || villageChest.Slots[0].ID != 315 {
		t.Fatalf("expected slot 0 to contain Iron Ingot (315), got %+v", villageChest.Slots[0])
	}

	// 玩家直接打开村庄宝箱，必须丝滑开启，不能报格式错误！
	s.openWindow(p, chestKey, "chest", 5)
	if p.window == nil {
		t.Fatalf("village chest failed to open!")
	}
	if len(p.window.Slots) != 27 {
		t.Fatalf("expected open window slots to be 27, got %d", len(p.window.Slots))
	}
}
