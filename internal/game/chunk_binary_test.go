package game

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"hash/crc32"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChunkBinaryEncodeDecodeRoundtrip(t *testing.T) {
	base := make([]byte, chunkVolume)
	for i := range base {
		base[i] = byte(i % 100)
	}

	d := &diskChunk{
		Version:           1,
		X:                 15,
		Z:                 -8,
		Generator:         "generator_v1",
		VegetationVersion: 2,
		Base:              base,
		CRC:               crc32.ChecksumIEEE(base),
		Edits:             map[int]int{10: 1, 2048: 5},
		Containers: map[int]*Container{
			10: {Kind: "chest", Slots: Stacks{stack(1, 64, 0)}},
		},
		Authors: map[int]EditAuthor{
			10: {Token: "test_token", After: 1},
		},
		Blocks: map[int]*BlockData{
			10: {Kind: "piston", Facing: 2, Power: 15},
		},
	}

	encoded, err := encodeChunkBinary(d)
	if err != nil {
		t.Fatalf("encodeChunkBinary error: %v", err)
	}
	if !isChunkBinary(encoded) {
		t.Fatalf("expected isChunkBinary to return true")
	}

	decoded, err := decodeChunkBinary(encoded)
	if err != nil {
		t.Fatalf("decodeChunkBinary error: %v", err)
	}

	if decoded.X != d.X || decoded.Z != d.Z || decoded.Version != d.Version || decoded.VegetationVersion != d.VegetationVersion {
		t.Fatalf("header mismatch: %+v vs %+v", decoded, d)
	}
	if !bytes.Equal(decoded.Base, d.Base) {
		t.Fatalf("base terrain mismatch")
	}
	if !reflect.DeepEqual(decoded.Edits, d.Edits) {
		t.Fatalf("edits mismatch: %+v vs %+v", decoded.Edits, d.Edits)
	}
	if !reflect.DeepEqual(decoded.Authors, d.Authors) {
		t.Fatalf("authors mismatch")
	}
	if len(decoded.Containers) != len(d.Containers) || decoded.Containers[10].Kind != "chest" {
		t.Fatalf("containers mismatch")
	}
	if len(decoded.Blocks) != len(d.Blocks) || decoded.Blocks[10].Power != 15 {
		t.Fatalf("blocks mismatch")
	}
}

func TestChunkBinarySaveLoadAndLegacyBackup(t *testing.T) {
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := newEngine("../../../public/engine.wasm")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.runtime.Close(context.Background())

	dir := t.TempDir()
	s := &Server{
		config: Config{DataDir: dir, EngineFile: "../../../public/engine.wasm"},
		rooms:  map[string]*Room{},
		rules:  rules,
		engine: eng,
		rng:    rand.New(rand.NewSource(1)),
	}
	eng.server = s

	r := newRoom("test_bin_world", "Test", "survival", 0)
	r.W = 128
	r.Infinite = true
	s.rooms[r.ID] = r

	coord := ChunkCoord{X: 2, Z: 3}
	file := s.chunkFile(r, coord)

	// 1. Manually write a legacy gzipped JSON chunk
	base := make([]byte, chunkVolume)
	legacyChunk := &diskChunk{
		Version:   1,
		X:         coord.X,
		Z:         coord.Z,
		Generator: r.Generator,
		Base:      base,
		CRC:       crc32.ChecksumIEEE(base),
		Edits:     map[int]int{100: 4},
	}
	jsonData, _ := json.Marshal(legacyChunk)
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	gz.Write(jsonData)
	gz.Close()
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicPrivateFile(file, gzBuf.Bytes()); err != nil {
		t.Fatal(err)
	}

	// 2. Read chunk: should read legacy JSON successfully
	loaded, generated, err := s.readChunkData(r, coord, file)
	if err != nil {
		t.Fatalf("read legacy chunk error: %v", err)
	}
	if generated {
		t.Fatalf("expected existing chunk, got generated")
	}
	if loaded.Edits[100] != 4 {
		t.Fatalf("expected edit 4, got %d", loaded.Edits[100])
	}

	// 3. Save chunk: should create .pre-binary-v1.bak and write binary chunk
	wc := &WorldChunk{
		Base:     loaded.Base,
		MetaHash: chunkMetadataHash(loaded),
	}
	// Add an edit so hash changes and triggers save
	r.Edits[chunkKey(coord, 101)] = 5
	if err := s.saveChunk(r, coord, wc); err != nil {
		t.Fatalf("saveChunk error: %v", err)
	}

	// Verify backup was created
	bakFile := file + ".pre-binary-v1.bak"
	if _, err := os.Stat(bakFile); os.IsNotExist(err) {
		t.Fatalf("expected backup file %s to exist", bakFile)
	}

	// Verify current file is binary
	newData, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !isChunkBinary(newData) {
		t.Fatalf("expected saved file to be binary format")
	}

	// 4. Read back binary chunk
	reloaded, _, err := s.readChunkData(r, coord, file)
	if err != nil {
		t.Fatalf("read binary chunk error: %v", err)
	}
	if reloaded.Edits[101] != 5 {
		t.Fatalf("expected edit 5, got %d", reloaded.Edits[101])
	}
}
