package game

import (
	"testing"
)

// 测试骷髅在白天阳光下点燃、受到伤害致死以及水中/屋顶灭火
func TestSkeletonBurnsInDaylightAndDies(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := mobTestWorld(s)
	p.state.Health = 1000 // 赋予测试观察者高生命值，确保生存模式掉落生成且玩家不被射死
	r.Time = 6000         // 白天正午

	// 1. 生成骷髅
	skel, err := s.spawnMob(r, nil, "skeleton", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := skel.ID
	initHealth := skel.Health
	if initHealth <= 0 {
		t.Fatalf("骷髅初始生命值异常: %f", initHealth)
	}

	// 2. 模拟 20 个 tick (2 秒) 日光直射
	for i := 1; i <= 20; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
	}

	skel = r.Mobs[id]
	if skel == nil {
		t.Fatal("骷髅在 2 秒内不应过快消失")
	}
	if skel.Flags&8 == 0 {
		t.Fatal("骷髅在白天阳光直射下必须带有燃烧标志 (m.Flags&8 != 0)")
	}
	if skel.Health >= initHealth {
		t.Fatalf("骷髅必须受到阳光灼烧伤害: 原生命=%f, 当前生命=%f", initHealth, skel.Health)
	}

	// 3. 模拟持续燃烧直到死亡 (骷髅 20 点血，需要约 200 个 tick = 20 秒)
	for i := 21; i <= 250; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
		if r.Mobs[id] == nil {
			break
		}
	}

	if r.Mobs[id] != nil {
		t.Fatalf("骷髅在持续阳光暴晒下必须最终死亡被清除，但当前仍存活: Health=%f", r.Mobs[id].Health)
	}

	// 验证掉落物生成（骷髅掉落骨头或箭）
	if len(r.Drops) == 0 {
		t.Fatal("骷髅白天烧死后必须正常生成掉落物")
	}
}

// 测试骷髅在屋顶遮蔽下不受阳光伤害
func TestSkeletonProtectedByRoof(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := mobTestWorld(s)
	r.Mode = "creative"
	r.Time = 6000 // 白天

	skel, err := s.spawnMob(r, nil, "skeleton", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}
	// 在头上铺设大范围遮阳屋顶，确保怪物无论怎么移动都在屋顶遮蔽下
	for x := 5; x <= 35; x++ {
		for z := 5; z <= 25; z++ {
			r.Edits[blockKey(x, 64, z)] = 1
		}
	}
	skel.burnMS = 0

	for i := 1; i <= 30; i++ {
		s.tick = uint64(i)
		s.mobsTick(r)
	}

	if skel.Flags&8 != 0 {
		t.Fatal("有屋顶遮蔽时骷髅不应点燃火焰")
	}
	if skel.Health != 20 {
		t.Fatal("有屋顶遮蔽时骷髅不应受到阳光伤害")
	}
}

// 测试骷髅站在水中时火焰熄灭
func TestSkeletonExtinguishedInWater(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, _ := mobTestWorld(s)
	r.Mode = "creative"
	r.Time = 6000 // 白天

	skel, err := s.spawnMob(r, nil, "skeleton", 12.5, 60, 10.5, "test")
	if err != nil {
		t.Fatal(err)
	}

	// 先让骷髅在阳光下点燃
	s.tick = 10
	s.mobsTick(r)
	if skel.Flags&8 == 0 {
		t.Fatal("骷髅应在阳光下被点燃")
	}

	// 站在水方块中
	r.Edits[blockKey(12, 60, 10)] = 9 // 水方块
	s.tick = 11
	s.mobsTick(r)

	if skel.burnMS != 0 || skel.Flags&8 != 0 {
		t.Fatal("骷髅进入水中后火焰必须立即熄灭")
	}
}
