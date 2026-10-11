package game

import (
	"encoding/binary"
	"github.com/gorilla/websocket"
	"math"
	"sort"
	"time"
)

const renderWidth = 1024

func (r *Room) usesSurvival() bool { return r.Mode == "survival" || r.Infinite }
func (p *Peer) setOrigin() {
	if !p.room.Infinite {
		return
	}
	p.originX = floorDiv(int(math.Floor(p.state.X))-renderWidth/2, 16) * 16
	p.originZ = floorDiv(int(math.Floor(p.state.Z))-renderWidth/2, 16) * 16
	p.epoch++
	p.known = map[int]bool{}
	p.chunkStreaming = true
	p.chunkStreamAt = time.Time{}
	p.knownDrops = map[uint32]bool{}
	p.knownMobs = map[uint32]bool{}
	for _, e := range p.room.rt().orbs {
		delete(e.seen, p)
	}
	for _, e := range p.room.rt().projectiles {
		delete(e.seen, p)
	}
}
func (s *Server) peerSurvivalInfo(p *Peer) map[string]any {
	m := s.survivalInfo(p.room)
	m["blockProtocol"] = p.blockProtocol
	if p.room.Infinite {
		m["w"] = renderWidth
		m["infinite"] = true
		m["origin"] = []int{p.originX, p.originZ}
		m["epoch"] = p.epoch
		m["generator"] = p.room.Generator
		m["border"] = worldBorder
	}
	return m
}
func (p *Peer) knows(x, z int) bool {
	if p.room.Infinite {
		x -= p.originX
		z -= p.originZ
		if x < 0 || z < 0 || x >= renderWidth || z >= renderWidth {
			return false
		}
		return p.known[floorDiv(z, 16)*(renderWidth/16)+floorDiv(x, 16)]
	}
	return p.known[floorDiv(z, 16)*p.room.cn()+floorDiv(x, 16)]
}
func (p *Peer) localChunk(c ChunkCoord) int {
	return (c.Z-floorDiv(p.originZ, 16))*(renderWidth/16) + c.X - floorDiv(p.originX, 16)
}
func encodeTerrain(base []byte) []byte {
	out := make([]byte, 0, 4096)
	for i := 0; i < len(base); {
		j := i + 1
		for j < len(base) && base[j] == base[i] && j-i < 65535 {
			j++
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(j-i))
		out = append(out, base[i])
		i = j
	}
	return out
}

const (
	chunkStreamBatch    = 8
	chunkStreamBudget   = 2 * time.Millisecond
	chunkStreamInterval = 40 * time.Millisecond
)

