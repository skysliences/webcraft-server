package game

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MCC is a versioned WebCraft format, not Minecraft's Anvil/NBT format. Region
// directories group 32x32 independently atomic chunk files, allowing backups
// and cache eviction without rewriting an entire world.
type WorldChunk struct {
	Base []byte
	// Terrain is the immutable, vegetation-decorated base encoded for API v1.
	// Live edits are applied separately; it is never written to a save file.
	Terrain           []byte
	Terrain16         []byte // Protocol 2 cache; also immutable and never saved.
	Used              uint64
	Saved             bool
	MetaHash          [32]byte
	VegetationVersion int
}
type diskChunk struct {
	Version           int                `json:"version"`
	X                 int                `json:"x"`
	Z                 int                `json:"z"`
	Generator         string             `json:"generator"`
	VegetationVersion int                `json:"vegetationVersion,omitempty"`
	Base              []byte             `json:"base"`
	CRC               uint32             `json:"crc"`
	Edits             map[int]int        `json:"edits,omitempty"`
	Containers        map[int]*Container `json:"containers,omitempty"`
	Authors           map[int]EditAuthor `json:"authors,omitempty"`
	Blocks            map[int]*BlockData `json:"blocks,omitempty"`
	VillageSpawns     []villageSpawn     `json:"-"` // Prepared intents, published only for newly committed chunks.
}

func (s *Server) chunkFile(r *Room, c ChunkCoord) string {
	return filepath.Join(s.config.DataDir, "worlds", r.root().ID, r.dimName(), "region", fmt.Sprintf("r.%d.%d", floorDiv(c.X, 32), floorDiv(c.Z, 32)), fmt.Sprintf("c.%d.%d.mcc", c.X, c.Z))
}
func chunkData(r *Room, c ChunkCoord, base []byte) *diskChunk {
	d := &diskChunk{Version: 1, X: c.X, Z: c.Z, Generator: r.Generator, Base: base, CRC: crc32.ChecksumIEEE(base), Edits: map[int]int{}, Containers: map[int]*Container{}, Authors: map[int]EditAuthor{}, Blocks: map[int]*BlockData{}}
	for k, v := range r.Edits {
		x, y, z := coords(k)
		if chunkCoord(x, z) == c {
			d.Edits[chunkIndex(x, y, z)] = v
		}
	}
	for k, v := range r.Containers {
		x, y, z := coords(k)
		if chunkCoord(x, z) == c {
			d.Containers[chunkIndex(x, y, z)] = v
		}
	}
	for k, v := range r.Authors {
		x, y, z := coords(k)
		if chunkCoord(x, z) == c {
			d.Authors[chunkIndex(x, y, z)] = v
		}
	}
	if r.Ext != nil {
		for k, v := range r.Ext.Blocks {
			x, y, z := coords(k)
			if chunkCoord(x, z) == c {
				d.Blocks[chunkIndex(x, y, z)] = v
			}
		}
	}
	return d
}
func chunkKey(c ChunkCoord, i int) uint64 { return blockKey(c.X*16+i/128%16, i%128, c.Z*16+i/2048) }
func (s *Server) ensureChunk(r *Room, c ChunkCoord) (*WorldChunk, error) {
	if r.chunks == nil {
		r.chunks = map[ChunkCoord]*WorldChunk{}
	}
	if v := r.chunks[c]; v != nil {
		v.Used = s.tick
		return v, nil
	}
	// Streaming can prepare disk I/O and pure noise generation without holding
	// Server.mu. World maps, mechanics and plugin events still commit here.
	d, generated, err, ready := s.takePreparedChunk(r, c)
	if !ready {
		s.discardPreparedChunk(r, c)
		d, generated, err = s.readChunkData(r, c, s.chunkFile(r, c))
	}
	if err != nil {
		return nil, err
	}
	for i, v := range d.Edits {
		k := chunkKey(c, i)
		if _, live := r.Edits[k]; !live {
			r.Edits[k] = v
		}
	}
	for i, v := range d.Containers {
		k := chunkKey(c, i)
		if _, live := r.Containers[k]; !live {
			r.Containers[k] = v
		}
	}
	for i, v := range d.Authors {
		k := chunkKey(c, i)
		if _, live := r.Authors[k]; !live {
			r.Authors[k] = v
		}
	}
	if len(d.Blocks) > 0 {
		if r.Ext == nil {
			r.Ext = &RoomExt{}
		}
		if r.Ext.Blocks == nil {
			r.Ext.Blocks = map[uint64]*BlockData{}
		}
		for i, v := range d.Blocks {
			k := chunkKey(c, i)
			if _, live := r.Ext.Blocks[k]; !live {
				r.Ext.Blocks[k] = v
			}
		}
	}
	v := &WorldChunk{Base: d.Base, Used: s.tick, Saved: !generated, MetaHash: chunkMetadataHash(d)}
	v.VegetationVersion = d.VegetationVersion
	r.chunks[c] = v
	for i := range d.Edits {
		k := chunkKey(c, i)
		b := r.Edits[k]
		s.trackMechanicBlock(r, k, b)
		if s.redstoneKind(b) != "" {
			if d := r.blockExt().Blocks[k]; d != nil && d.Redstone != nil {
				restoreRedstoneComponent(r, k, d.Redstone)
			}
			s.trackRedstoneBlock(r, k, b)
			r.rt().redstoneDirty = true
		}
	}
	if err = s.populateVegetation(r, c, v, generated); err != nil {
		delete(r.chunks, c)
		return nil, err
	}
	s.restoreChunkLeafDecay(r, c, v)
	if generated {
		// Structure generation may produce loot and entity intents. The detached
		// worker cannot publish them; only a committed world chunk may do so.
		for _, spawn := range d.VillageSpawns {
			spawn.Room = r
			s.pendingVillageSpawns = append(s.pendingVillageSpawns, spawn)
		}
		r.Dirty = true
		s.emit(api.ChunkGenerated, r, nil, map[string]any{"x": c.X, "z": c.Z, "generator": r.Generator, "reason": "exploration"})
	}
	s.emit(api.ChunkLoaded, r, nil, map[string]any{"x": c.X, "z": c.Z, "generated": generated, "reason": "access"})
	return v, nil
}

