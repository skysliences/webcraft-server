package game

import (
	"testing"
)

func TestPlantMiningWithToolsAndSwords(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	plants := []string{
		"Short Grass",
		"Dandelion",
		"Poppy",
		"Cornflower",
		"Oxeye Daisy",
		"Fern",
		"Dead Bush",
	}

	swords := []string{
		"Wooden Sword",
		"Stone Sword",
		"Iron Sword",
		"Golden Sword",
		"Diamond Sword",
	}

	otherTools := []string{
		"Wooden Pickaxe",
		"Iron Pickaxe",
		"Diamond Pickaxe",
		"Wooden Axe",
		"Iron Axe",
		"Wooden Shovel",
		"Iron Shovel",
		"Wooden Hoe",
		"Diamond Hoe",
	}

	// 1. 验证所有剑对所有草和花的 MiningRule 均为 Ticks=0, Wear=0
	for _, plantName := range plants {
		plantID, ok := s.rules.byName[plantName]
		if !ok {
			t.Fatalf("missing block: %s", plantName)
		}
		for _, swordName := range swords {
			swordID, ok := s.rules.byName[swordName]
			if !ok {
				t.Fatalf("missing item: %s", swordName)
			}
			rule := s.rules.mine(plantID, swordID)
			if rule.Ticks != 0 {
				t.Errorf("%s with %s: expected ticks=0, got %d", plantName, swordName, rule.Ticks)
			}
			if rule.Wear != 0 {
				t.Errorf("%s with %s: expected wear=0, got %d", plantName, swordName, rule.Wear)
			}
		}

		// 验证其它工具对草和花的 MiningRule
		for _, toolName := range otherTools {
			toolID, ok := s.rules.byName[toolName]
			if !ok {
				t.Fatalf("missing item: %s", toolName)
			}
			rule := s.rules.mine(plantID, toolID)
			if rule.Ticks != 0 {
				t.Errorf("%s with %s: expected ticks=0, got %d", plantName, toolName, rule.Ticks)
			}
			if rule.Wear != 0 {
				t.Errorf("%s with %s: expected wear=0, got %d", plantName, toolName, rule.Wear)
			}
		}
	}

	// 2. 模拟真实服务端 dig 消息流程：玩家手持钻石剑破坏短草 (Short Grass)
	r, p := dropTestWorld(s)
	grassID := s.rules.byName["Short Grass"]
	diaSwordID := s.rules.byName["Diamond Sword"]

	// 玩家快捷栏第0格放一把全新钻石剑
	p.state.Inv.Selected = 0
	p.state.Inv.Slots[0] = stack(diaSwordID, 1, 0)

	targetX, targetY, targetZ := 10, 60, 10
	key := blockKey(targetX, targetY, targetZ)
	r.Edits[key] = grassID

	// 发送 dig 0 (开始破坏) 和 dig 2 (完成破坏)
	s.survivalMessage(p, Message{T: "dig", S: 0, X: targetX, Y: targetY, Z: targetZ, F: 1})
	s.survivalMessage(p, Message{T: "dig", S: 2, X: targetX, Y: targetY, Z: targetZ, F: 1})

	// 验证方块已被破坏移除 (Edits 中为 0)
	if r.Edits[key] != 0 {
		t.Fatalf("Short Grass was not mined successfully, block is %d", r.Edits[key])
	}

	// 验证手持钻石剑耐久未受任何消耗
	heldSword := p.state.Inv.Slots[0]
	if heldSword == nil || heldSword.ID != diaSwordID {
		t.Fatalf("held sword missing or changed: %+v", heldSword)
	}
	if heldSword.Damage != 0 {
		t.Fatalf("sword should not lose durability when breaking grass, got damage=%d", heldSword.Damage)
	}

	// 3. 模拟真实服务端 dig 消息流程：玩家手持铁镐破坏虞美人 (Poppy)
	poppyID := s.rules.byName["Poppy"]
	ironPickID := s.rules.byName["Iron Pickaxe"]
	p.state.Inv.Slots[0] = stack(ironPickID, 1, 0)

	targetX2, targetY2, targetZ2 := 11, 60, 10
	key2 := blockKey(targetX2, targetY2, targetZ2)
	r.Edits[key2] = poppyID

	s.survivalMessage(p, Message{T: "dig", S: 0, X: targetX2, Y: targetY2, Z: targetZ2, F: 1})
	s.survivalMessage(p, Message{T: "dig", S: 2, X: targetX2, Y: targetY2, Z: targetZ2, F: 1})

	if r.Edits[key2] != 0 {
		t.Fatalf("Poppy was not mined successfully, block is %d", r.Edits[key2])
	}
	heldPick := p.state.Inv.Slots[0]
	if heldPick == nil || heldPick.ID != ironPickID || heldPick.Damage != 0 {
		t.Fatalf("pickaxe should not lose durability when breaking flower, got %+v", heldPick)
	}
}
