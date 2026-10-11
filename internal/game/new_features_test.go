package game

import (
	api "mcraft/backend/pkg/plugin"
	"testing"
)

func createAdvancedTestWorld(s *Server) (*Room, *Peer) {
	r := newRoom("test_room_advanced", "测试世界", "survival", 0)
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

// 1. 测试副手切换与盾牌格挡
func TestShieldBlockAndOffhand(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	_, p := createAdvancedTestWorld(s)

	// 准备主手物品与副手盾牌
	diamondSword := stack(s.rules.byName["Diamond Sword"], 1, 0)
	shield := stack(s.rules.byName["Shield"], 1, 0)
	p.state.Inv.Slots[p.state.Inv.Selected] = diamondSword
	p.state.Inv.Slots[40] = shield

	// 1. 测试 F 键主副手原子交换
	s.message(p, Message{T: "swap_offhand"})
	if p.state.Inv.Slots[p.state.Inv.Selected] != shield || p.state.Inv.Slots[40] != diamondSword {
		t.Fatalf("swap_offhand failed: mainhand=%v offhand=%v",
			p.state.Inv.Slots[p.state.Inv.Selected], p.state.Inv.Slots[40])
	}

	// 换回盾牌到副手
	s.message(p, Message{T: "swap_offhand"})

	// 2. 测试举盾格挡检测
	p.state.Yaw = 0 // 面向 Z 正方向
	peerBlocking[p] = true

	// 来自正前方 (0, 0, 5) 的攻击，在正面 180° 扇面内
	ctxFront := &api.DamageContext{
		Cause: "mob_attack",
		Attacker: &api.Combatant{
			X: p.state.X,
			Y: p.state.Y,
			Z: p.state.Z + 5,
		},
		RawAmount: 10,
		Amount:    10,
	}
	if !s.isShieldBlocking(p, ctxFront) {
		t.Fatalf("expected shield to block attack from front")
	}

	// 来自正后方 (0, 0, -5) 的攻击，不在格挡范围内
	ctxBack := &api.DamageContext{
		Cause: "mob_attack",
		Attacker: &api.Combatant{
			X: p.state.X,
			Y: p.state.Y,
			Z: p.state.Z - 5,
		},
		RawAmount: 10,
		Amount:    10,
	}
	if s.isShieldBlocking(p, ctxBack) {
		t.Fatalf("expected shield NOT to block attack from behind")
	}

	// 3. 测试盾牌承受伤害与耐久扣减
	shieldItem := p.state.Inv.Slots[40]
	s.applyShieldBlock(p, ctxFront)
	if shieldItem.Damage <= 0 {
		t.Fatalf("expected shield durability to decrease, got damage=%d", shieldItem.Damage)
	}
}

// 2. 测试红石活塞与漏斗
func TestRedstonePistonAndHopper(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createAdvancedTestWorld(s)

	// A. 测试活塞推动方块
	// (10, 61, 10) 为活塞，面向 Z 正方向 (0, 0, 1)，(10, 61, 11) 为石头
	s.setBlock(r, 10, 61, 10, 168, setQuiet)
	s.setBlock(r, 10, 61, 11, blkStone, setQuiet)
	s.setBlock(r, 10, 61, 12, 0, setQuiet)

	s.pushPiston(r, 10, 61, 10, [3]int{0, 0, 1}, false)

	// 推动后，石头应该被推到 (10, 61, 12)
	if s.block(r, 10, 61, 12) != blkStone {
		t.Fatalf("expected stone to be pushed to (10, 61, 12), got block=%d", s.block(r, 10, 61, 12))
	}

	// B. 测试粘性活塞拉回方块
	s.pullPiston(r, 10, 61, 10, [3]int{0, 0, 1})
	if s.block(r, 10, 61, 11) != blkStone {
		t.Fatalf("expected stone to be pulled back to (10, 61, 11), got block=%d", s.block(r, 10, 61, 11))
	}

	// C. 测试漏斗传输物品
	topChest := &Container{Kind: "chest", Slots: make(Stacks, 27)}
	topChest.Slots[0] = stack(s.rules.byName["Iron Ingot"], 5, 0)
	r.Containers[blockKey(10, 62, 10)] = topChest

	hopper := &Container{Kind: "hopper", Slots: make(Stacks, 5)}
	r.Containers[blockKey(10, 61, 10)] = hopper

	r.Edits[blockKey(10, 62, 10)] = s.id("Chest")
	r.Edits[blockKey(10, 61, 10)] = blkHopper
	transferred := s.moveHopperItem(r, blockKey(10, 61, 10), blockKey(10, 62, 10), blockKey(10, 61, 10), 0, 1, "hopper_pull")
	if !transferred {
		t.Fatalf("expected hopper transfer: chest=%d:%s hopper=%d:%s item=%+v valid=%v", s.id("Chest"), s.nameOf(s.id("Chest")), blkHopper, s.nameOf(blkHopper), topChest.Slots[0], s.validStack(topChest.Slots[0], 64))
	}

	// 验证漏斗从上方箱子抽取了 1 个铁锭
	if hopper.Slots[0] == nil || hopper.Slots[0].Count != 1 {
		t.Fatalf("expected hopper to extract 1 item, got %+v", hopper.Slots[0])
	}
	if topChest.Slots[0].Count != 4 {
		t.Fatalf("expected top chest to have 4 items left, got %d", topChest.Slots[0].Count)
	}

	// D. 测试 6 向活塞推拉 (朝下推 Facing 0 和 朝东推 Facing 5)
	// 朝下推：(15, 63, 15) 活塞，(15, 62, 15) 石头，(15, 61, 15) 空
	s.setBlock(r, 15, 63, 15, 168, setQuiet)
	s.setBlock(r, 15, 62, 15, blkStone, setQuiet)
	s.setBlock(r, 15, 61, 15, 0, setQuiet)
	s.pushPiston(r, 15, 63, 15, facing6[0], false)
	if s.block(r, 15, 61, 15) != blkStone {
		t.Fatalf("downward piston failed: expected stone at (15, 61, 15)")
	}
}

// 测试红石线 0..15 BFS 信号衰减与中继器
func TestRedstoneWireAndRepeater(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createAdvancedTestWorld(s)
	rs := getRedstone(r)

	// 1. 设置红石块作为电源 (10, 61, 10)
	s.setBlock(r, 10, 61, 10, 40, setQuiet)

	// 2. 铺设连续 3 格红石线 (11, 61, 10) -> (12, 61, 10) -> (13, 61, 10)
	k1 := blockKey(11, 61, 10)
	k2 := blockKey(12, 61, 10)
	k3 := blockKey(13, 61, 10)
	rs.Wires[k1] = 0
	rs.Wires[k2] = 0
	rs.Wires[k3] = 0

	s.recalculateWires(r)

	// 验证逐格衰减: 15 -> 14 -> 13
	if rs.Wires[k1] != 15 || rs.Wires[k2] != 14 || rs.Wires[k3] != 13 {
		t.Fatalf("redstone wire decay failed: got w1=%d w2=%d w3=%d",
			rs.Wires[k1], rs.Wires[k2], rs.Wires[k3])
	}

	// 3. 中继器测试：在 (14, 61, 10) 放置中继器，朝东(Facing=1)，背靠 (13, 61, 10)
	kRep := blockKey(14, 61, 10)
	rep := &RepeaterBlock{Facing: 1, Delay: 1}
	rs.Repeaters[kRep] = rep

	// 验证背后受电判定
	if !s.isRepeaterInputPowered(r, 14, 61, 10, rep.Facing) {
		t.Fatalf("expected repeater input to receive power from wire")
	}

	// 经过延时触发
	rep.Powered = true
	// 验证正面 (15, 61, 10) 发射 15 级强充能
	outPower := s.emittedPower(r, 14, 61, 10, 1, 0, 0)
	if outPower != 15 {
		t.Fatalf("repeater boost failed: expected 15 power output, got %d", outPower)
	}

	// 4. 侧面锁存测试：在侧面 (14, 61, 9) 放置另一个中继器指向其侧面
	kSide := blockKey(14, 61, 9)
	rs.Repeaters[kSide] = &RepeaterBlock{Facing: 2, Powered: true} // Facing 2 points South
	if !s.isRepeaterSideLocked(r, 14, 61, 10, rep.Facing) {
		t.Fatalf("expected repeater to be locked by side repeater")
	}
}

// 测试红石比较器与压力板
func TestRedstoneComparatorAndPressurePlate(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	rs := getRedstone(r)

	// 1. 比较器容器检测：在 (10, 61, 10) 放置箱子，在 (10, 61, 11) 放置比较器(Facing=2, 朝南)
	boxKey := blockKey(10, 61, 10)
	box := &Container{Slots: make(Stacks, 27)}
	// 装满 1 组物品
	box.Slots[0] = stack(s.rules.byName["Iron Ingot"], 64, 0)
	r.Containers[boxKey] = box

	cmpKey := blockKey(10, 61, 11)
	cmp := &ComparatorBlock{Facing: 2, Subtract: false}
	rs.Comparators[cmpKey] = cmp

	out := s.calculateComparatorOutput(r, 10, 61, 11, cmp)
	if out <= 0 {
		t.Fatalf("expected comparator to detect container content, got %d", out)
	}

	// 2. 减法模式测试
	cmp.Subtract = true
	// 侧面输入 5 级信号
	rs.Wires[blockKey(9, 61, 11)] = 5
	subOut := s.calculateComparatorOutput(r, 10, 61, 11, cmp)
	if subOut != max(0, out-5) {
		t.Fatalf("comparator subtract failed: expected %d, got %d", max(0, out-5), subOut)
	}

	// 3. 压力板踏踩检测
	plateKey := blockKey(10, 61, 15)
	rs.PressurePlates[plateKey] = &PressurePlateBlock{Wooden: true}

	// 移动玩家到压力板上方
	p.state.X = 10.5
	p.state.Y = 61.0
	p.state.Z = 15.5
	if !s.entityOnPlate(r, 10, 61, 15) {
		t.Fatalf("expected entity on plate to be true")
	}

	// 移动玩家离开压力板
	p.state.X = 20.5
	if s.entityOnPlate(r, 10, 61, 15) {
		t.Fatalf("expected entity on plate to be false after player stepped off")
	}
}

// 3. 测试村民交易逻辑
func TestVillagerTrade(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)

	// 生成村民
	villager, err := s.spawnMob(r, p, "villager", 10.5, 61, 10.5, "test")
	if err != nil || villager == nil {
		t.Fatalf("failed to spawn villager: %v", err)
	}

	// 给玩家提供 20 个小麦
	wheatID := s.rules.byName["Wheat"]
	p.state.Inv.Slots[0] = stack(wheatID, 20, 0)

	// 执行交易 0：20 个小麦换 1 个绿宝石
	s.message(p, Message{T: "trade_execute", I: 0})

	// 检查玩家背包是否得到绿宝石，小麦是否被消耗 15 个（剩余 5 个）
	hasEmerald := false
	for _, sl := range p.state.Inv.Slots {
		if sl != nil && (s.rules.byID[sl.ID].Name == "Block of Emerald" || sl.ID == 38) {
			hasEmerald = true
			break
		}
	}
	if !hasEmerald {
		t.Fatalf("expected player to receive emerald from trade")
	}
	if p.state.Inv.Slots[0] == nil || p.state.Inv.Slots[0].Count != 5 {
		t.Fatalf("expected 5 wheat remaining, got %+v", p.state.Inv.Slots[0])
	}
}

