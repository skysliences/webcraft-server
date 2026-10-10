package game

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func operationChunkArchive(t *testing.T, source, destination, target string, replacement []byte, sidecar []byte) {
	t.Helper()
	archive, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	found := false
	for _, entry := range archive.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == target {
			raw, found = replacement, true
		}
		output, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = output.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("source backup missing tested chunk")
	}
	if sidecar != nil {
		output, err := writer.Create(target + ".pre-binary-v1.bak")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = output.Write(sidecar); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func operationGzip(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestOperationsRestoreBinaryLegacyAndCorruptChunkBackups(t *testing.T) {
	dir := t.TempDir()
	s, _ := testApp(t, dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("chunkbackupworld", "formats", "survival", 0)
	r.Infinite, r.Generator = true, generatorVersion
	s.rooms[r.ID] = r
	c := ChunkCoord{-2, 3}
	k := chunkKey(c, chunkIndex(-17, 70, 49))
	r.Edits[k] = s.id("Chest")
	item := customStack(s.id("Diamond Sword"), 1)
	item.Ench = map[string]int{"sharpness": 3}
	r.Containers[k] = &Container{Kind: "chest", Slots: Stacks{item}}
	name, err := s.backup("test", "formats")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "backups", name)
	chunkFile := s.chunkFile(r, c)
	target, err := filepath.Rel(dir, chunkFile)
	if err != nil {
		t.Fatal(err)
	}
	target = filepath.ToSlash(target)
	encoded, err := os.ReadFile(chunkFile)
	if err != nil || !isChunkBinary(encoded) {
		t.Fatal("backup fixture is not real new binary storage", err)
	}
	decoded, err := decodeChunkBinary(encoded)
	if err != nil {
		t.Fatal(err)
	}
	// Read the actual gzip member to EOF, proving its checksum/trailer exists.
	reader, err := gzip.NewReader(bytes.NewReader(encoded[4:]))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	legacy := operationGzip(t, legacyJSON)
	wrongCRC := *decoded
	wrongCRC.CRC ^= 1
	crcMismatch, err := encodeChunkBinary(&wrongCRC)
	if err != nil {
		t.Fatal(err)
	}
	wrongLength := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(wrongLength[20:24], ^uint32(0))
	oversizedLength := append(append([]byte(nil), chunkBinaryMagic[:]...), operationGzip(t, wrongLength)...)
	badBinaryTrailer := append([]byte(nil), encoded...)
	badBinaryTrailer[len(badBinaryTrailer)-8] ^= 1
	badLegacyTrailer := append([]byte(nil), legacy...)
	badLegacyTrailer[len(badLegacyTrailer)-8] ^= 1
	binaryTrailingPayload := append(append([]byte(nil), chunkBinaryMagic[:]...), operationGzip(t, append(payload, 42))...)
	for _, test := range []struct {
		name         string
		raw, sidecar []byte
		valid        bool
	}{
		{"binary", encoded, nil, true},
		{"legacy-gzip-json", legacy, nil, true},
		{"migration-sidecar", encoded, legacy, true},
		{"binary-gzip-crc", badBinaryTrailer, nil, false},
		{"legacy-gzip-crc", badLegacyTrailer, nil, false},
		{"binary-truncated", encoded[:len(encoded)-3], nil, false},
		{"legacy-truncated", legacy[:len(legacy)-3], nil, false},
		{"terrain-checksum", crcMismatch, nil, false},
		{"oversized-declared-length", oversizedLength, nil, false},
		{"binary-trailing-payload", binaryTrailingPayload, nil, false},
		{"legacy-trailing-json", operationGzip(t, append(append([]byte(nil), legacyJSON...), []byte(` {}`)...)), nil, false},
		{"compressed-trailing-bytes", append(append([]byte(nil), encoded...), 42), nil, false},
		{"corrupt-sidecar", encoded, []byte("not-gzip"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			backupName := "formats-" + test.name + ".zip"
			operationChunkArchive(t, source, filepath.Join(dir, "backups", backupName), target, test.raw, test.sidecar)
			previous := s.rooms[r.ID]
			roomBefore, err := os.ReadFile(s.roomFile(r.ID))
			if err != nil {
				t.Fatal(err)
			}
			chunkBefore, err := os.ReadFile(chunkFile)
			if err != nil {
				t.Fatal(err)
			}
			err = s.restoreBackup(backupName, "test")
			if test.valid {
				if err != nil {
					t.Fatal("valid backup rejected", err)
				}
				current := s.rooms[r.ID]
				if current == nil {
					t.Fatal("restored world missing")
				}
				if _, err := s.ensureChunk(current, c); err != nil {
					t.Fatal(err)
				}
				if current.Edits[k] != s.id("Chest") || !reflect.DeepEqual(current.Containers[k].Slots[0], item) {
					t.Fatal("restore changed block/item metadata")
				}
			} else {
				if err == nil || s.rooms[r.ID] != previous {
					t.Fatal("corrupt backup accepted or changed world")
				}
				roomAfter, _ := os.ReadFile(s.roomFile(r.ID))
				chunkAfter, _ := os.ReadFile(chunkFile)
				if !bytes.Equal(roomBefore, roomAfter) || !bytes.Equal(chunkBefore, chunkAfter) {
					t.Fatal("corrupt backup changed live files")
				}
			}
		})
	}
}

func TestOperationsSecurityPersistenceAndRestore(t *testing.T) {
	dir := t.TempDir()
	s, h := testApp(t, dir)
	call := func(command string, key bool) *http.Response {
		raw, _ := json.Marshal(map[string]any{"command": command})
		req, _ := http.NewRequest("POST", h.URL+"/api/admin/console", bytes.NewReader(raw))
		req.Header.Set("content-type", "application/json")
		if key {
			req.Header.Set("Authorization", "Bearer "+s.ops.Key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := call("status", false)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("public console accepted")
	}
	res = call("status", true)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("local console denied")
	}
	settingsRaw, _ := json.Marshal(s.ops.Settings)
	for i, want := range []int{200, 409} {
		req, _ := http.NewRequest("POST", h.URL+"/api/admin/settings", bytes.NewReader(settingsRaw))
		req.Header.Set("Authorization", "Bearer "+s.ops.Key)
		req.Header.Set("content-type", "application/json")
		result, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		result.Body.Close()
		if result.StatusCode != want {
			t.Fatalf("settings request %d: %d, want %d", i, result.StatusCode, want)
		}
	}
	if _, err := New(Config{DataDir: dir, EngineFile: "../../../public/engine.wasm"}); err == nil {
		t.Fatal("second writer accepted")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops.Settings.Permissions["tester"] = PermissionGrant{Role: "admin", Deny: []string{"server.backup"}}
	if s.hasPermission("tester", "server.backup") || !s.hasPermission("tester", "server.status") {
		t.Fatal("permission deny precedence")
	}
	r := newRoom("restoretestworld", "restore", "survival", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	s.rooms[r.ID] = r
	key := blockKey(-17000000, 80, 17000000)
	c := chunkCoord(-17000000, 17000000)
	r.Edits[key] = s.id("Chest")
	v := customStack(s.id("Diamond Sword"), 1)
	v.Ench = map[string]int{"sharpness": 3}
	r.Containers[key] = &Container{Kind: "chest", Slots: Stacks{v}}
	backup, err := s.backup("test", "manual")
	if err != nil {
		t.Fatal(err)
	}
	r.Edits[key] = 0
	r.Containers[key].Slots[0] = nil
	if err = s.restoreBackup(backup, "test"); err != nil {
		t.Fatal(err)
	}
	restored := s.rooms[r.ID]
	if _, err = s.ensureChunk(restored, c); err != nil {
		t.Fatal(err)
	}
	if restored.Edits[key] != s.id("Chest") || !reflect.DeepEqual(restored.Containers[key].Slots[0], v) || !s.hasPermission("tester", "server.status") {
		t.Fatal("restore lost block/item metadata/settings")
	}
	// A corrupt archive is rejected before current files or memory are changed.
	bad := filepath.Join(dir, "backups", "unsafe.zip")
	f, _ := os.Create(bad)
	z := zip.NewWriter(f)
	entry, _ := z.Create("../accounts.json")
	entry.Write([]byte("{}"))
	z.Close()
	f.Close()
	if err = s.restoreBackup("unsafe.zip", "test"); err == nil || s.rooms[r.ID] != restored {
		t.Fatal("unsafe restore changed world")
	}
}

func TestExtensionCancellationModificationAndCommit(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	s.rooms[r.ID] = r
	defer delete(r.Peers, p)
	if s.giveXP(p, 13) || p.ext().XPPoints != 0 {
		t.Fatal("cancelled xp committed")
	}
	if !s.giveXP(p, 7) || p.ext().XPLevel != 1 || p.ext().XPPoints != 7 {
		t.Fatal("mutable xp ignored")
	}
	if s.addEffect(p, "slowness", 20, 0) || p.ext().Effects["slowness"] != nil {
		t.Fatal("cancelled effect committed")
	}
	if !s.addEffect(p, "speed", 20, 0) || p.ext().Effects["speed"].Ticks != 40 || p.ext().Effects["speed"].Amp != 2 {
		t.Fatal("mutable effect ignored")
	}
	p.ext().Absorb = 6
	before := p.state.Health
	if s.damagePlayer(p, 4, "immune", &api.DamageContext{Cause: "plugin"}) || p.ext().Absorb != 6 || p.state.Health != before {
		t.Fatal("cancelled damage consumed absorption")
	}
	if s.setRule(r, p, "pvp", false, "protected") || !s.gameRule(r, "pvp") {
		t.Fatal("cancelled rule committed")
	}
	p.state.Dead = true
	p.state.Health = 0
	if s.respawnPlayer(p) || !p.state.Dead || p.state.Health != 0 {
		t.Fatal("cancelled dimension respawn committed")
	}
	p.state.Dead = false
	p.state.Health = 20
	pr := s.launchFrom(p, projArrow, 1)
	pr.Damage = 13
	if s.spawnProjectile(r, pr) || len(r.rt().projectiles) != 0 {
		t.Fatal("cancelled projectile committed")
	}
	if _, err := s.backup("tester", "blocked"); err == nil {
		t.Fatal("cancelled backup committed")
	}
	source, landing := blockKey(10, 65, 10), blockKey(10, 60, 10)
	r.Edits[source] = blkSand
	r.Edits[landing] = 0
	if s.setBlocks(r, [][4]int{{10, 65, 10, 0}, {10, 60, 10, blkSand}}, 0, "gravity") || r.Edits[source] != blkSand || r.Edits[landing] != 0 {
		t.Fatal("cancelled physics lost block")
	}
	path := filepath.Join(s.config.DataDir, "plugins", "test", "extensions.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	successXP, successEffect := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Name == api.PlayerExperienceChanged {
			successXP++
		}
		if e.Name == api.PlayerEffectApplied {
			successEffect++
		}
		if e.Data["undeclared"] != nil {
			t.Fatal("undeclared modification escaped")
		}
		if e.Name == api.PlayerDimensionChanged || e.Name == api.BackupCreated || e.Name == api.BlockPhysicsChanged || e.Name == api.ProjectileLaunched {
			t.Fatalf("cancelled success event %s", e.Name)
		}
	}
	if successXP != 1 || successEffect != 1 {
		t.Fatal("after events missing or duplicated")
	}
}

func TestInfiniteStreamingPerPlayerOriginsAndEpoch(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r := newRoom("signedwireworld1", "wire", "creative", 0)
	r.Infinite = true
	r.Generator = generatorVersion
	s.rooms[r.ID] = r
	x, y, z := -18000000, 100, 18000000
	makePeer := func(id uint16, x float64) *Peer {
		p := &Peer{room: r, id: id, send: make(chan packet, 256), rd: 1, state: &Player{X: x, Y: 100, Z: 18000000.5, Health: 20, Inv: &Inventory{Slots: make(Stacks, 46)}}}
		p.setOrigin()
		r.Peers[p] = true
		s.stream(p)
		// API v1 terrain now arrives in small batches after background preparation.
		// Drive one continuation while holding the world lock; worker generation
		// is independent of it, and the nearest chunk must be delivered first.
		center := chunkCoord(int(math.Floor(p.state.X)), int(math.Floor(p.state.Z)))
		awaitPreparedChunk(t, s, r, center)
		p.chunkStreamAt = time.Time{}
		s.stream(p)
		return p
	}
	a, b := makePeer(1, -18000000.5), makePeer(2, -17999984.5)
	defer delete(r.Peers, a)
	defer delete(r.Peers, b)
	awaitPreparedChunk(t, s, r, chunkCoord(x, z))
	for _, p := range []*Peer{a, b} {
		full := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			p.chunkStreamAt = time.Time{}
			s.stream(p)
			for len(p.send) > 0 {
				v := <-p.send
				if len(v.body) > 8 && v.body[0] == 13 {
					full = true
					if binary.LittleEndian.Uint32(v.body[4:]) != p.epoch {
						t.Fatal("terrain epoch mismatch")
					}
				}
			}
			// A limited batch may contain only the center, whereas the shared
			// edit is in A's adjacent chunk. Wait for that target to be visible
			// to both peers before asserting the following edit broadcast.
			if full && p.knows(x, z) {
				break
			}
			s.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			s.mu.Lock()
		}
		if !full || !p.knows(x, z) {
			t.Fatal("missing generated terrain for shared edit")
		}
	}
	if !s.setBlock(r, x, y, z, 1, setQuiet) {
		t.Fatal("signed edit rejected")
	}
	for _, p := range []*Peer{a, b} {
		var v packet
		select {
		case v = <-p.send:
		default:
			t.Fatal("visible terrain edit was not broadcast")
		}
		data := v.body
		if data[0] != 12 || int(binary.LittleEndian.Uint32(data[4:])) != p.localChunk(chunkCoord(x, z)) || int(binary.LittleEndian.Uint16(data[10:])) != chunkIndex(x, y, z) || data[12] != 1 {
			t.Fatal("delta origin mismatch")
		}
	}
	before := a.state.X
	packet := make([]byte, 34)
	packet[0] = 14
	binary.LittleEndian.PutUint32(packet[1:], a.epoch-1)
	s.binary(a, packet)
	if a.state.X != before {
		t.Fatal("stale epoch moved player")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	file := s.chunkFile(r, chunkCoord(x, z))
	info, _ := os.Stat(file)
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(file)
	if info.ModTime() != again.ModTime() {
		t.Fatal("unchanged chunk recompressed")
	}
}
