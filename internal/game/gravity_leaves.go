package game

import (
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"strings"
)

const (
	blkSand             = 8
	blkGravel           = 13
	blkGraveSand        = 181
	leafSupportDistance = 6
	leafCrownTickBudget = 64
	leafDecayTickBudget = 16
	leafDecayRetryTicks = 20
)

func isFallingBlock(b int) bool { return b == blkSand || b == blkGravel || b == blkGraveSand }
func isLog(b int) bool          { return b == blkOakLog || b == blkBirchLog || b == blkSpruceLog }
func isLeaves(b int) bool {
	return b == blkOakLeaves || b == blkBirchLeaves || b == blkSpruceLeaves || b == 171 || b == 172 || b == 173
}

// Registry names cover new species and rotated/stripped log variants.
func (s *Server) leafLog(b int) bool {
	name := s.nameOf(b)
	return isLog(b) || strings.HasSuffix(name, " Log") || strings.HasSuffix(name, " Wood") || strings.HasSuffix(name, " Stem") || strings.HasSuffix(name, " Hyphae")
}
func (s *Server) leafBlock(b int) bool {
	return isLeaves(b) || s.kind(b) == 2 && strings.HasSuffix(s.nameOf(b), " Leaves")
}
func (s *Server) persistentLeaf(r *Room, k uint64, b int) bool {
	if b == 171 || b == 172 || b == 173 || strings.Contains(s.fullName(b), "(Placed)") {
		return true
	}
	// Old player placements can use natural IDs. Saved authors distinguish
	// those decorations from generated/grown leaves without a format migration.
	author := r.Authors[k]
	return author.Token != "" && author.After == b
}

type leafDecayCandidate struct {
	key   uint64
	ready uint64
}

// Reconstruct from saved edits and loaded chunks; never explore unloaded forests.
type leafDecayRuntime struct {
	frontier   []uint64
	explored   map[uint64]bool
	candidates []leafDecayCandidate
	queued     map[uint64]bool
}

func (r *Room) leavesRT() *leafDecayRuntime {
	if r.rt().leafDecay == nil {
		r.rt().leafDecay = &leafDecayRuntime{explored: map[uint64]bool{}, queued: map[uint64]bool{}}
	}
	return r.rt().leafDecay
}
func (s *Server) queueLeafCrown(r *Room, k uint64) {
	rt := r.leavesRT()
	if len(rt.frontier) == 0 {
		rt.explored = map[uint64]bool{}
	}
	if !rt.explored[k] {
		rt.explored[k] = true
		rt.frontier = append(rt.frontier, k)
	}
}
func (s *Server) queueLeafCheck(r *Room, k uint64, ready uint64) {
	rt := r.leavesRT()
	if !rt.queued[k] {
		rt.queued[k] = true
		rt.candidates = append(rt.candidates, leafDecayCandidate{k, ready})
	}
}

// Every committed native/SDK block change can schedule the same decay rules,
// even when neighbouring physics updates were intentionally suppressed.
func (s *Server) queueLeafChange(r *Room, k uint64, before, after int) {
	if !r.usesSurvival() || before == after || !(s.leafLog(before) || s.leafBlock(before) || s.leafLog(after) || s.leafBlock(after)) {
		return
	}
	r.leavesRT().explored = map[uint64]bool{}
	s.queueLeafCrown(r, k)
}

