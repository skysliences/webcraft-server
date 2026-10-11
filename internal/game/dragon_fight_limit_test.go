package game

import (
	"encoding/json"
	"testing"
)

func TestDragonSingleInstanceAndDeduplication(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	// 创建 end 维度测试 Room
	r := newRoom("test_room_the_end", "末地维度", "survival", 0)
	r.Dim = "end"
	df := getDragonFight(r)

	// 模拟之前异常生成的 3 条末影龙
	m1, err1 := s.spawnMob(r, nil, "ender_dragon", 64, 80, 64, "dragon_spawn")
	m2, err2 := s.spawnMob(r, nil, "ender_dragon", 70, 80, 70, "dragon_spawn")
	m3, err3 := s.spawnMob(r, nil, "ender_dragon", 80, 80, 80, "dragon_spawn")
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("failed to spawn initial test dragons: %v %v %v", err1, err2, err3)
	}
	m1.Health = 50
	m2.Health = 200
	m3.Health = 200

	// 运行 initDragonFight 与 tickDragonFight
	s.initDragonFight(r, df)
	s.tickDragonFight(r, df)

	// 统计末地房间中的末影龙数量，必须严格为 1
	dragonCount := 0
	for _, m := range r.Mobs {
		if m != nil && m.Type == "ender_dragon" {
			dragonCount++
		}
	}
	if dragonCount != 1 {
		t.Fatalf("expected exactly 1 dragon after deduplication, got %d", dragonCount)
	}
	if df.DragonID != m1.ID {
		t.Fatalf("expected active dragon to adopt m1, got %d", df.DragonID)
	}
	if df.Health != 50 {
		t.Fatalf("expected adopted dragon health to be 50, got %f", df.Health)
	}
}

func TestDragonLethalDamageDyingSequenceAndEgg(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r := newRoom("test_room_the_end_dying", "末地维度", "survival", 0)
	r.Dim = "end"
	df := getDragonFight(r)
	s.initDragonFight(r, df)

	dragonMob := r.Mobs[df.DragonID]
	if dragonMob == nil {
		t.Fatalf("expected dragon mob to be initialized")
	}

	// 造成致命伤害 (200 点伤害)
	s.damageMob(r, nil, dragonMob, 200, "attack")

	// 致命打击后，血量必须归零，并进入 dying 状态，不能直接被 delete(r.Mobs)，更不能以残血复活
	if df.Health != 0 {
		t.Fatalf("expected df.Health == 0 after lethal hit, got %f", df.Health)
	}
	if dragonMob.Health != 0 {
		t.Fatalf("expected dragonMob.Health == 0 after lethal hit, got %f", dragonMob.Health)
	}
	if df.DyingTicks != 1 {
		t.Fatalf("expected df.DyingTicks == 1, got %d", df.DyingTicks)
	}
	if r.Mobs[df.DragonID] == nil {
		t.Fatalf("dragon should still be present in r.Mobs during 120-tick dying sequence")
	}

	// 模拟执行 120 ticks 死亡流程
	for i := 0; i < 125; i++ {
		s.tickDragonFight(r, df)
	}

	// 120 ticks 结束后，末影龙必须被标记 Defeated，实体被清理，且中心生成龙蛋与出口传送门
	if !df.Defeated {
		t.Fatalf("expected df.Defeated == true after 120 ticks")
	}
	if r.Mobs[df.DragonID] != nil {
		t.Fatalf("expected dragon mob to be deleted from r.Mobs after dying sequence completed")
	}

	// 检查中心出口传送门顶部是否生成了龙蛋 (210)
	centerX := r.width() / 2
	centerZ := r.width() / 2
	eggBlock := s.block(r, centerX, 53, centerZ)
	if eggBlock != 210 {
		t.Fatalf("expected dragon egg (210) at center portal top, got block %d", eggBlock)
	}

	// 检查是否生成了折跃门 (blkPortalX)
	gatewayBlock := s.block(r, centerX+35, 75, centerZ)
	if gatewayBlock != blkPortalX {
		t.Fatalf("expected end gateway (blkPortalX) to be spawned, got block %d", gatewayBlock)
	}

	// 验证击败后，后续 tick 绝对不会重新生成任何末影龙
	s.tickDragonFight(r, df)
	for _, m := range r.Mobs {
		if m != nil && m.Type == "ender_dragon" {
			t.Fatalf("no dragon should spawn after df.Defeated == true")
		}
	}
}

func TestPlayerLeavingEndDismissesBossbar(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _, guest := dimensionTestWorld(s)
	end := s.dimRoom(r, "end")
	nether := s.dimRoom(r, "nether")

	// 玩家先进入末地
	if !s.moveToDimension(guest, end, 612.5, 49, 512.5, true) {
		t.Fatalf("failed to move player to end")
	}

	// 清空历史收包
drain:
	for {
		select {
		case <-guest.send:
		default:
			break drain
		}
	}

	// 传送离开末地到下界
	ok := s.moveToDimension(guest, nether, 64, 60, 64, true)
	if !ok {
		t.Fatalf("failed to move player to nether")
	}

	// 验证玩家收到 bossbar 隐藏包
	foundHidePacket := false
readPackets:
	for {
		select {
		case pkt := <-guest.send:
			var m map[string]any
			if err := json.Unmarshal(pkt.body, &m); err == nil {
				if m["t"] == "bossbar" && m["visible"] == false {
					foundHidePacket = true
					break readPackets
				}
			}
		default:
			break readPackets
		}
	}
	if !foundHidePacket {
		t.Fatalf("expected player leaving end dimension to receive {t: bossbar, visible: false}")
	}
}

