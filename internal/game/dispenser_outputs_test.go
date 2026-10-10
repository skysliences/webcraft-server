package game

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "mcraft/backend/pkg/plugin"
)

func dispenserOutputSlots(e *api.Event, key string) Stacks {
	raw, _ := json.Marshal(e.Data[key])
	var slots Stacks
	json.Unmarshal(raw, &slots)
	return slots
}

func dispenserOutputMetaStack(id, count int) *Stack {
	v := customStack(id, count)
	v.Meta.Attributes = []api.AttributeModifier{{ID: "test:output", Attribute: api.MaxHealth, Amount: 3, Operation: api.Add, Slot: "any"}}
	v.Meta.CustomData["test:nested"] = map[string]any{"values": []any{"kept", float64(2)}}
	return v
}

func TestDispenserFluidOutputsMetadataEventsAndNether(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tc := range []struct {
		name                 string
		item, target, result int
		next                 int
		dim                  string
		before, after        string
	}{
		{"water_empty", itemWaterBucket, 0, itemBucket, blkWaterSource, "overworld", api.BucketEmptying, api.BucketEmptied},
		{"lava_empty", 308, 0, itemBucket, blkLavaSource, "overworld", api.BucketEmptying, api.BucketEmptied},
		{"nether_water", itemWaterBucket, 0, itemBucket, 0, "nether", api.BucketEmptying, api.BucketEmptied},
		{"water_fill", itemBucket, blkWaterSource, itemWaterBucket, 0, "overworld", api.BucketFilling, api.BucketFilled},
		{"lava_fill", itemBucket, blkLavaSource, 308, 0, "overworld", api.BucketFilling, api.BucketFilled},
		{"bottle_fill", itemGlassBottle, blkWaterSource, itemWaterBottle, blkWaterSource, "overworld", api.BottleFilling, api.BottleFilled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, k, state, _, _ := dispenserEntityFixture(s, tc.item, 1)
			r.Dim = tc.dim
			r.Edits[blockKey(13, 60, 10)] = tc.target
			r.Containers[k].Slots[0] = dispenserOutputMetaStack(tc.item, 1)
			before := copyStack(r.Containers[k].Slots[0])
			start := len(mechanicEvents(t, s))
			if !s.dispenseMechanic(r, k, state) || s.block(r, 13, 60, 10) != tc.next || r.Containers[k].Slots[0] == nil || r.Containers[k].Slots[0].ID != tc.result || !reflect.DeepEqual(before.Meta, r.Containers[k].Slots[0].Meta) || len(r.Drops) != 0 {
				t.Fatalf("fluid transaction changed world/source/meta incorrectly: %v %v", r.Containers[k].Slots[0], s.block(r, 13, 60, 10))
			}
			seenBefore, seenAfter := false, false
			for _, event := range mechanicEvents(t, s)[start:] {
				if event.Name != tc.before && event.Name != tc.after && event.Name != api.BlockDispensed {
					continue
				}
				seenBefore = seenBefore || event.Name == tc.before
				seenAfter = seenAfter || event.Name == tc.after
				if event.Item("stack").ID != tc.item || event.Item("before").ID != tc.item || event.Item("after").ID != tc.result || !reflect.DeepEqual(event.Item("stack").Meta, before.Meta) || event.Data["origin"] == nil || event.Data["target"] == nil || event.String("source") != "dispenser" {
					t.Fatalf("event lacks full source/result metadata: %+v", event)
				}
				slotsBefore, slotsAfter := dispenserOutputSlots(event, "containerBefore"), dispenserOutputSlots(event, "containerAfter")
				if len(slotsBefore) != 9 || len(slotsAfter) != 9 || slotsBefore[0].ID != tc.item || slotsAfter[0].ID != tc.result {
					t.Fatal("fluid event omitted committed source container snapshots")
				}
				if tc.name == "nether_water" && event.Name == tc.after && event.Data["evaporated"] != true {
					t.Fatal("nether evaporation completion omitted its result")
				}
			}
			if !seenBefore || !seenAfter {
				t.Fatal("fluid-specific before/after events were omitted")
			}
		})
	}
}