// Missing chunks are unknown, not air: do not decay a leaf whose supporting
// trunk may still be in an unloaded neighbouring canonical chunk.
func (s *Server) leafBlockAt(r *Room, x, y, z int) (int, bool) {
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return 0, true
	}
	k := blockKey(x, y, z)
	if b, ok := r.Edits[k]; ok {
		return b, true
	}
	if r.Infinite {
		v := r.chunks[chunkCoord(x, z)]
		if v == nil {
			return 0, false
		}
		return int(v.Base[chunkIndex(x, y, z)]), true
	}
	// Storage-only loaders do not initialize the terrain engine. Their edits
	// are authoritative, while unedited base terrain remains unknown.
	if s.engine == nil {
		return 0, false
	}
	return s.block(r, x, y, z), true
}
func (s *Server) leafSupport(r *Room, x, y, z int) (supported, complete bool) {
	visited := map[[3]int]bool{{x, y, z}: true}
	queue := [][4]int{{x, y, z, 0}}
	complete = true
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		b, known := s.leafBlockAt(r, at[0], at[1], at[2])
		if !known {
			complete = false
			continue
		}
		if s.leafLog(b) {
			return true, true
		}
		if !s.leafBlock(b) || at[3] == leafSupportDistance {
			continue
		}
		for _, d := range faceOffsets {
			pos := [3]int{at[0] + d[0], at[1] + d[1], at[2] + d[2]}
			if !visited[pos] {
				visited[pos] = true
				queue = append(queue, [4]int{pos[0], pos[1], pos[2], at[3] + 1})
			}
		}
	}
	return false, complete
}
func (s *Server) hasLogNearby(r *Room, x, y, z int) bool {
	supported, complete := s.leafSupport(r, x, y, z)
	return supported || !complete
}
func (s *Server) restoreLeafDecay(r *Room) {
	if !r.usesSurvival() {
		return
	}
	for k, b := range r.Edits {
		// A cleared generated trunk is saved as an air edit while its crown is
		// still in the finite engine's base terrain rather than the edit map.
		if s.leafBlock(b) || s.leafLog(b) {
			s.queueLeafCrown(r, k)
		} else if b == 0 {
			// Excavated terrain can contain millions of unrelated air edits.
			// Only crown-adjacent holes belong in the bounded decay frontier.
			x, y, z := coords(k)
			for _, d := range faceOffsets {
				n, known := s.leafBlockAt(r, x+d[0], y+d[1], z+d[2])
				if known && (s.leafBlock(n) || s.leafLog(n)) {
					s.queueLeafCrown(r, k)
					break
				}
			}
		}
	}
}
func (s *Server) restoreChunkLeafDecay(r *Room, c ChunkCoord, v *WorldChunk) {
	if !r.usesSurvival() || v == nil {
		return
	}
	for i, raw := range v.Base {
		k := chunkKey(c, i)
		b := int(raw)
		if edited, ok := r.Edits[k]; ok {
			b = edited
		}
		if s.leafBlock(b) {
			s.queueLeafCrown(r, k)
		}
	}
}

// Independent vanilla-style sapling, stick and oak apple rolls.
func (s *Server) leafDecayDrops(b int) Stacks {
	var out Stacks
	add := func(name string, count int) {
		if id := s.id(name); id > 0 {
			out = append(out, stack(id, count, 0))
		}
	}
	if s.rng.Intn(20) == 0 {
		add(strings.TrimSuffix(s.nameOf(b), "Leaves")+"Sapling", 1)
	}
	if s.rng.Intn(50) == 0 {
		add("Stick", 1+s.rng.Intn(2))
	}
	if s.nameOf(b) == "Oak Leaves" && s.rng.Intn(200) == 0 {
		add("Apple", 1)
	}
	return out
}
func leafDropSnapshots(stacks Stacks) []*api.ItemStack {
	items := make([]*api.ItemStack, 0, len(stacks))
	for _, v := range stacks {
		items = append(items, itemSnapshot(v))
	}
	return items
}
func leafBlockSnapshot(r *Room, k uint64) *BlockData {
	if r.Ext == nil || r.Ext.Blocks[k] == nil {
		return nil
	}
	value := *r.Ext.Blocks[k]
	value.Redstone = cloneRedstone(value.Redstone)
	return &value
}