// Chunk preparation is asynchronous, while publication and events remain under
// Server.mu. Small nearest-first packets give clients time to mesh each arrival
// and leave headroom for movement, inventory and other players' messages.
func (s *Server) streamInfinite(p *Peer) {
	if p.closed || p.room == nil || p.state == nil {
		return
	}
	now := time.Now()
	p.chunkStreaming = true
	if now.Sub(p.chunkStreamAt) < chunkStreamInterval || len(p.send) >= cap(p.send)-min(16, cap(p.send)/4) {
		return
	}
	p.chunkStreamAt = now
	cx, cz := floorDiv(int(math.Floor(p.state.X)), 16), floorDiv(int(math.Floor(p.state.Z)), 16)
	radius := p.rd + 2
	cn := renderWidth / 16
	for ci := range p.known {
		if abs(ci%cn+floorDiv(p.originX, 16)-cx) > 15 || abs(ci/cn+floorDiv(p.originZ, 16)-cz) > 15 {
			delete(p.known, ci)
		}
	}
	var needed []ChunkCoord
	for z := cz - radius; z <= cz+radius; z++ {
		for x := cx - radius; x <= cx+radius; x++ {
			c := ChunkCoord{x, z}
			if !p.knows(x*16, z*16) && p.room.inside(x*16, z*16) {
				needed = append(needed, c)
			}
		}
	}
	sort.Slice(needed, func(i, j int) bool {
		a, b := needed[i], needed[j]
		da, db := (a.X-cx)*(a.X-cx)+(a.Z-cz)*(a.Z-cz), (b.X-cx)*(b.X-cx)+(b.Z-cz)*(b.Z-cz)
		if da != db {
			return da < db
		}
		if a.Z != b.Z {
			return a.Z < b.Z
		}
		return a.X < b.X
	})
	if len(needed) == 0 {
		p.chunkStreaming = false
		s.syncDropsFor(p)
		s.syncMobsFor(p)
		s.syncTransportFor(p)
		return
	}
	// A teleport cannot enqueue an entire view distance. The worker/cache limits
	// also apply across players and dimensions; duplicate requests share a job.
	for _, c := range needed[:min(chunkPrepareAhead, len(needed))] {
		s.requestChunkPreparation(p.room, c)
	}
	type readyChunk struct {
		coord ChunkCoord
		chunk *WorldChunk
	}
	ready := make([]readyChunk, 0, chunkStreamBatch)
	for _, c := range needed {
		if len(ready) >= chunkStreamBatch || len(ready) > 0 && time.Since(now) >= chunkStreamBudget {
			break
		}
		if p.room.chunks[c] == nil && !s.preparedChunkReady(p.room, c) {
			continue
		}
		v, err := s.ensureChunk(p.room, c)
		if err != nil {
			p.room.SaveError = err.Error()
			p.sys("区块读取失败，已保留原文件。")
			return
		}
		ready = append(ready, readyChunk{c, v})
	}
	if len(ready) == 0 {
		return
	}
	// Group live edits once per small batch instead of 32768 hash lookups per
	// chunk. Clean chunks reuse immutable RLE terrain; edited chunks always
	// overlay the current authoritative map, including plugin and restore edits.
	wanted := make(map[ChunkCoord]bool, len(ready))
	for _, v := range ready {
		wanted[v.coord] = true
	}
	edits := map[ChunkCoord]map[int]int{}
	for key, block := range p.room.Edits {
		x, y, z := coords(key)
		c := chunkCoord(x, z)
		if !wanted[c] {
			continue
		}
		if edits[c] == nil {
			edits[c] = map[int]int{}
		}
		edits[c][chunkIndex(x, y, z)] = block
	}
	header := func() []byte {
		kind := byte(13)
		if p.blockProtocol == 2 {
			kind = frameTerrain16
		}
		out := []byte{kind, p.room.dimIndex(), 0, 0}
		return binary.LittleEndian.AppendUint32(out, p.epoch)
	}
	out := header()
	var sent []ChunkCoord
	flush := func() bool {
		if len(sent) == 0 {
			return true
		}
		binary.LittleEndian.PutUint16(out[2:], uint16(len(sent)))
		select {
		case p.send <- packet{websocket.BinaryMessage, out}:
			for _, c := range sent {
				p.known[p.localChunk(c)] = true
			}
		default:
			return false // Retry next tick; never mark an unsent chunk as known.
		}
		out, sent = header(), nil
		return true
	}
	for _, v := range ready {
		var data []byte
		if p.blockProtocol == 2 {
			data = chunkWireTerrain16(v.chunk, edits[v.coord])
		} else {
			data = chunkWireTerrain(v.chunk, edits[v.coord])
		}
		if len(out)+12+len(data) > 60000 && !flush() {
			return
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(int32(v.coord.X)))
		out = binary.LittleEndian.AppendUint32(out, uint32(int32(v.coord.Z)))
		out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
		out = append(out, data...)
		sent = append(sent, v.coord)
	}
	if !flush() {
		return
	}
	p.chunkStreaming = len(ready) < len(needed)
	s.syncRedstoneFor(p)
	s.syncTNTFor(p)
	s.syncMechanicsFor(p)
	s.syncDropsFor(p)
	s.syncMobsFor(p)
	s.syncTransportFor(p)
}

func chunkWireTerrain(v *WorldChunk, edits map[int]int) []byte {
	if len(edits) == 0 {
		if v.Terrain == nil {
			v.Terrain = encodeTerrain(v.Base)
		}
		return v.Terrain
	}
	base := append([]byte(nil), v.Base...)
	for i, b := range edits {
		base[i] = byte(b)
	}
	return encodeTerrain(base)
}

func chunkWireTerrain16(v *WorldChunk, edits map[int]int) []byte {
	if len(edits) == 0 {
		if v.Terrain16 == nil {
			v.Terrain16 = encodeTerrain16(v.Base, nil)
		}
		return v.Terrain16
	}
	return encodeTerrain16(v.Base, edits)
}
func (s *Server) broadcastInfiniteDelta(r *Room, x, y, z, b int, quiet bool) {
	for p := range r.Peers {
		if !p.knows(x, z) {
			continue
		}
		ci := p.localChunk(chunkCoord(x, z))
		out := []byte{chunkFrame(p), r.dimIndex(), 1, 0}
		out = binary.LittleEndian.AppendUint32(out, uint32(ci))
		out = binary.LittleEndian.AppendUint16(out, 1)

		flag := byte(0)
		if quiet {
			flag = 128
		}
		out = appendChunkRecord(out, p, chunkIndex(x, y, z), b, flag)
		p.enqueue(websocket.BinaryMessage, out)
	}
}
