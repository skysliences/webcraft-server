// Package worldgen runs the same structure program linked into the browser
// engine. Each call owns its instance; preparation workers never share state.
package worldgen

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed structures.wasm
var program []byte

type Event struct {
	Kind, X, Y, Z, Style int
	Seed                 uint32
}
type Terrain struct {
	Height, Biome func(int, int) int
	Write         func(int, int, int, int)
}
type terrainKey struct{}

var once sync.Once
var runtime wazero.Runtime
var compiled wazero.CompiledModule
var initErr error
var instances = make(chan api.Module, 4)
var slots = make(chan struct{}, 4)

func initialize() {
	ctx := context.Background()
	runtime = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(4))
	_, initErr = runtime.NewHostModuleBuilder("terrain").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, x, z uint32) uint32 {
		return uint32(ctx.Value(terrainKey{}).(Terrain).Height(int(int32(x)), int(int32(z))))
	}).Export("height").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, x, z uint32) uint32 {
		return uint32(ctx.Value(terrainKey{}).(Terrain).Biome(int(int32(x)), int(int32(z))))
	}).Export("biome").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, x, y, z, b uint32) {
		ctx.Value(terrainKey{}).(Terrain).Write(int(int32(x)), int(y), int(int32(z)), int(b))
	}).Export("write").Instantiate(ctx)
	if initErr == nil {
		compiled, initErr = runtime.CompileModule(ctx, program)
	}
}

func Generate(seed uint32, cx, cz int, terrain Terrain) ([]Event, error) {
	once.Do(initialize)
	if initErr != nil {
		return nil, initErr
	}
	slots <- struct{}{}
	defer func() { <-slots }()
	ctx := context.WithValue(context.Background(), terrainKey{}, terrain)
	var mod api.Module
	select {
	case mod = <-instances:
	default:
		var err error
		mod, err = runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			return nil, err
		}
	}
	healthy := false
	defer func() {
		if healthy {
			instances <- mod
		} else {
			_ = mod.Close(context.Background())
		}
	}()
	if _, err := mod.ExportedFunction("wg_generate").Call(ctx, uint64(seed), uint64(uint32(cx)), uint64(uint32(cz))); err != nil {
		return nil, fmt.Errorf("structure generation: %w", err)
	}
	ptr, err := mod.ExportedFunction("wg_meta_ptr").Call(ctx)
	if err != nil {
		return nil, err
	}
	count, err := mod.ExportedFunction("wg_meta_count").Call(ctx)
	if err != nil {
		return nil, err
	}
	data, ok := mod.Memory().Read(uint32(ptr[0]), uint32(count[0])*24)
	if !ok {
		return nil, fmt.Errorf("invalid structure metadata")
	}
	result := make([]Event, int(count[0]))
	for i := range result {
		b := data[i*24:]
		n := func(j int) int { return int(int32(binary.LittleEndian.Uint32(b[j:]))) }
		result[i] = Event{n(0), n(4), n(8), n(12), n(16), binary.LittleEndian.Uint32(b[20:])}
	}
	healthy = true
	return result, nil
}

// Candidate is exposed for diagnostics and deterministic test fixtures.
func hash(s uint32, x, z int, salt uint32) uint32 {
	h := s ^ uint32(x)*0x8da6b343 ^ uint32(z)*0xd8163841 ^ salt
	h = (h ^ (h >> 16)) * 0x7feb352d
	h = (h ^ (h >> 15)) * 0x846ca68b
	return h ^ (h >> 16)
}
func VillageCandidate(seed uint32, rx, rz int) (int, int) {
	h := hash(seed, rx, rz, 10387312)
	return rx*32 + int(h%24), rz*32 + int(hash(h, rx, rz, 1)%24)
}
