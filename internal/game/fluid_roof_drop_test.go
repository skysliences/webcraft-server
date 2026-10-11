package game

import (
	"testing"
)

func TestRoofEdgeFluidDropAndBlockPlacement(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createBatch1TestWorld(s)

	// 1. 搭建一个悬空的屋顶平台: x 从 10 到 15, z 从 10 到 15, 高度 y = 60
	// x >= 16 处是悬空空气 (边缘落差)
	for x := 10; x <= 15; x++ {
		for z := 10; z <= 15; z++ {
			s.setBlock(r, x, 60, z, blkCobblestone, 0)
		}
	}
	// 清空上方和悬空外侧的空气
	for x := 8; x <= 18; x++ {
		for z := 8; z <= 18; z++ {
			for y := 61; y <= 65; y++ {
				s.setBlock(r, x, y, z, 0, 0)
			}
			if x > 15 {
				for y := 50; y <= 60; y++ {
					s.setBlock(r, x, y, z, 0, 0)
				}
			}
		}
	}

	// 2. 在屋顶边缘 x=15, z=12, y=61 放置水源 (离悬空边缘 x=16 仅差 1 格)
	p.state.Inv.Slots[0] = stack(itemWaterBucket, 1, 0)
	p.state.Inv.Selected = 0
	s.survivalMessage(p, Message{T: "place", X: 15, Y: 60, Z: 12, F: 1})

	if b := s.block(r, 15, 61, 12); b != blkWaterSource {
		t.Fatalf("expected water source at (15,61,12), got %d (%s)", b, s.fullName(b))
	}

	// 3. 推进 2 个 ticks，水流因落差寻路，仅朝向 x=16 (落差点) 流动
	for i := 0; i < 2; i++ {
		for _, fn := range roomTickHooks {
			fn(s, r)
		}
	}

	// 边缘外侧 (16, 61, 12) 应该是下落水流 (blkWaterFalling)
	if b := s.block(r, 16, 61, 12); b != blkWaterFalling {
		t.Fatalf("expected water falling at roof edge (16,61,12), got %d (%s)", b, s.fullName(b))
	}

	// 验证内侧 (14, 61, 12) 因为落差优先寻路，没有在第一时间往内侧扩散
	if b := s.block(r, 14, 61, 12); b != 0 {
		t.Fatalf("expected no fluid inward at (14,61,12) due to drop-seeking priority, got %d", b)
	}

	// 4. 推进更多 ticks，水流沿着 x=16 垂直向下落
	for i := 0; i < 6; i++ {
		for _, fn := range roomTickHooks {
			fn(s, r)
		}
	}

	// 下方 (16, 60, 12) 和 (16, 59, 12) 应该是垂直下落水
	if b := s.block(r, 16, 60, 12); b != blkWaterFalling {
		t.Fatalf("expected water falling at (16,60,12), got %d (%s)", b, s.fullName(b))
	}

	// 核心验证：水柱在空中垂直下落时，四周必须保持空气，绝不能向空中横向扩散！
	// 检查 (17, 60, 12), (16, 60, 13), (16, 60, 11) 等悬空位置
	if b := s.block(r, 17, 60, 12); b != 0 {
		t.Fatalf("fluid unexpectedly spread horizontally in mid-air at (17,60,12): %d (%s)", b, s.fullName(b))
	}
	if b := s.block(r, 16, 60, 13); b != 0 {
		t.Fatalf("fluid unexpectedly spread horizontally in mid-air at (16,60,13): %d (%s)", b, s.fullName(b))
	}
	if b := s.block(r, 16, 60, 11); b != 0 {
		t.Fatalf("fluid unexpectedly spread horizontally in mid-air at (16,60,11): %d (%s)", b, s.fullName(b))
	}

	// 5. 核心验证：允许在流动水处直接放置方块置换水流！
	// 玩家准备在边缘流动水 (16, 61, 12) 放置圆石方块
	p.state.Inv.Slots[1] = stack(blkCobblestone, 64, 0)
	p.state.Inv.Selected = 1
	// 对着 (15, 61, 12) 的 +X 面 (F=5) 点击放置，落点即为 (16, 61, 12)
	s.survivalMessage(p, Message{T: "place", X: 15, Y: 61, Z: 12, F: 5})

	if b := s.block(r, 16, 61, 12); b != blkCobblestone {
		t.Fatalf("expected cobblestone placed at flowing water position (16,61,12), got %d (%s)", b, s.fullName(b))
	}
	if p.state.Inv.Slots[1].Count != 63 {
		t.Fatalf("expected cobblestone count 63, got %d", p.state.Inv.Slots[1].Count)
	}

	// 6. 验证方块堵住水流后，下方水瀑布退潮消散
	for i := 0; i < 10; i++ {
		for _, fn := range roomTickHooks {
			fn(s, r)
		}
	}
	if b := s.block(r, 16, 60, 12); b != 0 {
		t.Fatalf("expected waterfall below to recede to 0 at (16,60,12) after blocked by block, got %d", b)
	}
}
