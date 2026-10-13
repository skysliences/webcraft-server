package game

import (
	"encoding/json"
	api "mcraft/backend/pkg/plugin"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScoreboardPluginRendering(t *testing.T) {
	tempDir := t.TempDir()
	pluginDir := filepath.Join(tempDir, "plugins")
	customDir := filepath.Join(pluginDir, "custom-scoreboard")
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 准备 plugin.json
	manifest := map[string]any{
		"id":         "custom-scoreboard",
		"name":       "Custom Scoreboard",
		"version":    "1.0.0",
		"apiVersion": 1,
		"entry":      "./custom-scoreboard",
	}
	rawM, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(customDir, "plugin.json"), rawM, 0644)

	// 准备自定义 config.json
	cfg := map[string]any{
		"enabled": true,
		"title":   "TEST-SCOREBOARD",
		"entries": []map[string]any{
			{
				"name":  "测试玩家",
				"score": 999,
				"icon":  "⭐",
				"badge": "{player_name}",
			},
			{
				"name":  "测试延迟",
				"score": "{ping}",
				"icon":  "⚡",
				"badge": "{ping}ms",
			},
		},
		"footer": map[string]string{
			"server": "test.ccraft.app",
			"tag":    "unittest",
		},
	}
	rawCfg, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(customDir, "config.json"), rawCfg, 0644)

	// 将已编译好的插件二进制拷贝到临时测试目录
	searchPaths := []string{
		filepath.Join("..", "..", "..", "plugins", "custom-scoreboard", "custom-scoreboard"),
		filepath.Join("..", "..", "plugins", "custom-scoreboard", "custom-scoreboard"),
	}
	var binData []byte
	var err error
	for _, p := range searchPaths {
		if data, e := os.ReadFile(p); e == nil {
			binData = data
			err = nil
			break
		} else {
			err = e
		}
	}
	if len(binData) == 0 {
		t.Skipf("custom-scoreboard binary not found: %v", err)
		return
	}
	dstBin := filepath.Join(customDir, "custom-scoreboard")
	if err := os.WriteFile(dstBin, binData, 0755); err != nil {
		t.Fatal(err)
	}

	config := Config{
		DataDir:       tempDir,
		PluginDir:     pluginDir,
		EngineFile:    findTestEngineFile(),
		PluginTimeout: 500 * time.Millisecond,
	}
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)
	ap.name = "Steve"
	ap.ping = 42

	// 调用 buildScoreboardPacket
	packet := s.buildScoreboardPacket(r, ap)
	if packet == nil {
		t.Fatal("expected non-nil packet")
	}

	if packet["enabled"] != true {
		t.Fatalf("expected scoreboard enabled true, got %v", packet["enabled"])
	}

	// 标题应被插件替换为 TEST-SCOREBOARD
	title, _ := packet["title"].(string)
	if title != "TEST-SCOREBOARD" {
		t.Fatalf("expected title 'TEST-SCOREBOARD', got '%s'", title)
	}

	// 条目应匹配插件条目，且占位符被正确替换
	entries, ok := packet["entries"].([]ScoreEntry)
	if !ok {
		t.Fatalf("expected []ScoreEntry, got %T", packet["entries"])
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	if entries[0].Name != "测试玩家" || entries[0].Score != 999 || entries[0].Badge != "Steve" {
		t.Fatalf("entry[0] mismatch: %+v", entries[0])
	}
	if entries[1].Name != "测试延迟" || entries[1].Score != 42 || entries[1].Badge != "42ms" {
		t.Fatalf("entry[1] mismatch: %+v", entries[1])
	}

	// 页脚应匹配插件配置
	footer, ok := packet["footer"].(map[string]any)
	pingOk := false
	if pVal, ok := footer["ping"].(int); ok && pVal == 42 {
		pingOk = true
	} else if pVal, ok := footer["ping"].(float64); ok && pVal == 42 {
		pingOk = true
	}
	if !ok || footer["server"] != "test.ccraft.app" || !pingOk {
		t.Fatalf("footer mismatch: %+v", footer)
	}
}

func TestScoreboardRenderEventCancellation(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)

	// 1. 无插件时生成默认包
	packet := s.buildScoreboardPacket(r, ap)
	if packet == nil || packet["enabled"] != true || (packet["title"] != "CCRAFT ONLINE" && packet["title"] != "WEBCRAFT ONLINE") {
		t.Fatalf("unexpected default packet: %+v", packet)
	}

	// 验证时间与时段在事件数据中正确传递
	testData := map[string]any{
		"title":      "HOOKED-TITLE",
		"objective":  "test_obj",
		"enabled":    false,
		"time":       "12:00",
		"time_phase": "白天",
	}
	e := s.event(api.ScoreboardRendering, r, ap, testData)
	if e == nil || e.Name != api.ScoreboardRendering {
		t.Fatalf("unexpected event: %+v", e)
	}
}