func TestDispenserFluidOverflowActualOutputAndVeto(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range []int{itemBucket, itemGlassBottle} {
		for _, cancelled := range []string{"", api.ItemSpawning, api.BlockDispensing, api.InventoryChanging} {
			r, k, state, _, _ := dispenserEntityFixture(s, item, s.rules.max(item))
			r.Edits[blockKey(13, 60, 10)] = blkWaterSource
			for i := range r.Containers[k].Slots {
				r.Containers[k].Slots[i] = dispenserOutputMetaStack(item, s.rules.max(item))
			}
			before := cloneStacks(r.Containers[k].Slots)
			beforeDropID := r.NextDropID
			start := len(mechanicEvents(t, s))
			s.pluginSource = ""
			if cancelled != "" {
				s.pluginSource = "plugin:cancel-" + cancelled
			}
			ok := s.dispenseMechanic(r, k, state)
			s.pluginSource = ""
			if cancelled != "" {
				if ok || !reflect.DeepEqual(before, r.Containers[k].Slots) || s.block(r, 13, 60, 10) != blkWaterSource || len(r.Drops) != 0 || r.NextDropID != beforeDropID || mechanicSnapshot(r, k).Powered {
					t.Fatalf("%s overflow veto changed source/fluid/drop ID", cancelled)
				}
				for _, event := range mechanicEvents(t, s)[start:] {
					if !strings.HasSuffix(event.Name, ".before") {
						t.Fatalf("overflow veto emitted success %s", event.Name)
					}
				}
				continue
			}
			out := itemWaterBucket
			if item == itemGlassBottle {
				out = itemWaterBottle
			}
			if !ok || len(r.Drops) != 1 {
				t.Fatal("full source did not eject its transformed return item")
			}
			for _, drop := range r.Drops {
				if drop.Stack.ID != out || drop.Stack.Count != 1 || !reflect.DeepEqual(before[0].Meta, drop.Stack.Meta) {
					t.Fatal("overflow lost result identity or complete metadata")
				}
			}
			total := 0
			for _, slot := range r.Containers[k].Slots {
				if slot == nil || slot.ID != item || !reflect.DeepEqual(slot.Meta, before[0].Meta) {
					t.Fatal("overflow changed an unrelated source slot")
				}
				total += slot.Count
			}
			if total != 9*s.rules.max(item)-1 {
				t.Fatal("overflow did not consume exactly one source")
			}
			seen := 0
			for _, event := range mechanicEvents(t, s)[start:] {
				if event.Name == api.ItemSpawning || event.Name == api.ItemSpawned {
					seen++
					if event.Item("stack").ID != out || event.Item("input").ID != item || event.Item("sourceBefore").Count != s.rules.max(item) || event.Item("sourceAfter").Count != s.rules.max(item)-1 || event.Number("count") != 1 || event.Data["origin"] == nil {
						t.Fatal("overflow drop event described the input instead of the actual output")
					}
				}
			}
			if seen != 2 {
				t.Fatal("overflow missed real drop before/after events")
			}
		}
	}
}

func TestDispenserFluidProtectionRetainsAllState(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tc := range []struct {
		item, target int
		event        string
	}{
		{itemWaterBucket, 0, api.BucketEmptying},
		{itemWaterBucket, 0, api.BlockPlacing},
		{itemBucket, blkWaterSource, api.BucketFilling},
		{itemBucket, blkWaterSource, api.BlockBreaking},
		{itemGlassBottle, blkWaterSource, api.BottleFilling},
	} {
		r, k, state, _, _ := dispenserEntityFixture(s, tc.item, 1)
		r.Edits[blockKey(13, 60, 10)] = tc.target
		before := cloneStacks(r.Containers[k].Slots)
		start := len(mechanicEvents(t, s))
		s.pluginSource = "plugin:cancel-" + tc.event
		if s.dispenseMechanic(r, k, state) || !reflect.DeepEqual(before, r.Containers[k].Slots) || s.block(r, 13, 60, 10) != tc.target || mechanicSnapshot(r, k).Powered || r.NextDropID != 1 {
			t.Fatalf("%s fluid protection partially committed", tc.event)
		}
		for _, event := range mechanicEvents(t, s)[start:] {
			if !strings.HasSuffix(event.Name, ".before") {
				t.Fatalf("cancelled fluid operation emitted %s", event.Name)
			}
		}
	}
	s.pluginSource = ""
}

