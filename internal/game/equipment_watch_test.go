package game

import (
	"encoding/json"
	"testing"
)

func drainPackets(p *Peer) []string {
	var res []string
	for {
		select {
		case pkt := <-p.send:
			res = append(res, string(pkt.body))
		default:
			return res
		}
	}
}

func TestWatchEquipmentAndNoSurvivalSpam(t *testing.T) {
	s := utilitiesPluginApp(t)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p1 := redstoneTestPeer(s, 297) // player 1
	p2 := dropTestPlayer(r, 2, 10, 60, 10)
	p2.sid = "player2"
	p2.name = "PlayerTwo"
	r.Peers[p2] = true

	// Give p2 some armor and weapons
	p2.state.Inv.Slots[0] = stack(276, 1, 0)  // Diamond Sword in main hand
	p2.state.Inv.Slots[40] = stack(382, 1, 0) // Shield in off-hand
	p2.state.Inv.Slots[39] = stack(310, 1, 0) // Diamond Helmet
	p2.state.Inv.Slots[38] = stack(311, 1, 0) // Diamond Chestplate
	p2.state.Inv.Slots[37] = stack(312, 1, 0) // Diamond Leggings
	p2.state.Inv.Slots[36] = stack(313, 1, 0) // Diamond Boots
	p2.state.Inv.Selected = 0

	drainPackets(p1)

	// 1. Peer 1 watches Peer 2
	s.survivalMessage(p1, Message{
		T:   "watch",
		Add: []uint16{p2.id},
	})

	// Peer 1 should have received an "eq" message with p2's equipment
	pkts1 := drainPackets(p1)
	var foundEq bool
	for _, raw := range pkts1 {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if m["t"] == "eq" {
			foundEq = true
			rows, ok := m["r"].([]any)
			if !ok || len(rows) != 1 {
				t.Fatalf("expected 1 equipment row, got: %v", m["r"])
			}
			row, ok := rows[0].([]any)
			if !ok || len(row) != 12 {
				t.Fatalf("expected row of length 12, got: %v", rows[0])
			}
			// Check id
			if int(row[0].(float64)) != int(p2.id) {
				t.Errorf("expected target ID %d, got %v", p2.id, row[0])
			}
			// Check main hand: 276
			if int(row[1].(float64)) != 276 {
				t.Errorf("expected main hand 276, got %v", row[1])
			}
			// Check off hand: 382
			if int(row[2].(float64)) != 382 {
				t.Errorf("expected off hand 382, got %v", row[2])
			}
			// Check boots: 313
			if int(row[3].(float64)) != 313 {
				t.Errorf("expected boots 313, got %v", row[3])
			}
			// Check legs: 312
			if int(row[4].(float64)) != 312 {
				t.Errorf("expected legs 312, got %v", row[4])
			}
			// Check chest: 311
			if int(row[5].(float64)) != 311 {
				t.Errorf("expected chest 311, got %v", row[5])
			}
			// Check helm: 310
			if int(row[6].(float64)) != 310 {
				t.Errorf("expected helm 310, got %v", row[6])
			}
			break
		}
	}
	if !foundEq {
		t.Fatalf("expected p1 to receive eq message for p2, but got packets: %v", pkts1)
	}

	// 2. Peer 2 selects another slot -> p1 should receive updated eq broadcast
	drainPackets(p1)
	p2.state.Inv.Slots[1] = stack(278, 1, 0) // Diamond Pickaxe in slot 1
	s.survivalMessage(p2, Message{T: "sel", I: 1})

	pkts2 := drainPackets(p1)
	foundBroadcast := false
	for _, raw := range pkts2 {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if m["t"] == "eq" {
			foundBroadcast = true
			rows := m["r"].([]any)
			row := rows[0].([]any)
			if int(row[1].(float64)) != 278 {
				t.Errorf("expected updated main hand 278, got %v", row[1])
			}
			break
		}
	}
	if !foundBroadcast {
		t.Fatalf("expected p1 to receive broadcast eq when p2 changes slot, got: %v", pkts2)
	}

	// 3. Test unknown or ambient packet -> NO "此功能尚未包含在基础生存模式中。" system message!
	drainPackets(p1)
	s.survivalMessage(p1, Message{T: "ambient_test_packet_random"})
	pkts3 := drainPackets(p1)
	for _, raw := range pkts3 {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			if m["t"] == "sys" && m["m"] == "此功能尚未包含在基础生存模式中。" {
				t.Fatalf("found unwanted error spam in chat for ambient packet: %v", raw)
			}
		}
	}
}
