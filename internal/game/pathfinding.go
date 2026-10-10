package game

import (
	"container/heap"
	"math"
)

// VoxelPos represents a discrete 3D block coordinate.
type VoxelPos struct {
	X, Y, Z int
}

type aStarItem struct {
	pos    VoxelPos
	gCost  float64
	fCost  float64
	parent *aStarItem
	index  int
}

type priorityQueue []*aStarItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].fCost < pq[j].fCost }
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*aStarItem)
	item.index = n
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

// findVoxelPath performs lightweight 3D A* pathfinding on the voxel grid.
// It avoids hazards (fire, lava, deep water, cliffs) and navigates around walls.
func (s *Server) findVoxelPath(r *Room, m *Mob, sx, sy, sz, tx, ty, tz int) []VoxelPos {
	if r == nil || m == nil {
		return nil
	}

	// Ensure starting on solid surface if floating slightly
	for sy > 1 && !s.mobSolid(r, sx, sy-1, sz) {
		sy--
	}
	for ty > 1 && !s.mobSolid(r, tx, ty-1, tz) {
		ty--
	}

	start := VoxelPos{sx, sy, sz}
	target := VoxelPos{tx, ty, tz}
	if start == target {
		return nil
	}

	dist := math.Hypot(float64(tx-sx), float64(tz-sz))
	if dist > 28 {
		return nil
	}

	hFunc := func(pos VoxelPos) float64 {
		dx := float64(target.X - pos.X)
		dy := float64(target.Y - pos.Y)
		dz := float64(target.Z - pos.Z)
		return math.Hypot(dx, dz) + math.Abs(dy)*1.3
	}

	pq := &priorityQueue{}
	heap.Init(pq)
	startNode := &aStarItem{pos: start, gCost: 0, fCost: hFunc(start)}
	heap.Push(pq, startNode)

	visited := map[VoxelPos]bool{start: true}
	bestCost := map[VoxelPos]float64{start: 0}

	var bestNode *aStarItem = startNode
	bestH := hFunc(start)

	iterations := 0
	maxIterations := 96

	dirs := [4][2]int{
		{1, 0},
		{-1, 0},
		{0, 1},
		{0, -1},
	}

	for pq.Len() > 0 && iterations < maxIterations {
		iterations++
		curr := heap.Pop(pq).(*aStarItem)

		hCurr := hFunc(curr.pos)
		if hCurr < bestH {
			bestH = hCurr
			bestNode = curr
		}

		// Close enough to target
		if curr.pos.X == target.X && curr.pos.Z == target.Z && math.Abs(float64(curr.pos.Y-target.Y)) <= 1.5 {
			bestNode = curr
			break
		}

		// Explore 4 horizontal neighbors
		for _, d := range dirs {
			nx, nz := curr.pos.X+d[0], curr.pos.Z+d[1]
			if !r.inside(nx, nz) {
				continue
			}

			// Try stepping flat (dy=0), stepping up (dy=+1), or stepping down (dy=-1, -2, -3)
			yCandidates := []int{curr.pos.Y, curr.pos.Y + 1, curr.pos.Y - 1, curr.pos.Y - 2}
			for _, ny := range yCandidates {
				if ny < 1 || ny >= 126 {
					continue
				}

				// Check ground support
				if !s.mobSolid(r, nx, ny-1, nz) {
					continue
				}

				// If jumping up, need clearance above current position
				if ny > curr.pos.Y {
					if !s.mobFits(r, m.Type, float64(curr.pos.X)+0.5, float64(curr.pos.Y+1), float64(curr.pos.Z)+0.5) {
						continue
					}
				}

				// Check headroom and fit at target position
				if !s.mobFits(r, m.Type, float64(nx)+0.5, float64(ny), float64(nz)+0.5) {
					continue
				}

				// Check cliff / drop limit: cannot drop more than 3 blocks
				if curr.pos.Y-ny > 3 {
					continue
				}

				// Hazard cost assessment
				groundBlock := s.block(r, nx, ny-1, nz)
				bodyBlock := s.block(r, nx, ny, nz)
				stepCost := 1.0

				// Avoid fire and lava
				if bodyBlock == blkFlintFire || isLava(bodyBlock) || groundBlock == blkFlintFire || isLava(groundBlock) {
					continue
				}

				// Water avoidance: land mobs penalize entering water heavily so they prefer dry land
				if isWater(bodyBlock) || isWater(groundBlock) {
					stepCost += 12.0
				}

				// Slight penalty for changing elevation
				if ny != curr.pos.Y {
					stepCost += 0.4
				}

				nextPos := VoxelPos{nx, ny, nz}
				newG := curr.gCost + stepCost
				if oldG, exists := bestCost[nextPos]; exists && newG >= oldG {
					continue
				}

				bestCost[nextPos] = newG
				visited[nextPos] = true
				neighbor := &aStarItem{
					pos:    nextPos,
					gCost:  newG,
					fCost:  newG + hFunc(nextPos),
					parent: curr,
				}
				heap.Push(pq, neighbor)
				break // Found best valid height for this horizontal direction
			}
		}
	}

	// Reconstruct path from bestNode back to start
	var path []VoxelPos
	node := bestNode
	for node != nil && node.pos != start {
		path = append(path, node.pos)
		node = node.parent
	}

	// Reverse path so it goes from start to target
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path
}
