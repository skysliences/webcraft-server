package game

import (
	"math"
)

func (s *Server) mechanicsPower(r *Room, k uint64, dx, dy, dz int) int {
	if v := mechanicSnapshot(r, k); v != nil {
		if v.Kind == "daylight_detector" {
			return v.Power
		}
		if v.Kind == "observer" && v.Power > 0 {
			d := facing6[v.Facing]
			if dx == -d[0] && dy == -d[1] && dz == -d[2] {
				return v.Power
			}
		}
	}
	if v := railState(r, k); v != nil && v.Kind == "detector_rail" && v.Powered {
		return 15
	}
	return 0
}
func (s *Server) daylightPower(r *Room, k uint64, v *MechanicState) int {
	if r.dimName() != "overworld" {
		return 0
	}
	x, y, z := coords(k)
	for h := y + 1; h < 128; h++ {
		if s.mobSolid(r, x, h, z) {
			return 0
		}
	}
	t := float64(r.Time % 24000)
	power := int(math.Round(math.Max(0, math.Cos((t-6000)/24000*2*math.Pi)) * 15))
	if r.ext().Rain {
		power = power * 2 / 3
	}
	if v.Inverted {
		power = 15 - power
	}
	return power
}
func (s *Server) tickMechanics(r *Room) {
	s.tickRails(r)
	for _, k := range sortedMechanicKeys(r) {
		v := mechanicSnapshot(r, k)
		if v == nil {
			continue
		}
		x, y, z := coords(k)
		b := s.block(r, x, y, z)
		if mechanicKinds[b] != v.Kind {
			continue
		}
		if !s.mechanicSupported(r, k, b) {
			s.breakUnsupportedMechanic(r, k, b)
			continue
		}
		switch v.Kind {
		case "observer":
			next := cloneMechanic(v)
			observation := s.observerObservation(r, k, v.Facing)
			if observation != v.Observed {
				next.Observed = observation
				next.Pulse = 2
				next.Power = 15
				next.Powered = true
				s.changeMechanic(r, nil, k, next, "observer_detect")
			} else if v.Pulse > 0 {
				next.Pulse--
				if next.Pulse == 0 {
					next.Power = 0
					next.Powered = false
				}
				s.changeMechanic(r, nil, k, next, "observer_pulse")
			}
		case "lamp":
			v.Power = s.powerAt(r, x, y, z)
			v.Powered = v.Power > 0
			s.changeMechanic(r, nil, k, v, "lamp_signal")
		case "daylight_detector":
			v.Power = s.daylightPower(r, k, v)
			v.Powered = v.Power > 0
			s.changeMechanic(r, nil, k, v, "daylight_time")
		case "dispenser", "dropper":
			powered := s.blockPowered(r, x, y, z)
			if powered && !v.Powered {
				s.dispenseMechanic(r, k, v)
			} else if !powered && v.Powered {
				v.Powered = false
				v.Power = 0
				s.changeMechanic(r, nil, k, v, "mechanic_signal")
			}
		}
	}
}
func (s *Server) breakUnsupportedMechanic(r *Room, k uint64, b int) bool {
	x, y, z := coords(k)
	nextID := r.NextDropID
	var stacks Stacks
	if s.gameRule(r, "doTileDrops") {
		stacks = s.dropsAt(r, k, b, 0)
	}
	drops, err := s.prepareDropsWithContext(r, nil, stacks, float64(x)+.5, float64(y)+.35, float64(z)+.5, "mechanic_support_lost", map[string]any{"actor": "server", "source": "physics", "target": map[string]any{"kind": "block", "x": x, "y": y, "z": z}}, true)
	if err != nil {
		r.NextDropID = nextID
		return false
	}
	if !s.setBlocksWithCommit(r, [][4]int{{x, y, z, 0}}, setQuiet, "mechanic_support_lost", func() {
		for _, d := range drops {
			r.Drops[d.ID] = d
		}
	}) {
		r.NextDropID = nextID
		return false
	}
	s.publishDrops(r, nil, drops, "mechanic_support_lost")
	return true
}
func (s *Server) notifyMechanicContainers(r *Room, keys ...uint64) {
	for p := range r.Peers {
		if p.window == nil {
			continue
		}
		for _, k := range keys {
			if p.window.Key == k || p.window.Key2 != 0 && p.window.Key2 == k {
				if p.window.Key2 != 0 {
					if c := r.Containers[p.window.Key]; c != nil {
						copy(p.window.Slots[:27], c.Slots)
					}
					if c := r.Containers[p.window.Key2]; c != nil {
						copy(p.window.Slots[27:], c.Slots)
					}
				} else if c := r.Containers[k]; c != nil {
					p.window.Slots = c.Slots
				}
				p.json(map[string]any{"t": "wslot", "w": p.window.ID, "s": p.window.Slots})
				break
			}
		}
	}
}
