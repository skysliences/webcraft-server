package main

import (
	"encoding/json"
	"fmt"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
)

func main() {
	p := api.New()
	protected := map[string]bool{}
	file := ""
	var config struct {
		AllowedPrimaryWorldID     string   `json:"allowedPrimaryWorldId"`
		BlockedSpawnEggTypes      []string `json:"blockedSpawnEggTypes"`
		BlockedNaturalMobTypes    []string `json:"blockedNaturalMobTypes"`
		BlockedNaturalBiomes      []string `json:"blockedNaturalBiomes"`
		BlockedDimensions         []string `json:"blockedDimensions"`
		PreserveDistantMobs       bool     `json:"preserveDistantMobs"`
		BlockedRedstoneComponents []string `json:"blockedRedstoneComponents"`
		PreserveNaturalLeaves     bool     `json:"preserveNaturalLeaves"`
		BlockedTNTReasons         []string `json:"blockedTntReasons"`
		TNTFuseTicks              int      `json:"tntFuseTicks"`
		BlockedMechanics          []string `json:"blockedMechanics"`
		BlockedVehicleTypes       []string `json:"blockedVehicleTypes"`
		DisableHopperTransfers    bool     `json:"disableHopperTransfers"`
		DisableAnimalFeeding      bool     `json:"disableAnimalFeeding"`
		DisableAnimalBreeding     bool     `json:"disableAnimalBreeding"`
		DisableFishing            bool     `json:"disableFishing"`
		DisableDispensing         bool     `json:"disableDispensing"`
		BlockedDispenseBehaviors  []string `json:"blockedDispenseBehaviors"`
	}
	p.OnEnable = func(c *api.Context) error {
		if len(c.Config) > 0 {
			if err := json.Unmarshal(c.Config, &config); err != nil {
				return err
			}
		}
		if config.TNTFuseTicks < 0 || config.TNTFuseTicks > 12000 {
			return fmt.Errorf("tntFuseTicks must be 0 (default) or 1..12000")
		}
		file = filepath.Join(c.DataDir, "worlds.json")
		raw, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &protected)
	}
	for _, name := range []string{api.InventoryMoving, api.InventoryPickingUp, api.AnimalFeeding, api.AnimalBreeding} {
		p.On(name, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
			if config.DisableHopperTransfers && e.String("source") == "hopper" || config.DisableAnimalFeeding && e.Name == api.AnimalFeeding || config.DisableAnimalBreeding && e.Name == api.AnimalBreeding {
				e.Cancel("world_policy.automation_denied")
			}
			if e.Name == api.InventoryMoving || e.Name == api.InventoryPickingUp {
				if item := e.Item("stack"); item != nil && item.Meta != nil && item.Meta.CustomData["world-policy:lock_transfer"] == true {
					e.Cancel("world_policy.item_transfer_locked")
				}
			}
			return nil
		})
	}
	p.On(api.ServerWorldSelecting, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if config.AllowedPrimaryWorldID != "" && e.String("worldId") != config.AllowedPrimaryWorldID {
			e.Cancel("world_policy.primary_world_denied")
		}
		return nil
	})
	p.On(api.PlayerDimensionChanging, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if transfer := e.PlayerTransfer(); transfer != nil {
			for _, dim := range config.BlockedDimensions {
				if dim == transfer.Dimension {
					e.Cancel("world_policy.dimension_denied")
				}
			}
		}
		return nil
	})
	p.Command(api.Command{Name: "worldpolicy", Permission: "owner", Usage: "/worldpolicy protect|open", Description: "示例：世界爆炸保护"}, func(c *api.Context, e *api.Event, args []string) error {
		if len(args) != 1 || args[0] != "protect" && args[0] != "open" {
			c.Message(e, "用法：/worldpolicy protect|open")
			return nil
		}
		next := map[string]bool{}
		for k, v := range protected {
			next[k] = v
		}
		next[e.World.RootID] = args[0] == "protect"
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err = os.WriteFile(file+".tmp", raw, 0600); err != nil {
			return err
		}
		if err = os.Rename(file+".tmp", file); err != nil {
			return err
		}
		protected = next
		c.Message(e, "世界爆炸保护已更新。")
		return nil
	})
	p.On(api.ExplosionCreating, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if e.World != nil && protected[e.World.RootID] {
			e.Cancel("world_policy.explosion_protected")
		}
		return nil
	})
	p.On(api.LeavesDecaying, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if config.PreserveNaturalLeaves {
			e.Cancel("world_policy.leaves_preserved")
		}
		return nil
	})
	p.On(api.TNTPriming, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if e.World != nil && protected[e.World.RootID] {
			e.Cancel("world_policy.tnt_protected")
			return nil
		}
		for _, reason := range config.BlockedTNTReasons {
			if reason == e.String("reason") {
				e.Cancel("world_policy.tnt_reason_denied")
				return nil
			}
		}
		if config.TNTFuseTicks > 0 {
			e.Data["fuseTicks"] = config.TNTFuseTicks
		}
		return nil
	})
	p.On(api.ItemSpawnEggUsing, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		for _, kind := range config.BlockedSpawnEggTypes {
			if e.String("type") == kind {
				e.Cancel("world_policy.spawn_egg_type_denied")
				break
			}
		}
		return nil
	})
	p.On(api.EntitySpawning, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if mob := e.MobLifecycle(); mob != nil && (mob.SpawnReason == "natural" || mob.SpawnReason == "chunk_generation") {
			for _, biome := range config.BlockedNaturalBiomes {
				if mob.Biome == biome {
					e.Cancel("world_policy.biome_denied")
				}
			}
			for _, kind := range config.BlockedNaturalMobTypes {
				if mob.Type == kind {
					e.Cancel("world_policy.natural_mob_denied")
					break
				}
			}
		}
		return nil
	})
	p.On(api.EntityDespawning, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		if config.PreserveDistantMobs && (e.String("reason") == "distance" || e.String("reason") == "random_distance") {
			e.Cancel("world_policy.distant_mob_preserved")
		}
		return nil
	})
	p.On(api.RedstoneComponentChanging, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
		for _, component := range config.BlockedRedstoneComponents {
			if e.String("component") == component {
				e.Cancel("world_policy.redstone_component_denied")
				break
			}
		}
		return nil
	})
	for _, name := range []string{api.MechanicChanging, api.BlockDispensing, api.VehiclePlacing, api.VehicleEntering, api.VehicleDestroying, api.FishingCasting, api.FishingReeling} {
		p.On(name, api.Subscription{Priority: api.High}, func(c *api.Context, e *api.Event) error {
			for _, kind := range config.BlockedMechanics {
				if e.String("mechanic") == kind {
					e.Cancel("world_policy.mechanic_denied")
				}
			}
			for _, kind := range config.BlockedVehicleTypes {
				if e.String("vehicleType") == kind || e.String("type") == kind {
					e.Cancel("world_policy.vehicle_denied")
				}
			}
			if config.DisableFishing && (e.Name == api.FishingCasting || e.Name == api.FishingReeling) {
				e.Cancel("world_policy.fishing_denied")
			}
			if config.DisableDispensing && e.Name == api.BlockDispensing {
				e.Cancel("world_policy.dispense_denied")
			}
			if e.Name == api.BlockDispensing {
				for _, behavior := range config.BlockedDispenseBehaviors {
					if behavior == e.String("behavior") {
						e.Cancel("world_policy.dispense_behavior_denied")
					}
				}
			}
			return nil
		})
	}
	// Monitor can observe a committed action, but its changes/actions are ignored.
	for _, name := range []string{api.PlayerDimensionChanged, api.EntitySpawned, api.EntityDespawned, api.ServerWorldSelected, api.WorldRuleChanged, api.BackupCreated, api.PluginsReloaded, api.ChunkPopulated, api.PlayerOperatorChanged, api.PlayerGameModeChanged, api.ServerDefaultGameModeChanged, api.ItemSpawnEggUsed, api.RedstoneComponentChanged, api.LeavesDecayed, api.TNTPrimed, api.MechanicChanged, api.RailChanged, api.BlockDispensed, api.SignChanged, api.VehiclePlaced, api.VehicleEntered, api.VehicleExited, api.VehicleDestroyed, api.FishingCast, api.FishingBit, api.FishingReeled, api.BlockIgnited, api.PortalCreated, api.PlayerEquipped, api.BottleFilled, api.ItemEggHatched, api.EntityGrown, api.EntitySheared, api.PlayerFoodChanged} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(raw, '\n'))
			return err
		})
	}
	if err := p.Run(); err != nil {
		os.Exit(1)
	}
}
