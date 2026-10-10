package game

import (
	"testing"
)

// 场景 A: 先有红石电源，再放活塞
func TestPistonPlacedNextToExistingPower(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// 先在 (9, 61, 10) 放置红石块 (ID 40)
	s.setBlock(r, 9, 61, 10, 40, 0)
	s.extTick(r)

	// 玩家手持活塞 (item 358)
	p.state.Inv.Slots[0] = &Stack{ID: 358, Count: 64}
	p.state.Inv.Selected = 0

	// 放置活塞在 (10, 61, 10)，点击 (9, 61, 10) 的东面 (F=5)
	placed := s.handleRedstoneUse(p, Message{T: "place", X: 9, Y: 61, Z: 10, F: 5}, nil, false)
	t.Logf("handleRedstoneUse returned: %v", placed)
	s.extTick(r)

	pKey := blockKey(10, 61, 10)
	snap := redstoneSnapshot(r, pKey)
	if snap == nil {
		t.Fatalf("Piston was not placed at (10, 61, 10)! Block is: %v", s.block(r, 10, 61, 10))
	}
	t.Logf("Placed piston: Extended=%v, Facing=%v", snap.Extended, snap.Facing)
	if !snap.Extended {
		t.Errorf("FAIL Scene A: Piston placed next to existing power source should be triggered immediately!")
	}
}

// 场景 B: 红石火把给红石线供电 -> 红石线触发活塞
func TestPistonTriggeredByWireViaTorch(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// 放置活塞在 (10, 61, 10)，点击地面 (10, 60, 10) 的顶面 (F=1)
	p.state.Inv.Slots[0] = &Stack{ID: 358, Count: 64}
	p.state.Inv.Selected = 0
	s.handleRedstoneUse(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1}, nil, false)

	// 放置红石线在 (9, 61, 10)，点击地面 (9, 60, 10) 的顶面 (F=1)
	p.state.Inv.Slots[0] = &Stack{ID: 320, Count: 64} // Spark Dust (redstone wire)
	s.handleRedstoneUse(p, Message{T: "place", X: 9, Y: 60, Z: 10, F: 1}, nil, false)
	s.extTick(r)

	pKey := blockKey(10, 61, 10)
	pSnap := redstoneSnapshot(r, pKey)
	if pSnap == nil {
		t.Fatalf("Piston was not placed!")
	}
	t.Logf("Before torch: piston extended=%v", pSnap.Extended)

	// 放置红石火把在 (8, 61, 10)，点击地面 (8, 60, 10) 的顶面 (F=1) 给红石线供电
	p.state.Inv.Slots[0] = &Stack{ID: 355, Count: 64} // Redstone Torch
	s.handleRedstoneUse(p, Message{T: "place", X: 8, Y: 60, Z: 10, F: 1}, nil, false)
	s.extTick(r)

	wKey := blockKey(9, 61, 10)
	wSnap := redstoneSnapshot(r, wKey)
	pSnap = redstoneSnapshot(r, pKey)
	if wSnap == nil {
		t.Fatalf("Wire was not placed or snapshot nil!")
	}
	t.Logf("After torch: wire power=%v, piston extended=%v", wSnap.Power, pSnap.Extended)
	if !pSnap.Extended {
		t.Errorf("FAIL Scene B: Piston should be extended by powered wire!")
	}
}

// 场景 C: 红石火把直接放在活塞旁边
func TestPistonTriggeredByAdjacentTorch(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// 放活塞在 (10, 61, 10)
	p.state.Inv.Slots[0] = &Stack{ID: 358, Count: 64}
	p.state.Inv.Selected = 0
	s.handleRedstoneUse(p, Message{T: "place", X: 10, Y: 60, Z: 10, F: 1}, nil, false)

	// 放红石火把在 (9, 61, 10)
	p.state.Inv.Slots[0] = &Stack{ID: 355, Count: 64} // Redstone Torch
	s.handleRedstoneUse(p, Message{T: "place", X: 9, Y: 60, Z: 10, F: 1}, nil, false)
	s.extTick(r)

	pKey := blockKey(10, 61, 10)
	pSnap := redstoneSnapshot(r, pKey)
	if pSnap == nil {
		t.Fatalf("Piston was not placed!")
	}
	t.Logf("After adjacent torch: piston extended=%v", pSnap.Extended)
	if !pSnap.Extended {
		t.Errorf("FAIL Scene C: Piston should be extended by adjacent torch!")
	}

	// 破坏火把 (9, 61, 10)
	s.setBlock(r, 9, 61, 10, 0, 0)
	s.extTick(r)

	pSnapRetract := redstoneSnapshot(r, pKey)
	t.Logf("After torch broken: piston extended=%v", pSnapRetract.Extended)
	if pSnapRetract.Extended {
		t.Errorf("FAIL Scene C: Piston should be retracted when power source is broken!")
	}
}

