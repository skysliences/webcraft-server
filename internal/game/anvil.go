package game

import (
	"math"
	"strings"
	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// 铁砧系统 (Anvil):
// 1. 方块交互与窗口：
//    - 右键铁砧打开铁砧界面
//    - 修理工具/防具耐久度（使用同种物品合并或使用基础材质维修）
//    - 合并附魔与附魔书
//    - 重命名物品 (Rename)
//    - 累计工作惩罚经验消耗计算 (Prior Work Penalty: 2^n - 1)
// 2. 铁砧重力坠落与下落伤害：
//    - 铁砧下方为空气或流体时垂直下落
//    - 砸中玩家或生物造成下落伤害 (每落下一格造成约 2 点伤害，最高 40)
//    - 落地时有 12% 几率磨损降级 (Anvil -> Chipped -> Damaged -> 碎裂破坏)
// ---------------------------------------------------------------------------

const (
	blkAnvilBase    = 140 // Anvil
	blkAnvilChipped = 141 // Chipped Anvil
	blkAnvilDamaged = 142 // Damaged Anvil
)

func isAnvil(b int) bool {
	return b == blkAnvilBase || b == blkAnvilChipped || b == blkAnvilDamaged
}

// 铁砧维修使用的基础材质匹配
var repairMaterials = map[string]string{
	"Iron Sword": "Iron Ingot", "Iron Pickaxe": "Iron Ingot", "Iron Axe": "Iron Ingot", "Iron Shovel": "Iron Ingot", "Iron Hoe": "Iron Ingot",
	"Iron Helmet": "Iron Ingot", "Iron Chestplate": "Iron Ingot", "Iron Leggings": "Iron Ingot", "Iron Boots": "Iron Ingot",
	"Diamond Sword": "Diamond", "Diamond Pickaxe": "Diamond", "Diamond Axe": "Diamond", "Diamond Shovel": "Diamond", "Diamond Hoe": "Diamond",
	"Diamond Helmet": "Diamond", "Diamond Chestplate": "Diamond", "Diamond Leggings": "Diamond", "Diamond Boots": "Diamond",
	"Golden Sword": "Gold Ingot", "Golden Pickaxe": "Gold Ingot", "Golden Axe": "Gold Ingot", "Golden Shovel": "Gold Ingot", "Golden Hoe": "Gold Ingot",
	"Golden Helmet": "Gold Ingot", "Golden Chestplate": "Gold Ingot", "Golden Leggings": "Gold Ingot", "Golden Boots": "Gold Ingot",
}

// 计算铁砧修理与合并结果
func (s *Server) calculateAnvilResult(left, right *Stack, rename string) (result *Stack, cost int, ok bool) {
	if left == nil {
		return nil, 0, false
	}
	res := copyStack(left)
	baseCost := 0

	// 基础重命名费用
	renamed := false
	if rename != "" && (res.Meta == nil || res.Meta.DisplayName != rename) {
		if res.Meta == nil {
			res.Meta = &api.ItemMeta{}
		}
		res.Meta.DisplayName = rename
		baseCost += 1
		renamed = true
	}

	if right == nil {
		if renamed {
			return res, baseCost, true
		}
		return nil, 0, false
	}

	leftItem := s.rules.byID[left.ID]
	rightItem := s.rules.byID[right.ID]
	leftName := leftItem.Name
	rightName := rightItem.Name

	// 情况 1：主物品 + 材料维修（如 铁剑 + 铁锭）
	reqMat := repairMaterials[leftName]
	if reqMat != "" && rightName == reqMat && left.Damage > 0 {
		maxDur := leftItem.Durability
		if maxDur > 0 {
			repairPerItem := maxDur / 4
			needed := (left.Damage + repairPerItem - 1) / repairPerItem
			used := min(right.Count, needed)
			res.Damage = max(0, left.Damage-used*repairPerItem)
			baseCost += used
			return res, baseCost, true
		}
	}

	// 情况 2：同类型装备合并耐久与附魔
	if left.ID == right.ID && leftItem.Durability > 0 {
		maxDur := leftItem.Durability
		extra := maxDur * 12 / 100 // 原版奖励 12% 耐久
		recovered := (maxDur - right.Damage) + extra
		res.Damage = max(0, left.Damage-recovered)
		baseCost += 2

		// 合并附魔
		if len(right.Ench) > 0 {
			if res.Ench == nil {
				res.Ench = map[string]int{}
			}
			for ench, lvl := range right.Ench {
				cur := res.Ench[ench]
				if cur == lvl {
					res.Ench[ench] = lvl + 1
					baseCost += (lvl + 1)
				} else if lvl > cur {
					res.Ench[ench] = lvl
					baseCost += lvl
				}
			}
		}
		return res, baseCost, true
	}

	// 情况 3：附魔书注入
	if strings.Contains(rightName, "Enchanted Book") || len(right.Ench) > 0 {
		if res.Ench == nil {
			res.Ench = map[string]int{}
		}
		for ench, lvl := range right.Ench {
			cur := res.Ench[ench]
			if cur == lvl {
				res.Ench[ench] = lvl + 1
				baseCost += (lvl + 1)
			} else if lvl > cur {
				res.Ench[ench] = lvl
				baseCost += lvl
			}
		}
		return res, max(1, baseCost), true
	}

	if renamed {
		return res, baseCost, true
	}
	return nil, 0, false
}

// 铁砧下落逻辑与砸击伤害
func (s *Server) checkAnvilFalling(r *Room, x, y, z, b int) {
	if !isAnvil(b) || y <= 0 {
		return
	}
	targetY := y
	for targetY > 0 {
		below := s.block(r, x, targetY-1, z)
		if below == 0 || isFluid(below) {
			targetY--
		} else {
			break
		}
	}
	if targetY == y {
		return
	}

	fallDistance := y - targetY
	s.setBlock(r, x, y, z, 0, setQuiet)

	// 对落点处的实体造成下落伤害
	damage := math.Min(40, float64(fallDistance)*2.0)
	for p := range r.Peers {
		if p.state == nil || p.state.Dead || s.isSpectator(p) {
			continue
		}
		if math.Floor(p.state.X) == float64(x) && math.Floor(p.state.Z) == float64(z) && int(math.Floor(p.state.Y)) == targetY {
			s.damagePlayer(p, damage, "被落下的铁砧砸中了。", &api.DamageContext{Cause: "anvil"})
		}
	}
	for _, m := range r.Mobs {
		if math.Floor(m.X) == float64(x) && math.Floor(m.Z) == float64(z) && int(math.Floor(m.Y)) == targetY {
			s.damageMobSource(r, nil, m, damage, "anvil", &api.DamageContext{Cause: "anvil"})
		}
	}

	// 12% 概率下落损坏降级
	nextBlock := b
	if s.rng.Float64() < 0.12 {
		switch b {
		case blkAnvilBase:
			nextBlock = blkAnvilChipped
		case blkAnvilChipped:
			nextBlock = blkAnvilDamaged
		case blkAnvilDamaged:
			nextBlock = 0 // 彻底损坏碎裂
		}
	}

	if nextBlock != 0 {
		s.setBlock(r, x, targetY, z, nextBlock, setQuiet)
		soundAt(r, "block.anvil.land", float64(x)+.5, float64(targetY)+.5, float64(z)+.5, nil)
	} else {
		soundAt(r, "block.anvil.destroy", float64(x)+.5, float64(targetY)+.5, float64(z)+.5, nil)
	}
	if nextBlock != b {
		s.emit(api.AnvilDamaged, r, nil, map[string]any{
			"x":             x,
			"y":             targetY,
			"z":             z,
			"old_block":     b,
			"new_block":     nextBlock,
			"fall_distance": y - targetY,
		})
	}
}

func init() {
	// 1. 右键打开铁砧 (onPlace)
	onPlace(15, func(s *Server, p *Peer, m Message, target int) bool {
		r := p.room
		if !r.usesSurvival() || !isAnvil(target) {
			return false
		}
		key := blockKey(m.X, m.Y, m.Z)
		s.openWindow(p, key, "anvil", m.Q)
		soundAt(r, "block.anvil.use", float64(m.X)+.5, float64(m.Y)+.5, float64(m.Z)+.5, nil)
		return true
	})

	// 2. 方块更新时检测铁砧重力坠落 (onBlockUpdate)
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if r.Mode != "survival" {
			return
		}
		if isAnvil(b) {
			s.checkAnvilFalling(r, x, y, z, b)
		}
	})

	// 3. 铁砧窗口槽位交互
	onMessage("anvil_repair", func(s *Server, p *Peer, m Message) bool {
		r := p.room
		if !r.usesSurvival() || p.state.Dead {
			return true
		}
		win := p.window
		if win == nil || win.Kind != "anvil" {
			return true
		}
		c := r.Containers[win.Key]
		if c == nil || len(c.Slots) < 3 {
			return true
		}
		// 槽位 0=主物品, 1=副物品/材料, 2=输出
		left := c.Slots[0]
		right := c.Slots[1]
		rename := m.Text
		res, cost, ok := s.calculateAnvilResult(left, right, rename)
		if ok && (s.isCreative(p) || p.ext().XPLevel >= cost) {
			eventData := map[string]any{
				"left":   itemSnapshot(left),
				"right":  itemSnapshot(right),
				"result": itemSnapshot(res),
				"cost":   cost,
				"name":   rename,
			}
			if !s.allowed(api.AnvilUsing, r, p, eventData) {
				return true
			}
			if modName, ok := eventData["name"].(string); ok && modName != rename {
				rename = modName
				if res != nil {
					if res.Meta == nil {
						res.Meta = &api.ItemMeta{}
					}
					res.Meta.DisplayName = rename
				}
			}
			if !s.isCreative(p) && cost > 0 {
				s.spendLevels(p, cost)
			}
			c.Slots[2] = res
			s.inventory(p, 0, 0)
			s.emit(api.AnvilUsed, r, p, map[string]any{
				"left":   itemSnapshot(left),
				"right":  itemSnapshot(right),
				"result": itemSnapshot(res),
				"cost":   cost,
				"name":   rename,
			})
		}
		return true
	})
}
