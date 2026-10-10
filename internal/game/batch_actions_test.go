package game

import (
	"context"
	"encoding/json"
	"math/rand"
	api "mcraft/backend/pkg/plugin"
	"net/http/httptest"
	"testing"
)

func TestBatchBlocksActionAndQueryBlocksAPI(t *testing.T) {
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

	r := newRoom("test_batch_world", "Test", "survival", 0)
	r.W = 128
	s.rooms[r.ID] = r

	// 1. Execute set_blocks action
	action := api.Action{
		Type:    "set_blocks",
		WorldID: r.ID,
		Blocks: []api.BlockChange{
			{X: 10, Y: 20, Z: 30, Block: 1}, // Stone
			{X: 11, Y: 20, Z: 30, Block: 4}, // Cobblestone
			{X: 12, Y: 20, Z: 30, Block: 5}, // Oak Planks
		},
	}
	s.applyAction(action)

	if s.block(r, 10, 20, 30) != 1 {
		t.Errorf("expected block (10,20,30) to be 1, got %d", s.block(r, 10, 20, 30))
	}
	if s.block(r, 11, 20, 30) != 4 {
		t.Errorf("expected block (11,20,30) to be 4, got %d", s.block(r, 11, 20, 30))
	}
	if s.block(r, 12, 20, 30) != 5 {
		t.Errorf("expected block (12,20,30) to be 5, got %d", s.block(r, 12, 20, 30))
	}

	// 2. Query blocks via HTTP Handler
	req := httptest.NewRequest("GET", "/api/rooms/test_batch_world/blocks?minX=10&maxX=12&minY=20&maxY=20&minZ=30&maxZ=30", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var snap api.WorldSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if snap.WorldID != r.ID || snap.Count != 3 || len(snap.Blocks) != 3 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	foundStone := false
	for _, b := range snap.Blocks {
		if b.X == 10 && b.Y == 20 && b.Z == 30 && b.Block == 1 {
			foundStone = true
			if b.Name != "Stone" {
				t.Errorf("expected name Stone, got %q", b.Name)
			}
		}
	}
	if !foundStone {
		t.Errorf("did not find stone in block snapshot")
	}
}
