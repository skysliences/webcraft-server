package game

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Administrative undo must include edits outside the current player cache.
func (s *Server) loadAdministrativeChunks(r *Room) error {
	if !r.Infinite {
		return nil
	}
	dir := filepath.Dir(filepath.Dir(s.chunkFile(r, ChunkCoord{})))
	return filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".mcc") {
			return nil
		}
		var c ChunkCoord
		if _, err = fmt.Sscanf(e.Name(), "c.%d.%d.mcc", &c.X, &c.Z); err != nil {
			return err
		}
		_, err = s.ensureChunk(r, c)
		return err
	})
}
func (s *Server) resetInfinite(r *Room, p *Peer) error {
	if _, err := s.backup(p.sid, "pre-world-reset"); err != nil {
		return err
	}
	dir := filepath.Dir(filepath.Dir(filepath.Dir(s.chunkFile(r, ChunkCoord{}))))
	rollback := filepath.Join(s.config.DataDir, "backups", "reset-"+r.ID+"-"+r.dimName()+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	exists := false
	if _, err := os.Stat(dir); err == nil {
		if err = os.Rename(dir, rollback); err != nil {
			return err
		}
		exists = true
	} else if !os.IsNotExist(err) {
		return err
	}
	edits, authors, containers, chunks, blocks := r.Edits, r.Authors, r.Containers, r.chunks, r.blockExt().Blocks
	r.Edits = map[uint64]int{}
	r.Authors = map[uint64]EditAuthor{}
	r.Containers = map[uint64]*Container{}
	r.chunks = map[ChunkCoord]*WorldChunk{}
	r.blockExt().Blocks = nil
	r.Dirty = true
	if err := s.saveRoom(r); err != nil {
		r.Edits, r.Authors, r.Containers, r.chunks, r.blockExt().Blocks = edits, authors, containers, chunks, blocks
		if exists {
			os.RemoveAll(dir)
			os.Rename(rollback, dir)
		}
		return err
	}
	for peer := range r.Peers {
		s.closeWindow(peer)
		peer.setOrigin()
		peer.json(map[string]any{"t": "place", "st": peer.state, "sv": s.peerSurvivalInfo(peer)})
		s.stream(peer)
	}
	return nil
}
