package game

import (
	"encoding/json"
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"regexp"
	"unicode/utf8"
)

type Stack api.ItemStack
type Stacks []*Stack

func stack(id, count, damage int) *Stack { return &Stack{ID: id, Count: count, Damage: damage} }

// Old saves and clients use [id,count,damage]. Enchantments ({key: level})
// occupy index 3 and metadata index 4, matching the client's tuple layout.
func (v Stack) MarshalJSON() ([]byte, error) {
	if v.Meta == nil && len(v.Ench) == 0 {
		return json.Marshal([3]int{v.ID, v.Count, v.Damage})
	}
	var ench any
	if len(v.Ench) > 0 {
		ench = v.Ench
	}
	if v.Meta == nil {
		return json.Marshal([]any{v.ID, v.Count, v.Damage, ench})
	}
	return json.Marshal([]any{v.ID, v.Count, v.Damage, ench, v.Meta})
}
func (v *Stack) UnmarshalJSON(raw []byte) error {
	*v = Stack{}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return err
	}
	if len(parts) < 3 || len(parts) > 5 {
		return fmt.Errorf("invalid item tuple length %d", len(parts))
	}
	for i, p := range []*int{&v.ID, &v.Count, &v.Damage} {
		if err := json.Unmarshal(parts[i], p); err != nil {
			return err
		}
	}
	if len(parts) >= 4 && string(parts[3]) != "null" {
		if err := json.Unmarshal(parts[3], &v.Ench); err != nil {
			return fmt.Errorf("unsupported item enchantment data")
		}
		if !validEnch(v.Ench) {
			return fmt.Errorf("unsupported item enchantment data")
		}
	}
	if len(parts) == 5 {
		return json.Unmarshal(parts[4], &v.Meta)
	}
	return nil
}
func validEnch(e map[string]int) bool {
	if len(e) > 16 {
		return false
	}
	for k, l := range e {
		if len(k) == 0 || len(k) > 40 || l < 1 || l > 255 {
			return false
		}
	}
	return true
}
func itemSnapshot(v *Stack) *api.ItemStack { return (*api.ItemStack)(v).Clone() }
func legacyStack(v *Stack) []int {
	if v == nil {
		return nil
	}
	return []int{v.ID, v.Count, v.Damage}
}

var namespaceRE = regexp.MustCompile(`^[a-z0-9_.-]+:[a-z0-9_./-]+$`)

func validMeta(m *api.ItemMeta) bool {
	if m == nil {
		return true
	}
	if !utf8.ValidString(m.DisplayName) || len([]rune(m.DisplayName)) > 80 || len(m.Lore) > 32 || len(m.Attributes) > 32 || len(m.CustomData) > 64 {
		return false
	}
	for _, line := range m.Lore {
		if !utf8.ValidString(line) || len([]rune(line)) > 160 {
			return false
		}
	}
	seen := map[string]bool{}
	for _, a := range m.Attributes {
		if !namespaceRE.MatchString(a.ID) || len(a.ID) > 128 || seen[a.ID] || !number(a.Amount, -1000, 1000) {
			return false
		}
		seen[a.ID] = true
		switch a.Attribute {
		case api.AttackDamage, api.AttackSpeed, api.Armor, api.MaxHealth:
		default:
			return false
		}
		switch a.Operation {
		case api.Add, api.MultiplyBase, api.MultiplyTotal:
		default:
			return false
		}
		switch a.Slot {
		case "mainhand", "offhand", "head", "chest", "legs", "feet", "armor", "any":
		default:
			return false
		}
	}
	for k := range m.CustomData {
		if !namespaceRE.MatchString(k) || len(k) > 128 {
			return false
		}
	}
	// Portable inventories have a separate bounded payload. Each contained
	// item retains the ordinary metadata limit and nested boxes are refused.
	base := *m
	if contents, exists := m.CustomData[shulkerDataKey]; exists {
		text, ok := contents.(string)
		if !ok || len(text) > 512*1024 {
			return false
		}
		var slots Stacks
		if json.Unmarshal([]byte(text), &slots) != nil || len(slots) != 27 {
			return false
		}
		for _, v := range slots {
			if v != nil && v.Meta != nil {
				if _, nested := v.Meta.CustomData[shulkerDataKey]; nested {
					return false
				}
			}
			if v != nil && (v.ID <= 0 || v.ID == itemShulkerBox || v.ID == blkShulkerBox || v.Count < 1 || v.Count > 64 || v.Damage < 0 || !validMeta(v.Meta) || !validEnch(v.Ench)) {
				return false
			}
		}
		base.CustomData = map[string]any{}
		for k, v := range m.CustomData {
			if k != shulkerDataKey {
				base.CustomData[k] = v
			}
		}
	}
	raw, err := json.Marshal(&base)
	return err == nil && len(raw) <= 8192
}
func (s *Server) validStack(v *Stack, maxCount int) bool {
	if v == nil || s.rules.max(v.ID) == 0 || v.Count < 1 || v.Count > maxCount || v.Damage < 0 || !validMeta(v.Meta) || !validEnch(v.Ench) {
		return false
	}
	wear := s.rules.byID[v.ID].Durability
	return (wear == 0 && v.Damage == 0) || (wear > 0 && v.Damage < wear)
}
func (s *Server) setItemMeta(p *Peer, a api.Action) bool {
	if p == nil || p.state.Inv == nil || p.state.Dead || a.Slot < 0 || a.Slot >= 45 || a.Stack == nil || a.Expected == nil {
		return false
	}
	v := p.state.Inv.Slots[a.Slot]
	next := (*Stack)(a.Stack.Clone())
	if !s.validStack(next, s.rules.max(next.ID)) || !reflect.DeepEqual(itemSnapshot(v), a.Expected) || v.ID != next.ID || v.Count != next.Count || v.Damage != next.Damage {
		return false
	}
	data := map[string]any{"slot": a.Slot, "before": itemSnapshot(v), "after": itemSnapshot(next), "reason": "plugin.set_item_meta"}
	if !s.allowed(api.ItemMetaChanging, p.room, p, data) || !s.allowed(api.InventoryChanging, p.room, p, data) {
		return false
	}
	p.state.Inv.Slots[a.Slot] = copyStack(next)
	p.room.Dirty = true
	s.inventory(p, 0, 0)
	s.emit(api.ItemMetaChanged, p.room, p, data)
	s.emit(api.InventoryChanged, p.room, p, data)
	return true
}
