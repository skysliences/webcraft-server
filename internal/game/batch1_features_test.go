package game

import (
	"testing"
	"time"
)

func createBatch1TestWorld(s *Server) (*Room, *Peer) {
	r := newRoom("test_room_batch1", "第一批测试世界", "survival", 0)
	for x := 0; x < 32; x++ {
		for z := 0; z < 32; z++ {
			r.Edits[blockKey(x, 60, z)] = blkDirt
			r.Edits[blockKey(x, 61, z)] = 0
			r.Edits[blockKey(x, 62, z)] = 0
			r.Edits[blockKey(x, 63, z)] = 0
		}
	}
	p := dropTestPlayer(r, 1, 10.5, 61, 10.5)
	r.Peers[p] = true
	return r, p
}

func TestBatch1Farming(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 锄地：泥土变为耕地
	p.state.Inv.Slots[0] = stack(s.rules.byName["Iron Hoe"], 1, 0)
	p.state.Inv.Selected = 0
	r.Edits[blockKey(10, 60, 10)] = blkDirt
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1})
	if b := s.block(r, 10, 60, 10); b != blkFarmlandDry && b != blkFarmlandWet {
		t.Fatalf("expected farmland at (10,60,10), got %d (%s)", b, s.fullName(b))
	}

	// 2. 播种小麦种子
	p.state.Inv.Slots[0] = stack(itemWheatSeeds, 5, 0)
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1})
	if b := s.block(r, 10, 61, 10); b != blkWheatMin {
		t.Fatalf("expected wheat at (10,61,10), got %d (%s)", b, s.fullName(b))
	}
	if p.state.Inv.Slots[0].Count != 4 {
		t.Fatalf("expected 4 wheat seeds remaining, got %d", p.state.Inv.Slots[0].Count)
	}

	// 3. 骨粉催熟小麦
	p.state.Inv.Slots[0] = stack(itemBoneMeal, 10, 0)
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 61, Z: 10, F: 1})
	if b := s.block(r, 10, 61, 10); b <= blkWheatMin {
		t.Fatalf("expected wheat to have grown, got %d", b)
	}

	// 4. 树苗生长
	r.Edits[blockKey(15, 60, 15)] = blkDirt
	r.Edits[blockKey(15, 61, 15)] = blkOakSapling
	ok := s.growTree(r, 15, 61, 15, blkOakSapling)
	if !ok {
		t.Fatalf("expected tree growth to succeed")
	}
	if b := s.block(r, 15, 61, 15); b != blkOakLog {
		t.Fatalf("expected oak log at tree base, got %d (%s)", b, s.fullName(b))
	}
}

func TestBatch1DoorsAndBeds(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 放置门
	p.state.Inv.Slots[0] = stack(110, 1, 0) // Oak Door item
	p.state.Inv.Selected = 0
	r.Edits[blockKey(10, 60, 10)] = blkCobblestone
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1})
	lower := s.block(r, 10, 61, 10)
	upper := s.block(r, 10, 62, 10)
	if !isDoor(lower) || upper != blkDoorUpper {
		t.Fatalf("door not placed correctly: lower=%d, upper=%d", lower, upper)
	}

	// 2. 开关门
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 61, Z: 10, F: 1})
	openLower := s.block(r, 10, 61, 10)
	if openLower == lower {
		t.Fatalf("door toggle failed: was %d, still %d", lower, openLower)
	}

	// 3. 破坏门下半部分，上半部分应该联动被破坏
	p.mining = &digState{X: 10, Y: 61, Z: 10, Block: openLower, At: time.Now().Add(-5 * time.Second)}
	s.survivalMessage(p, Message{T: "dig", S: 2, X: 10, Y: 61, Z: 10, F: 1})
	if b := s.block(r, 10, 62, 10); b != 0 {
		t.Fatalf("expected upper door to be removed, got %d", b)
	}

	// 4. 放置床
	p.state.Inv.Slots[0] = stack(132, 1, 0) // Bed item
	r.Edits[blockKey(11, 60, 10)] = blkCobblestone
	r.Edits[blockKey(11, 60, 11)] = blkCobblestone
	s.survivalMessage(p, Message{T: "place", X: 11, Y: 60, Z: 10, F: 1})
	foot := s.block(r, 11, 61, 10)
	if !isBed(foot) {
		t.Fatalf("bed foot not placed: %d", foot)
	}

	// 5. 右键床睡觉（单人模式全员睡着自动跳夜至黎明，并设置重生点）
	r.root().Time = 14000 // 夜晚
	s.survivalMessage(p, Message{T: "place", X: 11, Y: 61, Z: 10, F: 1})
	if r.root().Time != 24000 {
		t.Fatalf("expected world time to advance to dawn (24000) after sleeping, got %d", r.root().Time)
	}
	if !p.ext().SpawnSet || p.ext().SpawnX != 11 || p.ext().SpawnY != 61 || p.ext().SpawnZ != 10 {
		t.Fatalf("spawnpoint not set properly: %+v", p.ext())
	}
}

