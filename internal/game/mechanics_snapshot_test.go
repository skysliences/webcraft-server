package game

import (
	"encoding/json"
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"reflect"
	"testing"
)

func TestMechanicSnapshotBatchesCompleteAndRecordMetadata(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, count := range []int{0, 256, 257, 512} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r, p := redstoneTestPeer(s, 500)
			r.Mode = "creative"
			p.epoch = 8
			record := customStack(500, 1)
			for i := 0; i < count; i++ {
				x, z := 8+i%16, 8+i/16
				k := blockKey(x, 60, z)
				r.Edits[k] = blkJukebox
				storeMechanic(r, k, &MechanicState{Kind: "jukebox", Facing: 3, Record: record})
				rk := blockKey(x, 61, z)
				r.Edits[rk] = blkRail
				storeRail(r, rk, &RailState{Kind: "rail", Shape: "north_south"})
			}
			s.syncMechanicsFor(p)
			for _, typ := range []string{"mech", "rail"} {
				batches := max(1, (count+255)/256)
				total := 0
				for index := 0; index < batches; index++ {
					if len(p.send) == 0 {
						t.Fatal("missing snapshot batch")
					}
					var packet struct {
						T        string `json:"t"`
						Reset    bool   `json:"reset"`
						Complete bool   `json:"complete"`
						Epoch    uint32 `json:"epoch"`
						WorldID  string `json:"worldId"`
						Blocks   []struct {
							ID     int            `json:"id"`
							Record *api.ItemStack `json:"record"`
						} `json:"blocks"`
					}
					if err := json.Unmarshal((<-p.send).body, &packet); err != nil {
						t.Fatal(err)
					}
					if packet.T != typ || packet.Reset != (index == 0) || packet.Complete != (index == batches-1) || packet.Epoch != 8 || packet.WorldID != r.ID || len(packet.Blocks) > 256 {
						t.Fatalf("invalid snapshot batch flags: %+v", packet)
					}
					for _, block := range packet.Blocks {
						if typ == "mech" && (block.ID != blkJukebox || !reflect.DeepEqual(block.Record, itemSnapshot(record))) {
							t.Fatal("record snapshot lost complete item metadata")
						}
					}
					total += len(packet.Blocks)
				}
				if total != count {
					t.Fatalf("snapshot has %d components, want %d", total, count)
				}
			}
			if len(p.send) != 0 {
				t.Fatal("unexpected trailing snapshot")
			}
		})
	}
}
