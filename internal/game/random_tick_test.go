package game

import (
	"context"
	"math/rand"
	"testing"
)

func TestRandomTickSpeedRuleAndEvents(t *testing.T) {
	s := &Server{
		config: Config{DataDir: t.TempDir()},
		rooms:  map[string]*Room{},
	}
	r := newRoom("test_room", "Test", "survival", 0)
	r.Ext = &RoomExt{}
	s.rooms[r.ID] = r

	// Default should be 3
	if speed := s.randomTickSpeed(r); speed != 3 {
		t.Fatalf("expected default randomTickSpeed 3, got %d", speed)
	}

	// Change randomTickSpeed
	if !s.setRandomTickSpeed(r, nil, 10, "test") {
		t.Fatalf("expected setRandomTickSpeed to succeed")
	}
	if speed := s.randomTickSpeed(r); speed != 10 {
		t.Fatalf("expected randomTickSpeed 10, got %d", speed)
	}

	// Setting to 0 should be valid (disables random ticks)
	if !s.setRandomTickSpeed(r, nil, 0, "test") {
		t.Fatalf("expected setRandomTickSpeed(0) to succeed")
	}
	if speed := s.randomTickSpeed(r); speed != 0 {
		t.Fatalf("expected randomTickSpeed 0, got %d", speed)
	}

	// Negative should fail
	if s.setRandomTickSpeed(r, nil, -1, "test") {
		t.Fatalf("expected negative speed to fail")
	}
}

func TestRandomTickCropsSugarCaneAndSnow(t *testing.T) {
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
	r := newRoom("test_tick", "Test", "survival", 0)
	r.W = 128
	r.Ext = &RoomExt{
		Rules: map[string]bool{"doFireTick": true},
	}
	s.rooms[r.ID] = r

	// 1. Sugar cane growth
	// Place ground dirt at y=9, sugar cane at y=10, air at y=11
	r.Edits[blockKey(20, 9, 20)] = blkDirt
	r.Edits[blockKey(20, 10, 20)] = blkSugarCane
	r.Edits[blockKey(20, 11, 20)] = 0
	// Force random tick
	for i := 0; i < 20; i++ {
		s.handleRandomTick(r, 20, 10, 20, blkSugarCane)
		if s.block(r, 20, 11, 20) == blkSugarCane {
			break
		}
	}
	if s.block(r, 20, 11, 20) != blkSugarCane {
		t.Errorf("expected sugar cane to grow at y=11")
	}

	// 2. Farmland near water becomes wet
	r.Edits[blockKey(30, 9, 30)] = blkDirt
	r.Edits[blockKey(30, 10, 30)] = blkFarmlandDry
	r.Edits[blockKey(30, 11, 30)] = 0
	r.Edits[blockKey(31, 10, 30)] = blkWaterSource
	s.handleRandomTick(r, 30, 10, 30, blkFarmlandDry)
	if s.block(r, 30, 10, 30) != blkFarmlandWet {
		t.Errorf("expected dry farmland near water to become wet, got %d", s.block(r, 30, 10, 30))
	}

	// 3. Snow block next to torch melts
	r.Edits[blockKey(40, 9, 40)] = blkDirt
	r.Edits[blockKey(40, 10, 40)] = blkSnowBlock
	r.Edits[blockKey(40, 11, 40)] = 0
	r.Edits[blockKey(40, 10, 41)] = 82 // Torch
	s.handleRandomTick(r, 40, 10, 40, blkSnowBlock)
	if s.block(r, 40, 10, 40) != 0 {
		t.Errorf("expected snow block next to torch to melt to air, got %d", s.block(r, 40, 10, 40))
	}
}