// readChunkData reads one atomic chunk snapshot (binary or legacy JSON), or creates deterministic base
// terrain. It does not publish state, write files or dispatch plugin events.
// Background callers must pass a detached immutable room snapshot with LegacyW=0.
func (s *Server) readChunkData(r *Room, c ChunkCoord, file string) (*diskChunk, bool, error) {
	var d diskChunk
	f, err := os.Open(file)
	generated := false
	if os.IsNotExist(err) {
		generated = true
		if legacyChunkCoord(r, c) {
			d = diskChunk{Version: 1, X: c.X, Z: c.Z, Base: s.engine.legacyChunk(r, c)}
		} else {
			prepared, e := s.generateChunkSnapshot(r, c)
			if e != nil {
				return nil, false, e
			}
			d = *prepared
		}
	} else if err == nil {
		raw, readErr := io.ReadAll(io.LimitReader(f, chunkSnapshotLimit+1))
		f.Close()
		if readErr != nil {
			return nil, false, fmt.Errorf("read chunk %s: %w", file, readErr)
		}
		decoded, decodeErr := decodeChunkSnapshot(raw)
		if decodeErr != nil {
			return nil, false, fmt.Errorf("read chunk %s: %w", file, decodeErr)
		}
		d = *decoded
		if d.Version != 1 || d.X != c.X || d.Z != c.Z || len(d.Base) != chunkVolume || crc32.ChecksumIEEE(d.Base) != d.CRC {
			return nil, false, fmt.Errorf("invalid chunk %s", file)
		}
	} else {
		return nil, false, err
	}
	if err = s.validateChunk(&d); err != nil {
		return nil, false, fmt.Errorf("invalid chunk %s: %w", file, err)
	}
	return &d, generated, nil
}

