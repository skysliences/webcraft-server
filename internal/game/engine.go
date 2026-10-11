package game

import (
	"context"
	"fmt"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"os"
)

// One engine is shared under Server.mu. It holds base terrain only; edits live in
// the room store. The browser and server use the same seed, generator and WASM.
type Engine struct {
	runtime wazero.Runtime
	mod     api.Module
	current string
	server *Server
}

func newEngine(file string) (*Engine, error) {
	ctx := context.Background()
	code, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(8192))
	mod, err := rt.Instantiate(ctx, code)
	if err != nil {
		rt.Close(ctx)
		return nil, err
	}
	return &Engine{runtime: rt, mod: mod}, nil
}
func (e *Engine) call(name string, args ...uint64) uint64 {
	result, err := e.mod.ExportedFunction(name).Call(context.Background(), args...)
	if err != nil {
		panic(fmt.Errorf("engine %s: %w", name, err))
	}
	if len(result) == 0 {
		return 0
	}
	return result[0]
}
func seed(id string) uint32 {
	h := uint32(2166136261)
	for _, c := range id {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}
func (e *Engine) use(r *Room) {
	if e.current == r.ID {
		return
	}
	e.call("init", uint64(seed(r.seedID())))
	e.call("set_world", uint64(r.width()))
	e.call("set_gen", uint64(r.gen()))
	e.current = r.ID
}
func (e *Engine) block(r *Room, x, y, z int) int {
	if r.Infinite {
	 if !r.inside(x,z)||y<0||y>=128 {return 0}
	 c,err:=e.server.ensureChunk(r,chunkCoord(x,z));if err!=nil {r.SaveError=err.Error();return 0};return int(c.Base[chunkIndex(x,y,z)])
	}
	if !r.inside(x, z) || y < 0 || y >= 128 {
		return 0
	}
	e.use(r)
	v := int32(uint32(e.call("get_block", uint64(int64(x)), uint64(int64(y)), uint64(int64(z)))))
	if v < 0 {
		return 0
	}
	return int(v)
}
func (e *Engine) spawn(r *Room) [3]int {
	if r.Infinite && r.LegacyW==0 {
	 for n:=0;n<512;n++ {x,z:=512+n%32*4,512+n/32*4;h,b:=terrainColumn(seed(r.seedID()),x,z);if b!="ocean"&&h>53 {return [3]int{x,h+2,z}} };return [3]int{512,80,512}
	}
	e.use(r)
	ptr := uint32(e.call("find_spawn"))
	var out [3]int
	for i := range out {
		v, ok := e.mod.Memory().ReadUint32Le(ptr + uint32(i*4))
		if !ok {
			panic("spawn outside engine memory")
		}
		out[i] = int(int32(v))
	}
	return out
}

func (e *Engine) legacyChunk(r *Room,c ChunkCoord)[]byte {
 old:=*r;old.Infinite=false;old.W=r.LegacyW;e.current="";e.use(&old)
 n:=int(e.call("copy_box",uint64(c.X*16),0,uint64(c.Z*16),16,128,16))
 if n!=chunkVolume {panic("legacy terrain copy failed")}
 data,ok:=e.mod.Memory().Read(uint32(e.call("ebuf_ptr")),uint32(n));if !ok {panic("legacy terrain outside memory")}
 out:=make([]byte,n)
 // copy_box orders Y,Z,X; the chunk protocol orders Z,X,Y.
 for y:=0;y<128;y++ {for z:=0;z<16;z++ {for x:=0;x<16;x++ {out[(z*16+x)*128+y]=data[(y*16+z)*16+x]}}};return out
}
