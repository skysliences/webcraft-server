package plugin

const (
	MechanicChanging              = "block.mechanic.before"
	MechanicChanged               = "block.mechanic.changed"
	RailChanging                  = "rail.state.before"
	RailChanged                   = "rail.state.changed"
	BlockDispensing               = "block.dispense.before"
	BlockDispensed                = "block.dispensed"
	BlockIgniting                 = "block.ignite.before"
	BlockIgnited                  = "block.ignited"
	PortalCreating                = "portal.create.before"
	PortalCreated                 = "portal.created"
	PlayerEquipping               = "player.equip.before"
	PlayerEquipped                = "player.equipped"
	PlayerFoodChanging            = "player.food.before"
	PlayerFoodChanged             = "player.food.changed"
	ItemEggHatching               = "item.egg.hatch.before"
	ItemEggHatched                = "item.egg.hatched"
	EntityGrowing                 = "entity.grow.before"
	EntityGrown                   = "entity.grown"
	EntityShearing                = "entity.shear.before"
	EntitySheared                 = "entity.sheared"
	SignChanging                  = "sign.text.before"
	SignChanged                   = "sign.text.changed"
	VehiclePlacing                = "vehicle.place.before"
	VehiclePlaced                 = "vehicle.placed"
	VehicleEntering               = "vehicle.enter.before"
	VehicleEntered                = "vehicle.entered"
	VehicleExiting                = "vehicle.exit.before"
	VehicleExited                 = "vehicle.exited"
	VehicleMoving                 = "vehicle.move.before"
	VehicleMoved                  = "vehicle.moved"
	VehicleDestroying             = "vehicle.destroy.before"
	VehicleDestroyed              = "vehicle.destroyed"
	FishingCasting                = "fishing.cast.before"
	FishingCast                   = "fishing.cast"
	FishingBiting                 = "fishing.bite.before"
	FishingBit                    = "fishing.bit"
	FishingReeling                = "fishing.reel.before"
	FishingReeled                 = "fishing.reeled"
	LeavesDecaying                = "leaves.decay.before"
	LeavesDecayed                 = "leaves.decayed"
	TNTPriming                    = "tnt.prime.before"
	TNTPrimed                     = "tnt.primed"
	RedstoneComponentChanging     = "redstone.component.before"
	RedstoneComponentChanged      = "redstone.component.changed"
	ItemSpawnEggUsing             = "item.spawn_egg.before"
	ItemSpawnEggUsed              = "item.spawn_egg.used"
	PlayerOperatorChanging        = "player.operator.before"
	PlayerOperatorChanged         = "player.operator.changed"
	ServerDefaultGameModeChanging = "server.default_gamemode.before"
	ServerDefaultGameModeChanged  = "server.default_gamemode.changed"
	ChunkGenerated                = "chunk.generated"
	ChunkPopulating               = "chunk.populate.before"
	ChunkPopulated                = "chunk.populated"
	ChunkLoaded                   = "chunk.loaded"
	ChunkSaving                   = "chunk.saving"
	ChunkSaved                    = "chunk.saved"
	ChunkSaveFailed               = "chunk.save.failed"
	ChunkUnloaded                 = "chunk.unloaded"
	ServerSettingsChanging        = "server.settings.before"
	ServerSettingsChanged         = "server.settings.changed"
	ServerCommandExecuting        = "server.command.before"
	ServerCommandExecuted         = "server.command.executed"
	PluginsReloading              = "server.plugins.reload.before"
	PluginsReloaded               = "server.plugins.reloaded"
	PluginsReloadFailed           = "server.plugins.reload.failed"
	BackupCreating                = "server.backup.before"
	BackupCreated                 = "server.backup.created"
	BackupFailed                  = "server.backup.failed"
	BackupRestoring               = "server.restore.before"
	BackupRestored                = "server.restored"
	BackupRestoreFailed           = "server.restore.failed"
	PlayerGameModeChanging        = "player.gamemode.before"
	PlayerGameModeChanged         = "player.gamemode.changed"
	WorldDifficultyChanging       = "world.difficulty.before"
	WorldDifficultyChanged        = "world.difficulty.changed"
	WorldRuleChanging             = "world.rule.before"
	WorldRuleChanged              = "world.rule.changed"
	WorldWeatherChanging          = "world.weather.before"
	WorldWeatherChanged           = "world.weather.changed"
	PlayerDimensionChanging       = "player.dimension.before"
	PlayerDimensionChanged        = "player.dimension.changed"
	PlayerExperienceChanging      = "player.experience.before"
	PlayerExperienceChanged       = "player.experience.changed"
	PlayerLevelChanging           = "player.level.before"
	PlayerLevelChanged            = "player.level.changed"
	PlayerEffectApplying          = "player.effect.apply.before"
	PlayerEffectApplied           = "player.effect.applied"
	PlayerEffectRemoving          = "player.effect.remove.before"
	PlayerEffectRemoved           = "player.effect.removed"
	PlayerIgniting                = "player.ignite.before"
	PlayerIgnited                 = "player.ignited"
	ItemEnchanting                = "item.enchant.before"
	ItemEnchanted                 = "item.enchanted"
	BrewingCompleting             = "brewing.complete.before"
	BrewingCompleted              = "brewing.completed"
	ProjectileLaunching           = "projectile.launch.before"
	ProjectileLaunched            = "projectile.launched"
	ProjectileHitting             = "projectile.hit.before"
	ProjectileHit                 = "projectile.hit"
	ProjectileRemoved             = "projectile.removed"
	BlockPhysicsChanging          = "block.physics.before"
	BlockPhysicsChanged           = "block.physics.changed"
	ExplosionCreating             = "world.explosion.before"
	ExplosionCreated              = "world.explosion.created"

	// 农业与作物
	FarmingTilling  = "farming.till.before"
	FarmingTilled   = "farming.tilled"
	CropGrowing     = "crop.grow.before"
	CropGrown       = "crop.grown"
	CropFertilizing = "crop.fertilize.before"
	CropFertilized  = "crop.fertilized"
	CropHarvesting  = "crop.harvest.before"
	CropHarvested   = "crop.harvested"

	// 丢弃与合并
	ItemDropping = "item.drop.before"
	ItemDropped  = "item.dropped"
	ItemMerging  = "item.merge.before"
	ItemMerged   = "item.merged"

	// 流体与桶
	FluidFlowing   = "fluid.flow.before"
	FluidFlowed    = "fluid.flowed"
	BucketFilling  = "bucket.fill.before"
	BucketFilled   = "bucket.filled"
	BucketEmptying = "bucket.empty.before"
	BucketEmptied  = "bucket.emptied"
	BottleFilling  = "item.bottle.fill.before"
	BottleFilled   = "item.bottle.filled"

	// 门与床
	DoorToggling      = "door.toggle.before"
	DoorToggled       = "door.toggled"
	PlayerSleeping    = "player.sleep.before"
	PlayerSlept       = "player.slept"
	PlayerWaking      = "player.wake.before"
	PlayerWoke        = "player.woke"
	WorldNightSkipped = "world.night_skip"

	// 铁砧
	AnvilUsing   = "anvil.use.before"
	AnvilUsed    = "anvil.used"
	AnvilDamaged = "anvil.damaged"

	// 动物
	AnimalBreeding = "animal.breed.before"
	AnimalBred     = "animal.bred"
	AnimalTaming   = "animal.tame.before"
	AnimalTamed    = "animal.tamed"
	AnimalShearing = "animal.shear.before"
	AnimalSheared  = "animal.sheared"

	// 反作弊违规
	PlayerAnticheatViolation = "player.anticheat.violation"

	// 计分板自定义渲染
	ScoreboardRendering = "scoreboard.render"
)