func portableOutputFixture(t *testing.T) (*Stack, Stacks) {
	t.Helper()
	contents := make(Stacks, 27)
	contents[0] = dispenserOutputMetaStack(318, 64)
	contents[14] = dispenserOutputMetaStack(276, 1)
	contents[14].Damage, contents[14].Ench = 7, map[string]int{"sharpness": 2}
	contents[26] = dispenserOutputMetaStack(itemArrow, 3)
	raw, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	box := dispenserOutputMetaStack(itemShulkerBox, 1)
	box.Meta.DisplayName, box.Meta.Lore = "完整元数据便携盒", []string{"自定义 Lore", "保持第二行"}
	box.Meta.CustomData[shulkerDataKey] = string(raw)
	return box, contents
}

func minePortableOutput(s *Server, p *Peer, x, y, z int) {
	p.state.Inv.Slots[0], p.state.Inv.Selected = customStack(s.id("Iron Pickaxe"), 1), 0
	s.survivalMessage(p, Message{T: "dig", S: 0, X: x, Y: y, Z: z, F: 1})
	if p.mining != nil {
		p.mining.At = time.Now().Add(-time.Hour)
	}
	s.survivalMessage(p, Message{T: "dig", S: 2, X: x, Y: y, Z: z, F: 1})
}

