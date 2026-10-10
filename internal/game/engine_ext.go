package game

// Extra queries on the WASM engine (structures).

func (e *Engine) readI32s(ptr uint32, n int) []int {
	out := make([]int, n)
	for i := range out {
		v, ok := e.mod.Memory().ReadUint32Le(ptr + uint32(i*4))
		if !ok {
			return nil
		}
		out[i] = int(int32(v))
	}
	return out
}

// strongholdNear returns the nearest stronghold portal room (x, y, z) for an
// overworld room, and whether one exists.
func (e *Engine) strongholdNear(r *Room, x, z int) ([3]int, bool) {
	e.use(r)
	ptr := uint32(e.call("stronghold_near", uint64(uint32(int32(x))), uint64(uint32(int32(z)))))
	v := e.readI32s(ptr, 7)
	if v == nil || v[6] < 0 {
		return [3]int{}, false
	}
	return [3]int{v[0], v[1] + 11, v[2]}, true
}

// endSpikes returns ten [x,z,height,radius,cage] pillars and the exit portal
// position (x,y,z) for an End room.
func (e *Engine) endSpikes(r *Room) ([][5]int, [3]int) {
	e.use(r)
	ptr := uint32(e.call("end_spikes"))
	v := e.readI32s(ptr, 51)
	c := r.width() / 2
	if v == nil {
		return nil, [3]int{c, 64, c}
	}
	sp := make([][5]int, 10)
	for i := range sp {
		copy(sp[i][:], v[i*5:i*5+5])
	}
	y := v[50]
	if y < 1 || y > 120 {
		y = 64
	}
	return sp, [3]int{c, y, c}
}
