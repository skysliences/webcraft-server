package game

import (
	"testing"
)

func createTransportationTestWorld(s *Server) (*Room, *Peer) {
	r := newRoom("test_room_transport", "交通测试世界", "survival", 0)
	for x := 0; x < 32; x++ {
		for z := 0; z < 32; z++ {
			r.Edits[blockKey(x, 60, z)] = blkDirt
			r.Edits[blockKey(x, 61, z)] = 0
			r.Edits[blockKey(x, 62, z)] = 0
		}
	}
	p := dropTestPlayer(r, 1, 10.5, 61, 10.5)
	r.Peers[p] = true
	return r, p
}

func TestRailsAndMinecart(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)
	vs := getVehicles(r)

	// 1. 放置普通铁轨与充能铁轨
	s.setBlock(r, 10, 61, 10, blkRail, 0)
	s.setBlock(r, 11, 61, 10, blkPoweredRail, 0)
	s.setBlock(r, 12, 61, 10, blkDetectorRail, 0)

	// 2. 玩家手持矿车右键放置
	p.state.Inv.Slots[0] = stack(itemMinecart, 1, 0)
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 61, Z: 10, F: 1})

	if len(vs.Minecarts) != 1 {
		t.Fatalf("expected 1 minecart spawned on rail, got %d", len(vs.Minecarts))
	}
	var cart *MinecartEntity
	for _, c := range vs.Minecarts {
		cart = c
		break
	}
	if cart.X < 10.0 || cart.X > 11.0 || cart.Z < 10.0 || cart.Z > 11.0 {
		t.Fatalf("unexpected minecart position: (%f, %f, %f)", cart.X, cart.Y, cart.Z)
	}

	// 3. 玩家右键骑乘矿车
	s.survivalMessage(p, Message{T: "interact_entity", E: cart.ID})
	if cart.RiderID != p.id {
		t.Fatalf("expected player %d to ride minecart, got %d", p.id, cart.RiderID)
	}

	// 4. 矿车在充能铁轨上滑行
	cart.X = 11.5
	cart.Z = 10.5
	cart.VX = 0.1
	// 供电充能铁轨
	s.setBlock(r, 11, 60, 10, 40, 0) // 红石块供电
	r.rt().redstoneDirty = true
	s.recalculateWires(r)

	for _, fn := range roomTickHooks {
		fn(s, r)
	}
	if cart.VX <= 0.1 {
		t.Fatalf("expected minecart to accelerate on powered rail, got VX=%f", cart.VX)
	}
	// 玩家坐标同步
	if p.state.X != cart.X {
		t.Fatalf("player coordinates not synced with ridden minecart")
	}

	// 5. 下车
	s.survivalMessage(p, Message{T: "interact_entity", E: cart.ID})
	if cart.RiderID != 0 {
		t.Fatalf("expected player to dismount, rider is %d", cart.RiderID)
	}
}

func TestBoatPlacementAndWater(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)
	vs := getVehicles(r)

	// 挖坑放水 (在玩家可触及范围内, 如 (11, 60, 10))
	s.setBlock(r, 11, 60, 10, blkWaterSource, 0)

	// 放置木船
	p.state.Inv.Slots[0] = stack(itemBoat, 1, 0)
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "place", X: 11, Y: 60, Z: 10, F: 1})

	if len(vs.Boats) != 1 {
		t.Fatalf("expected 1 boat spawned, got %d", len(vs.Boats))
	}

	// 推进时钟验证水面浮力
	for _, fn := range roomTickHooks {
		fn(s, r)
	}
	var boat *BoatEntity
	for _, b := range vs.Boats {
		boat = b
		break
	}
	if boat.Y < 60.5 {
		t.Fatalf("expected boat to float above water, got Y=%f", boat.Y)
	}
}

