package game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAnticheatConfigAndReload(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)
	ap.name = "AdminPlayer"
	ap.sid = "admin_sid_12345"

	// 1. 验证默认反作弊配置
	cfg := s.anticheatConfig()
	if !cfg.Enabled {
		t.Fatal("expected default anticheat to be enabled")
	}
	if cfg.ReachDistance != 6.5 {
		t.Fatalf("expected reach 6.5, got %f", cfg.ReachDistance)
	}
	if cfg.AttackReach != 4.5 {
		t.Fatalf("expected attack reach 4.5, got %f", cfg.AttackReach)
	}
	if cfg.MaxSpeed != 8.5 {
		t.Fatalf("expected max speed 8.5, got %f", cfg.MaxSpeed)
	}

	// 2. 验证 ReachDistance 动态影响 reachable
	ap.state.X = 10.0
	ap.state.Y = 60.0
	ap.state.Z = 10.0
	// 方块距离玩家约 7.0 格 (> 6.5)
	farMsg := Message{X: 17, Y: 60, Z: 10, F: 1}
	if s.reachable(ap, farMsg) {
		t.Fatal("expected block 7.0m away to be unreachable with reach 6.5")
	}

	// 调大 reachDistance 至 8.0
	cfg.ReachDistance = 8.0
	s.setAnticheatConfig(cfg)
	if !s.reachable(ap, farMsg) {
		t.Fatal("expected block 7.0m away to be reachable after setting reach to 8.0")
	}

	// 3. 验证关闭反作弊时直接放行
	superFarMsg := Message{X: 50, Y: 60, Z: 10, F: 1}
	if s.reachable(ap, superFarMsg) {
		t.Fatal("expected super far block to be unreachable when enabled")
	}
	cfg.Enabled = false
	s.setAnticheatConfig(cfg)
	if !s.reachable(ap, superFarMsg) {
		t.Fatal("expected super far block to be reachable when anticheat disabled")
	}

	// 恢复启用
	cfg.Enabled = true
	s.setAnticheatConfig(cfg)

	// 4. 验证从 anticheat.json 文件持久化与热加载
	customFile := AnticheatConfig{
		Enabled:         true,
		ReachDistance:   9.5,
		AttackReach:     5.5,
		MaxSpeed:        12.0,
		SpeedTolerance:  2.0,
		MaxTeleportDist: 15.0,
		NoclipCheck:     false,
		FlyCheck:        false,
		MaxAirTicks:     50,
	}
	raw, err := json.Marshal(customFile)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(tempDir, "anticheat.json")
	if err := os.WriteFile(filePath, raw, 0644); err != nil {
		t.Fatal(err)
	}

	if err := s.reloadAnticheatConfig(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	reloaded := s.anticheatConfig()
	if reloaded.ReachDistance != 9.5 || reloaded.MaxSpeed != 12.0 || reloaded.NoclipCheck != false {
		t.Fatalf("unexpected reloaded config: %+v", reloaded)
	}

	// 5. 验证控制台命令
	res, err := s.console("admin", "anticheat status", r.ID, ap.name)
	if err != nil {
		t.Fatalf("anticheat status console command failed: %v", err)
	}
	statusMap, ok := res.(map[string]any)
	if !ok || statusMap["reachDistance"] != 9.5 {
		t.Fatalf("unexpected console output: %v", res)
	}

	res, err = s.console("admin", "anticheat toggle", r.ID, ap.name)
	if err != nil {
		t.Fatalf("anticheat toggle console command failed: %v", err)
	}
	if s.anticheatConfig().Enabled != false {
		t.Fatal("expected anticheat to be disabled after toggle")
	}
}
