package game

import "testing"

func TestNaturalSpawnChecksWholeBody(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := mobTestWorld(s)
	r.Time = 18000
	if !s.mobFits(r, "spider", 12.5, 60, 10.5) {
		t.Fatal("empty spider space rejected")
	}
	// A spider spans three columns. Checking only its four corners misses
	// the centre column and both middle edges of its bounding box.
	for _, pos := range [][3]int{{12, 60, 10}, {12, 60, 9}, {11, 60, 10}} {
		for _, block := range []int{1, 9, 10, 68} {
			r.Edits[blockKey(pos[0], pos[1], pos[2])] = block
			if s.mobFits(r, "spider", 12.5, 60, 10.5) || s.canNaturalSpawn(r, "spider", 12, 60, 10) {
				t.Errorf("spider overlaps block %d at %v", block, pos)
			}
		}
		r.Edits[blockKey(pos[0], pos[1], pos[2])] = 0
	}
	r.Edits[blockKey(12, 62, 10)] = 1
	if s.mobFits(r, "enderman", 12.5, 60, 10.5) {
		t.Fatal("enderman overlaps ceiling")
	}
	if s.mobFits(r, "enderman", 12.5, 126, 10.5) {
		t.Fatal("enderman exceeds world height")
	}
}

func TestNaturalSpawnDefaultRange(t *testing.T) {
	if got := DefaultMobSpawningConfig().SpawnRange; got != 8 {
		t.Fatalf("default natural spawn range = %d, want 8", got)
	}
}