// Prepare loot before the protected block transaction. A failed/cancelled
// preparation preserves the leaf and every item's complete metadata.
func (s *Server) decayLeafWithDrops(r *Room, k uint64, b int, stacks Stacks) (committed bool) {
	x, y, z := coords(k)
	if current, known := s.leafBlockAt(r, x, y, z); !known || current != b || !s.leafBlock(b) || s.persistentLeaf(r, k, b) {
		return false
	}
	if supported, complete := s.leafSupport(r, x, y, z); supported || !complete {
		return false
	}
	beforeData, beforeAuthor := leafBlockSnapshot(r, k), r.Authors[k]
	leaf := itemSnapshot(stack(b, 1, 0))
	data := map[string]any{"reason": "leaf_decay", "actor": "server", "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}, "x": x, "y": y, "z": z, "oldBlock": b, "block": 0, "stack": leaf.Clone(), "leaf": leaf.Clone(), "blockData": beforeData, "supportDistance": leafSupportDistance, "drops": leafDropSnapshots(stacks)}
	if !s.allowed(api.LeavesDecaying, r, nil, data) {
		return false
	}
	previousID := r.NextDropID
	defer func() {
		if !committed {
			r.NextDropID = previousID
		}
	}()
	dropContext := map[string]any{"actor": "server", "target": data["target"], "leaf": leaf.Clone(), "blockData": beforeData, "supportDistance": leafSupportDistance}
	prepared, err := s.prepareDropsWithContext(r, nil, stacks, float64(x)+.5, float64(y)+.5, float64(z)+.5, "leaf_decay", dropContext, true)
	if err != nil {
		return false
	}
	// Plugin actions are queued; compare snapshots to protect stale callers too.
	current, known := s.leafBlockAt(r, x, y, z)
	if !known || current != b || beforeAuthor != r.Authors[k] || !reflect.DeepEqual(beforeData, leafBlockSnapshot(r, k)) || len(r.Drops)+len(prepared) > maxWorldDrops {
		return false
	}
	actual := Stacks{}
	for _, d := range prepared {
		if !s.validDrop(d) || r.Drops[d.ID] != nil || r.Mobs[d.ID] != nil {
			return false
		}
		actual = append(actual, copyStack(d.Stack))
	}
	data["drops"] = leafDropSnapshots(actual)
	if !s.setBlocksWithContext(r, [][4]int{{x, y, z, 0}}, 0, "leaf_decay", func() { s.publishDropsWithContext(r, nil, prepared, "leaf_decay", dropContext) }, data) {
		return false
	}
	committed = true
	s.emit(api.LeavesDecayed, r, nil, data)
	return true
}
func (s *Server) leafDecayTick(r *Room) {
	rt := r.rt().leafDecay
	if rt == nil {
		return
	}
	for budget := leafCrownTickBudget; budget > 0 && len(rt.frontier) > 0; budget-- {
		k := rt.frontier[0]
		rt.frontier = rt.frontier[1:]
		x, y, z := coords(k)
		b, known := s.leafBlockAt(r, x, y, z)
		if !known {
			continue
		}
		if s.leafBlock(b) && !s.persistentLeaf(r, k, b) {
			s.queueLeafCheck(r, k, r.rt().tickCount)
		}
		for _, d := range faceOffsets {
			nx, ny, nz := x+d[0], y+d[1], z+d[2]
			nb, found := s.leafBlockAt(r, nx, ny, nz)
			if found && (s.leafBlock(nb) || s.leafLog(nb)) {
				nk := blockKey(nx, ny, nz)
				if !rt.explored[nk] {
					rt.explored[nk] = true
					rt.frontier = append(rt.frontier, nk)
				}
			}
		}
	}
	for budget := min(leafDecayTickBudget, len(rt.candidates)); budget > 0; budget-- {
		candidate := rt.candidates[0]
		rt.candidates = rt.candidates[1:]
		delete(rt.queued, candidate.key)
		if candidate.ready > r.rt().tickCount {
			s.queueLeafCheck(r, candidate.key, candidate.ready)
			continue
		}
		x, y, z := coords(candidate.key)
		b, known := s.leafBlockAt(r, x, y, z)
		if known && (!s.leafBlock(b) || s.persistentLeaf(r, candidate.key, b)) {
			continue
		}
		if !known {
			s.queueLeafCheck(r, candidate.key, r.rt().tickCount+leafDecayRetryTicks)
			continue
		}
		supported, complete := s.leafSupport(r, x, y, z)
		if supported {
			continue
		}
		if !complete {
			s.queueLeafCheck(r, candidate.key, r.rt().tickCount+leafDecayRetryTicks)
			continue
		}
		var drops Stacks
		if s.gameRule(r, "doTileDrops") {
			drops = s.leafDecayDrops(b)
		}
		if !s.decayLeafWithDrops(r, candidate.key, b, drops) {
			s.queueLeafCheck(r, candidate.key, r.rt().tickCount+leafDecayRetryTicks)
		}
	}
}
func (s *Server) checkFalling(r *Room, x, y, z int, blocks ...int) {
	b := s.block(r, x, y, z)
	if len(blocks) > 0 {
		b = blocks[0]
	}
	if !isFallingBlock(b) || y <= 0 {
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
	if targetY != y {
		s.setBlocks(r, [][4]int{{x, y, z, 0}, {x, targetY, z, b}}, setQuiet, "gravity")
	}
}
func init() {
	onBlockUpdate(func(s *Server, r *Room, x, y, z, b int) {
		if !r.usesSurvival() {
			return
		}
		if r.Mode == "survival" && isFallingBlock(b) {
			s.checkFalling(r, x, y, z, b)
		}
		if s.leafBlock(b) || s.leafLog(b) {
			k := blockKey(x, y, z)
			s.queueLeafCrown(r, k)
			if s.leafBlock(b) && !s.persistentLeaf(r, k, b) {
				s.queueLeafCheck(r, k, r.rt().tickCount)
			}
		}
	})
	onBreak(func(s *Server, p *Peer, x, y, z, old int) {
		s.queueLeafChange(p.room, blockKey(x, y, z), old, 0)
	})
	onAfterPlace(func(s *Server, p *Peer, x, y, z, placed int, m Message) {
		if isFallingBlock(placed) {
			s.checkFalling(p.room, x, y, z, placed)
		}
	})
	onRoomTick(func(s *Server, r *Room) {
		if !r.usesSurvival() || len(r.Peers) == 0 {
			return
		}
		s.leafDecayTick(r)
		if r.Mode != "survival" || r.rt().tickCount%10 != 0 {
			return
		}
		// Existing cactus random ticks stay independent of leaf decay.
		for p := range r.Peers {
			if p.state == nil || p.state.Dead {
				continue
			}
			px, py, pz := int(p.state.X), int(p.state.Y), int(p.state.Z)
			for i := 0; i < 4; i++ {
				rx, rz, ry := px+s.rng.Intn(32)-16, pz+s.rng.Intn(32)-16, py+s.rng.Intn(16)-8
				if !r.inside(rx, rz) || ry < 1 || ry >= 127 {
					continue
				}
				b, cactusID := s.block(r, rx, ry, rz), s.id("Cactus")
				if cactusID <= 0 || b != cactusID {
					continue
				}
				h := 1
				for ry-h >= 0 && s.block(r, rx, ry-h, rz) == cactusID {
					h++
				}
				if h < 3 && ry+1 < 128 && s.block(r, rx, ry+1, rz) == 0 {
					blocked := false
					for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
						if s.solidAt(r, rx+d[0], ry+1, rz+d[1]) {
							blocked = true
							break
						}
					}
					if !blocked && s.rng.Intn(3) == 0 {
						s.setBlock(r, rx, ry+1, rz, cactusID, setQuiet)
					}
				}
			}
		}
	})
}