func legacyChunkCoord(r *Room, c ChunkCoord) bool {
	return r.LegacyW > 0 && c.X >= 0 && c.Z >= 0 && c.X < r.LegacyW/16 && c.Z < r.LegacyW/16
}

func (s *Server) saveChunk(r *Room, c ChunkCoord, v *WorldChunk) error {
	s.persistRedstone(r)
	d := chunkData(r, c, v.Base)
	d.VegetationVersion = v.VegetationVersion
	hash := chunkMetadataHash(d)
	if v.Saved && hash == v.MetaHash {
		return nil
	}
	encoded, err := encodeChunkBinary(d)
	if err != nil {
		return err
	}
	file := s.chunkFile(r, c)
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	// If existing file is legacy (non-binary), create backup before upgrading
	if prevData, err := os.ReadFile(file); err == nil && !isChunkBinary(prevData) {
		bakFile := file + ".pre-binary-v1.bak"
		if _, err := os.Stat(bakFile); os.IsNotExist(err) {
			_ = atomicPrivateFile(bakFile, prevData)
		}
	}
	s.emit(api.ChunkSaving, r, nil, map[string]any{"x": c.X, "z": c.Z, "reason": "autosave"})
	if err = atomicPrivateFile(file, encoded); err != nil {
		s.emit(api.ChunkSaveFailed, r, nil, map[string]any{"x": c.X, "z": c.Z, "error": err.Error()})
		return err
	}
	v.Saved = true
	v.MetaHash = hash
	s.emit(api.ChunkSaved, r, nil, map[string]any{"x": c.X, "z": c.Z, "bytes": len(encoded), "reason": "autosave"})
	return nil
}
func chunkMetadataHash(d *diskChunk) [32]byte {
	// Base terrain is immutable after its versioned vegetation pass. Compare mutable state to avoid compressing
	// hundreds of unchanged terrain chunks on every player movement save.
	v := struct {
		Edits             map[int]int
		Containers        map[int]*Container
		Authors           map[int]EditAuthor
		Blocks            map[int]*BlockData
		VegetationVersion int
	}{d.Edits, d.Containers, d.Authors, d.Blocks, d.VegetationVersion}
	raw, _ := json.Marshal(v)
	return sha256.Sum256(raw)
}
func (s *Server) validateChunk(d *diskChunk) error {
	if d.VegetationVersion < 0 || d.VegetationVersion > vegetationVersion {
		return fmt.Errorf("invalid vegetation version")
	}
	if len(d.Base) != chunkVolume {
		return fmt.Errorf("invalid terrain length")
	}
	for _, b := range d.Base {
		if !s.rules.validBlock(int(b)) {
			return fmt.Errorf("invalid terrain block")
		}
	}
	for i, b := range d.Edits {
		if i < 0 || i >= chunkVolume || !s.rules.validBlock(b) {
			return fmt.Errorf("invalid edit")
		}
	}
	for i, c := range d.Containers {
		if i < 0 || i >= chunkVolume || c == nil || len(c.Slots) > 128 || c.Burn < 0 || c.BurnTotal < 0 || c.Progress < 0 {
			return fmt.Errorf("invalid container")
		}
		if c.Item != nil && (!s.validStack(c.Item, 1) || c.Item.ID != itemShulkerBox && c.Item.ID != blkShulkerBox) {
			return fmt.Errorf("invalid portable container item")
		}
		switch c.Kind {
		case "chest", "furnace", "hopper", "brew", "ender", "dispenser", "dropper":
		default:
			return fmt.Errorf("invalid container kind")
		}
		for _, v := range c.Slots {
			if v != nil && !s.validStack(v, s.rules.max(v.ID)) {
				return fmt.Errorf("invalid container item")
			}
		}
	}
	for i, a := range d.Authors {
		if i < 0 || i >= chunkVolume || a.After < 0 || a.After >= len(s.rules.Names) || a.Before < 0 || a.Before >= len(s.rules.Names) && a.Before != 255 {
			return fmt.Errorf("invalid author")
		}
	}
	for i, b := range d.Blocks {
		if i < 0 || i >= chunkVolume || b == nil || b.Facing < 0 || b.Facing > 5 || b.Power < 0 || b.Power > 15 || b.Fuse < 0 || b.Fuse > 1000000 || b.Age < 0 || b.Age > 1000000 || !validRedstoneComponent(b.Redstone) || !s.validMechanic(b.Mechanic) || !validRail(b.Rail) || !s.validTNTState(b.TNT) || !s.validTNTState(b.Fire) || b.TNT != nil && b.Fuse > maxTNTFuse {
			return fmt.Errorf("invalid block state")
		}
	}
	return nil
}
func (s *Server) validateChunkFiles() error {
	return filepath.WalkDir(filepath.Join(s.config.DataDir, "worlds"), func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".mcc.pre-binary-v1.bak") {
			// Saving legacy JSON preserves this private sidecar before upgrade.
			// Backups include it; validate rather than silently ignore it.
			name = strings.TrimSuffix(name, ".pre-binary-v1.bak")
		}
		var c ChunkCoord
		if n, e := fmt.Sscanf(name, "c.%d.%d.mcc", &c.X, &c.Z); n != 2 || e != nil || name != fmt.Sprintf("c.%d.%d.mcc", c.X, c.Z) {
			return fmt.Errorf("unexpected world file: %s", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(f, chunkSnapshotLimit+1))
		f.Close()
		if err != nil {
			return err
		}
		d, err := decodeChunkSnapshot(raw)
		if err != nil {
			return fmt.Errorf("chunk %s: %w", path, err)
		}
		if d.Version != 1 || d.X != c.X || d.Z != c.Z || crc32.ChecksumIEEE(d.Base) != d.CRC {
			return fmt.Errorf("chunk checksum/version: %s", path)
		}
		if err = s.validateChunk(d); err != nil {
			return fmt.Errorf("chunk %s: %w", path, err)
		}
		return nil
	})
}

