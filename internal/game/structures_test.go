package game

import (
	"bytes"
	"mcraft/backend/internal/worldgen"
	"reflect"
	"testing"
)

func findStructureVillage(t *testing.T, s *Server, r *Room) (ChunkCoord, ChunkCoord) {
	t.Helper()
	for rx := -2; rx <= 2; rx++ {
		for rz := -2; rz <= 2; rz++ {
			x, z := worldgen.VillageCandidate(seed(r.seedID()), rx, rz)
			center := ChunkCoord{x, z}
			if structureBiome(seed(r.seedID()), x*16+8, z*16+8) < 0 {
				continue
			}
			for dz := -3; dz <= 3; dz++ {
				for dx := -3; dx <= 3; dx++ {
					c := ChunkCoord{x + dx, z + dz}
					d, err := s.generateChunkSnapshot(r, c)
					if err != nil {
						t.Fatal(err)
					}
					if len(d.Containers) > 0 && len(d.VillageSpawns) > 0 {
						return center, c
					}
				}
			}
		}
	}
	t.Fatal("no deterministic village fixture")
	return ChunkCoord{}, ChunkCoord{}
}

func TestStructureDeterminismAndMineshaftPersistence(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := vegetationRoom()
	s.rooms[r.ID] = r
	var target ChunkCoord
	found := false
	for z := -24; z < 24 && !found; z++ {
		for x := -24; x < 24; x++ {
			c := ChunkCoord{x, z}
			d, err := s.generateChunkSnapshot(r, c)
			if err != nil {
				t.Fatal(err)
			}
			rails := 0
			for _, data := range d.Blocks {
				if data.Rail != nil {
					rails++
				}
			}
			if rails > 3 && len(d.Containers) > 0 {
				target = c
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no mine with rails and chest")
	}
	a, err := s.generateChunkSnapshot(r, target)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.generateChunkSnapshot(r, ChunkCoord{target.X + 1, target.Z})
	b, err := s.generateChunkSnapshot(r, target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Base, b.Base) || !reflect.DeepEqual(a.Blocks, b.Blocks) || !reflect.DeepEqual(a.Containers, b.Containers) {
		t.Fatal("generation depends on chunk order")
	}
	for i, data := range a.Blocks {
		if data.Rail != nil {
			if a.Base[i] != blkRail || a.Base[i-1] != 5 {
				t.Fatal("unsupported generated rail")
			}
			if !validRail(data.Rail) {
				t.Fatal("invalid generated rail state")
			}
		}
	}
	chunk, err := s.ensureChunk(r, target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Containers {
		delete(r.Containers, chunkKey(target, i))
		r.Edits[chunkKey(target, i)] = 0
	}
	if err = s.saveChunk(r, target, chunk); err != nil {
		t.Fatal(err)
	}
	fresh := vegetationRoom()
	got, err := s.ensureChunk(fresh, target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chunk.Base, got.Base) {
		t.Fatal("saved base regenerated")
	}
	for i := range a.Containers {
		if fresh.Containers[chunkKey(target, i)] != nil || fresh.Edits[chunkKey(target, i)] != 0 {
			t.Fatal("broken loot chest respawned")
		}
	}
	for i, data := range a.Blocks {
		if data.Rail != nil && !reflect.DeepEqual(railState(r, chunkKey(target, i)), railState(fresh, chunkKey(target, i))) {
			t.Fatal("rail direction not restored")
		}
	}
}