// 4. 测试末影龙与水晶机制
func TestDragonBossFight(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	// 创建 end 维度测试 Room
	r := newRoom("test_room_the_end", "末地维度", "survival", 0)
	df := getDragonFight(r)

	s.initDragonFight(r, df)

	if !df.Active {
		t.Fatalf("expected dragon fight to be active")
	}
	if len(df.Crystals) != 10 {
		t.Fatalf("expected 10 crystals spawned, got %d", len(df.Crystals))
	}
	if df.DragonID == 0 {
		t.Fatalf("expected valid DragonID mob")
	}

	// 测试摧毁第 0 个水晶触发爆炸并扣除末影龙生命
	firstCrystal := df.Crystals[0]
	// 模拟水晶 Mob 被消灭
	delete(r.Mobs, firstCrystal.ID)

	// 把龙移动到水晶旁边
	df.X = firstCrystal.X + 2
	df.Y = firstCrystal.Y
	df.Z = firstCrystal.Z + 2
	initialHP := df.Health

	s.tickDragonFight(r, df)

	if firstCrystal.Alive {
		t.Fatalf("expected crystal to be marked dead")
	}
	if df.Health >= initialHP {
		t.Fatalf("expected dragon to take explosion backlash damage: initial=%f now=%f", initialHP, df.Health)
	}
}