func TestDispenserPortableBoxMetadataPlacementBreakAndRestart(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	box, contents := portableOutputFixture(t)
	r, k, state, _, _ := dispenserEntityFixture(s, itemShulkerBox, 1)
	r.Containers[k].Slots[0] = copyStack(box)
	if !s.dispenseMechanic(r, k, state) || s.block(r, 13, 60, 10) != blkShulkerBox || r.Containers[k].Slots[0] != nil {
		t.Fatal("dispenser did not place the portable box")
	}
	target := blockKey(13, 60, 10)
	c := r.Containers[target]
	if c == nil || !reflect.DeepEqual(c.Slots, contents) || !reflect.DeepEqual(c.Item, box) {
		t.Fatal("portable box lost source base metadata or one of its 27 slots")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil || !reflect.DeepEqual(loader.rooms[r.ID].Containers[target], c) {
		t.Fatalf("restart lost portable container state: %v", err)
	}
	for p := range r.Peers {
		minePortableOutput(s, p, 13, 60, 10)
		break
	}
	if s.block(r, 13, 60, 10) != 0 || r.Containers[target] != nil || len(r.Drops) != 1 {
		t.Fatal("mining a portable box duplicated or deleted its contents")
	}
	for _, drop := range r.Drops {
		if !reflect.DeepEqual(drop.Stack, box) {
			t.Fatalf("dropped portable box lost Lore, attributes or nested contents: %+v", drop.Stack)
		}
		c, ok := s.portableBox(drop.Stack)
		if !ok || !reflect.DeepEqual(c.Slots, contents) {
			t.Fatal("mined box could not restore all contents")
		}
	}
}

func TestPortableBoxLastPlayerItemAndCancellationKeepContents(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range []string{api.InventoryChanging, api.BlockPlacing, ""} {
		r, p := dropTestWorld(s)
		box, contents := portableOutputFixture(t)
		p.state.Inv.Slots[0], p.state.Inv.Selected = copyStack(box), 0
		before := copyStack(p.state.Inv.Slots[0])
		start := len(mechanicEvents(t, s))
		s.pluginSource = ""
		if cancel != "" {
			s.pluginSource = "plugin:cancel-" + cancel
		}
		s.survivalMessage(p, Message{T: "place", X: 12, Y: 59, Z: 10, F: 1})
		k := blockKey(12, 60, 10)
		if cancel != "" {
			if s.block(r, 12, 60, 10) != 0 || r.Containers[k] != nil || !reflect.DeepEqual(before, p.state.Inv.Slots[0]) {
				t.Fatal("cancelled last-box placement consumed metadata or contents")
			}
			for _, event := range mechanicEvents(t, s)[start:] {
				if !strings.HasSuffix(event.Name, ".before") {
					t.Fatal("cancelled box placement published success")
				}
			}
			continue
		}
		if s.block(r, 12, 60, 10) != blkShulkerBox || p.state.Inv.Slots[0] != nil || r.Containers[k] == nil || !reflect.DeepEqual(r.Containers[k].Slots, contents) || !reflect.DeepEqual(r.Containers[k].Item, box) {
			t.Fatal("placing the last player item erased its portable box contents")
		}
		beforeContainer := r.Containers[k]
		start = len(mechanicEvents(t, s))
		s.pluginSource = "plugin:cancel-" + api.ItemSpawning
		minePortableOutput(s, p, 12, 60, 10)
		if s.block(r, 12, 60, 10) != blkShulkerBox || r.Containers[k] != beforeContainer || len(r.Drops) != 0 || r.NextDropID != 1 || !reflect.DeepEqual(beforeContainer.Slots, contents) {
			t.Fatal("cancelled portable box drop destroyed stored contents")
		}
		for _, event := range mechanicEvents(t, s)[start:] {
			if event.Name == api.BlockBroken || event.Name == api.ItemSpawned || event.Name == api.InventoryChanged {
				t.Fatal("cancelled portable box break emitted success")
			}
		}
	}
	s.pluginSource = ""
}

func TestPortableBoxAllRichSlotsPackingPluginSnapshotsAndRestart(t *testing.T) {
	s := mechanicPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	contents := make(Stacks, 27)
	for i := range contents {
		contents[i] = dispenserOutputMetaStack(318, 64)
		contents[i].Meta.Lore = make([]string, 10)
		for line := range contents[i].Meta.Lore {
			contents[i].Meta.Lore[line] = strings.Repeat("L", 160)
		}
		contents[i].Meta.CustomData["test:slot"] = float64(i)
		if !s.validStack(contents[i], 64) {
			t.Fatal("individual rich item exceeds ordinary item validation")
		}
	}
	base := dispenserOutputMetaStack(itemShulkerBox, 1)
	base.Meta.Lore = []string{"便携盒本身的 Lore"}
	box := s.boxDrop(&Container{Kind: "chest", Slots: contents, Item: base})
	raw, err := json.Marshal(box.Meta)
	if err != nil || len(raw) <= 8192 || !s.validStack(box, 1) {
		t.Fatal("27 individually valid rich items cannot be packed as a portable inventory")
	}
	decoded, ok := s.portableBox(box)
	if !ok || !reflect.DeepEqual(decoded.Slots, contents) || decoded.Item.Meta.Lore[0] != base.Meta.Lore[0] {
		t.Fatal("packed rich inventory erased base Lore or child metadata")
	}
	r, k, state, _, _ := dispenserEntityFixture(s, itemShulkerBox, 1)
	for i := range r.Containers[k].Slots {
		r.Containers[k].Slots[i] = copyStack(box)
	}
	start := len(mechanicEvents(t, s))
	if !s.dispenseMechanic(r, k, state) || s.block(r, 13, 60, 10) != blkShulkerBox {
		t.Fatal("plugin transport could not carry bounded full container snapshots")
	}
	target := blockKey(13, 60, 10)
	if !reflect.DeepEqual(r.Containers[target].Slots, contents) || !reflect.DeepEqual(r.Containers[target].Item, box) {
		t.Fatal("dispenser changed the large portable inventory")
	}
	seen := 0
	for _, event := range mechanicEvents(t, s)[start:] {
		if event.Name != api.BlockDispensed {
			continue
		}
		seen++
		before, after := dispenserOutputSlots(event, "containerBefore"), dispenserOutputSlots(event, "containerAfter")
		if len(before) != 9 || len(after) != 9 || !reflect.DeepEqual(before[0], box) || event.Item("stack").Meta.Lore[0] != base.Meta.Lore[0] {
			t.Fatal("large plugin completion snapshot was truncated")
		}
		count := 0
		for _, v := range after {
			if v != nil {
				count++
				if !reflect.DeepEqual(v, box) {
					t.Fatal("large unrelated portable inventory changed")
				}
			}
		}
		if count != 8 {
			t.Fatal("large container transaction did not consume one box")
		}
	}
	if seen != 1 {
		t.Fatal("large transaction did not publish one completion event")
	}
	if err := s.saveRoom(r); err != nil {
		t.Fatal(err)
	}
	loader := &Server{config: s.config, rules: s.rules, rooms: map[string]*Room{}, rng: rand.New(rand.NewSource(1))}
	if err := loader.loadRooms(); err != nil || !reflect.DeepEqual(loader.rooms[r.ID].Containers[target], r.Containers[target]) {
		t.Fatalf("large portable inventory did not survive restart: %v", err)
	}
	if !reflect.DeepEqual(loader.rooms[r.ID].Containers[k].Slots, r.Containers[k].Slots) {
		t.Fatal("restart erased an unrelated rich portable box")
	}
}

func TestPortableBoxRejectsNestedReservedPayloadAndOrdinaryOversize(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kind := range []string{"nested_box", "nested_reserved_key", "child_oversize", "base_oversize"} {
		contents := make(Stacks, 27)
		contents[0] = customStack(318, 1)
		box := customStack(itemShulkerBox, 1)
		switch kind {
		case "nested_box":
			contents[0] = stack(itemShulkerBox, 1, 0)
		case "nested_reserved_key":
			// A non-box ID still cannot smuggle a reserved portable inventory.
			raw, _ := json.Marshal(make(Stacks, 27))
			contents[0].Meta.CustomData[shulkerDataKey] = string(raw)
		case "child_oversize":
			contents[0].Meta.CustomData["test:ordinary"] = strings.Repeat("x", 9000)
		case "base_oversize":
			box.Meta.CustomData["test:ordinary"] = strings.Repeat("x", 9000)
		}
		raw, _ := json.Marshal(contents)
		box.Meta.CustomData[shulkerDataKey] = string(raw)
		if validMeta(box.Meta) || s.validStack(box, 1) {
			t.Fatalf("%s portable contents bypassed bounded validation", kind)
		}
		if _, ok := s.portableBox(box); ok {
			t.Fatalf("%s restored an invalid nested inventory", kind)
		}
	}
}

func TestPortableBoxFullMetadataMCCEvictionAndReload(t *testing.T) {
	s := dispenserEntityTestServer(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	base, contents := portableOutputFixture(t)
	for i := range contents {
		contents[i] = dispenserOutputMetaStack(318, 64)
		contents[i].Meta.CustomData["test:slot"] = float64(i)
	}
	box := s.boxDrop(&Container{Kind: "chest", Slots: contents, Item: base})
	container, ok := s.portableBox(box)
	if !ok {
		t.Fatal("valid 27-slot box could not be prepared")
	}
	r := newRoom("abc0123456789def", "portable MCC", "survival", 0)
	r.Infinite, r.Generator = true, generatorVersion
	c := chunkCoord(-17, 18000)
	k := blockKey(-17, 95, 18000)
	r.Edits[k], r.Containers[k] = blkShulkerBox, container
	terrain := make([]byte, chunkVolume)
	d := chunkData(r, c, terrain)
	d.VegetationVersion = vegetationVersion
	if s.validateChunk(d) != nil {
		t.Fatal("valid portable chunk was rejected")
	}
	raw, _ := json.Marshal(d)
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(raw)
	gz.Close()
	file := s.chunkFile(r, c)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := newRoom(r.ID, r.Name, r.Mode, 0)
	fresh.Infinite, fresh.Generator = true, generatorVersion
	if _, err := s.ensureChunk(fresh, c); err != nil || !reflect.DeepEqual(fresh.Containers[k], container) {
		t.Fatalf("MCC lost portable base/item metadata: %v", err)
	}
	fresh.chunks[c].Used = 0
	for i := 0; i < 512; i++ {
		fresh.chunks[ChunkCoord{1000 + i, 0}] = &WorldChunk{Base: terrain, Used: 1, VegetationVersion: vegetationVersion}
	}
	s.trimChunks(fresh)
	if fresh.chunks[c] != nil || fresh.Containers[k] != nil {
		t.Fatal("evicted portable container remained live")
	}
	if _, err := s.ensureChunk(fresh, c); err != nil || !reflect.DeepEqual(fresh.Containers[k], container) || !reflect.DeepEqual(s.boxDrop(fresh.Containers[k]), box) {
		t.Fatalf("MCC reload changed full portable contents: %v", err)
	}
	d.Containers[chunkIndex(-17, 95, 18000)].Item = stack(1, 1, 0)
	if s.validateChunk(d) == nil {
		t.Fatal("MCC accepted a non-portable item as a portable base snapshot")
	}
}