const chunkSnapshotLimit = 4 << 20

// A full read is required: gzip.Close alone does not verify the trailer CRC.
// Bound both compressed input and output before parsing untrusted backups.
func readChunkGzip(raw []byte) ([]byte, error) {
	if len(raw) > chunkSnapshotLimit {
		return nil, fmt.Errorf("chunk compressed data exceeds limit")
	}
	source := bytes.NewReader(raw)
	gz, err := gzip.NewReader(source)
	if err != nil {
		return nil, err
	}
	gz.Multistream(false)
	payload, readErr := io.ReadAll(io.LimitReader(gz, chunkSnapshotLimit+1))
	closeErr := gz.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(payload) > chunkSnapshotLimit {
		return nil, fmt.Errorf("chunk decompressed data exceeds limit")
	}
	if source.Len() != 0 {
		return nil, fmt.Errorf("chunk compressed data has trailing bytes")
	}
	return payload, nil
}

func decodeChunkSnapshot(raw []byte) (*diskChunk, error) {
	if len(raw) > chunkSnapshotLimit {
		return nil, fmt.Errorf("chunk file exceeds limit")
	}
	if isChunkBinary(raw) {
		return decodeChunkBinary(raw)
	}
	payload, err := readChunkGzip(raw)
	if err != nil {
		return nil, err
	}
	var d diskChunk
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err = decoder.Decode(&d); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("chunk JSON has trailing data")
	}
	return &d, nil
}
func atomicPrivateFile(file string, data []byte) error {
	f, err := os.OpenFile(file+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}
func (s *Server) saveChunks(r *Room) error {
	// Before dropping legacy JSON terrain, materialize every edited/container chunk.
	needed := map[ChunkCoord]bool{}
	for k := range r.Edits {
		x, _, z := coords(k)
		needed[chunkCoord(x, z)] = true
	}
	for k := range r.Containers {
		x, _, z := coords(k)
		needed[chunkCoord(x, z)] = true
	}
	if r.Ext != nil {
		for k := range r.Ext.Blocks {
			x, _, z := coords(k)
			needed[chunkCoord(x, z)] = true
		}
	}
	for c := range needed {
		if _, err := s.ensureChunk(r, c); err != nil {
			return err
		}
	}
	keys := make([]ChunkCoord, 0, len(r.chunks))
	for c := range r.chunks {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Z == keys[j].Z {
			return keys[i].X < keys[j].X
		}
		return keys[i].Z < keys[j].Z
	})
	for _, c := range keys {
		if err := s.saveChunk(r, c, r.chunks[c]); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) trimChunks(r *Room) {
	s.trimChunksBudget(r, 0, 0)
}

// Exploration evicts a few chunks per tick, rather than compressing and fsyncing
// an entire excess view under the global lock. A failed save never deletes state.
func (s *Server) trimChunksBudget(r *Room, maximum int, budget time.Duration) {
	if len(r.chunks) <= 512 {
		return
	}
	started, removed := time.Now(), 0
	protected := map[ChunkCoord]bool{}
	if r.extRT != nil {
		for k := range r.extRT.scheduled {
			x, _, z := coords(k)
			protected[chunkCoord(x, z)] = true
		}
		for _, k := range r.extRT.pending {
			x, _, z := coords(k)
			protected[chunkCoord(x, z)] = true
		}
	}
	for _, d := range r.Drops {
		protected[chunkCoord(int(mathFloor(d.X)), int(mathFloor(d.Z)))] = true
	}
	for _, m := range r.Mobs {
		protected[chunkCoord(int(mathFloor(m.X)), int(mathFloor(m.Z)))] = true
	}
	for p := range r.Peers {
		c := chunkCoord(int(mathFloor(p.state.X)), int(mathFloor(p.state.Z)))
		for dz := -p.rd - 2; dz <= p.rd+2; dz++ {
			for dx := -p.rd - 2; dx <= p.rd+2; dx++ {
				protected[ChunkCoord{c.X + dx, c.Z + dz}] = true
			}
		}
	}
	var keys []ChunkCoord
	for c := range r.chunks {
		if !protected[c] {
			keys = append(keys, c)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return r.chunks[keys[i]].Used < r.chunks[keys[j]].Used })
	for _, c := range keys {
		if len(r.chunks) <= 512 || maximum > 0 && removed >= maximum || budget > 0 && removed > 0 && time.Since(started) >= budget {
			break
		}
		if err := s.saveChunk(r, c, r.chunks[c]); err != nil {
			r.SaveError = err.Error()
			break
		}
		delete(r.chunks, c)
		removed++
		for k := range r.Edits {
			x, _, z := coords(k)
			if chunkCoord(x, z) == c {
				delete(r.Edits, k)
			}
		}
		for k := range r.Containers {
			x, _, z := coords(k)
			if chunkCoord(x, z) == c {
				delete(r.Containers, k)
			}
		}
		for k := range r.Authors {
			x, _, z := coords(k)
			if chunkCoord(x, z) == c {
				delete(r.Authors, k)
			}
		}
		if r.Ext != nil {
			for k := range r.Ext.Blocks {
				x, _, z := coords(k)
				if chunkCoord(x, z) == c {
					clearRuntimeRedstone(getRedstone(r), k)
					syncMechanicView(r, k, nil)
					delete(r.Ext.Blocks, k)
				}
			}
		}
		s.emit(api.ChunkUnloaded, r, nil, map[string]any{"x": c.X, "z": c.Z, "reason": "cache_pressure"})
	}
}
func mathFloor(v float64) float64 {
	i := int(v)
	if float64(i) > v {
		i--
	}
	return float64(i)
}