// 场景 E: 活塞推石头方块，并验证方块位移和广播消息
func TestPistonPushBlockAndBroadcast(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// 放置活塞在 (10, 61, 10)，朝向设为东 Facing=5 (+X)
	s.setBlock(r, 10, 61, 10, 168, setQuiet)
	storeRedstoneComponent(r, blockKey(10, 61, 10), &RedstoneComponent{Kind: "piston", Facing: 5})

	// 前面 (11, 61, 10) 放置一块石头
	s.setBlock(r, 11, 61, 10, blkStone, setQuiet)
	s.setBlock(r, 12, 61, 10, 0, setQuiet)

	// 清空客户端消息队列，准备捕获推送事件
	for len(p.send) > 0 {
		<-p.send
	}

	// 在活塞背面 (9, 61, 10) 放置红石火把供电
	p.state.Inv.Slots[0] = &Stack{ID: 355, Count: 64}
	p.state.Inv.Selected = 0
	s.handleRedstoneUse(p, Message{T: "place", X: 9, Y: 60, Z: 10, F: 1}, nil, false)
	s.extTick(r)

	pSnap := redstoneSnapshot(r, blockKey(10, 61, 10))
	if !pSnap.Extended {
		t.Fatalf("Piston failed to extend!")
	}

	// 检查石头是否被推到了 (12, 61, 10)
	if s.block(r, 12, 61, 10) != blkStone {
		t.Errorf("Stone was not pushed to (12, 61, 10), got %v", s.block(r, 12, 61, 10))
	}
	// 检查 (11, 61, 10)
	t.Logf("Block at (11, 61, 10) is: %v", s.block(r, 11, 61, 10))

	// 检查给玩家发送的消息
	for len(p.send) > 0 {
		m := <-p.send
		t.Logf("Client received packet: %s", string(m.body))
	}
}

// 场景 F: 红石粉铺在石头上方，石头侧面紧贴活塞 -> 石头充能激活活塞
func TestPistonTriggerByBlockPowering(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// (10, 61, 10) 活塞
	s.setBlock(r, 10, 61, 10, 168, setQuiet)
	storeRedstoneComponent(r, blockKey(10, 61, 10), &RedstoneComponent{Kind: "piston", Facing: 5})

	// (9, 61, 10) 石头
	s.setBlock(r, 9, 61, 10, blkStone, setQuiet)

	// (9, 62, 10) 红石粉 (铺在石头顶上)
	s.setBlock(r, 9, 62, 10, 108, setQuiet)
	storeRedstoneComponent(r, blockKey(9, 62, 10), &RedstoneComponent{Kind: "wire", Power: 15})

	// 触发方块更新或检查致动器
	s.checkActuators(r, 10, 61, 10)

	pSnap := redstoneSnapshot(r, blockKey(10, 61, 10))
	t.Logf("Piston powered by adjacent stone with wire above: Extended=%v", pSnap.Extended)
	if !pSnap.Extended {
		t.Errorf("FAIL Scene F: Piston should be triggered by powered solid block!")
	}
}

// 场景 G: 活塞准连接性 (Quasi-Connectivity) - 活塞斜上方有电源时被激活
func TestPistonQuasiConnectivity(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := createAdvancedTestWorld(s)
	p.state.X, p.state.Y, p.state.Z = 10.5, 61.0, 7.0

	// (10, 61, 10) 活塞
	s.setBlock(r, 10, 61, 10, 168, setQuiet)
	storeRedstoneComponent(r, blockKey(10, 61, 10), &RedstoneComponent{Kind: "piston", Facing: 5})

	// 活塞斜上方 (9, 62, 10) 放置红石块 (ID 40)
	s.setBlock(r, 9, 62, 10, 40, 0)
	s.extTick(r)

	pSnap := redstoneSnapshot(r, blockKey(10, 61, 10))
	t.Logf("Piston with power at quasi-connected position (9, 62, 10): Extended=%v", pSnap.Extended)
	if !pSnap.Extended {
		t.Errorf("FAIL Scene G: Piston should be triggered by quasi-connectivity (QC)!")
	}
}
