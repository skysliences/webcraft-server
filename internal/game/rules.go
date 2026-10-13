package game

import (
	_ "embed"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

//go:embed rules.json
var rulesJSON []byte

type Item struct {
	ID         int    `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Stack      int    `json:"stack"`
	Durability int    `json:"durability"`
	Ench       int    `json:"ench"`
	Tool       *struct {
		Type  string  `json:"type"`
		Level int     `json:"level"`
		Speed float64 `json:"speed"`
	} `json:"tool"`
	PlacesID int    `json:"placesId"`
	SpawnEgg string `json:"spawnEgg,omitempty"`
	Fuel     int    `json:"fuel"`
	Smelt    *struct {
		ID int     `json:"id"`
		XP float64 `json:"xp"`
	} `json:"smelt"`
	Food *struct {
		Hunger     int     `json:"hunger"`
		Saturation float64 `json:"saturation"`
		Always     bool    `json:"always"`
	} `json:"food"`
	Armor *struct {
		Slot   string `json:"slot"`
		Points int    `json:"points"`
	} `json:"armor"`
	Attack *struct {
		Damage float64 `json:"damage"`
	} `json:"attack"`
}
type MiningRule struct {
	Ticks   int  `json:"ticks"`
	Harvest bool `json:"harvest"`
	Wear    int  `json:"wear"`
}
type Recipe struct {
	ID          int     `json:"id"`
	Count       int     `json:"count"`
	W           int     `json:"w"`
	H           int     `json:"h"`
	Cells       [][]int `json:"cells"`
	Ingredients [][]int `json:"ingredients"`
}
type Rules struct {
	Kinds       []int                   `json:"kinds"`
	Names       []string                `json:"names"`
	Items       []Item                  `json:"items"`
	Recipes     []Recipe                `json:"recipes"`
	Mining      []map[string]MiningRule `json:"mining"`
	byID        map[int]Item
	byName      map[string]int
	blockModels map[int]int
	itemModels  map[int]int // Custom tool IDs reuse the template's mining rules.
}

func loadRules() (*Rules, error) {
	var r Rules
	err := json.Unmarshal(rulesJSON, &r)
	r.byID = map[int]Item{}
	r.byName = map[string]int{}
	for _, it := range r.Items {
		r.byID[it.ID] = it
		r.byName[it.Name] = it.ID
	}
	for id, n := range r.Names {
		if _, ok := r.byName[n]; !ok {
			r.byName[n] = id
		}
	}
	return &r, err
}
func (r *Rules) max(id int) int {
	if it, ok := r.byID[id]; ok && it.Stack > 0 {
		return it.Stack
	}
	return 0
}
func (r *Rules) mine(block, held int) MiningRule {
	if block < 0 || block >= len(r.Mining) {
		return MiningRule{Ticks: -1}
	}
	if model, ok := r.itemModels[held]; ok {
		held = model
	}
	m, ok := r.Mining[block][strconv.Itoa(held)]
	if !ok {
		m = r.Mining[block]["0"]
	}
	return m
}
func contains(ids []int, id int) bool {
	for _, i := range ids {
		if i == id {
			return true
		}
	}
	return false
}
func (r *Rules) craft(grid Stacks, w int) *Stack {
	x0, y0, x1, y1 := w, w, -1, -1
	var present []int
	for i, v := range grid {
		if v == nil || v.Count < 1 {
			continue
		}
		x, y := i%w, i/w
		x0 = min(x0, x)
		x1 = max(x1, x)
		y0 = min(y0, y)
		y1 = max(y1, y)
		present = append(present, v.ID)
	}
	if x1 < 0 {
		return nil
	}
	for _, rec := range r.Recipes {
		if rec.W > 0 {
			if rec.W != x1-x0+1 || rec.H != y1-y0+1 {
				continue
			}
			for _, mirror := range []bool{false, true} {
				ok := true
				for i, cell := range rec.Cells {
					x := i % rec.W
					if mirror {
						x = rec.W - 1 - x
					}
					v := grid[(y0+i/rec.W)*w+x0+x]
					id := 0
					if v != nil {
						id = v.ID
					}
					if (len(cell) == 0 && id != 0) || (len(cell) > 0 && !contains(cell, id)) {
						ok = false
						break
					}
				}
				if ok {
					return stack(rec.ID, rec.Count, 0)
				}
			}
		} else if len(rec.Ingredients) == len(present) {
			var match func(int, int) bool
			match = func(i, mask int) bool {
				if i == len(rec.Ingredients) {
					return true
				}
				for j, id := range present {
					if mask&(1<<j) == 0 && contains(rec.Ingredients[i], id) && match(i+1, mask|1<<j) {
						return true
					}
				}
				return false
			}
			if match(0, 0) {
				return stack(rec.ID, rec.Count, 0)
			}
		}
	}
	return nil
}
func baseName(n string) string { return strings.Split(n, " (")[0] }
func (s *Server) drops(block, held int) Stacks {
	if !s.rules.mine(block, held).Harvest {
		return nil
	}
	name := baseName(s.rules.Names[block])
	id := s.rules.byName[name]
	count := 1
	switch name {
	case "Stone":
		id = s.rules.byName["Cobblestone"]
	case "Grass Block", "Farmland":
		id = s.rules.byName["Dirt"]
	case "Iron Ore":
		id = s.rules.byName["Raw Iron"]
	case "Gold Ore":
		id = s.rules.byName["Raw Gold"]
	case "Coal Ore":
		id = s.rules.byName["Coal"]
	case "Diamond Ore":
		id = s.rules.byName["Diamond"]
	case "Lapis Lazuli Ore":
		id = s.rules.byName["Lapis Lazuli"]
		count = 4 + s.rng.Intn(6)
	case "Spark Ore":
		id = s.rules.byName["Spark Dust"]
		count = 4 + s.rng.Intn(2)
	case "Glass", "Fire", "Mob Cage":
		return nil
	case "Snow Block":
		id = s.rules.byName["Snowball"]
		count = 4
	case "Clay":
		id = s.rules.byName["Clay Ball"]
		count = 4
	case "Bookshelf":
		id = s.rules.byName["Book"]
		count = 3
	case "Melon":
		id = s.rules.byName["Melon Slice"]
		count = 3 + s.rng.Intn(5)
	case "Short Grass", "Fern", "Tall Grass", "Large Fern":
		if s.rng.Intn(8) != 0 {
			return nil
		}
		id = s.rules.byName["Wheat Seeds"]
	case "Oak Leaves", "Birch Leaves", "Spruce Leaves":
		if held == s.rules.byName["Shears"] {
			break
		}
		if name == "Oak Leaves" && s.rng.Intn(200) == 0 {
			return Stacks{stack(s.rules.byName["Apple"], 1, 0)}
		}
		if s.rng.Intn(20) != 0 {
			return nil
		}
		id = s.rules.byName[strings.TrimSuffix(name, "Leaves")+"Sapling"]
	case "Dead Bush":
		id = s.rules.byName["Stick"]
		count = s.rng.Intn(3)
	case "Wall Torch":
		id = s.rules.byName["Torch"]
	case "Gravel":
		if s.rng.Intn(10) == 0 {
			id = s.rules.byName["Flint"]
		}
	case "Wheat Crops":
		seeds := s.rules.byName["Wheat Seeds"]
		if seeds == 0 {
			seeds = itemWheatSeeds
		}
		if block == blkWheatMax || strings.Contains(s.rules.Names[block], "Mature") {
			wheat := s.rules.byName["Wheat"]
			if wheat == 0 {
				wheat = itemWheat
			}
			return Stacks{stack(wheat, 1, 0), stack(seeds, 1+s.rng.Intn(3), 0)}
		}
		return Stacks{stack(seeds, 1, 0)}
	case "Carrots":
		carrot := s.rules.byName["Carrot"]
		if carrot == 0 {
			carrot = itemCarrot
		}
		if block == blkCarrotMax || strings.Contains(s.rules.Names[block], "Mature") {
			return Stacks{stack(carrot, 1+s.rng.Intn(4), 0)}
		}
		return Stacks{stack(carrot, 1, 0)}
	case "Potatoes":
		potato := s.rules.byName["Potato"]
		if potato == 0 {
			potato = itemPotato
		}
		if block == blkPotatoMax || strings.Contains(s.rules.Names[block], "Mature") {
			drops := Stacks{stack(potato, 1+s.rng.Intn(4), 0)}
			if s.rng.Intn(50) == 0 {
				poison := s.rules.byName["Poisonous Potato"]
				if poison == 0 {
					poison = itemPoisonPotato
				}
				drops = append(drops, stack(poison, 1, 0))
			}
			return drops
		}
		return Stacks{stack(potato, 1, 0)}
	}
	if id == 0 || count == 0 || s.rules.max(id) == 0 {
		return nil
	}
	return Stacks{stack(id, count, 0)}
}
func number(v float64, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}
