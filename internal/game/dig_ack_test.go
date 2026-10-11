package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lastDigAck(t *testing.T, p *Peer) map[string]any {
	t.Helper()
	var ack map[string]any
	for len(p.send) > 0 {
		raw := <-p.send
		if raw.kind != 1 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw.body, &m) == nil && m["t"] == "digack" {
			ack = m
		}
	}
	return ack
}
func TestCreativeDigAcknowledgesImmediateCommitAndRefusals(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.ext().GameMode = "creative"
	key := blockKey(12, 61, 10)
	stone := s.rules.byName["Stone"]
	r.Edits[key] = stone
	m := Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1}
	s.survivalMessage(p, m)
	m.S = 2
	m.Q = 17
	s.survivalMessage(p, m)
	ack := lastDigAck(t, p)
	if r.Edits[key] != 0 || len(r.Drops) != 0 || ack["q"] != float64(17) || ack["b"] != float64(0) {
		t.Fatalf("creative commit: %v", ack)
	}
	r.Edits[key] = stone
	p.mining = nil
	m.Q = 18
	s.survivalMessage(p, m)
	ack = lastDigAck(t, p)
	if r.Edits[key] != stone || ack["b"] != float64(stone) || ack["q"] != float64(18) {
		t.Fatalf("refused missing start: %v", ack)
	}
	p.ext().GameMode = "survival"
	m.S = 0
	s.survivalMessage(p, m)
	m.S = 2
	m.Q = 19
	s.survivalMessage(p, m)
	ack = lastDigAck(t, p)
	if r.Edits[key] != stone || ack["b"] != float64(stone) {
		t.Fatalf("survival timing bypassed: %v", ack)
	}
	m.Q = 0
	s.survivalMessage(p, m)
	if lastDigAck(t, p) != nil {
		t.Fatal("legacy request received opt-in ack")
	}
	m.Q = 20
	m.X = 1000
	s.survivalMessage(p, m)
	if lastDigAck(t, p) != nil {
		t.Fatal("invalid reach probed world state")
	}
}
func TestCancelledCreativeDigAcknowledgesCanonicalBlock(t *testing.T) {
	s, _ := pluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := dropTestWorld(s)
	defer delete(r.Peers, p)
	p.ext().GameMode = "creative"
	stone := s.rules.byName["Stone"]
	key := blockKey(12, 61, 10)
	r.Edits[key] = stone
	m := Message{T: "dig", S: 0, X: 12, Y: 61, Z: 10, F: 1}
	s.survivalMessage(p, m)
	m.S = 2
	m.Q = 31
	s.survivalMessage(p, m)
	ack := lastDigAck(t, p)
	if r.Edits[key] != stone || len(r.Drops) != 0 || ack["q"] != float64(31) || ack["b"] != float64(stone) {
		t.Fatalf("plugin cancellation not reconciled: %v", ack)
	}
	raw, err := os.ReadFile(filepath.Join(s.config.DataDir, "plugins", "test", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event api.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Name == api.BlockBroken {
			t.Fatal("cancelled dig emitted success")
		}
		if event.Name == api.BlockBreaking {
			before++
		}
	}
	if before != 1 {
		t.Fatalf("expected one cancellable break event, got %d", before)
	}
}
