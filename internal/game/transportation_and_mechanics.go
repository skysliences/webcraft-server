package game

import "math/rand"

// Shared numeric IDs and the compatibility view used by older extensions.
// Authority lives in the protected rail/mechanic, vehicle and fishing kernels.
const (
	blkCauldron         = 179
	blkSign             = 191
	blkJukebox          = 192
	blkRail             = 211
	blkPoweredRail      = 212
	blkDetectorRail     = 213
	blkDispenser        = 214
	blkDropper          = 215
	blkObserver         = 216
	blkRedstoneLamp     = 217
	blkDaylightDetector = 218
	blkComposter        = 219

	itemBow      = 301
	itemArrow    = 302
	itemSaddle   = 329
	itemSnowball = 332
	itemApple    = 337
	itemBread    = 339

	itemRail             = 365
	itemPoweredRail      = 366
	itemDetectorRail     = 367
	itemMinecart         = 368
	itemBoat             = 369
	itemFishingRod       = 370
	itemCod              = 371
	itemSalmon           = 372
	itemCookedCod        = 373
	itemCookedSalmon     = 374
	itemPufferfish       = 375
	itemDispenser        = 376
	itemDropper          = 377
	itemObserver         = 378
	itemRedstoneLamp     = 379
	itemDaylightDetector = 380
	itemComposter        = 381
	itemCauldron         = 382
	itemOakSign          = 383
	itemJukebox          = 384
)

type MinecartEntity = Vehicle
type BoatEntity = Vehicle
type BobberEntity = FishingBobber

type VehicleState struct {
	Minecarts      map[uint32]*MinecartEntity
	Boats          map[uint32]*BoatEntity
	Bobbers        map[uint16]*BobberEntity
	Composters     map[uint64]int
	Signs          map[uint64][4]string
	Jukeboxes      map[uint64]int
	ObserverPulses map[uint64]int
}

func getVehicles(r *Room) *VehicleState {
	rt := r.rt()
	if rt.vehicleView == nil {
		rt.vehicleView = &VehicleState{Minecarts: map[uint32]*MinecartEntity{}, Boats: map[uint32]*BoatEntity{}, Bobbers: map[uint16]*BobberEntity{}, Composters: map[uint64]int{}, Signs: map[uint64][4]string{}, Jukeboxes: map[uint64]int{}, ObserverPulses: map[uint64]int{}}
	}
	return rt.vehicleView
}
func isRailBlock(b int) bool { return b == blkRail || b == blkPoweredRail || b == blkDetectorRail }
func findPeerByID(r *Room, id uint16) *Peer {
	for p := range r.Peers {
		if p != nil && p.id == id {
			return p
		}
	}
	return nil
}

// Kept for external source compatibility; the authoritative fishing kernel
// uses the server random source and complete item snapshots.
func rollFishingLoot() int {
	n := rand.Float64()
	if n < .6 {
		return itemCod
	}
	if n < .85 {
		return itemSalmon
	}
	if n < .95 {
		return itemPufferfish
	}
	a := []int{itemBow, itemSaddle, 322}
	return a[rand.Intn(len(a))]
}
func isCompostable(id int) bool {
	return id == itemWheatSeeds || id == itemWheat || id == itemCarrot || id == itemPotato || id == itemApple || id == itemBread || id == 7 || id == 17 || id == 73
}
func isMusicDisc(id int) bool { return id >= 500 && id <= 511 }
