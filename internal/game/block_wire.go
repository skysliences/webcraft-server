package game

import (
	"encoding/binary"
	"github.com/gorilla/websocket"
)

// Protocol 2 adds u16 IDs in new frame kinds. Legacy v1 frames keep their ABI.
const (
	frameChunk16   byte = 32
	frameSync16    byte = 33
	frameTerrain16 byte = 34
	frameDeny16    byte = 35
	frameEdit16    byte = 36
)

func chunkFrame(p *Peer) byte {
	if p.blockProtocol == 2 {
		return frameChunk16
	}
	return 12
}
func chunkRecordSize(p *Peer) int {
	if p.blockProtocol == 2 {
		return 5
	}
	return 4
}
func appendChunkRecord(out []byte, p *Peer, index, b int, flags byte) []byte {
	out = binary.LittleEndian.AppendUint16(out, uint16(index))
	if p.blockProtocol == 2 {
		out = binary.LittleEndian.AppendUint16(out, uint16(b))
	} else {
		out = append(out, byte(b))
	}
	return append(out, flags)
}
func encodeTerrain16(base []byte, edits map[int]int) []byte {
	out := make([]byte, 0, 4096)
	at := func(i int) int {
		if b, ok := edits[i]; ok {
			return b
		}
		return int(base[i])
	}
	for i := 0; i < len(base); {
		b := at(i)
		j := i + 1
		for j < len(base) && at(j) == b && j-i < 65535 {
			j++
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(j-i))
		out = binary.LittleEndian.AppendUint16(out, uint16(b))
		i = j
	}
	return out
}
func sendBlockSync(p *Peer, key uint64, b int) {
	if p.blockProtocol == 2 {
		out := binary.LittleEndian.AppendUint32([]byte{frameSync16}, toWireKey(key))
		out = binary.LittleEndian.AppendUint16(out, uint16(b))
		p.enqueue(websocket.BinaryMessage, out)
	} else {
		out := binary.LittleEndian.AppendUint32([]byte{3}, toWireKey(key))
		p.enqueue(websocket.BinaryMessage, append(out, byte(b)))
	}
}

// API v1 player edits include the actor so legacy clients animate the swing.
// Keep this frame intact rather than replacing it with a snapshot packet.
func sendPlayerBlock(p *Peer, actor uint16, key uint64, b int) {
	if p.blockProtocol == 2 {
		sendBlockSync(p, key, b)
		return
	}
	out := []byte{5, 0, 0, 1, 0}
	out = binary.LittleEndian.AppendUint16(out, actor)
	out = binary.LittleEndian.AppendUint32(out, toWireKey(key))
	p.enqueue(websocket.BinaryMessage, append(out, byte(b)))
}

func (s *Server) denyBlock(p *Peer, wire uint32, old int) {
	kind := byte(4)
	if p.blockProtocol == 2 {
		kind = frameDeny16
	}
	out := binary.LittleEndian.AppendUint32([]byte{kind}, wire)
	if p.blockProtocol == 2 {
		out = binary.LittleEndian.AppendUint16(out, uint16(old))
	} else {
		out = append(out, byte(old))
	}
	p.enqueue(websocket.BinaryMessage, out)
}
func (s *Server) creativeBlock(id int) bool {
	return creativeBlock(id) || id >= 220 && s.rules.validBlock(id)
}
func (s *Server) creativeItem(id int) bool {
	return creativeItem(id) || id >= 220 && s.rules.max(id) > 0
}