func TestBatch1Fluids(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 水遇岩浆反应 -> 生成黑曜石
	r.Edits[blockKey(5, 61, 5)] = blkWaterSource
	r.Edits[blockKey(5, 61, 6)] = blkLavaSource
	s.fluidReaction(r, 5, 61, 5)
	if b := s.block(r, 5, 61, 6); b != blkObsidian {
		t.Fatalf("expected obsidian at (5,61,6), got %d (%s)", b, s.fullName(b))
	}

	// 2. 空桶舀取水源
	p.state.Inv.Slots[0] = stack(itemBucket, 1, 0)
	p.state.Inv.Selected = 0
	r.Edits[blockKey(8, 61, 8)] = blkWaterSource
	s.survivalMessage(p, Message{T: "place", X: 8, Y: 61, Z: 8, F: 1})
	if s.block(r, 8, 61, 8) != 0 {
		t.Fatalf("water source was not removed by bucket")
	}
	if p.state.Inv.Slots[0].ID != itemWaterBucket {
		t.Fatalf("expected water bucket in inventory, got %d", p.state.Inv.Slots[0].ID)
	}

	// 3. 水桶放置水源
	s.survivalMessage(p, Message{T: "place", X: 8, Y: 60, Z: 8, F: 1})
	if s.block(r, 8, 61, 8) != blkWaterSource {
		t.Fatalf("water bucket failed to place water source")
	}
	if p.state.Inv.Slots[0].ID != itemBucket {
		t.Fatalf("expected empty bucket returned, got %d", p.state.Inv.Slots[0].ID)
	}

	// 4. 空桶挤奶
	mob, err := s.spawnMob(r, p, "cow", 12, 61, 12, "test")
	if err != nil || mob == nil {
		t.Fatalf("failed to spawn cow: %v", err)
	}
	s.survivalMessage(p, Message{T: "milk", E: mob.ID})
	if p.state.Inv.Slots[0].ID != itemMilkBucket {
		t.Fatalf("expected milk bucket after milking cow, got %d", p.state.Inv.Slots[0].ID)
	}
}

func TestBatch1GravityAndLeaves(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createBatch1TestWorld(s)

	// 1. 沙子重力坠落
	r.Edits[blockKey(10, 65, 10)] = blkSand
	s.checkFalling(r, 10, 65, 10, blkSand)
	if s.block(r, 10, 65, 10) != 0 {
		t.Fatalf("sand did not leave original position")
	}
	if s.block(r, 10, 61, 10) != blkSand {
		t.Fatalf("sand did not fall to floor (10,61,10), block=%d", s.block(r, 10, 61, 10))
	}

	// 2. 树叶附近原木检测
	r.Edits[blockKey(15, 61, 15)] = blkOakLeaves
	if s.hasLogNearby(r, 15, 61, 15) {
		t.Fatalf("isolated leaf should not have log nearby")
	}
	r.Edits[blockKey(15, 61, 16)] = blkOakLog
	if !s.hasLogNearby(r, 15, 61, 15) {
		t.Fatalf("leaf with adjacent log should have log nearby")
	}
}

func TestBatch1FireAndTNT(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 打火石放置火焰
	p.state.Inv.Slots[0] = stack(itemFlintAndSteel, 1, 0)
	p.state.Inv.Selected = 0
	r.Edits[blockKey(10, 60, 10)] = blkCobblestone
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1})
	if s.block(r, 10, 61, 10) != blkFlintFire {
		t.Fatalf("flint and steel failed to place fire, got %d", s.block(r, 10, 61, 10))
	}

	// 2. 挖掘动作扑灭火焰
	s.survivalMessage(p, Message{T: "dig", S: 0, X: 10, Y: 60, Z: 10, F: 1})
	if s.block(r, 10, 61, 10) != 0 {
		t.Fatalf("fire was not extinguished by dig start")
	}

	// 3. 打火石点燃 TNT
	r.Edits[blockKey(12, 61, 12)] = blkExplosiveTNT
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 12, F: 1})
	key := blockKey(12, 61, 12)
	if r.ext().Blocks[key] == nil || r.ext().Blocks[key].Fuse <= 0 {
		t.Fatalf("TNT was not ignited with fuse")
	}

	// 4. 模拟爆炸破坏周围方块
	r.Edits[blockKey(11, 61, 12)] = blkDirt
	s.explode(r, 12.5, 61.5, 12.5, 3.0, true)
	if s.block(r, 11, 61, 12) != 0 {
		t.Fatalf("expected dirt block next to TNT to be destroyed by explosion")
	}
}

