package game

import (
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	api "mcraft/backend/pkg/plugin"
)

const torchBlock = 82

func isTorch(b int) bool       { return b >= torchBlock && b <= 86 }
func creativeBlock(b int) bool { return b >= 0 && b <= 67 || isTorch(b) }
func creativeItem(b int) bool  { return b >= 0 && b <= 67 || b == torchBlock }

var torchOffsets = [5][3]int{{0, -1, 0}, {0, 0, 1}, {0, 0, -1}, {1, 0, 0}, {-1, 0, 0}}

func (s *Server) supportsTorch(b int) bool {
	if b < 0 || b >= len(s.rules.Kinds) {
		return false
	}
	k := s.rules.Kinds[b]
	return k == 1 || k == 2 || k == 3
}
func (s *Server) torchSupported(r *Room, key uint64, b int) bool {
	if !isTorch(b) {
		return true
	}
	x, y, z := coords(key)
	off := torchOffsets[b-torchBlock]
	return s.supportsTorch(s.block(r, x+off[0], y+off[1], z+off[2]))
}
func torchForFace(face int) int {
	if face == 1 {
		return torchBlock
	}
	if face >= 2 && face <= 5 {
		return 81 + face
	}
	return 0
}

type torchChange struct {
	key   uint64
	drops []*ItemDrop
	data  map[string]any
}

// Reserve all affected drops before changing the supporting block. A full
// entity budget or a cancelled physics event leaves the whole edit intact.
func (s *Server) prepareTorchChanges(r *Room, p *Peer, key uint64, b, reserved int) ([]torchChange, error) {
	if s.supportsTorch(b) {
		return nil, nil
	}
	x, y, z := coords(key)
	var changes []torchChange
	for _, off := range torchOffsets {
		tx, ty, tz := x-off[0], y-off[1], z-off[2]
		if !r.inside(tx, tz) || ty < 1 || ty >= 128 {
			continue
		}
		id := s.block(r, tx, ty, tz)
		attached := isTorch(id) && torchOffsets[id-torchBlock] == off
		if id == 109 {
			face := 1
			if state := redstoneSnapshot(r, blockKey(tx, ty, tz)); state != nil && state.SupportFace >= 1 && state.SupportFace <= 5 {
				face = state.SupportFace
			}
			attached = faceOffsets[face] == [3]int{-off[0], -off[1], -off[2]}
		}
		if !attached {
			continue
		}
		child := blockKey(tx, ty, tz)
		data := s.blockData(r, child, 0)
		data["reason"] = "support_lost"
		if id == 109 {
			data = s.redstoneBlockChangeData(r, p, child, 0, "redstone_support_lost")
			if !s.allowed(api.BlockPhysicsChanging, r, p, data) || !s.allowed(api.RedstoneComponentChanging, r, p, data) {
				return nil, fmt.Errorf("插件阻止了红石火把失去支撑，操作未提交。")
			}
		}
		if !s.allowed(api.BlockBreaking, r, p, data) {
			return nil, fmt.Errorf("插件阻止了火把失去支撑，操作未提交。")
		}
		change := torchChange{key: child}
		if id == 109 {
			change.data = data
		}
		changes = append(changes, change)
	}
	if !r.usesSurvival() {
		return changes, nil
	}
	if len(r.Drops)+reserved+len(changes) > maxWorldDrops {
		return nil, fmt.Errorf("世界掉落物过多，请先拾取附近的物品。")
	}
	for i := range changes {
		tx, ty, tz := coords(changes[i].key)
		item := torchBlock
		if s.block(r, tx, ty, tz) == 109 {
			item = 355 // Redstone torch item, independent of its support face.
		}
		drops, err := s.prepareDrops(r, p, Stacks{stack(item, 1, 0)}, float64(tx)+.5, float64(ty)+.35, float64(tz)+.5, "support_lost")
		if err != nil {
			return nil, err
		}
		changes[i].drops = drops
	}
	extra, err := s.prepareMechanicSupportChanges(r, p, key, b, reserved+len(changes))
	if err != nil {
		return nil, err
	}
	changes = append(changes, extra...)
	return changes, nil
}
func (s *Server) applyTorchChanges(r *Room, p *Peer, changes []torchChange) {
	for _, change := range changes {
		if change.data != nil {
			for _, d := range change.drops {
				r.Drops[d.ID] = d
			}
			if p != nil {
				s.commitPlayerBlock(p, change.key, 0, change.data)
			} else {
				r.Edits[change.key] = 0
				delete(r.Authors, change.key)
				s.trackRedstoneBlock(r, change.key, 0)
				s.trackMechanicBlock(r, change.key, 0)
				r.Dirty = true
				r.rt().redstoneDirty = true
				s.broadcastChunk(r, change.key, 0)
				s.blockEvents(r, nil, change.data)
				if _, ok := change.data["mechanicAfter"]; ok {
					s.broadcastMechanic(r, change.key)
				}
				if _, ok := change.data["railAfter"]; ok {
					s.broadcastRail(r, change.key)
				}
			}
			if _, ok := change.data["componentAfter"]; ok {
				s.broadcastRedstone(r, change.key)
			}
			s.emit(api.BlockPhysicsChanged, r, p, change.data)
			s.publishDrops(r, p, change.drops, "mechanic_support_lost")
			continue
		}
		if p != nil {
			s.editReason(p, change.key, 0, "support_lost")
		} else {
			data := s.blockData(r, change.key, 0)
			data["reason"] = "support_lost"
			r.Edits[change.key] = 0
			delete(r.Authors, change.key)
			r.Dirty = true
			if r.usesSurvival() {
				s.broadcastChunk(r, change.key, 0)
			}
			s.blockEvents(r, nil, data)
		}
		if r.Mode == "creative" {
			packet := binary.LittleEndian.AppendUint32([]byte{3}, toWireKey(change.key))
			packet = append(packet, 0)
			if p != nil {
				p.enqueue(websocket.BinaryMessage, packet)
			} else {
				for peer := range r.Peers {
					peer.enqueue(websocket.BinaryMessage, packet)
				}
			}
		} else {
			s.publishDrops(r, p, change.drops, "support_lost")
		}
	}
}
