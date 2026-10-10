package plugin

import "encoding/json"

// ItemStack is a complete item snapshot. It is independent from the host's
// compact inventory protocol; changing a snapshot never changes the world.
type ItemStack struct {
	ID     int       `json:"id"`
	Count  int       `json:"count"`
	Damage int       `json:"damage"`
	Meta   *ItemMeta `json:"meta,omitempty"`
	// Ench maps enchantment keys (sharpness, power...) to levels.
	Ench map[string]int `json:"ench,omitempty"`
}

type ItemMeta struct {
	DisplayName string              `json:"displayName,omitempty"`
	Lore        []string            `json:"lore,omitempty"`
	Attributes  []AttributeModifier `json:"attributes,omitempty"`
	CustomData  map[string]any      `json:"customData,omitempty"`
	Unbreakable bool                `json:"unbreakable,omitempty"`
}

type AttributeModifier struct {
	ID        string  `json:"id"` // namespaced, unique across equipped items
	Attribute string  `json:"attribute"`
	Amount    float64 `json:"amount"`
	Operation string  `json:"operation"` // add, multiply_base, multiply_total
	Slot      string  `json:"slot"`      // mainhand, offhand, head, chest, legs, feet, armor, any
}

const (
	AttackDamage  = "minecraft:attack_damage"
	AttackSpeed   = "minecraft:attack_speed"
	Armor         = "minecraft:armor"
	MaxHealth     = "minecraft:max_health"
	Add           = "add"
	MultiplyBase  = "multiply_base"
	MultiplyTotal = "multiply_total"
)

func (v *ItemStack) Clone() *ItemStack {
	if v == nil {
		return nil
	}
	out := *v
	if v.Meta != nil {
		meta := *v.Meta
		meta.Lore = append([]string(nil), v.Meta.Lore...)
		meta.Attributes = append([]AttributeModifier(nil), v.Meta.Attributes...)
		if v.Meta.CustomData != nil {
			meta.CustomData = map[string]any{}
			for k, value := range v.Meta.CustomData {
				meta.CustomData[k] = cloneCustomValue(value)
			}
		}
		out.Meta = &meta
	}
	if v.Ench != nil {
		out.Ench = map[string]int{}
		for k, l := range v.Ench {
			out.Ench[k] = l
		}
	}
	return &out
}
func cloneCustomValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			out[k] = cloneCustomValue(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = cloneCustomValue(x)
		}
		return out
	default:
		// Preserve invalid scalar values so the host rejects them instead of
		// silently erasing metadata. Valid JSON containers are copied deeply.
		raw, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var out any
		if json.Unmarshal(raw, &out) != nil {
			return v
		}
		return out
	}
}

// Item returns a complete snapshot, including Lore and custom data. The legacy
// Inventory tuples remain available so existing API v1 plugins keep working.
func (p *Player) Item(slot int) *ItemStack {
	if slot < 0 {
		return nil
	}
	if slot < len(p.Items) {
		return p.Items[slot].Clone()
	}
	if slot < len(p.Inventory) && len(p.Inventory[slot]) >= 3 {
		v := p.Inventory[slot]
		return &ItemStack{ID: v[0], Count: v[1], Damage: v[2]}
	}
	return nil
}

// Item reads an item in event data (stack, before, after, result, input),
// accepting both API objects and the legacy compact tuple representation.
func (e *Event) Item(key string) *ItemStack {
	raw, err := json.Marshal(e.Data[key])
	if err != nil || string(raw) == "null" {
		return nil
	}
	var item ItemStack
	if len(raw) > 0 && raw[0] == '[' {
		var parts []json.RawMessage
		if json.Unmarshal(raw, &parts) != nil || len(parts) < 3 {
			return nil
		}
		for i, p := range []*int{&item.ID, &item.Count, &item.Damage} {
			if json.Unmarshal(parts[i], p) != nil {
				return nil
			}
		}
		if len(parts) > 3 && string(parts[3]) != "null" && json.Unmarshal(parts[3], &item.Ench) != nil {
			return nil
		}
		if len(parts) > 4 && json.Unmarshal(parts[4], &item.Meta) != nil {
			return nil
		}
	} else if json.Unmarshal(raw, &item) != nil {
		return nil
	}
	return &item
}

func (c *Context) GiveStack(e *Event, stack *ItemStack) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "give_stack", WorldID: e.World.ID, PlayerID: e.Player.ID, Stack: stack.Clone()})
	}
}
func (c *Context) DropStack(world string, stack *ItemStack, x, y, z float64) {
	c.Actions = append(c.Actions, Action{Type: "drop_stack", WorldID: world, Stack: stack.Clone(), X: x, Y: y, Z: z})
}

// SetItemMeta uses compare-and-set: a deferred action cannot overwrite an item
// that moved, was consumed, or wore down since this event's snapshot.
func (c *Context) SetItemMeta(e *Event, slot int, meta *ItemMeta) {
	if e.Player != nil && e.World != nil {
		before := e.Player.Item(slot)
		if before != nil {
			after := before.Clone()
			after.Meta = meta
			c.Actions = append(c.Actions, Action{Type: "set_item_meta", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: slot, Stack: after.Clone(), Expected: before})
		}
	}
}

// DamagePlayer also goes through cancellable damage and death events.
func (c *Context) DamagePlayer(world string, player uint16, amount float64, cause string) {
	c.Actions = append(c.Actions, Action{Type: "damage_player", WorldID: world, PlayerID: player, Amount: amount, Reason: cause})
}
func (c *Context) DamageEntity(world string, entity uint32, amount float64, cause string) {
	c.Actions = append(c.Actions, Action{Type: "damage_entity", WorldID: world, EntityID: entity, Amount: amount, Reason: cause})
}

// UseSpawnEgg uses the selected inventory slot against a reachable block face.
// The expected snapshot prevents deferred actions from consuming an item that
// moved or changed. Lock, game mode, spawn protection and inventory events apply.
func (c *Context) UseSpawnEgg(e *Event, slot, x, y, z, face int) {
	if e.Player != nil && e.World != nil {
		if before := e.Player.Item(slot); before != nil {
			c.Actions = append(c.Actions, Action{Type: "use_spawn_egg", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: slot, X: float64(x), Y: float64(y), Z: float64(z), Face: face, Expected: before})
		}
	}
}

// UseRedstone invokes the same block-face interaction or selected-item
// placement as a player, including permission and snapshot checks.
func (c *Context) UseRedstone(e *Event, x, y, z, face int) {
	if e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "use_redstone", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: e.Player.Selected, X: float64(x), Y: float64(y), Z: float64(z), Face: face, Expected: e.Player.Item(e.Player.Selected)})
	}
}

// PrimeTNT invokes protected TNT priming at a reachable block. The selected
// item snapshot and ordinary building permissions are revalidated by the host.
// A zero fuseTicks requests the default fuse; listeners may change only the
// catalogued fuseTicks field before the host validates and commits it.
func (c *Context) PrimeTNT(e *Event, x, y, z, fuseTicks int) {
	if e != nil && e.Player != nil && e.World != nil {
		c.Actions = append(c.Actions, Action{Type: "prime_tnt", WorldID: e.World.ID, PlayerID: e.Player.ID, Slot: e.Player.Selected, X: float64(x), Y: float64(y), Z: float64(z), FuseTicks: fuseTicks, Expected: e.Player.Item(e.Player.Selected)})
	}
}