func TestFishingSystem(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)
	vs := getVehicles(r)

	// 水池
	s.setBlock(r, 11, 60, 10, blkWaterSource, 0)

	// 1. 手持鱼竿右键抛出浮标
	p.state.Inv.Slots[0] = stack(itemFishingRod, 1, 0)
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "use", S: 0})

	bobber := vs.Bobbers[p.id]
	if bobber == nil {
		t.Fatalf("expected fishing bobber to be spawned for player")
	}

	// 2. 模拟落水与咬钩
	bobber.X = 11.5
	bobber.Y = 60.8
	bobber.Z = 10.5
	bobber.WaitTicks = 1
	for _, fn := range roomTickHooks {
		fn(s, r)
	}
	if bobber.BiteTicks <= 0 {
		t.Fatalf("expected bobber to enter bite stage after wait ticks expired, got %d", bobber.BiteTicks)
	}

	// 3. 及时提竿收线
	s.survivalMessage(p, Message{T: "use", S: 0})
	if vs.Bobbers[p.id] != nil {
		t.Fatalf("expected bobber to be removed after retrieval")
	}
	// 验证战利品掉落产生
	if len(r.Drops) == 0 {
		t.Fatalf("expected fishing loot drops to be created")
	}
	// 验证经验获得
	if p.ext().XPTotal <= 0 {
		t.Fatalf("expected player to gain XP from fishing, got %d", p.ext().XPTotal)
	}
}

func TestComposterAndCauldron(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createTransportationTestWorld(s)
	vs := getVehicles(r)

	// 1. 堆肥桶投入小麦
	s.setBlock(r, 10, 61, 10, blkComposter, 0)
	p.state.Inv.Slots[0] = stack(itemWheat, 16, 0)
	p.state.Inv.Selected = 0

	increased := false
	for i := 0; i < 20; i++ {
		s.survivalMessage(p, Message{T: "place", X: 10, Y: 61, Z: 10, F: 1})
		k := blockKey(10, 61, 10)
		if vs.Composters[k] > 0 {
			increased = true
			break
		}
	}
	if !increased {
		t.Fatalf("expected composter level to increase")
	}
	// 满级收获骨粉
	k := blockKey(10, 61, 10)
	full := mechanicSnapshot(r, k)
	full.Level = 7
	storeMechanic(r, k, full)
	s.survivalMessage(p, Message{T: "place", X: 10, Y: 61, Z: 10, F: 1})
	if vs.Composters[k] != 0 {
		t.Fatalf("expected composter to be harvested and reset to 0, got %d", vs.Composters[k])
	}

	// 2. 炼药锅水桶交互
	s.setBlock(r, 12, 61, 10, blkCauldron, 0)
	p.state.Inv.Slots[0] = stack(itemWaterBucket, 1, 0)
	s.survivalMessage(p, Message{T: "place", X: 12, Y: 61, Z: 10, F: 1})
	if p.state.Inv.Slots[0].ID != itemBucket {
		t.Fatalf("expected water bucket to be emptied into cauldron, got %d", p.state.Inv.Slots[0].ID)
	}
}

func TestDispenserAndObserver(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createTransportationTestWorld(s)

	// 1. 发射器装填实体箭并红石激活
	s.setBlock(r, 10, 61, 10, blkDispenser, 0)
	dk := blockKey(10, 61, 10)
	r.Containers[dk] = &Container{
		Kind:  "dispenser",
		Slots: make(Stacks, 9),
	}
	r.Containers[dk].Slots[0] = stack(itemArrow, 5, 0)

	// 模拟红石供电
	s.setBlock(r, 10, 60, 10, 40, 0) // 底下红石块供电
	r.rt().redstoneDirty = true
	s.recalculateWires(r)

	s.tickMechanics(r)

	if len(r.rt().projectiles) != 1 {
		t.Fatalf("expected 1 arrow projectile shot from dispenser, got %d", len(r.rt().projectiles))
	}
	if r.Containers[dk].Slots[0].Count != 4 {
		t.Fatalf("expected 4 arrows remaining in dispenser, got %d", r.Containers[dk].Slots[0].Count)
	}

	// 2. 侦测器更新检测
	s.setBlock(r, 15, 61, 15, blkObserver, 0)
	// 相邻方块更新
	s.setBlock(r, 15, 61, 16, 1, 0)
	for _, fn := range blockUpdateHooks {
		fn(s, r, 15, 61, 16, 1)
	}
	s.tickMechanics(r)
	vs := getVehicles(r)
	ok := blockKey(15, 61, 15)
	if vs.ObserverPulses[ok] <= 0 {
		t.Fatalf("expected observer pulse to be triggered on neighbor block update")
	}
}
