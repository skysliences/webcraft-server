package game

import (
	"testing"
)

func TestScoreboardLifecycleAndCommands(t *testing.T) {
	s, _ := testApp(t, t.TempDir())
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ap := createAdvancedTestWorld(s)
	ap.name = "AdminPlayer"
	ap.sid = "admin_sid_12345"

	np := dropTestPlayer(r, 2, 15.5, 61, 15.5)
	np.name = "NormalPlayer"
	np.sid = "normal_sid_67890"
	r.Peers[np] = true

	// 赋予 ap 管理员权限
	next := s.copySettings()
	if next.Operators == nil {
		next.Operators = map[string]bool{}
	}
	next.Operators[ap.sid] = true
	if err := s.updateSettings(next, "setup", "test"); err != nil {
		t.Fatal(err)
	}

	// 1. 测试默认状态世界概览计分板构建
	defaultPkt := s.buildScoreboardPacket(r, ap)
	if defaultPkt == nil {
		t.Fatal("expected default scoreboard packet to be non-nil")
	}
	if defaultPkt["objective"] != "world_overview" {
		t.Fatalf("expected default objective 'world_overview', got %v", defaultPkt["objective"])
	}
	entries, ok := defaultPkt["entries"].([]ScoreEntry)
	if !ok || len(entries) < 2 {
		t.Fatalf("expected default entries list with players and time, got %+v", defaultPkt["entries"])
	}

	// 2. 普通玩家尝试修改计分板应被拦截
	if s.executeCommand(np, "/scoreboard objectives add test dummy 测试") {
		t.Fatal("non-operator should not be able to add objectives")
	}
	if s.executeCommand(np, "/scoreboard players set NormalPlayer test 100") {
		t.Fatal("non-operator should not be able to set player score")
	}

	// 3. 管理员创建自定义计分项目
	if !s.executeCommand(ap, "/scoreboard objectives add arena dummy 竞技积分榜") {
		t.Fatal("/scoreboard objectives add failed")
	}
	if r.scoreboard.Objectives["arena"] == nil {
		t.Fatal("arena objective should be registered")
	}
	if r.scoreboard.Objectives["arena"].DisplayName != "竞技积分榜" {
		t.Fatalf("expected display name '竞技积分榜', got %s", r.scoreboard.Objectives["arena"].DisplayName)
	}

	// 4. 绑定侧边栏显示槽位
	if !s.executeCommand(ap, "/scoreboard objectives setdisplay sidebar arena") {
		t.Fatal("/scoreboard objectives setdisplay sidebar arena failed")
	}
	if r.scoreboard.DisplaySlots["sidebar"] != "arena" {
		t.Fatalf("expected sidebar bound to 'arena', got %s", r.scoreboard.DisplaySlots["sidebar"])
	}

	// 5. 设置与累加玩家分数
	if !s.executeCommand(ap, "/scoreboard players set PlayerA arena 1500") {
		t.Fatal("/scoreboard players set PlayerA arena 1500 failed")
	}
	if r.scoreboard.Scores["arena"]["PlayerA"] != 1500 {
		t.Fatalf("expected PlayerA score 1500, got %d", r.scoreboard.Scores["arena"]["PlayerA"])
	}

	if !s.executeCommand(ap, "/scoreboard players add PlayerA arena 300") {
		t.Fatal("/scoreboard players add PlayerA arena 300 failed")
	}
	if r.scoreboard.Scores["arena"]["PlayerA"] != 1800 {
		t.Fatalf("expected PlayerA score 1800, got %d", r.scoreboard.Scores["arena"]["PlayerA"])
	}

	// 6. 添加更高分玩家 PlayerB，验证排位排序与 Top 1/Top 2 徽章
	if !s.executeCommand(ap, "/scoreboard players set PlayerB arena 2400") {
		t.Fatal("/scoreboard players set PlayerB arena 2400 failed")
	}
	arenaPkt := s.buildScoreboardPacket(r, ap)
	arenaEntries, ok := arenaPkt["entries"].([]ScoreEntry)
	if !ok || len(arenaEntries) != 2 {
		t.Fatalf("expected 2 arena entries, got %+v", arenaPkt["entries"])
	}
	if arenaEntries[0].Name != "PlayerB" || arenaEntries[0].Rank != 1 || arenaEntries[0].Icon != "👑" {
		t.Fatalf("expected PlayerB as Rank 1 with crown, got %+v", arenaEntries[0])
	}
	if arenaEntries[1].Name != "PlayerA" || arenaEntries[1].Rank != 2 || arenaEntries[1].Icon != "🥈" {
		t.Fatalf("expected PlayerA as Rank 2 with silver medal, got %+v", arenaEntries[1])
	}

	// 7. 测试 /scoreboard toggle 切换显示
	if !s.executeCommand(ap, "/scoreboard toggle") {
		t.Fatal("/scoreboard toggle failed")
	}
	if r.scoreboard.Enabled {
		t.Fatal("expected scoreboard to be disabled after toggle")
	}
	toggledPkt := s.buildScoreboardPacket(r, ap)
	if toggledPkt["enabled"] != false {
		t.Fatalf("expected packet enabled false, got %v", toggledPkt["enabled"])
	}

	// 恢复显示
	s.executeCommand(ap, "/scoreboard toggle")
	if !r.scoreboard.Enabled {
		t.Fatal("expected scoreboard to be re-enabled")
	}

	// 8. 重置玩家分数与移除项目
	if !s.executeCommand(ap, "/scoreboard players reset PlayerA arena") {
		t.Fatal("/scoreboard players reset failed")
	}
	if _, exists := r.scoreboard.Scores["arena"]["PlayerA"]; exists {
		t.Fatal("PlayerA score should be cleared")
	}

	if !s.executeCommand(ap, "/scoreboard objectives remove arena") {
		t.Fatal("/scoreboard objectives remove arena failed")
	}
	if r.scoreboard.Objectives["arena"] != nil {
		t.Fatal("arena objective should be deleted")
	}
}
