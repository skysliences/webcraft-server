package game

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var chunkBinaryMagic = [4]byte{'M', 'C', 'B', 1}

// encodeChunkBinary serializes diskChunk into a compact, high-performance binary format.
func encodeChunkBinary(d *diskChunk) ([]byte, error) {
	if d == nil {
		return nil, errors.New("nil diskChunk")
	}

	var buf bytes.Buffer
	// 1. Magic
	buf.Write(chunkBinaryMagic[:])

	// We compress the payload with gzip for maximum compression ratio
	gz := gzip.NewWriter(&buf)

	var header [24]byte
	binary.LittleEndian.PutUint32(header[0:4], uint32(d.X))
	binary.LittleEndian.PutUint32(header[4:8], uint32(d.Z))
	binary.LittleEndian.PutUint32(header[8:12], uint32(d.Version))
	binary.LittleEndian.PutUint32(header[12:16], uint32(d.VegetationVersion))
	binary.LittleEndian.PutUint32(header[16:20], d.CRC)
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(d.Generator)))
	if _, err := gz.Write(header[:]); err != nil {
		return nil, err
	}
	if len(d.Generator) > 0 {
		if _, err := gz.Write([]byte(d.Generator)); err != nil {
			return nil, err
		}
	}

	// 2. Base terrain (raw 32768 bytes, no JSON base64 bloat)
	var baseLen [4]byte
	binary.LittleEndian.PutUint32(baseLen[:], uint32(len(d.Base)))
	if _, err := gz.Write(baseLen[:]); err != nil {
		return nil, err
	}
	if len(d.Base) > 0 {
		if _, err := gz.Write(d.Base); err != nil {
			return nil, err
		}
	}

	// 3. Edits
	var editsCount [2]byte
	binary.LittleEndian.PutUint16(editsCount[:], uint16(len(d.Edits)))
	if _, err := gz.Write(editsCount[:]); err != nil {
		return nil, err
	}
	for idx, val := range d.Edits {
		var entry [4]byte
		binary.LittleEndian.PutUint16(entry[0:2], uint16(idx))
		binary.LittleEndian.PutUint16(entry[2:4], uint16(val))
		if _, err := gz.Write(entry[:]); err != nil {
			return nil, err
		}
	}

	// 4. Containers
	var containersCount [2]byte
	binary.LittleEndian.PutUint16(containersCount[:], uint16(len(d.Containers)))
	if _, err := gz.Write(containersCount[:]); err != nil {
		return nil, err
	}
	for idx, c := range d.Containers {
		cBytes, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		var entry [6]byte
		binary.LittleEndian.PutUint16(entry[0:2], uint16(idx))
		binary.LittleEndian.PutUint32(entry[2:6], uint32(len(cBytes)))
		if _, err := gz.Write(entry[:]); err != nil {
			return nil, err
		}
		if _, err := gz.Write(cBytes); err != nil {
			return nil, err
		}
	}

	// 5. Authors
	var authorsCount [2]byte
	binary.LittleEndian.PutUint16(authorsCount[:], uint16(len(d.Authors)))
	if _, err := gz.Write(authorsCount[:]); err != nil {
		return nil, err
	}
	for idx, a := range d.Authors {
		var entry [6]byte
		binary.LittleEndian.PutUint16(entry[0:2], uint16(idx))
		binary.LittleEndian.PutUint16(entry[2:4], uint16(len(a.Token)))
		binary.LittleEndian.PutUint16(entry[4:6], uint16(a.After))
		if _, err := gz.Write(entry[:]); err != nil {
			return nil, err
		}
		if len(a.Token) > 0 {
			if _, err := gz.Write([]byte(a.Token)); err != nil {
				return nil, err
			}
		}
	}

	// 6. Blocks
	var blocksCount [2]byte
	binary.LittleEndian.PutUint16(blocksCount[:], uint16(len(d.Blocks)))
	if _, err := gz.Write(blocksCount[:]); err != nil {
		return nil, err
	}
	for idx, blk := range d.Blocks {
		bBytes, err := json.Marshal(blk)
		if err != nil {
			return nil, err
		}
		var entry [6]byte
		binary.LittleEndian.PutUint16(entry[0:2], uint16(idx))
		binary.LittleEndian.PutUint32(entry[2:6], uint32(len(bBytes)))
		if _, err := gz.Write(entry[:]); err != nil {
			return nil, err
		}
		if _, err := gz.Write(bBytes); err != nil {
			return nil, err
		}
	}

	if err := gz.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// decodeChunkBinary parses a binary chunk buffer into diskChunk.
