package game

import (
	"fmt"
	api "mcraft/backend/pkg/plugin"
)

func (s *Server) mechanicSupportChildren(r *Room, key uint64, b int) [][4]int {
	if s.supportsTorch(b) || baseName(s.rules.Names[b]) == "Chest" {
		return nil
	}
	x, y, z := coords(key)
	var children [][4]int
	for face := 0; face <= 5; face++ {
		off := faceOffsets[face]
		cx, cy, cz := x+off[0], y+off[1], z+off[2]
		if !r.inside(cx, cz) || cy < 1 || cy >= 128 {
			continue
		}
		id := s.block(r, cx, cy, cz)
		child := blockKey(cx, cy, cz)
		supported := face == 1 && (isRailBlock(id) || id == blkDaylightDetector)
		kind := s.redstoneKind(id)
		if kind == "wire" || kind == "repeater" || kind == "comparator" || kind == "stone_pressure_plate" || kind == "oak_pressure_plate" {
			supported = face == 1
		}
		if kind == "lever" || kind == "button" || kind == "wooden_button" {
			support := 1
			if state := redstoneSnapshot(r, child); state != nil {
				support = state.SupportFace
			}
			supported = support == face
		}
		if id == blkSign {
			v := mechanicSnapshot(r, child)
			support := 1
			if v != nil {
				support = v.SupportFace
			}
			supported = support == face
		}
		if supported {
			children = append(children, [4]int{cx, cy, cz, 0})
		}
	}
	return children
}
func (s *Server) prepareMechanicSupportChanges(r *Room, p *Peer, key uint64, b, reserved int) ([]torchChange, error) {
	var changes []torchChange
	nextID := r.NextDropID
	success := false
	defer func() {
		if !success {
			r.NextDropID = nextID
		}
	}()
	for _, at := range s.mechanicSupportChildren(r, key, b) {
		k := blockKey(at[0], at[1], at[2])
		data := s.mechanicBlockChangeData(r, p, k, 0, "mechanic_support_lost")
		if component := s.redstoneBlockChangeData(r, p, k, 0, "redstone_support_lost"); component != nil {
			data = component
		}
		if _, ok := data["componentAfter"]; ok && !s.allowed(api.RedstoneComponentChanging, r, p, data) {
			return nil, fmt.Errorf("插件阻止了红石元件失去支撑，操作未提交。")
		}
		if !s.allowed(api.BlockPhysicsChanging, r, p, data) || !s.allowed(api.BlockBreaking, r, p, data) || !s.mechanicBlockBefore(r, p, data) {
			return nil, fmt.Errorf("插件阻止了方块失去支撑，操作未提交。")
		}
		var drops []*ItemDrop
		if r.usesSurvival() && s.gameRule(r, "doTileDrops") {
			stacks := s.dropsAt(r, k, s.block(r, at[0], at[1], at[2]), 0)
			if len(r.Drops)+reserved+len(changes)+len(stacks) > maxWorldDrops {
				return nil, fmt.Errorf("世界掉落物过多")
			}
			var err error
			drops, err = s.prepareDropsWithContext(r, p, stacks, float64(at[0])+.5, float64(at[1])+.35, float64(at[2])+.5, "mechanic_support_lost", map[string]any{"actor": data["actor"], "source": "physics", "target": data["target"]}, true)
			if err != nil {
				return nil, err
			}
		}
		changes = append(changes, torchChange{key: k, drops: drops, data: data})
	}
	success = true
	return changes, nil
}
