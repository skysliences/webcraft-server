package game

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedBlockKeysAndNoiseTerrain(t *testing.T) {
	for _, p := range [][3]int{{-1, 127, -16}, {-30000000, 0, 29999999}, {0, 63, 0}, {17000000, 90, -17000000}} {
		x, y, z := coords(blockKey(p[0], p[1], p[2]))
		if [3]int{x, y, z} != p {
			t.Fatalf("round trip: %v -> %v", p, [3]int{x, y, z})
		}
	}
	if chunkCoord(-1, -17) != (ChunkCoord{-1, -2}) {
		t.Fatal("negative chunk floor")
	}
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{rules: rules}
	r := newRoom("abcdefghijklmnop", "noise", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	for _, c := range []ChunkCoord{{0, 0}, {-1, -1}, {100000, -100000}} {
		a := s.generateTerrain(r, c)
		b := s.generateTerrain(r, c)
		if len(a) != chunkVolume || !bytes.Equal(a, b) {
			t.Fatal("generation not deterministic")
		}
		for i := 0; i < 256; i++ {
			if a[i*128] != 12 {
				t.Fatal("missing bedrock")
			}
		}
	}
	other := newRoom("ponmlkjihgfedcba", "noise", "survival", 0)
	if bytes.Equal(s.generateTerrain(r, ChunkCoord{}), s.generateTerrain(other, ChunkCoord{})) {
		t.Fatal("seed ignored")
	}
}
func TestChunkPersistenceMetadataAndCorruption(t *testing.T) {
	dir := t.TempDir()
	s, _ := testApp(t, dir)
	s.mu.Lock()
	r := newRoom("abcdefghijklmnop", "chunks", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	s.rooms[r.ID] = r
	key := blockKey(-17, 70, 18000)
	c := chunkCoord(-17, 18000)
	r.Edits[key] = 26
	r.Containers[key] = &Container{Kind: "chest", Slots: make(Stacks, 27)}
	v := stack(1, 5, 0)
	r.Containers[key].Slots[0] = v
	r.Authors[key] = EditAuthor{Token: "test", Before: 0, After: 26}
	if _, err := s.ensureChunk(r, c); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if err := s.saveRoom(r); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(s.roomFile(r.ID))
	if err != nil || bytes.Contains(metadata, []byte(`"containers":{`)) {
		s.mu.Unlock()
		t.Fatal("terrain left in root JSON")
	}
	file := s.chunkFile(r, c)
	raw, err := os.ReadFile(file)
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite = true
	fresh.Generator = generatorVersion
	if _, err = s.ensureChunk(fresh, c); err != nil || fresh.Edits[key] != 26 || fresh.Containers[key].Slots[0].Count != 5 || fresh.Authors[key].Token != "test" {
		s.mu.Unlock()
		t.Fatalf("chunk restore: %v", err)
	}
	if err = os.WriteFile(file, []byte("corrupt"), 0600); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	delete(fresh.chunks, c)
	if _, err = s.ensureChunk(fresh, c); err == nil {
		s.mu.Unlock()
		t.Fatal("corrupt chunk accepted")
	}
	after, _ := os.ReadFile(file)
	if string(after) != "corrupt" {
		s.mu.Unlock()
		t.Fatal("corrupt chunk overwritten")
	}
	os.WriteFile(file, raw, 0600)
	s.mu.Unlock()
	if _, err = os.Stat(filepath.Join(dir, "worlds", r.ID, "overworld", "region", "r.-1.35")); err != nil {
		t.Fatal(err)
	}
}
func TestLegacyChunkMatchesOriginalEngine(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[DefaultRoom]
	a := s.engine.block(r, 512, 32, 513)
	b := s.engine.block(r, 513, 63, 512)
	r.Infinite = true
	r.LegacyW = 1024
	r.Generator = generatorVersion
	c, err := s.ensureChunk(r, ChunkCoord{32, 32})
	if err != nil {
		t.Fatal(err)
	}
	if int(c.Base[chunkIndex(512, 32, 513)]) != a || int(c.Base[chunkIndex(513, 63, 512)]) != b {
		t.Fatal("legacy chunk layout/generation changed")
	}
}
