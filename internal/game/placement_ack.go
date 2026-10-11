package game

import "math"

// Preview coordinates only select a bounded reconciliation snapshot. They never
// select what gets placed, consume an item, or change plugin/rule validation.
type BlockPosition struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

func (s *Server) placementAck(p *Peer, m Message) func() {
	if m.T != "place" || m.Q <= 0 || m.Q > 2147483647 || len(m.Preview) == 0 || len(m.Preview) > 4 || !s.reachable(p, m) {
		return nil
	}
	r := p.room
	seen := map[BlockPosition]bool{}
	for _, c := range m.Preview {
		if seen[c] || !r.inside(c.X, c.Z) || c.Y < 1 || c.Y >= 128 || math.Abs(float64(c.X)-float64(m.X))+math.Abs(float64(c.Y)-float64(m.Y))+math.Abs(float64(c.Z)-float64(m.Z)) > 2 {
			return nil
		}
		seen[c] = true
	}
	p.inventoryPlaceQ = m.Q
	return func() {
		blocks := make([]map[string]any, 0, len(m.Preview))
		for _, c := range m.Preview {
			blocks = append(blocks, map[string]any{"x": c.X, "y": c.Y, "z": c.Z, "b": s.block(r, c.X, c.Y, c.Z)})
		}
		p.json(map[string]any{"t": "placeack", "q": m.Q, "blocks": blocks, "inventory": s.inventorySnapshot(p), "epoch": p.epoch, "dim": r.dimName()})
	}
}
