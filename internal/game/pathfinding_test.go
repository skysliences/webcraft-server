package game

import (
	"context"
	"math/rand"
	"testing"
)

func TestAStarPathfindingWallAndSteps(t *testing.T) {
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := newEngine("../../../public/engine.wasm")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.runtime.Close(context.Background())

	s := &Server{
		config: Config{DataDir: t.TempDir()},
		rooms:  map[string]*Room{},
		rules:  rules,
		engine: eng,
		rng:    rand.New(rand.NewSource(1)),
	}
	eng.server = s

	r := newRoom("test_pathfinding", "Test", "survival", 0)
	r.W = 128
	s.rooms[r.ID] = r

	// Build a flat stone floor at y=9
	for x := 0; x < 30; x++ {
		for z := 0; z < 30; z++ {
			r.Edits[blockKey(x, 9, z)] = 1 // Stone
			r.Edits[blockKey(x, 10, z)] = 0
			r.Edits[blockKey(x, 11, z)] = 0
			r.Edits[blockKey(x, 12, z)] = 0
		}
	}

	zombie := &Mob{
		ID:     0x80000001,
		Type:   "zombie",
		X:      10.5,
		Y:      10.0,
		Z:      10.5,
		Health: 20,
	}

	// 1. Straight path with no obstacles
	path := s.findVoxelPath(r, zombie, 10, 10, 10, 15, 10, 10)
	if len(path) == 0 {
		t.Fatalf("expected path to be found, got empty")
	}
	last := path[len(path)-1]
	if last.X != 15 || last.Z != 10 {
		t.Errorf("expected path to reach target (15, 10, 10), got (%d, %d, %d)", last.X, last.Y, last.Z)
	}

	// 2. Obstacle wall at x=12, from z=8 to z=12 (2 blocks high)
	// Leaving a gap at z=13 so the zombie must route around it
	for z := 8; z <= 12; z++ {
		r.Edits[blockKey(12, 10, z)] = 1
		r.Edits[blockKey(12, 11, z)] = 1
	}

	wallPath := s.findVoxelPath(r, zombie, 10, 10, 10, 14, 10, 10)
	if len(wallPath) == 0 {
		t.Fatalf("expected path around wall, got empty")
	}
	// Verify that the path does NOT pass through the wall at x=12, z in [8..12]
	for _, p := range wallPath {
		if p.X == 12 && p.Z >= 8 && p.Z <= 12 {
			t.Errorf("path stepped into wall block at (%d, %d, %d)", p.X, p.Y, p.Z)
		}
	}

	// 3. Step up 1 block: stone step at (20, 10, 10), destination at (21, 11, 10)
	r.Edits[blockKey(21, 10, 10)] = 1 // Step foundation
	stepPath := s.findVoxelPath(r, zombie, 19, 10, 10, 21, 11, 10)
	if len(stepPath) == 0 {
		t.Fatalf("expected path to step up 1 block, got empty")
	}
	lastStep := stepPath[len(stepPath)-1]
	if lastStep.Y != 11 {
		t.Errorf("expected final step Y to be 11, got %d", lastStep.Y)
	}
}

func TestAStarPathfindingHazardAvoidance(t *testing.T) {
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := newEngine("../../../public/engine.wasm")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.runtime.Close(context.Background())

	s := &Server{
		config: Config{DataDir: t.TempDir()},
		rooms:  map[string]*Room{},
		rules:  rules,
		engine: eng,
		rng:    rand.New(rand.NewSource(1)),
	}
	eng.server = s

	r := newRoom("test_hazards", "Test", "survival", 0)
	r.W = 128
	s.rooms[r.ID] = r

	// Floor at y=9
	for x := 0; x < 30; x++ {
		for z := 0; z < 30; z++ {
			r.Edits[blockKey(x, 9, z)] = 1
			r.Edits[blockKey(x, 10, z)] = 0
			r.Edits[blockKey(x, 11, z)] = 0
		}
	}

	zombie := &Mob{
		ID:     0x80000001,
		Type:   "zombie",
		X:      5.5,
		Y:      10.0,
		Z:      10.5,
		Health: 20,
	}

	// Place lava at (7, 10, 10) and (7, 9, 10)
	r.Edits[blockKey(7, 10, 10)] = blkLavaSource

	lavaPath := s.findVoxelPath(r, zombie, 5, 10, 10, 9, 10, 10)
	if len(lavaPath) == 0 {
		t.Fatalf("expected path avoiding lava, got empty")
	}
	for _, p := range lavaPath {
		if p.X == 7 && p.Z == 10 {
			t.Errorf("path stepped directly into lava at (7, 10, 10)!")
		}
	}
}