func decodeChunkBinary(data []byte) (*diskChunk, error) {
	if len(data) < 4 || !isChunkBinary(data) {
		return nil, errors.New("not a valid binary chunk")
	}

	payload, err := readChunkGzip(data[4:])
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	r := bytes.NewReader(payload)

	var header [24]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	d := &diskChunk{
		X:                 int(int32(binary.LittleEndian.Uint32(header[0:4]))),
		Z:                 int(int32(binary.LittleEndian.Uint32(header[4:8]))),
		Version:           int(binary.LittleEndian.Uint32(header[8:12])),
		VegetationVersion: int(binary.LittleEndian.Uint32(header[12:16])),
		CRC:               binary.LittleEndian.Uint32(header[16:20]),
		Edits:             map[int]int{},
		Containers:        map[int]*Container{},
		Authors:           map[int]EditAuthor{},
		Blocks:            map[int]*BlockData{},
	}

	genLen := int(binary.LittleEndian.Uint32(header[20:24]))
	if genLen > r.Len() {
		return nil, errors.New("invalid generator length")
	}
	if genLen > 0 {
		genBytes := make([]byte, genLen)
		if _, err := io.ReadFull(r, genBytes); err != nil {
			return nil, fmt.Errorf("read generator: %w", err)
		}
		d.Generator = string(genBytes)
	}

	// Base
	var baseLenBuf [4]byte
	if _, err := io.ReadFull(r, baseLenBuf[:]); err != nil {
		return nil, fmt.Errorf("read base len: %w", err)
	}
	bLen := int(binary.LittleEndian.Uint32(baseLenBuf[:]))
	if bLen > r.Len() {
		return nil, errors.New("invalid terrain length")
	}
	if bLen > 0 {
		d.Base = make([]byte, bLen)
		if _, err := io.ReadFull(r, d.Base); err != nil {
			return nil, fmt.Errorf("read base: %w", err)
		}
	}

	// Edits
	var countBuf [2]byte
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return nil, fmt.Errorf("read edits count: %w", err)
	}
	editsCount := int(binary.LittleEndian.Uint16(countBuf[:]))
	for i := 0; i < editsCount; i++ {
		var entry [4]byte
		if _, err := io.ReadFull(r, entry[:]); err != nil {
			return nil, fmt.Errorf("read edit entry: %w", err)
		}
		idx := int(binary.LittleEndian.Uint16(entry[0:2]))
		val := int(binary.LittleEndian.Uint16(entry[2:4]))
		d.Edits[idx] = val
	}

	// Containers
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return nil, fmt.Errorf("read containers count: %w", err)
	}
	cCount := int(binary.LittleEndian.Uint16(countBuf[:]))
	for i := 0; i < cCount; i++ {
		var entry [6]byte
		if _, err := io.ReadFull(r, entry[:]); err != nil {
			return nil, fmt.Errorf("read container header: %w", err)
		}
		idx := int(binary.LittleEndian.Uint16(entry[0:2]))
		cLen := int(binary.LittleEndian.Uint32(entry[2:6]))
		if cLen > r.Len() {
			return nil, errors.New("invalid container length")
		}
		cBytes := make([]byte, cLen)
		if _, err := io.ReadFull(r, cBytes); err != nil {
			return nil, fmt.Errorf("read container json: %w", err)
		}
		var c Container
		if err := json.Unmarshal(cBytes, &c); err != nil {
			return nil, fmt.Errorf("unmarshal container: %w", err)
		}
		d.Containers[idx] = &c
	}

	// Authors
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return nil, fmt.Errorf("read authors count: %w", err)
	}
	aCount := int(binary.LittleEndian.Uint16(countBuf[:]))
	for i := 0; i < aCount; i++ {
		var entry [6]byte
		if _, err := io.ReadFull(r, entry[:]); err != nil {
			return nil, fmt.Errorf("read author header: %w", err)
		}
		idx := int(binary.LittleEndian.Uint16(entry[0:2]))
		tLen := int(binary.LittleEndian.Uint16(entry[2:4]))
		after := int(binary.LittleEndian.Uint16(entry[4:6]))
		if tLen > r.Len() {
			return nil, errors.New("invalid author length")
		}
		tokenBytes := make([]byte, tLen)
		if _, err := io.ReadFull(r, tokenBytes); err != nil {
			return nil, fmt.Errorf("read author token: %w", err)
		}
		d.Authors[idx] = EditAuthor{Token: string(tokenBytes), After: after}
	}

	// Blocks
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return nil, fmt.Errorf("read blocks count: %w", err)
	}
	bCount := int(binary.LittleEndian.Uint16(countBuf[:]))
	for i := 0; i < bCount; i++ {
		var entry [6]byte
		if _, err := io.ReadFull(r, entry[:]); err != nil {
			return nil, fmt.Errorf("read block header: %w", err)
		}
		idx := int(binary.LittleEndian.Uint16(entry[0:2]))
		blkLen := int(binary.LittleEndian.Uint32(entry[2:6]))
		if blkLen > r.Len() {
			return nil, errors.New("invalid block data length")
		}
		bBytes := make([]byte, blkLen)
		if _, err := io.ReadFull(r, bBytes); err != nil {
			return nil, fmt.Errorf("read block json: %w", err)
		}
		var bData BlockData
		if err := json.Unmarshal(bBytes, &bData); err != nil {
			return nil, fmt.Errorf("unmarshal block data: %w", err)
		}
		d.Blocks[idx] = &bData
	}

	if r.Len() != 0 {
		return nil, errors.New("binary chunk has trailing data")
	}

	return d, nil
}

// isChunkBinary returns true if data starts with the binary chunk magic.
func isChunkBinary(data []byte) bool {
	return len(data) >= 4 && data[0] == chunkBinaryMagic[0] && data[1] == chunkBinaryMagic[1] && data[2] == chunkBinaryMagic[2] && data[3] == chunkBinaryMagic[3]
}