func TestBatch1Animals(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 剪羊毛
	sheep, err := s.spawnMob(r, p, "sheep", 10, 61, 10, "test")
	if err != nil || sheep == nil {
		t.Fatalf("failed to spawn sheep: %v", err)
	}
	p.state.Inv.Slots[0] = stack(304, 1, 0) // Shears
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "animal", E: sheep.ID})
	if sheep.Flags&mobFlagSheared == 0 {
		t.Fatalf("expected sheep to have sheared flag")
	}

	// 2. 喂牛繁殖
	cow, err := s.spawnMob(r, p, "cow", 15, 61, 15, "test")
	if err != nil || cow == nil {
		t.Fatalf("failed to spawn cow: %v", err)
	}
	p.state.Inv.Slots[0] = stack(itemWheat, 2, 0)
	s.survivalMessage(p, Message{T: "animal", E: cow.ID})
	if p.state.Inv.Slots[0].Count != 1 {
		t.Fatalf("expected wheat count to be 1, got %d", p.state.Inv.Slots[0].Count)
	}
}

func TestBatch1DropMerge(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createBatch1TestWorld(s)

	d1 := testDrop(t, s, r, itemWheat, 5, 10.0, 61.0, 10.0)
	d2 := testDrop(t, s, r, itemWheat, 3, 10.2, 61.0, 10.1)

	s.dropsTick(r)

	// 检查合并后的结果：其中一个增加到 8，另一个被移除
	remaining := 0
	totalCount := 0
	for _, d := range r.Drops {
		if d.Stack.ID == itemWheat {
			remaining++
			totalCount += d.Stack.Count
		}
	}
	if remaining != 1 || totalCount != 8 {
		t.Fatalf("drops did not merge properly: remaining drops=%d, total count=%d (d1.Count=%d, d2 in drops=%v)", remaining, totalCount, d1.Stack.Count, r.Drops[d2.ID] != nil)
	}
}

func TestFluidScheduledSpread(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 放置水源
	p.state.Inv.Slots[0] = stack(itemWaterBucket, 1, 0)
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1}) // 点击地面顶部放置在 (10, 61, 10)

	if b := s.block(r, 10, 61, 10); b != blkWaterSource {
		t.Fatalf("expected water source at (10,61,10), got %d (%s)", b, s.fullName(b))
	}

	// 放置初期：相邻四周必须仍然为空气（验证不会瞬间刷满扩散）
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, nz := 10+d[0], 10+d[1]
		if b := s.block(r, nx, 61, nz); b != 0 {
			t.Fatalf("fluid spread too quickly at T=0: expected 0 at (%d,61,%d), got %d", nx, nz, b)
		}
	}

	// 2. 推进第 1 个 tick (100ms) - 水的调度延迟为 2 ticks，此时周围仍应为 0
	for _, fn := range roomTickHooks {
		fn(s, r)
	}
	if b := s.block(r, 11, 61, 10); b != 0 {
		t.Fatalf("fluid spread before scheduled tick: expected 0 at (11,61,10), got %d", b)
	}

	// 3. 推进第 2 个 tick (200ms) - 水流第 1 层扩散就绪
	for _, fn := range roomTickHooks {
		fn(s, r)
	}
	if b := s.block(r, 11, 61, 10); b != blkWaterFlowMin {
		t.Fatalf("expected water flow level 1 at (11,61,10) after 2 ticks, got %d (%s)", b, s.fullName(b))
	}
	// 更外圈 (12, 61, 10) 仍应为空气
	if b := s.block(r, 12, 61, 10); b != 0 {
		t.Fatalf("outer ring should still be empty after 2 ticks, got %d at (12,61,10)", b)
	}

	// 4. 推进第 4 个 tick (400ms) - 更外圈扩散就绪
	for i := 0; i < 2; i++ {
		for _, fn := range roomTickHooks {
			fn(s, r)
		}
	}
	if b := s.block(r, 12, 61, 10); b != blkWaterFlowMin+1 {
		t.Fatalf("expected water flow level 2 at (12,61,10) after 4 ticks, got %d (%s)", b, s.fullName(b))
	}

	// 5. 舀走水源，验证退潮消散
	s.setBlock(r, 10, 61, 10, 0, 0)
	for _, d := range faceOffsets {
		s.scheduleFluid(r, 10+d[0], 61+d[1], 10+d[2])
	}

	// 推进数个 tick 观察逐步退潮
	for i := 0; i < 10; i++ {
		for _, fn := range roomTickHooks {
			fn(s, r)
		}
	}
	// 第 1 圈的水流应退散
	if b := s.block(r, 11, 61, 10); b != 0 {
		t.Fatalf("expected fluid to recede to 0 at (11,61,10), got %d", b)
	}
}

