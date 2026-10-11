package game

import (
	"encoding/json"
	"testing"
)

// Fail when offline durations and animal food drift from Go. Offline has its
// own fixed 32-mob budget; Go multiplayer spawning is configurable.
func TestBrowserAutomationRuleParity(t *testing.T) {
	var contract struct {
		Automation struct {
			HopperCooldownTicks int `json:"hopperCooldownTicks"`
			LoveTicks           int `json:"loveTicks"`
			BreedCooldownTicks  int `json:"breedCooldownTicks"`
			GrowthTicks         int `json:"growthTicks"`
			MatingIntervalTicks int `json:"matingIntervalTicks"`
			MaxMobs             int `json:"maxMobs"`
			Animals             map[string]struct {
				Food                  int
				Health, Width, Height float64
			} `json:"animals"`
		} `json:"automation"`
	}
	if err := json.Unmarshal(rulesJSON, &contract); err != nil {
		t.Fatal(err)
	}
	a := contract.Automation
	if a.HopperCooldownTicks != hopperCooldown*2 || a.LoveTicks != animalLoveTicks || a.BreedCooldownTicks != animalBreedCooldown || a.GrowthTicks != 24000 || a.MatingIntervalTicks != 40 || a.MaxMobs != 32 {
		t.Fatalf("browser/Go timing drift: %+v", a)
	}
	if len(a.Animals) != 4 {
		t.Fatal("missing animal contract")
	}
	for name, spec := range a.Animals {
		goSpec, ok := mobSpecs[name]
		if !ok || !animalFood(name, spec.Food) || spec.Health != goSpec.Health || spec.Width != goSpec.Width || spec.Height != goSpec.Height {
			t.Fatalf("browser/Go animal drift: %s %+v", name, spec)
		}
	}
}
