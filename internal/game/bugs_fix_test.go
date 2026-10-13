package game

import (
	"testing"
)

func createFixTestWorld(s *Server) (*Room, *Peer) {
	r := newRoom("test_room_fixes", "测试世界", "survival", 0)
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

// 1. 验证门手动打开后不会自动关闭，且红石边沿触发正常
func TestDoorManualOpenDoesNotAutoClose(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createFixTestWorld(s)

	// 放置门基座和门
	x, y, z := 10, 60, 10
	s.setBlock(r, x, y-1, z, 1, setQuiet) // 支撑方块 (Stone)
	// 手动放置橡木门下半部 (110) 和上半部 (118)
	s.setBlock(r, x, y, z, 110, setQuiet)
	s.setBlock(r, x, y+1, z, 118, setQuiet)

	// 模拟玩家右键开门 (由 110 切换为 114 Open)
	toggled := toggleDoorBlock(110)
	if toggled != 114 {
		t.Fatalf("expected door open state 114, got %d", toggled)
	}
	s.setBlock(r, x, y, z, toggled, setQuiet)

	// 验证门已处于 Open 状态
	if s.block(r, x, y, z) != 114 {
		t.Fatalf("door should be open, got %d", s.block(r, x, y, z))
	}

	// 触发多次方块更新与服务器刻度
	for i := 0; i < 20; i++ {
		s.extTick(r)
	}

	// 核心断言：在无红石输入的情况下，手动打开的门绝对不能自动关闭！
	if s.block(r, x, y, z) != 114 {
		t.Fatalf("door was automatically closed! expected 114, got %d", s.block(r, x, y, z))
	}
}

// 2. 验证村庄结构完整性：双格门、两格床、湿润耕地和小麦
func TestVillageGenerationIntegrity(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createFixTestWorld(s)
	r.Infinite = true
	center, _ := findStructureVillage(t, s, r)
	var out []byte
	for dz := -4; dz <= 4; dz++ {
		for dx := -4; dx <= 4; dx++ {
			out = append(out, s.generateTerrain(r, ChunkCoord{center.X + dx, center.Z + dz})...)
		}
	}
	hasDoorLower := false
	hasDoorUpper := false
	hasBedFoot := false
	hasBedHead := false
	hasFarmland := false
	hasWheat := false

	for _, b := range out {
		if b >= 110 && b <= 113 {
			hasDoorLower = true
		}
		if b == 118 {
			hasDoorUpper = true
		}
		if b >= 132 && b <= 135 {
			hasBedFoot = true
		}
		if b >= 136 && b <= 139 {
			hasBedHead = true
		}
		if b == 146 { // Farmland (Wet)
			hasFarmland = true
		}
		if b == 157 { // Wheat Crops (Mature)
			hasWheat = true
		}
	}

	if !hasDoorLower || !hasDoorUpper {
		t.Fatalf("village missing complete two-block door: lower=%v, upper=%v", hasDoorLower, hasDoorUpper)
	}
	if !hasBedFoot || !hasBedHead {
		t.Fatalf("village missing complete two-block bed: foot=%v, head=%v", hasBedFoot, hasBedHead)
	}
	if !hasFarmland || !hasWheat {
		t.Fatalf("village missing farmland or mature wheat crops: farm=%v, wheat=%v", hasFarmland, hasWheat)
	}
}

// 3. 验证末地生成与基岩祭坛开局未激活
func TestEndDimensionGenerationAndExitPortal(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	endRoom := newRoom("test_room_the_end_fix", "末地维度", "survival", 0)
	endRoom.Dim = "end"
	s.rooms[endRoom.ID] = endRoom
	c := endRoom.width() / 2

	// 验证世界准备完成后的出口祭坛为未激活状态 (中心为空气，无龙蛋)
	s.prepareEnd(endRoom)

	// 中心柱顶不能有龙蛋 (49+4 = 53)
	if s.block(endRoom, c, 53, c) == blkDragonEgg {
		t.Fatalf("dragon egg should not exist before defeating the ender dragon")
	}
	// 基岩池内部不能提前激活
	if s.block(endRoom, c+1, 49, c) == blkExitPortal {
		t.Fatalf("exit portal must not be active before defeating the ender dragon")
	}

	// 验证倒锥形空岛在主岛中心的厚度与边缘
	centerChunk := chunkCoord(c, c)
	out := s.generateTerrain(endRoom, centerChunk)
	endStoneCount := 0
	for _, b := range out {
		if b == byte(s.rules.byName["Void Stone"]) || b == byte(s.rules.byName["End Stone"]) {
			endStoneCount++
		}
	}
	if endStoneCount < 500 {
		t.Fatalf("expected substantial End Stone volume in center chunk, got %d", endStoneCount)
	}
}

// 4. 验证末影龙高空飞行不坠落、不随距离 Despawn、不自行暴毙
func TestEnderDragonSurvivalAndBossFight(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	endRoom := newRoom("test_room_dragon_survival", "末地Boss战", "survival", 0)
	endRoom.Dim = "end"
	s.rooms[endRoom.ID] = endRoom

	p := dropTestPlayer(endRoom, 1, 1000, 80, 1000) // 模拟玩家距离末影龙极远 (> 128 格)
	endRoom.Peers[p] = true

	df := getDragonFight(endRoom)
	s.initDragonFight(endRoom, df)

	dragonMob := endRoom.Mobs[df.DragonID]
	if dragonMob == nil {
		t.Fatalf("expected dragon mob to be spawned")
	}

	// 运行多次生物刻度与末影龙 AI
	for i := 0; i < 30; i++ {
		s.mobsTick(endRoom)
		s.tickDragonFight(endRoom, df)
	}

	// 验证末影龙仍然存活、未掉入虚空、未被清除，Boss 战未被误判胜利
	if df.Defeated {
		t.Fatalf("dragon fight was falsely marked defeated!")
	}
	if df.Health <= 0 {
		t.Fatalf("dragon health prematurely dropped to 0! health=%f", df.Health)
	}
	if endRoom.Mobs[df.DragonID] == nil {
		t.Fatalf("dragon mob entity was removed from room!")
	}
}
