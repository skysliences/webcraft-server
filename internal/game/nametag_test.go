package game

import (
	"os"
	"testing"

	api "mcraft/backend/pkg/plugin"
)

func TestNametagPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nametag_db_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := initEconomyDB(tmpDir)
	if err != nil {
		t.Fatalf("init economy db: %v", err)
	}
	defer db.Close()

	sid := "test_sid_nametag_123"
	playerName := "TestPlayer"

	// 1. Initial should be nil
	info, err := db.GetNametag(sid)
	if err != nil {
		t.Fatalf("get empty nametag: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil nametag initially, got %+v", info)
	}

	// 2. Save custom nametag
	saveInfo := &NametagInfo{
		Prefix:      "§6[至尊VIP]§r ",
		Suffix:      "  §a[Lv.100]§r",
		DisplayName: "勇者阿飞",
		Title:       "★ 屠龙战神 ★",
		TitleColor:  "#f1c40f",
		TitleIcon:   "crown",
	}
	if err := db.SaveNametag(sid, playerName, saveInfo); err != nil {
		t.Fatalf("save nametag: %v", err)
	}

	// 3. Read back and verify
	loaded, err := db.GetNametag(sid)
	if err != nil {
		t.Fatalf("load saved nametag: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded nametag, got nil")
	}
	if loaded.Prefix != saveInfo.Prefix || loaded.Suffix != saveInfo.Suffix ||
		loaded.Title != saveInfo.Title || loaded.TitleIcon != saveInfo.TitleIcon ||
		loaded.TitleColor != saveInfo.TitleColor || loaded.DisplayName != saveInfo.DisplayName {
		t.Fatalf("loaded nametag mismatch: %+v vs %+v", loaded, saveInfo)
	}

	// 4. Update
	saveInfo.TitleIcon = "dragon"
	saveInfo.Title = "末影终结者"
	if err := db.SaveNametag(sid, playerName, saveInfo); err != nil {
		t.Fatalf("update nametag: %v", err)
	}
	updated, err := db.GetNametag(sid)
	if err != nil || updated == nil || updated.TitleIcon != "dragon" || updated.Title != "末影终结者" {
		t.Fatalf("updated nametag mismatch: %+v", updated)
	}

	// 5. Clear
	if err := db.ClearNametag(sid); err != nil {
		t.Fatalf("clear nametag: %v", err)
	}
	cleared, err := db.GetNametag(sid)
	if err != nil || cleared != nil {
		t.Fatalf("expected nil after clear, got %+v", cleared)
	}
}

func TestNametagCommands(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, admin := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	admin.name = "Admin"
	admin.sid = "admin_sid"
	admin.owner = true
	if s.ops.Settings.Operators == nil {
		s.ops.Settings.Operators = map[string]bool{}
	}
	s.ops.Settings.Operators[admin.sid] = true

	target := dropTestPlayer(r, 2, 10, 64, 10)
	target.name = "PlayerA"
	target.sid = "playera_sid"
	r.Peers[target] = true

	// 1. Admin sets prefix
	ok, handled := s.handleNametagCommands(admin, "prefix", []string{"prefix", "PlayerA", "§6[公会长]§r"})
	if !ok || !handled {
		t.Fatal("prefix command failed")
	}
	targetInfo := s.getPlayerNametag(target)
	if targetInfo.Prefix != "§6[公会长]§r" {
		t.Fatalf("expected prefix '§6[公会长]§r', got %s", targetInfo.Prefix)
	}

	// 2. Admin sets suffix
	ok, handled = s.handleNametagCommands(admin, "suffix", []string{"suffix", "PlayerA", "§e★VIP"})
	if !ok || !handled {
		t.Fatal("suffix command failed")
	}
	targetInfo = s.getPlayerNametag(target)
	if targetInfo.Suffix != "§e★VIP" {
		t.Fatalf("expected suffix '§e★VIP', got %s", targetInfo.Suffix)
	}

	// 3. Admin sets image title
	ok, handled = s.handleNametagCommands(admin, "title", []string{"title", "PlayerA", "crown", "荣耀王者"})
	if !ok || !handled {
		t.Fatal("title command failed")
	}
	targetInfo = s.getPlayerNametag(target)
	if targetInfo.TitleIcon != "crown" || targetInfo.Title != "荣耀王者" {
		t.Fatalf("expected titleIcon crown and title 荣耀王者, got %s, %s", targetInfo.TitleIcon, targetInfo.Title)
	}

	// 4. Verify public() snapshot
	pub := target.public()
	if pub["prefix"] != "§6[公会长]§r" || pub["suffix"] != "§e★VIP" || pub["titleIcon"] != "crown" || pub["title"] != "荣耀王者" {
		t.Fatalf("public() snapshot missing nametag fields: %+v", pub)
	}

	// 5. Reset via command
	ok, handled = s.handleNametagCommands(admin, "nametag", []string{"nametag", "clear", "PlayerA"})
	if !ok || !handled {
		t.Fatal("nametag clear failed")
	}
	targetInfo = s.getPlayerNametag(target)
	if targetInfo.Prefix != "" || targetInfo.TitleIcon != "" {
		t.Fatalf("expected cleared nametag, got %+v", targetInfo)
	}
}

func TestNametagPluginAction(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p.name = "Hero"
	p.sid = "hero_sid"

	// Simulate plugin applying action "set_nametag"
	s.applyAction(api.Action{
		Type:     "set_nametag",
		WorldID:  r.ID,
		PlayerID: p.id,
		Nametag: &api.NametagAction{
			Prefix:    "[勇士] ",
			Suffix:    " (Lv.99)",
			Title:     "屠龙使者",
			TitleIcon: "dragon",
		},
	})

	info := s.getPlayerNametag(p)
	if info.Prefix != "[勇士] " || info.Suffix != " (Lv.99)" || info.TitleIcon != "dragon" || info.Title != "屠龙使者" {
		t.Fatalf("plugin set_nametag action did not apply correctly: %+v", info)
	}
}

func TestChatWithNametagPrefixSuffix(t *testing.T) {
	tempDir := t.TempDir()
	s, _ := testApp(t, tempDir)
	s.mu.Lock()
	defer s.mu.Unlock()

	r, p := createAdvancedTestWorld(s)
	s.rooms[r.ID] = r
	p.name = "Chatter"
	p.sid = "chatter_sid"

	// 1. 设置前缀与后缀
	info := &NametagInfo{
		Prefix: "§6[公会长]§r ",
		Suffix: " §e★VIP",
	}
	s.setPlayerNametag(p, info, false)

	// 2. 验证 renderPlayerNametag 获取正确
	rendered := s.renderPlayerNametag(p)
	if rendered.Prefix != "§6[公会长]§r " || rendered.Suffix != " §e★VIP" {
		t.Fatalf("expected prefix/suffix preserved, got %+v", rendered)
	}
}
