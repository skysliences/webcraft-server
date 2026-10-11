package game

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	api "mcraft/backend/pkg/plugin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBlockRegistryProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "block-registry-fixture" {
		return
	}
	p := api.New()
	p.OnEnable = func(c *api.Context) error {
		c.RegisterBlock(api.BlockDefinition{Key: "registry:highest", ID: 65535, Name: "Registry highest", ModelBlock: 1, Hardness: .5})
		c.RegisterBlock(api.BlockDefinition{Key: "registry:auto", Name: "Registry automatic", ModelBlock: 21, Hardness: 1})
		c.RegisterBlock(api.BlockDefinition{Key: "registry:cancelled", ID: 65000, Name: "Registry cancelled", ModelBlock: 1, Hardness: 1})
		c.RegisterItem(api.ItemDefinition{Key: "registry:tool", Name: "Registry tool", ModelItem: 276, Stack: 1, Durability: 200})
		return nil
	}
	for _, spec := range api.Events() {
		name := spec.Name
		if !strings.HasPrefix(name, "registry.") && name != api.BlockPlacing && name != api.BlockPlaced && name != api.BlockChanged && name != api.BlockBreaking && name != api.BlockBroken {
			continue
		}
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(raw, '\n'))
			e.Cancel("monitor ignored")
			e.Data["entry"] = nil
			return err
		})
	}
	p.On(api.RegistryRegistering, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		raw, _ := json.Marshal(e.Data["entry"])
		var d api.RegistryEntry
		json.Unmarshal(raw, &d)
		if entryKey(d) == "registry:cancelled" {
			e.Cancel("registration protected")
		}
		e.Data["entry"] = nil
		return nil
	})
	p.On(api.BlockPlacing, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Number("x") == 13 {
			e.Cancel("custom block protected")
		}
		return nil
	})
	p.On(api.BlockBreaking, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Number("x") == 12 {
			e.Cancel("custom break protected")
		}
		return nil
	})
	p.On(api.RegistrySyncing, api.Subscription{}, func(c *api.Context, e *api.Event) error {
		if e.Player != nil && e.Player.Name == "RegistrySyncReject" {
			e.Cancel("registry synchronization protected")
		}
		return nil
	})
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func registryFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	plugins := t.TempDir()
	dir := filepath.Join(plugins, "registry")
	os.MkdirAll(dir, 0700)
	exe, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "plugin"), exe, 0700)
	raw, _ := json.Marshal(map[string]any{"id": "registry", "name": "registry", "version": "1", "apiVersion": 1, "entry": "plugin", "args": []string{"-test.run=^TestBlockRegistryProcess$", "block-registry-fixture"}})
	os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600)
	data := t.TempDir()
	s, err := New(Config{DataDir: data, EngineFile: "../../../src/client/engine.wasm", PluginDir: plugins, PluginTimeout: time.Second, MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	return s, data, plugins
}
func registryEventsRead(t *testing.T, data string) []api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(data, "plugins/registry/events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Event
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var e api.Event
		if err = json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}
func TestBlockRegistryPersistenceCancellationAndMetadata(t *testing.T) {
	s, dir, plugins := registryFixture(t)
	id, tool := s.id("registry:auto"), s.id("registry:tool")
	if id != 1024 || tool != 1025 || !s.rules.validBlock(65535) || s.rules.validBlock(tool) || s.rules.max(tool) != 1 || s.id("registry:cancelled") != 0 {
		t.Fatal("invalid registry", s.registry)
	}
	if s.rules.mine(1, tool) != s.rules.mine(1, 276) {
		t.Fatal("custom tool lost its template mining rule")
	}
	for _, e := range registryEventsRead(t, dir) {
		if e.Name == api.RegistryRegistered {
			var v api.RegistryEntry
			raw, _ := json.Marshal(e.Data["entry"])
			json.Unmarshal(raw, &v)
			if entryKey(v) == "registry:cancelled" || entryID(v) == 0 {
				t.Fatal("cancelled or modified definition committed")
			}
		}
	}
	s.mu.Lock()
	r := newRoom("registryworld123", "registry", "survival", 0)
	s.rooms[r.ID] = r
	p := &Peer{room: r, id: 1, sid: "registryplayer12", builder: "registry", send: make(chan packet, 256), blockProtocol: 2, state: &Player{X: 10.5, Y: 60, Z: 10.5, Health: 20, Food: 20, Inv: &Inventory{Slots: make(Stacks, 46)}}, known: map[int]bool{}}
	p.state.Inv.Slots[0] = stack(65535, 2, 0)
	p.state.Inv.Slots[0].Meta = &api.ItemMeta{DisplayName: "kept", Lore: []string{"custom lore"}, CustomData: map[string]any{"registry:test": "kept"}}
	r.Players[p.sid] = p.state
	for _, x := range []int{12, 13} {
		r.Edits[blockKey(x, 59, 10)] = 1
		r.Edits[blockKey(x, 60, 10)] = 0
	}
	s.survivalMessage(p, placePredictionMessage(11))
	ackPlacedBlock(t, p, 11, 65535)
	if s.block(r, 12, 60, 10) != 65535 || s.heldItem(p).Count != 1 {
		t.Fatal("custom placement failed")
	}
	before := copyStack(s.heldItem(p))
	start := len(registryEventsRead(t, dir))
	m := placePredictionMessage(12)
	m.X = 13
	m.Preview = []BlockPosition{{X: 13, Y: 60, Z: 10}}
	s.survivalMessage(p, m)
	if s.block(r, 13, 60, 10) != 0 || !reflect.DeepEqual(before, s.heldItem(p)) {
		t.Fatal("cancelled custom placement changed state")
	}
	for _, e := range registryEventsRead(t, dir)[start:] {
		if e.Name == api.BlockPlaced || e.Name == api.BlockChanged {
			t.Fatal("cancelled placement emitted success")
		}
	}
	start = len(registryEventsRead(t, dir))
	s.survivalMessage(p, Message{T: "dig", S: 0, X: 12, Y: 60, Z: 10})
	if p.mining == nil {
		t.Fatal("custom block mining did not start")
	}
	p.mining.At = time.Now().Add(-5 * time.Second)
	s.survivalMessage(p, Message{T: "dig", S: 2, X: 12, Y: 60, Z: 10})
	if s.block(r, 12, 60, 10) != 65535 || !reflect.DeepEqual(before, s.heldItem(p)) {
		t.Fatal("cancelled custom mining changed world or inventory")
	}
	sawBreak := false
	for _, e := range registryEventsRead(t, dir)[start:] {
		if e.Name == api.BlockBroken || e.Name == api.BlockChanged {
			t.Fatal("cancelled custom mining emitted success")
		}
		if e.Name == api.BlockBreaking {
			sawBreak = true
			if e.String("actor") != p.sid || e.String("oldBlockKey") != "registry:highest" || e.Item("stack") == nil || !reflect.DeepEqual(e.Item("stack").Meta, before.Meta) {
				t.Fatal("custom break omitted identity or item metadata")
			}
		}
	}
	if !sawBreak {
		t.Fatal("missing cancellable custom block break event")
	}
	key := blockKey(12, 60, 10)
	if got := s.dropsAt(r, key, 65535, 0); len(got) != 1 || got[0].ID != 65535 {
		t.Fatal("custom drops", got)
	}
	r.Containers[blockKey(14, 60, 10)] = &Container{Kind: "chest", Slots: Stacks{copyStack(before)}}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Removing the plugin retains registered identities and saved items/blocks.
	s2, err := New(Config{DataDir: dir, EngineFile: "../../../src/client/engine.wasm", MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	s2.mu.Lock()
	restored := s2.rooms[r.ID]
	if restored == nil || s2.id("registry:auto") != id || restored.Edits[key] != 65535 || !reflect.DeepEqual(restored.Players[p.sid].Inv.Slots[0].Meta, before.Meta) || restored.Containers[blockKey(14, 60, 10)].Slots[0].ID != 65535 {
		t.Fatal("registered save restored incorrectly")
	}
	s2.mu.Unlock()
	s2.Close()
	s3, err := New(Config{DataDir: dir, EngineFile: "../../../src/client/engine.wasm", PluginDir: plugins, PluginTimeout: time.Second, MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if s3.id("registry:auto") != id || s3.id("registry:tool") != tool {
		t.Fatal("restart reassigned numeric IDs")
	}
}
func TestRegistryWire16AndChunkSave(t *testing.T) {
	p := &Peer{blockProtocol: 2}
	r := appendChunkRecord(nil, p, 32767, 65535, 128)
	if len(r) != 5 || binary.LittleEndian.Uint16(r[2:]) != 65535 || r[4] != 128 {
		t.Fatal("wide delta")
	}
	base := make([]byte, chunkVolume)
	base[7] = 1
	wire := encodeTerrain16(base, map[int]int{7: 65535})
	var blocks []uint16
	for i := 0; i < len(wire); i += 4 {
		count := binary.LittleEndian.Uint16(wire[i:])
		id := binary.LittleEndian.Uint16(wire[i+2:])
		for n := uint16(0); n < count; n++ {
			blocks = append(blocks, id)
		}
	}
	if len(blocks) != chunkVolume || blocks[7] != 65535 {
		t.Fatal("wide terrain truncated")
	}
	chunk := &WorldChunk{Base: base}
	clean := chunkWireTerrain16(chunk, nil)
	if &clean[0] != &chunkWireTerrain16(chunk, nil)[0] {
		t.Fatal("clean u16 chunks were not cached")
	}
	if bytes.Equal(clean, chunkWireTerrain16(chunk, map[int]int{7: 65535})) || !bytes.Equal(clean, chunkWireTerrain16(chunk, nil)) {
		t.Fatal("live edits changed the immutable u16 terrain cache")
	}
	d := &diskChunk{Version: 1, Base: base, Edits: map[int]int{7: 65535}, Authors: map[int]EditAuthor{7: {After: 65535}}, Containers: map[int]*Container{}, Blocks: map[int]*BlockData{}}
	raw, err := encodeChunkBinary(d)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeChunkBinary(raw)
	if err != nil || restored.Edits[7] != 65535 || restored.Authors[7].After != 65535 {
		t.Fatal("u16 chunk save", err)
	}
	s := &Server{}
	core, _ := loadRules()
	s.coreRules = core
	for _, e := range []api.RegistryEntry{
		{PluginID: "registry", Block: &api.BlockDefinition{Key: "wrong:block", ID: 65535, Name: "x", ModelBlock: 1, Stack: 64}},
		{PluginID: "registry", Block: &api.BlockDefinition{Key: "registry:block", ID: 65536, Name: "x", ModelBlock: 1, Stack: 64}},
		{PluginID: "registry", Block: &api.BlockDefinition{Key: "registry:block", ID: 276, Name: "x", ModelBlock: 1, Stack: 64}},
	} {
		if s.validateRegistryEntry(e) == nil {
			t.Fatal("invalid namespace, range or conflict accepted")
		}
	}
}

func TestBlockRegistryWebSocketNegotiationAndSync(t *testing.T) {
	s, data, _ := registryFixture(t)
	defer s.Close()
	s.accounts.hashCost = bcrypt.MinCost
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	id, key := postRoom(t, h.URL, "creative")
	dial := func(label, ownerKey string, protocol int) *websocket.Conn {
		t.Helper()
		info := accountFixture(t, h.URL, label)
		if ownerKey != "" {
			v, _ := testAccounts.Load(h.URL + " key:" + ownerKey)
			info = v.(testAccountInfo)
		}
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+id, http.Header{"Cookie": {info.Cookie.String()}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		send(t, c, map[string]any{"t": "hello", "v": 4, "key": ownerKey, "rd": 2, "blockProtocol": protocol})
		return c
	}
	owner := dial("RegistryOwner", key, 2)
	welcome := receive(t, owner, "welcome")
	if welcome["blockProtocol"] != float64(2) || welcome["registry"].(map[string]any)["version"] != float64(1) {
		t.Fatal("registry not negotiated before the world snapshot")
	}
	receive(t, owner, "synced")
	guest := dial("RegistryGuest", "", 2)
	receive(t, guest, "welcome")
	receive(t, guest, "synced")
	k := blockKey(510, 95, 510)
	edit := binary.LittleEndian.AppendUint32([]byte{frameEdit16}, toWireKey(k))
	edit = binary.LittleEndian.AppendUint16(edit, 65535)
	if err := owner.WriteMessage(websocket.BinaryMessage, edit); err != nil {
		t.Fatal(err)
	}
	sync := receiveBinary(t, guest, frameSync16)
	if len(sync) != 7 || binary.LittleEndian.Uint16(sync[5:]) != 65535 || binary.LittleEndian.Uint32(sync[1:]) != toWireKey(k) {
		t.Fatal("u16 edit was truncated or lost")
	}
	send(t, owner, map[string]any{"t": "lock", "mode": 2})
	receive(t, owner, "room")
	denied := binary.LittleEndian.AppendUint32([]byte{frameEdit16}, toWireKey(k))
	denied = binary.LittleEndian.AppendUint16(denied, 0)
	if err := guest.WriteMessage(websocket.BinaryMessage, denied); err != nil {
		t.Fatal(err)
	}
	rollback := receiveBinary(t, guest, frameDeny16)
	if binary.LittleEndian.Uint16(rollback[5:]) != 65535 {
		t.Fatal("u16 authoritative rollback was truncated")
	}
	old := dial("RegistryOldClient", "", 0)
	receive(t, old, "sys")
	if _, _, err := old.ReadMessage(); !websocket.IsCloseError(err, 4009) {
		t.Fatal("legacy clients must refresh before custom worlds", err)
	}
	before := len(registryEventsRead(t, data))
	blocked := dial("RegistrySyncReject", "", 2)
	blocked.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := blocked.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, 4009) {
				t.Fatal(err)
			}
			break
		}
		if bytes.Contains(raw, []byte(`"t":"welcome"`)) {
			t.Fatal("cancelled registry sync welcomed a player")
		}
	}
	for _, e := range registryEventsRead(t, data)[before:] {
		if e.Name == api.RegistrySynced && e.Player != nil && e.Player.Name == "RegistrySyncReject" {
			t.Fatal("cancelled registry sync emitted success")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[id].Players[fixtureID(h.URL, "RegistrySyncReject")]; ok {
		t.Fatal("cancelled registry sync committed player state")
	}
}
