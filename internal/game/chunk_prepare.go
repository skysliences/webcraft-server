package game

import (
	"math"
	"sync"
	"time"
)

const (
	chunkPrepareWorkers = 2
	chunkPrepareLimit   = 64
	chunkPrepareAhead   = 24
)

type chunkPrepareKey struct {
	room  *Room
	coord ChunkCoord
}

type chunkPreparation struct {
	key       chunkPrepareKey
	room      *Room // Detached generation inputs; never reads live world maps.
	file      string
	used      time.Time
	ready     bool
	data      *diskChunk
	generated bool
	err       error
}

// A bounded cache/queue shares preparation between players in the same world.
// Its separate lock is never held during disk access, generation or Server.mu.
// Workers only produce snapshots: ensureChunk remains the sole commit/event path.
type chunkPreparer struct {
	mu   sync.Mutex
	jobs map[chunkPrepareKey]*chunkPreparation
	work chan *chunkPreparation
	stop chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

func (s *Server) startChunkPreparation() {
	p := &chunkPreparer{jobs: map[chunkPrepareKey]*chunkPreparation{}, work: make(chan *chunkPreparation, chunkPrepareLimit), stop: make(chan struct{})}
	s.chunkPreparer = p
	for n := 0; n < chunkPrepareWorkers; n++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for {
				select {
				case <-p.stop:
					return
				case job := <-p.work:
					p.mu.Lock()
					active := p.jobs[job.key] == job
					p.mu.Unlock()
					if !active {
						continue
					}
					data, generated, err := s.readChunkData(job.room, job.key.coord, job.file)
					p.mu.Lock()
					if p.jobs[job.key] == job {
						job.data, job.generated, job.err, job.ready = data, generated, err, true
					}
					p.mu.Unlock()
				}
			}
		}()
	}
}

func (p *chunkPreparer) close() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.stop) })
	p.wg.Wait()
}

// requestChunkPreparation is called under Server.mu. Snapshot only immutable
// generation settings so an unload, world operation or dimension change cannot
// race with a worker. Legacy WASM generation keeps its existing engine lock.
func (s *Server) requestChunkPreparation(r *Room, c ChunkCoord) {
	p := s.chunkPreparer
	if p == nil || r.chunks[c] != nil || legacyChunkCoord(r, c) {
		return
	}
	key, now := chunkPrepareKey{r, c}, time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stop:
		return
	default:
	}
	if job := p.jobs[key]; job != nil {
		job.used = now
		return
	}
	for k, job := range p.jobs {
		if now.Sub(job.used) > 10*time.Second {
			delete(p.jobs, k)
		}
	}
	if len(p.jobs) >= chunkPrepareLimit {
		// Ready, abandoned exploration requests may otherwise occupy the queue
		// after a teleport. Evict the least recently wanted completed snapshot.
		var oldest *chunkPreparation
		for _, job := range p.jobs {
			if job.ready && (oldest == nil || job.used.Before(oldest.used)) {
				oldest = job
			}
		}
		if oldest == nil {
			return
		}
		delete(p.jobs, oldest.key)
	}
	room := &Room{ID: r.seedID(), Dim: r.dimName(), Mode: r.Mode, W: r.W, Infinite: true, Generator: r.Generator}
	job := &chunkPreparation{key: key, room: room, file: s.chunkFile(r, c), used: now}
	select {
	case p.work <- job:
		p.jobs[key] = job
	default:
	}
}

func (s *Server) preparedChunkReady(r *Room, c ChunkCoord) bool {
	p := s.chunkPreparer
	if p == nil || legacyChunkCoord(r, c) {
		return true // Compatibility fixtures and shared legacy WASM stay bounded.
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	job := p.jobs[chunkPrepareKey{r, c}]
	return job != nil && job.ready
}

func (s *Server) takePreparedChunk(r *Room, c ChunkCoord) (*diskChunk, bool, error, bool) {
	p := s.chunkPreparer
	if p == nil {
		return nil, false, nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := chunkPrepareKey{r, c}
	job := p.jobs[key]
	if job == nil || !job.ready {
		return nil, false, nil, false
	}
	delete(p.jobs, key)
	return job.data, job.generated, job.err, true
}

func (s *Server) discardPreparedChunk(r *Room, c ChunkCoord) {
	if p := s.chunkPreparer; p != nil {
		p.mu.Lock()
		delete(p.jobs, chunkPrepareKey{r, c})
		p.mu.Unlock()
	}
}

// generateChunkSnapshot isolates structure generators as well as noise inputs.
// Village/fortress generators can attach inventories, block state and entity
// intents to this detached room/server without touching live world state. The
// returned snapshot is validated and committed by ensureChunk under Server.mu.
func (s *Server) generateChunkSnapshot(r *Room, c ChunkCoord) (*diskChunk, error) {
	room := newRoom(r.seedID(), r.Name, r.Mode, r.Lock)
	room.Infinite, room.Dim, room.W, room.Generator = true, r.dimName(), r.W, r.Generator
	generator := &Server{rules: s.rules}
	base := generator.generateTerrainBase(room, c)
	d := chunkData(room, c, base)
	for _, spawn := range generator.pendingVillageSpawns {
		// Coordinates are absolute; binding to the authoritative room happens
		// during commit, never while a background worker is running.
		spawn.Room = nil
		d.VillageSpawns = append(d.VillageSpawns, spawn)
	}
	return d, nil
}

// villageSpawnPosition runs only under Server.mu after the generated chunk was
// committed. Stay inside that chunk so selection cannot recursively generate
// neighbors. Actual publication still goes through spawnMob and its before event.
func (s *Server) villageSpawnPosition(v villageSpawn) (float64, float64, float64, bool) {
	x, z := int(math.Floor(v.X)), int(math.Floor(v.Z))
	c := chunkCoord(x, z)
	for radius := 0; radius <= 2; radius++ {
		for dz := -radius; dz <= radius; dz++ {
			for dx := -radius; dx <= radius; dx++ {
				if max(abs(dx), abs(dz)) != radius || chunkCoord(x+dx, z+dz) != c || !v.Room.inside(x+dx, z+dz) {
					continue
				}
				px, pz := float64(x+dx)+.5, float64(z+dz)+.5
				if s.mobFits(v.Room, "villager", px, v.Y, pz) && s.mobSolid(v.Room, x+dx, int(math.Floor(v.Y))-1, z+dz) {
					return px, v.Y, pz, true
				}
			}
		}
	}
	return 0, 0, 0, false
}
