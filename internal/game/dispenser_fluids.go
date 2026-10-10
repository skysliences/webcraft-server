package game

import api "mcraft/backend/pkg/plugin"

type dispenserFluid struct {
	Behavior string
	Result   *Stack
	Edits    [][4]int
	Context  map[string]any
	Validate func() bool
	Publish  func()
}

// Failed bucket/bottle use follows the ordinary ejection path. Successful
// transformations retain metadata, including when the return item overflows.
func (s *Server) prepareDispenserFluid(r *Room, x, y, z int, one *Stack, context map[string]any) (*dispenserFluid, bool, bool) {
	target := s.block(r, x, y, z)
	result, fluid, change := 0, 0, 0
	before, after, behavior := "", "", ""
	edits := [][4]int{}
	switch one.ID {
	case itemWaterBucket, 308:
		if target != 0 && !s.isWashable(target) {
			return nil, false, true
		}
		fluid = blkWaterSource
		if one.ID == 308 {
			fluid = blkLavaSource
		}
		result, change, before, after, behavior = itemBucket, fluid, api.BucketEmptying, api.BucketEmptied, "bucket_empty"
		if fluid != blkWaterSource || r.dimName() != "nether" {
			edits = append(edits, [4]int{x, y, z, change})
		}
	case itemBucket:
		if target == blkWaterSource {
			result = itemWaterBucket
		} else if target == blkLavaSource {
			result = 308
		} else {
			return nil, false, true
		}
		fluid, before, after, behavior = target, api.BucketFilling, api.BucketFilled, "bucket_fill"
		edits = append(edits, [4]int{x, y, z, 0})
	case itemGlassBottle:
		if !isWater(target) {
			return nil, false, true
		}
		result, fluid, before, after, behavior = itemWaterBottle, target, api.BottleFilling, api.BottleFilled, "bottle_fill"
	default:
		return nil, false, true
	}
	out := copyStack(one)
	out.ID, out.Damage = result, 0
	if !s.validStack(out, s.rules.max(out.ID)) {
		return nil, true, false
	}
	data := dispenserFireContext(context)
	data["reason"], data["behavior"], data["fluid"] = "dispenser_"+behavior, behavior, fluid
	data["x"], data["y"], data["z"], data["result"] = x, y, z, itemSnapshot(out)
	data["evaporated"] = one.ID == itemWaterBucket && r.dimName() == "nether"
	if slots, ok := context["containerAfter"].(Stacks); ok {
		next := cloneStacks(slots)
		slot, _ := context["slot"].(int)
		if slot < 0 || slot >= len(next) {
			return nil, true, false
		}
		if next[slot] == nil {
			next[slot] = copyStack(out)
		} else if !s.insertDispensed(next, out, nil) {
			data["overflow"] = itemSnapshot(out)
		}
		data["after"], data["containerAfter"] = itemSnapshot(next[slot]), cloneStacks(next)
	}
	if !s.allowed(before, r, nil, data) {
		return nil, true, false
	}
	return &dispenserFluid{Behavior: behavior, Result: out, Edits: edits, Context: data, Validate: func() bool { return s.block(r, x, y, z) == target }, Publish: func() {
		s.emit(after, r, nil, data)
		if one.ID == itemGlassBottle {
			soundAt(r, "item.bottle.fill", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
		}
		if data["evaporated"] == true {
			soundAt(r, "block.fire.extinguish", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
		}
	}}, true, true
}