func extensionEvents() []EventSpec {
	out := []EventSpec{}
	names := []string{
		MechanicChanging, MechanicChanged, RailChanging, RailChanged,
		BlockDispensing, BlockDispensed, SignChanging, SignChanged,
		BlockIgniting, BlockIgnited, PortalCreating, PortalCreated,
		PlayerEquipping, PlayerEquipped, ItemEggHatching, ItemEggHatched,
		PlayerFoodChanging, PlayerFoodChanged,
		EntityGrowing, EntityGrown, EntityShearing, EntitySheared,
		VehiclePlacing, VehiclePlaced, VehicleEntering, VehicleEntered,
		VehicleExiting, VehicleExited, VehicleMoving, VehicleMoved, VehicleDestroying, VehicleDestroyed,
		FishingCasting, FishingCast, FishingBiting, FishingBit, FishingReeling, FishingReeled,
		LeavesDecaying, LeavesDecayed, TNTPriming, TNTPrimed,
		RedstoneComponentChanging, RedstoneComponentChanged,
		ItemSpawnEggUsing, ItemSpawnEggUsed,
		PlayerOperatorChanging, PlayerOperatorChanged, ServerDefaultGameModeChanging, ServerDefaultGameModeChanged,
		ChunkGenerated, ChunkPopulating, ChunkPopulated, ChunkLoaded, ChunkSaving, ChunkSaved, ChunkSaveFailed, ChunkUnloaded,
		ServerSettingsChanging, ServerSettingsChanged, ServerCommandExecuting, ServerCommandExecuted,
		PluginsReloading, PluginsReloaded, PluginsReloadFailed, BackupCreating, BackupCreated, BackupFailed,
		BackupRestoring, BackupRestored, BackupRestoreFailed, PlayerGameModeChanging, PlayerGameModeChanged,
		WorldDifficultyChanging, WorldDifficultyChanged, WorldRuleChanging, WorldRuleChanged,
		WorldWeatherChanging, WorldWeatherChanged, PlayerDimensionChanging, PlayerDimensionChanged,
		PlayerExperienceChanging, PlayerExperienceChanged, PlayerLevelChanging, PlayerLevelChanged,
		PlayerEffectApplying, PlayerEffectApplied, PlayerEffectRemoving, PlayerEffectRemoved,
		PlayerIgniting, PlayerIgnited, ItemEnchanting, ItemEnchanted, BrewingCompleting, BrewingCompleted,
		ProjectileLaunching, ProjectileLaunched, ProjectileHitting, ProjectileHit, ProjectileRemoved,
		BlockPhysicsChanging, BlockPhysicsChanged, ExplosionCreating, ExplosionCreated,
		// 新增系统事件
		FarmingTilling, FarmingTilled, CropGrowing, CropGrown, CropFertilizing, CropFertilized, CropHarvesting, CropHarvested,
		ItemDropping, ItemDropped, ItemMerging, ItemMerged,
		FluidFlowing, FluidFlowed, BucketFilling, BucketFilled, BucketEmptying, BucketEmptied,
		BottleFilling, BottleFilled,
		DoorToggling, DoorToggled, PlayerSleeping, PlayerSlept, PlayerWaking, PlayerWoke, WorldNightSkipped,
		AnvilUsing, AnvilUsed, AnvilDamaged,
		AnimalBreeding, AnimalBred, AnimalTaming, AnimalTamed, AnimalShearing, AnimalSheared,
		PlayerAnticheatViolation,
		ScoreboardRendering,
	}
	mutable := map[string][]string{
		TNTPriming:               {"fuseTicks"},
		PlayerExperienceChanging: {"amount"},
		PlayerEffectApplying:     {"ticks", "amplifier"},
		PlayerIgniting:           {"ticks"},
		WorldWeatherChanging:     {"duration"},
		ProjectileLaunching:      {"damage"},
		ExplosionCreating:        {"radius"},
		ItemDropping:             {"count"},
		AnvilUsing:               {"name"},
		ScoreboardRendering:      {"title", "enabled", "entries", "footer", "objective"},
	}
	for _, name := range names {
		before := (len(name) > 7 && name[len(name)-7:] == ".before") || name == ScoreboardRendering
		out = append(out, EventSpec{Name: name, Cancellable: before, Mutable: mutable[name]})
	}
	return out
}
