// This plugin demonstrates item attributes, Lore-driven effects and persistent
// custom data. It changes a world only when its owner runs a command.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	api "mcraft/backend/pkg/plugin"
	"strconv"
	"strings"
)

func newPlugin() *api.Plugin {
	p := api.New()
	// Observe committed creative transactions; full snapshots remain available
	// through e.Item("after") and e.Player.Item(slot). Monitor is read-only.
	p.On(api.InventoryCreativeChanged, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
		if e.String("reason") == "creative.take" {
			if v := e.Item("after"); v != nil && e.Player != nil {
				log.Printf("creative item: player=%s item=%d count=%d", e.Player.Name, v.ID, v.Count)
			}
		}
		return nil
	})
	p.Command(api.Command{Name: "itemdemo", Permission: "owner", Description: "领取 Lore 吸血剑和生命护甲（生存模式）"}, func(c *api.Context, e *api.Event, args []string) error {
		if e.World.Mode != "survival" {
			c.Message(e, "/itemdemo 需要生存模式。")
			return nil
		}
		c.GiveStack(e, &api.ItemStack{ID: 276, Count: 1, Meta: &api.ItemMeta{
			DisplayName: "赤月·吸血剑", Lore: []string{"§7由 custom-items 插件锻造", "§d[吸血] 命中后恢复实际伤害的 25%", "§7移除 [吸血] Lore 会停用吸血效果"},
			Attributes: []api.AttributeModifier{{ID: "demo:blade_damage", Attribute: api.AttackDamage, Amount: 3, Operation: api.Add, Slot: "mainhand"}},
			CustomData: map[string]any{"demo:lifesteal": .25},
		}})
		c.GiveStack(e, &api.ItemStack{ID: 298, Count: 1, Meta: &api.ItemMeta{
			DisplayName: "守望者胸甲", Lore: []string{"§7穿戴后提升生命上限和护甲", "§6熔岩伤害由插件事件免疫"},
			Attributes: []api.AttributeModifier{{ID: "demo:chest_health", Attribute: api.MaxHealth, Amount: 4, Operation: api.Add, Slot: "chest"}, {ID: "demo:chest_armor", Attribute: api.Armor, Amount: 2, Operation: api.Add, Slot: "chest"}},
			CustomData: map[string]any{"demo:fire_resistance": true},
		}})
		c.Message(e, "已请求发放吸血剑和胸甲。选中剑后可用 /itemlore、/itemattr、/itemdata 修改。")
		return nil
	})
	p.On(api.PlayerAttacked, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		d := e.Damage
		if d == nil || d.Weapon == nil || d.Weapon.Meta == nil || d.AppliedAmount <= 0 {
			return nil
		}
		meta := d.Weapon.Meta
		enabled := false
		for _, line := range meta.Lore {
			if strings.Contains(line, "[吸血]") {
				enabled = true
			}
		}
		rate, _ := meta.CustomData["demo:lifesteal"].(float64)
		if enabled && rate > 0 && rate <= 1 {
			c.Heal(e, d.AppliedAmount*rate)
		}
		return nil
	})
	p.On(api.PlayerDamaging, api.Subscription{Priority: api.High, IgnoreCancelled: true}, func(c *api.Context, e *api.Event) error {
		if e.Damage == nil || e.Damage.Cause != "lava" || e.Player == nil {
			return nil
		}
		for _, slot := range []int{36, 37, 38, 39} {
			v := e.Player.Item(slot)
			if v != nil && v.Meta != nil && v.Meta.CustomData["demo:fire_resistance"] == true {
				e.Cancel("守望者护甲免疫熔岩伤害。")
				break
			}
		}
		return nil
	})
	edit := func(c *api.Context, e *api.Event, args []string, kind string) error {
		v := e.Player.Item(e.Player.Selected)
		if v == nil {
			c.Message(e, "请先选中一个物品。")
			return nil
		}
		if v.Meta == nil {
			v.Meta = &api.ItemMeta{}
		}
		switch kind {
		case "lore":
			if len(args) == 0 {
				c.Message(e, "用法：/itemlore 文本（追加一行），或 /itemlore clear")
				return nil
			}
			if args[0] == "clear" {
				v.Meta.Lore = nil
			} else {
				v.Meta.Lore = append(v.Meta.Lore, strings.Join(args, " "))
			}
		case "attr":
			if len(args) < 2 || len(args) > 3 {
				c.Message(e, "用法：/itemattr attack_damage|attack_speed|armor|max_health 数值 [mainhand|chest|...]")
				return nil
			}
			attr := map[string]string{"attack_damage": api.AttackDamage, "attack_speed": api.AttackSpeed, "armor": api.Armor, "max_health": api.MaxHealth}[args[0]]
			amount, err := strconv.ParseFloat(args[1], 64)
			if attr == "" || err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || math.Abs(amount) > 1000 {
				c.Message(e, "属性或数值无效。")
				return nil
			}
			slot := "mainhand"
			if len(args) == 3 {
				slot = args[2]
			}
			if !strings.Contains("|mainhand|offhand|head|chest|legs|feet|armor|any|", "|"+slot+"|") {
				c.Message(e, "装备栏无效。")
				return nil
			}
			id := "demo:edit_" + args[0]
			var attrs []api.AttributeModifier
			for _, a := range v.Meta.Attributes {
				if a.ID != id {
					attrs = append(attrs, a)
				}
			}
			v.Meta.Attributes = append(attrs, api.AttributeModifier{ID: id, Attribute: attr, Amount: amount, Operation: api.Add, Slot: slot})
		case "data":
			if len(args) < 2 || !strings.Contains(args[0], ":") {
				c.Message(e, "用法：/itemdata demo:lifesteal 0.5（支持 JSON 值）")
				return nil
			}
			value := strings.Join(args[1:], " ")
			var parsed any
			if json.Unmarshal([]byte(value), &parsed) != nil {
				parsed = value
			}
			if v.Meta.CustomData == nil {
				v.Meta.CustomData = map[string]any{}
			}
			v.Meta.CustomData[args[0]] = parsed
		}
		c.SetItemMeta(e, e.Player.Selected, v.Meta)
		return nil
	}
	for _, cmd := range []struct{ name, kind, description string }{{"itemlore", "lore", "修改手持物品的 Lore"}, {"itemattr", "attr", "修改手持物品属性"}, {"itemdata", "data", "修改手持物品的持久化数据"}} {
		kind := cmd.kind
		p.Command(api.Command{Name: cmd.name, Permission: "owner", Description: cmd.description}, func(c *api.Context, e *api.Event, args []string) error { return edit(c, e, args, kind) })
	}
	p.On(api.ItemMetaChanged, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		c.Message(e, fmt.Sprintf("第 %d 格物品元数据已更新。", int(e.Number("slot"))+1))
		return nil
	})
	return p
}

func main() {
	if err := newPlugin().Run(); err != nil {
		log.Fatal(err)
	}
}
