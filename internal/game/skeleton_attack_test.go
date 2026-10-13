package game

import (
	"testing"
)

func TestSkeletonAttackPlayer(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p := mobTestWorld(s)
	r.Mode = "survival"
	r.Time = 18000

	p.state.Health = 20.0
	p.state.Dead = false
	p.state.X, p.state.Y, p.state.Z = 15.5, 60.0, 10.5

	skel, err := s.spawnMob(r, nil, "skeleton", 10.5, 60.0, 10.5, "test")
	if err != nil || skel == nil {
		t.Fatalf("failed to spawn skeleton: %v", err)
	}

	initialSkelHealth := skel.Health
	t.Logf("Initial skel health: %f, player health: %f", initialSkelHealth, p.state.Health)

	// Run world ticks (worldTick runs mobsTick and extTick)
	for i := 0; i < 30; i++ {
		s.worldTick(r)
	}

	t.Logf("After 30 ticks, skel health: %f, player health: %f, projectiles: %d", skel.Health, p.state.Health, len(r.rt().projectiles))
	if skel.Health < initialSkelHealth {
		t.Errorf("Skeleton took self-damage! skel.Health=%f", skel.Health)
	}
}
