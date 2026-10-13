package plugin

import "testing"

func TestMobLifecycleSDKAndReadonlyCatalog(t *testing.T) {
	e := &Event{Data: map[string]any{"entityId": uint32(123), "type": "zombie", "spawnReason": "natural", "category": "monster", "persistent": false, "target": &Combatant{EntityType: "zombie"}}}
	m := e.MobLifecycle()
	if m == nil || m.SpawnReason != "natural" || m.Category != "monster" {
		t.Fatal(m)
	}
	m.Target.EntityType = "cow"
	if e.MobLifecycle().Target.EntityType != "zombie" {
		t.Fatal("snapshot aliases event")
	}
	c := &Context{}
	c.SpawnPersistentMob("world", "zombie", 1, 60, 2)
	if len(c.Actions) != 1 || !c.Actions[0].Persistent || c.Actions[0].Type != "spawn_mob" {
		t.Fatal(c.Actions)
	}
	wanted := map[string]bool{EntitySpawning: true, EntitySpawned: false, EntityDespawning: true, EntityDespawned: false}
	for _, spec := range Events() {
		before, ok := wanted[spec.Name]
		if !ok {
			continue
		}
		if spec.Cancellable != before || len(spec.Mutable) != 0 {
			t.Fatal(spec)
		}
		for _, field := range []string{"spawnReason", "persistent", "actor", "source", "target", "categoryLimit", "eligibleChunks", "biome", "skyLight", "blockLight", "localLight", "minecraftTick", "spawningRules"} {
			found := false
			for _, f := range spec.Fields {
				if f == field {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s missing %s", spec.Name, field)
			}
		}
		delete(wanted, spec.Name)
	}
	if len(wanted) != 0 {
		t.Fatal(wanted)
	}
}
