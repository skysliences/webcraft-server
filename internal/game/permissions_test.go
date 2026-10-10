package game

import (
	"testing"
)

func TestPermissionWildcardMatching(t *testing.T) {
	cases := []struct {
		pattern string
		node    string
		matches bool
	}{
		{"*", "anything", true},
		{"*", "world.build", true},
		{"world.*", "world.build", true},
		{"world.*", "world.interact.chest", true},
		{"world.*", "server.kick", false},
		{"world.build.*", "world.build.block", true},
		{"world.build.*", "world.break.block", false},
		{"server.*.admin", "server.world.admin", true},
		{"server.*.admin", "server.world.player", false},
		{"game.play", "game.play", true},
		{"game.play", "game.other", false},
	}

	for _, c := range cases {
		got := permissionMatches(c.pattern, c.node)
		if got != c.matches {
			t.Errorf("permissionMatches(%q, %q) = %v; want %v", c.pattern, c.node, got, c.matches)
		}
	}
}

func TestRoleGroupInheritanceAndDenyOverride(t *testing.T) {
	s := &Server{
		config: Config{DataDir: t.TempDir()},
		rooms:  map[string]*Room{},
		ops: &operations{
			Settings: ServerSettings{
				Version:   1,
				Operators: map[string]bool{"admin_sid": true},
				Groups: map[string]RoleGroup{
					"vip": {
						ID:       "vip",
						Inherits: []string{"player"},
						Allow:    []string{"world.fly", "chat.color"},
					},
					"super_vip": {
						ID:       "super_vip",
						Inherits: []string{"vip"},
						Allow:    []string{"world.tp.*"},
						Deny:     []string{"world.tp.restricted"},
					},
				},
				Permissions: map[string]PermissionGrant{
					"user_super": {
						Groups: []string{"super_vip"},
					},
					"user_restricted": {
						Role:  "builder",
						Deny:  []string{"world.break.*"},
						Allow: []string{"special.access"},
					},
				},
			},
		},
	}

	// 1. Operator has universal permission
	if !s.hasPermission("admin_sid", "any.random.permission") {
		t.Errorf("operator should have all permissions")
	}

	// 2. user_super inherits: super_vip -> vip -> player
	// Should have "player.chat" (from player), "world.fly" (from vip), "world.tp.spawn" (from super_vip)
	if !s.hasPermission("user_super", "player.chat") {
		t.Errorf("expected user_super to inherit player.chat")
	}
	if !s.hasPermission("user_super", "world.fly") {
		t.Errorf("expected user_super to inherit world.fly")
	}
	if !s.hasPermission("user_super", "world.tp.spawn") {
		t.Errorf("expected user_super to have world.tp.spawn via world.tp.*")
	}
	// Denied permission in group: world.tp.restricted
	if s.hasPermission("user_super", "world.tp.restricted") {
		t.Errorf("expected world.tp.restricted to be denied by group deny")
	}

	// 3. user_restricted has builder (which allows world.build.*, world.break.*)
	// But direct Deny has "world.break.*"
	if !s.hasPermission("user_restricted", "world.build.stone") {
		t.Errorf("expected user_restricted to have world.build.stone")
	}
	if s.hasPermission("user_restricted", "world.break.stone") {
		t.Errorf("expected world.break.stone to be denied by direct user deny")
	}
	if !s.hasPermission("user_restricted", "special.access") {
		t.Errorf("expected user_restricted to have direct special.access")
	}
}
