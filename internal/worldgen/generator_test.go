package worldgen

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
)

func TestAllVillageThemesAndTerrainRejection(t *testing.T) {
	cx, cz := VillageCandidate(1234, 0, 0)
	signatures := map[[32]byte]bool{}
	for style := 0; style < 5; style++ {
		t.Run(fmt.Sprint(style), func(t *testing.T) {
			counts := map[int]int{}
			var signature []byte
			villagers, chests := 0, 0
			for z := cz - 4; z <= cz+4; z++ {
				for x := cx - 4; x <= cx+4; x++ {
					blocks := map[[3]int]int{}
					events, err := Generate(1234, x, z, Terrain{Height: func(x, z int) int { return 70 }, Biome: func(x, z int) int { return style }, Write: func(x, y, z, b int) { blocks[[3]int{x, y, z}] = b }})
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range events {
						if e.Kind == 3 {
							villagers++
						}
						if e.Kind == 1 {
							chests++
						}
					}
					for zz := z * 16; zz < z*16+16; zz++ {
						for xx := x * 16; xx < x*16+16; xx++ {
							for y := 69; y < 83; y++ {
								b := blocks[[3]int{xx, y, zz}]
								counts[b]++
								signature = append(signature, byte(b))
								if b >= 110 && b <= 113 && blocks[[3]int{xx, y + 1, zz}] != 118 {
									t.Fatal("door lacks upper half")
								}
							}
						}
					}
				}
			}
			if villagers < 4 || chests < 2 || counts[157] < 5 || counts[118] < 2 {
				t.Fatal("village pool missing residents, houses or crops", villagers, chests, counts)
			}
			sig := sha256.Sum256(signature)
			if signatures[sig] {
				t.Fatal("themes produced identical buildings")
			}
			signatures[sig] = true
		})
	}
	// Sea/mountain sites never contain village population, regardless of seed.
	for _, height := range []int{-1, 115} {
		events, err := Generate(1234, cx, cz, Terrain{Height: func(x, z int) int { return height }, Biome: func(x, z int) int { return -1 }, Write: func(x, y, z, b int) {}})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if e.Kind == 1 || e.Kind == 3 {
				t.Fatal("village in rejected terrain")
			}
		}
	}
}

func TestParallelGenerationIsIsolated(t *testing.T) {
	cx, cz := VillageCandidate(1, -1, -1)
	run := func() [32]byte {
		var data []byte
		_, err := Generate(1, cx, cz, Terrain{Height: func(x, z int) int { return 70 }, Biome: func(x, z int) int { return 2 }, Write: func(x, y, z, b int) {
			for _, v := range []int{x, y, z, b} {
				data = binary.LittleEndian.AppendUint32(data, uint32(v))
			}
		}})
		if err != nil {
			t.Error(err)
		}
		return sha256.Sum256(data)
	}
	expected := run()
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if got := run(); got != expected {
				t.Error("parallel generators shared mutable state")
			}
		}()
	}
	group.Wait()
}
