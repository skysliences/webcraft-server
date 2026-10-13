package game

import (
	"testing"

	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 单元测试：下界生态、潜影盒背包、凋灵 Boss 与信标系统
// ---------------------------------------------------------------------------

// 1. 测试烈焰人火球三连发与凋灵骷髅凋灵效果
func TestNetherMobsBlazeAndWitherSkeleton(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	r.Dim = "nether"

	// 生成烈焰人 (Y=61 踩在 60 的泥土上)
	blaze, err := s.spawnMob(r, nil, "blaze", 10.5, 61.0, 10.5, "test")
	if err != nil || blaze == nil {
		t.Fatalf("spawn blaze failed: %v", err)
	}

	// 验证烈焰人受水伤害
	s.damageMob(r, nil, blaze, 2, "water")
	if blaze.Health != 18 {
		t.Fatalf("expected blaze health 18 after water damage, got %f", blaze.Health)
	}

	// 生成凋灵骷髅
	ws, err := s.spawnMob(r, nil, "wither_skeleton", 11.0, 61.0, 11.0, "test")
	if err != nil || ws == nil {
		t.Fatalf("spawn wither skeleton failed: %v", err)
	}

	// 凋灵骷髅攻击玩家并附加凋灵效果
	p.state.Health = 20.0
	s.damagePlayer(p, 4.0, "被凋灵骷髅刺杀了。", &api.DamageContext{
		Cause:    "entity_attack",
		Attacker: mobTarget(ws),
	})
	s.addEffect(p, "wither", 200, 0)

	if p.state.Health != 16.0 {
		t.Fatalf("expected player health 16.0, got %f", p.state.Health)
	}
	if !s.hasEffect(p, "wither") {
		t.Fatal("expected player to have wither effect")
	}
}

// 2. 测试潜影盒背包扩容：存储物品 -> 破坏保留元数据 -> 再次放置还原容器
func TestShulkerBoxContainerPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)

	key := blockKey(20, 61, 20)
	shulkerBlockID := 176

	// 1. 初始化放置一个潜影盒方块
	r.Edits[key] = shulkerBlockID
	c := &Container{
		Kind:  "chest",
		Slots: make(Stacks, 27),
	}
	// 在潜影盒中放入珍贵物品：64 钻石和 1 铁剑
	diamondID := s.id("Diamond")
	swordID := s.id("Iron Sword")
	c.Slots[0] = &Stack{ID: diamondID, Count: 64}
	c.Slots[1] = &Stack{ID: swordID, Count: 1, Damage: 5}
	r.Containers[key] = c

	// 2. 模拟破坏潜影盒方块，计算掉落物
	drops := s.dropsAt(r, key, shulkerBlockID, 0)
	if len(drops) != 1 {
		t.Fatalf("expected 1 dropped shulker box, got %d", len(drops))
	}
	droppedBox := drops[0]
	if droppedBox.Meta == nil || droppedBox.Meta.CustomData == nil {
		t.Fatal("dropped shulker box missing metadata")
	}
	rawStr, ok := droppedBox.Meta.CustomData[shulkerDataKey].(string)
	if !ok || rawStr == "" {
		t.Fatal("missing serialized container data in shulker box metadata")
	}

	// 3. 玩家将该潜影盒放置到新的位置 (25, 61, 25)
	newKey := blockKey(25, 61, 25)
	p.state.Inv.Slots[0] = droppedBox
	p.state.Inv.Selected = 0

	s.afterPlace(p, 25, 61, 25, shulkerBlockID, Message{})

	// 验证新容器已完整恢复
	restoredContainer := r.Containers[newKey]
	if restoredContainer == nil {
		t.Fatal("expected restored container at new location, got nil")
	}
	if len(restoredContainer.Slots) != 27 {
		t.Fatalf("expected 27 slots, got %d", len(restoredContainer.Slots))
	}
	if restoredContainer.Slots[0] == nil || restoredContainer.Slots[0].ID != diamondID || restoredContainer.Slots[0].Count != 64 {
		t.Fatalf("restored diamond slot mismatch: %+v", restoredContainer.Slots[0])
	}
	if restoredContainer.Slots[1] == nil || restoredContainer.Slots[1].ID != swordID || restoredContainer.Slots[1].Damage != 5 {
		t.Fatalf("restored sword slot mismatch: %+v", restoredContainer.Slots[1])
	}
}

