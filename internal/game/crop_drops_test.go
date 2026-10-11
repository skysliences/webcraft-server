package game

import (
	"testing"
	"time"
)

// 1. 验证破坏成熟小麦产生合法的小麦与种子掉落物，且绝对不会报错“无效的掉落物品”
func TestWheatHarvestDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createFixTestWorld(s)
	x, y, z := 10, 61, 10
	s.setBlock(r, x, y-1, z, blkFarmlandWet, setQuiet)
	s.setBlock(r, x, y, z, blkWheatMax, setQuiet)

	// 模拟玩家开始挖掘成熟小麦
	p.mining = &digState{
		X:     x,
		Y:     y,
		Z:     z,
		Block: blkWheatMax,
		Held:  0,
		At:    time.Now().Add(-1 * time.Second),
	}

	// 模拟玩家提交破坏包 (S=2, T="dig")
	s.survivalMessage(p, Message{
		T: "dig",
		X: x,
		Y: y,
		Z: z,
		S: 2,
	})

	// 断言：方块必须被成功破坏并清除为空气 (0)
	if b := s.block(r, x, y, z); b != 0 {
		t.Fatalf("expected mature wheat to be broken (block=0), got %d", b)
	}

	// 断言：世界必须生成掉落物，且掉落物必须包含 Wheat (336) 和 Wheat Seeds (335)
	if len(r.Drops) == 0 {
		t.Fatalf("expected drops to be spawned in world, got 0")
	}

	var hasWheat, hasSeeds bool
	var wheatCount, seedsCount int
	for _, d := range r.Drops {
		if d.Stack == nil {
			continue
		}
		if d.Stack.ID == itemWheat {
			hasWheat = true
			wheatCount += d.Stack.Count
		}
		if d.Stack.ID == itemWheatSeeds {
			hasSeeds = true
			seedsCount += d.Stack.Count
		}
		// 校验每一个掉落物必须是合法物品
		if !s.validStack(d.Stack, 2304) {
			t.Fatalf("invalid drop stack in world: %+v", d.Stack)
		}
	}

	if !hasWheat || wheatCount != 1 {
		t.Fatalf("expected 1 wheat dropped, got %d (hasWheat=%v)", wheatCount, hasWheat)
	}
	if !hasSeeds || seedsCount < 1 || seedsCount > 3 {
		t.Fatalf("expected 1-3 seeds dropped, got %d (hasSeeds=%v)", seedsCount, hasSeeds)
	}
}

// 2. 验证未成熟作物破坏仅掉落种子/基础作物
func TestImmatureCropDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	// 未成熟小麦 (blkWheatMin = 150)
	drops := s.drops(blkWheatMin, 0)
	if len(drops) != 1 || drops[0].ID != itemWheatSeeds || drops[0].Count != 1 {
		t.Fatalf("expected 1 wheat seed from immature wheat, got: %+v", drops)
	}
	if !s.validStack(drops[0], 2304) {
		t.Fatalf("immature wheat drop is not a valid stack: %+v", drops[0])
	}

	// 未成熟胡萝卜 (blkCarrotMin = 158)
	cDrops := s.drops(blkCarrotMin, 0)
	if len(cDrops) != 1 || cDrops[0].ID != itemCarrot || cDrops[0].Count != 1 {
		t.Fatalf("expected 1 carrot from immature carrot, got: %+v", cDrops)
	}

	// 未成熟马铃薯 (blkPotatoMin = 162)
	pDrops := s.drops(blkPotatoMin, 0)
	if len(pDrops) != 1 || pDrops[0].ID != itemPotato || pDrops[0].Count != 1 {
		t.Fatalf("expected 1 potato from immature potato, got: %+v", pDrops)
	}
}

// 3. 验证成熟胡萝卜与马铃薯掉落
func TestMatureCarrotAndPotatoDrops(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	// 成熟胡萝卜 (161)
	cDrops := s.drops(blkCarrotMax, 0)
	if len(cDrops) != 1 || cDrops[0].ID != itemCarrot || cDrops[0].Count < 1 || cDrops[0].Count > 4 {
		t.Fatalf("expected 1-4 carrots from mature carrot, got: %+v", cDrops)
	}

	// 成熟马铃薯 (170)
	pDrops := s.drops(blkPotatoMax, 0)
	if len(pDrops) < 1 || pDrops[0].ID != itemPotato || pDrops[0].Count < 1 || pDrops[0].Count > 4 {
		t.Fatalf("expected 1-4 potatoes from mature potato, got: %+v", pDrops)
	}
}

// 4. 验证耕地退化或被移除时作物脱落并生成合法掉落物
func TestCropPopsOffWhenFarmlandBroken(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := createFixTestWorld(s)
	x, y, z := 10, 61, 10
	s.setBlock(r, x, y-1, z, 0, setQuiet) // 移除耕地
	s.setBlock(r, x, y, z, blkWheatMax, setQuiet)

	// 触发方块更新检查
	for _, fn := range blockUpdateHooks {
		fn(s, r, x, y, z, blkWheatMax)
	}

	// 作物应该变为空气 (0)
	if b := s.block(r, x, y, z); b != 0 {
		t.Fatalf("expected crop to pop off into air, got %d", b)
	}
	// 应该生成了掉落物
	if len(r.Drops) == 0 {
		t.Fatalf("expected drops spawned when crop popped off")
	}
}

// 5. 核心防御防线验证：扫描所有方块 ID，绝对不允许出现任何返回非法 Item 的掉落物！
func TestNoInvalidItemDropsForAnyBlock(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	for b := 0; b < len(s.rules.Names); b++ {
		drops := s.drops(b, 0)
		for _, st := range drops {
			if st == nil {
				continue
			}
			if !s.validStack(st, 2304) {
				t.Fatalf("CRITICAL: Block %d (%s) drops invalid item ID %d! Stack: %+v", b, s.rules.Names[b], st.ID, st)
			}
		}
	}
}
