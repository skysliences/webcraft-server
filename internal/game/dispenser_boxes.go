package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"reflect"
)

const itemShulkerBox = 362
const blkShulkerBox = 176

func (s *Server) portableBox(v *Stack) (*Container, bool) {
	if v == nil || v.ID != itemShulkerBox && v.ID != blkShulkerBox || !s.validStack(v, s.rules.max(v.ID)) {
		return nil, false
	}
	slots := make(Stacks, 27)
	if v.Meta != nil && v.Meta.CustomData != nil {
		if raw, exists := v.Meta.CustomData[shulkerDataKey]; exists {
			text, ok := raw.(string)
			if !ok || json.Unmarshal([]byte(text), &slots) != nil || len(slots) != 27 {
				return nil, false
			}
		}
	}
	for _, item := range slots {
		if item != nil && (!s.validStack(item, s.rules.max(item.ID)) || item.ID == itemShulkerBox || item.ID == blkShulkerBox) {
			return nil, false
		}
	}
	return &Container{Kind: "chest", Slots: cloneStacks(slots), Item: sized(v, 1)}, true
}

func (s *Server) boxDrop(c *Container) *Stack {
	v := stack(itemShulkerBox, 1, 0)
	if c == nil {
		return v
	}
	if c.Item != nil {
		v = sized(c.Item, 1)
		v.ID = itemShulkerBox
	}
	if v.Meta != nil && v.Meta.CustomData != nil {
		delete(v.Meta.CustomData, shulkerDataKey)
	}
	has := false
	for _, item := range c.Slots {
		if item != nil {
			has = true
			break
		}
	}
	if has {
		raw, _ := json.Marshal(c.Slots)
		if v.Meta == nil {
			v.Meta = &api.ItemMeta{}
		}
		if v.Meta.CustomData == nil {
			v.Meta.CustomData = map[string]any{}
		}
		v.Meta.CustomData[shulkerDataKey] = string(raw)
		if c.Item == nil {
			v.Meta.Lore = []string{"内含物品"}
		}
	}
	return v
}

func (s *Server) prepareDispenserBox(r *Room, x, y, z int, one *Stack, data map[string]any) (*dispenserUtility, bool, bool) {
	if one.ID != itemShulkerBox && one.ID != blkShulkerBox {
		return nil, false, true
	}
	old := s.block(r, x, y, z)
	if old != 0 && !s.isWashable(old) {
		return nil, true, false
	}
	c, ok := s.portableBox(one)
	if !ok {
		return nil, true, false
	}
	k := blockKey(x, y, z)
	if r.Containers[k] != nil {
		return nil, true, false
	}
	context := utilityContext(data, "dispenser_shulker_box")
	context["contents"], context["destinationBefore"], context["destinationAfter"] = cloneStacks(c.Slots), make(Stacks, 27), cloneStacks(c.Slots)
	if !s.allowed(api.InventoryChanging, r, nil, context) {
		return nil, true, false
	}
	return &dispenserUtility{Behavior: "shulker_box", Edits: [][4]int{{x, y, z, blkShulkerBox}}, Validate: func() bool { return s.block(r, x, y, z) == old && r.Containers[k] == nil }, Commit: func() { r.Containers[k] = c }, Publish: func() { s.emit(api.InventoryChanged, r, nil, context) }}, true, true
}

func (s *Server) placePortableBox(p *Peer, x, y, z int, held *Stack) bool {
	r := p.room
	k := blockKey(x, y, z)
	slot := p.state.Inv.Selected
	original := copyStack(held)
	c, ok := s.portableBox(original)
	if !ok || r.Containers[k] != nil {
		return false
	}
	after := copyStack(original)
	if !s.isCreative(p) {
		after = sized(original, original.Count-1)
	}
	data := map[string]any{"reason": "place_shulker_box", "actor": p.sid, "source": "player", "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "slot": slot, "stack": itemSnapshot(sized(original, 1)), "before": itemSnapshot(original), "after": itemSnapshot(after), "contents": cloneStacks(c.Slots)}
	if !s.allowed(api.InventoryChanging, r, p, data) {
		return false
	}
	valid := func() bool {
		return p.state.Inv.Selected == slot && reflect.DeepEqual(p.state.Inv.Slots[slot], original) && r.Containers[k] == nil
	}
	if !s.setBlocksValidated(r, [][4]int{{x, y, z, blkShulkerBox}}, 0, "place_shulker_box", func() { p.state.Inv.Slots[slot] = after; r.Containers[k] = c }, data, valid) {
		return false
	}
	s.emit(api.InventoryChanged, r, p, data)
	s.inventory(p, 0, 0)
	return true
}