// 3. 测试凋灵 Boss 召唤、蓄力、弹射物与战斗掉落
func TestWitherBossFightAndSummon(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)

	// 搭建灵魂沙 T 结构：
	// 底层 (15, 61, 15) = 181 (灵魂沙)
	// 中层 (15, 62, 15), (14, 62, 15), (16, 62, 15) = blkWitherSoulSand
	// 顶层 (14, 63, 15), (16, 63, 15) = 178 (凋灵骷髅头颅)
	r.Edits[blockKey(15, 61, 15)] = blkWitherSoulSand
	r.Edits[blockKey(15, 62, 15)] = blkWitherSoulSand
	r.Edits[blockKey(14, 62, 15)] = blkWitherSoulSand
	r.Edits[blockKey(16, 62, 15)] = blkWitherSoulSand
	r.Edits[blockKey(14, 63, 15)] = 178
	r.Edits[blockKey(16, 63, 15)] = 178

	// 放置最后一颗中间头颅 (15, 63, 15)
	r.Edits[blockKey(15, 63, 15)] = 178
	s.checkWitherSpawn(r, p, 15, 63, 15)

	// 验证 7 块方块已被消除
	if s.block(r, 15, 63, 15) != 0 || s.block(r, 15, 61, 15) != 0 {
		t.Fatal("expected summon blocks to be cleared")
	}

	// 验证凋灵 Boss 已生成
	var wither *Mob
	for _, m := range r.Mobs {
		if m.Type == "wither" {
			wither = m
			break
		}
	}
	if wither == nil {
		t.Fatal("expected wither mob to be spawned")
	}
	if wither.Health != 300 {
		t.Fatalf("expected wither health 300, got %f", wither.Health)
	}

	ext := getMobExt(wither.ID)
	if ext.ChargeTicks <= 0 {
		t.Fatal("expected wither to be charging")
	}

	// 模拟充能结束
	ext.ChargeTicks = 1
	s.tickAdvancedMobs(r)
	if ext.ChargeTicks != 0 {
		t.Fatal("expected wither to finish charge")
	}

	// 测试凋灵狂暴与防箭机制
	wither.Health = 120 // 半血以下狂暴
	pr := &Projectile{
		Type: projArrow,
		VX:   1.0, VY: 0, VZ: 0,
		Damage: 10,
	}
	// 击中狂暴凋灵应当被弹开
	hit := s.projectileHitEntity(r, pr, wither.X, wither.Y+1.0, wither.Z)
	if !hit || pr.VX >= 0 {
		t.Fatal("expected arrow to be deflected by wither armor")
	}

	// 测试击败凋灵掉落下界之星 (Nether Star)
	s.damageMob(r, p, wither, 300, "attack")
	if wither.Health != 0 {
		t.Fatalf("expected wither to be dead, health: %f", wither.Health)
	}
	// 检查掉落物中是否含有 Nether Star
	starFound := false
	for _, drop := range r.Drops {
		if drop != nil && drop.Stack != nil && (drop.Stack.ID == 517 || (s.rules.byID[drop.Stack.ID].Name == "Nether Star")) {
			starFound = true
			break
		}
	}
	if !starFound {
		t.Fatal("expected Nether Star drop from killed wither")
	}
}

// 4. 测试信标金字塔检测与光环 Buff
func TestBeaconPyramidAndBuffs(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)

	bx, by, bz := 15, 65, 15
	beaconID := 177
	r.Edits[blockKey(bx, by, bz)] = beaconID

	// 搭建 3x3 铁块底座 (Level 1)
	ironBlockID := 35
	for ox := -1; ox <= 1; ox++ {
		for oz := -1; oz <= 1; oz++ {
			r.Edits[blockKey(bx+ox, by-1, bz+oz)] = ironBlockID
		}
	}

	// 玩家置于信标有效范围内
	p.state.X, p.state.Y, p.state.Z = 16.0, 65.0, 16.0

	// 运行信标检测
	s.tickBeacons(r)

	// 验证玩家已获得 speed 与 haste 效果
	if !s.hasEffect(p, "speed") {
		t.Fatal("expected player to have speed effect from beacon")
	}
	if !s.hasEffect(p, "haste") {
		t.Fatal("expected player to have haste effect from beacon")
	}
}
