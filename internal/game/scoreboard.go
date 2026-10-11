package game

import (
	"fmt"
	"math"
	api "mcraft/backend/pkg/plugin"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Objective 表示一个计分项
type Objective struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Criteria    string `json:"criteria"` // "dummy", "health", "kills", etc.
}

// EntryMeta 保存条目的附加元数据（图标、排位、微标、进度、高亮等）
type EntryMeta struct {
	Icon     string  `json:"icon,omitempty"`
	Badge    string  `json:"badge,omitempty"`
	Color    string  `json:"color,omitempty"`
	Rank     int     `json:"rank,omitempty"`
	Progress float64 `json:"progress,omitempty"` // 0.0 ~ 1.0
}

// ScoreEntry 用于下发给客户端渲染的单行条目
type ScoreEntry struct {
	Name     string  `json:"name"`
	Score    int     `json:"score"`
	Icon     string  `json:"icon,omitempty"`
	Badge    string  `json:"badge,omitempty"`
	Color    string  `json:"color,omitempty"`
	Rank     int     `json:"rank,omitempty"`
	Progress float64 `json:"progress,omitempty"`
}

// Scoreboard 房间/世界级计分板管理器
type Scoreboard struct {
	mu           sync.RWMutex
	room         *Room
	Objectives   map[string]*Objective        `json:"objectives"`
	DisplaySlots map[string]string            `json:"displaySlots"` // "sidebar" -> objective name
	Scores       map[string]map[string]int    `json:"scores"`       // objName -> (holder -> score)
	Meta         map[string]map[string]EntryMeta `json:"meta"`         // objName -> (holder -> meta)
	Enabled      bool                         `json:"enabled"`
}

// newScoreboard 为世界创建计分板实例
func newScoreboard(r *Room) *Scoreboard {
	sc := &Scoreboard{
		room:         r,
		Objectives:   make(map[string]*Objective),
		DisplaySlots: make(map[string]string),
		Scores:       make(map[string]map[string]int),
		Meta:         make(map[string]map[string]EntryMeta),
		Enabled:      true,
	}
	return sc
}

// AddObjective 添加计分项目
func (sc *Scoreboard) AddObjective(name, criteria, displayName string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if displayName == "" {
		displayName = name
	}
	sc.Objectives[name] = &Objective{
		Name:        name,
		DisplayName: displayName,
		Criteria:    criteria,
	}
	if sc.Scores[name] == nil {
		sc.Scores[name] = make(map[string]int)
	}
	if sc.Meta[name] == nil {
		sc.Meta[name] = make(map[string]EntryMeta)
	}
	return true
}

// RemoveObjective 移除计分项目
func (sc *Scoreboard) RemoveObjective(name string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.Objectives[name] == nil {
		return false
	}
	delete(sc.Objectives, name)
	delete(sc.Scores, name)
	delete(sc.Meta, name)
	for slot, obj := range sc.DisplaySlots {
		if obj == name {
			delete(sc.DisplaySlots, slot)
		}
	}
	return true
}

// SetDisplay 绑定显示槽位
func (sc *Scoreboard) SetDisplay(slot, name string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	slot = strings.ToLower(strings.TrimSpace(slot))
	if name == "" {
		delete(sc.DisplaySlots, slot)
		return true
	}
	if sc.Objectives[name] == nil {
		return false
	}
	sc.DisplaySlots[slot] = name
	return true
}

// SetScore 设置玩家/词条分数
func (sc *Scoreboard) SetScore(holder, objName string, score int, meta ...EntryMeta) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.Objectives[objName] == nil {
		return false
	}
	if sc.Scores[objName] == nil {
		sc.Scores[objName] = make(map[string]int)
	}
	sc.Scores[objName][holder] = score
	if len(meta) > 0 {
		if sc.Meta[objName] == nil {
			sc.Meta[objName] = make(map[string]EntryMeta)
		}
		sc.Meta[objName][holder] = meta[0]
	}
	return true
}

// AddScore 累加分数
func (sc *Scoreboard) AddScore(holder, objName string, delta int) (int, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.Objectives[objName] == nil {
		return 0, false
	}
	if sc.Scores[objName] == nil {
		sc.Scores[objName] = make(map[string]int)
	}
	cur := sc.Scores[objName][holder]
	cur += delta
	sc.Scores[objName][holder] = cur
	return cur, true
}

// ResetScores 重置分数
func (sc *Scoreboard) ResetScores(holder, objName string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if objName == "" {
		// 重置所有项目中的该 holder
		for o := range sc.Scores {
			delete(sc.Scores[o], holder)
			if sc.Meta[o] != nil {
				delete(sc.Meta[o], holder)
			}
		}
		return true
	}
	if sc.Scores[objName] == nil {
		return false
	}
	delete(sc.Scores[objName], holder)
	if sc.Meta[objName] != nil {
		delete(sc.Meta[objName], holder)
	}
	return true
}

// BuildPacket 构建发送给特定玩家的计分板协议包
func (s *Server) buildScoreboardPacket(r *Room, p *Peer) map[string]any {
	if r == nil || r.scoreboard == nil {
		return nil
	}
	sc := r.scoreboard
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if !sc.Enabled {
		return map[string]any{
			"t":       "scoreboard",
			"enabled": false,
		}
	}

	activeObjName := sc.DisplaySlots["sidebar"]
	obj := sc.Objectives[activeObjName]

	var (
		title     string
		entries   []ScoreEntry
		footerMap map[string]any
	)

	// 1. 如果有活跃的自定义 Objective，渲染自定义条目
	if obj != nil {
		title = obj.DisplayName
		scoresMap := sc.Scores[activeObjName]
		metaMap := sc.Meta[activeObjName]

		for holder, val := range scoresMap {
			entry := ScoreEntry{
				Name:  holder,
				Score: val,
			}
			if metaMap != nil {
				if m, ok := metaMap[holder]; ok {
					entry.Icon = m.Icon
					entry.Badge = m.Badge
					entry.Color = m.Color
					entry.Rank = m.Rank
					entry.Progress = m.Progress
				}
			}
			entries = append(entries, entry)
		}

		// 按分数由大到小排序
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Score != entries[j].Score {
				return entries[i].Score > entries[j].Score
			}
			return entries[i].Name < entries[j].Name
		})

		// 自动标注 Top 1 / Top 2 / Top 3
		for i := range entries {
			if entries[i].Rank == 0 && i < 3 {
				entries[i].Rank = i + 1
				if entries[i].Icon == "" {
					if i == 0 {
						entries[i].Icon = "👑"
					} else if i == 1 {
						entries[i].Icon = "🥈"
					} else {
						entries[i].Icon = "🥉"
					}
				}
			}
		}
		footerMap = map[string]any{
			"server": r.Name,
			"mode":   r.Mode,
		}
		if p != nil {
			footerMap["ping"] = p.ping
		}
	} else {
		// 2. 默认智能世界状态计分板（Smart Default Scoreboard）
		activeObjName = "world_overview"
		title = "WEBCRAFT ONLINE"
		onlineCount := len(r.Peers)
		timeOfDay := r.root().Time % 24000
		hour := (timeOfDay/1000 + 6) % 24
		minute := (timeOfDay % 1000) * 60 / 1000
		timeStr := fmt.Sprintf("%02d:%02d", hour, minute)
		timePhase := "白天"
		if timeOfDay >= 13000 && timeOfDay < 23000 {
			timePhase = "黑夜"
		} else if timeOfDay >= 12000 && timeOfDay < 13000 {
			timePhase = "黄昏"
		}

		entries = []ScoreEntry{
			{
				Name:  "世界",
				Score: onlineCount,
				Icon:  "🌐",
				Badge: strings.ToUpper(r.Mode),
			},
			{
				Name:  "在线人数",
				Score: onlineCount,
				Icon:  "👥",
			},
			{
				Name:  "时刻 " + timePhase,
				Score: int(timeOfDay / 100),
				Icon:  "⏳",
				Badge: timeStr,
			},
		}

		// 添加服务器网络延迟
		if p != nil {
			pingVal := p.ping
			pingBadge := fmt.Sprintf("%dms", pingVal)
			if pingVal <= 0 {
				pingBadge = "--ms"
			}
			entries = append(entries, ScoreEntry{
				Name:  "服务器延迟",
				Score: pingVal,
				Icon:  "⚡",
				Badge: pingBadge,
			})
		}

		// 添加当前玩家自身的健康与状态
		if p != nil && p.state != nil {
			hp := int(math.Ceil(p.state.Health))
			entries = append(entries, ScoreEntry{
				Name:     p.name,
				Score:    hp,
				Icon:     "❤️",
				Badge:    "YOU",
				Progress: p.state.Health / 20.0,
			})
		}

		// 统计世界中其他玩家
		otherIdx := 1
		for other := range r.Peers {
			if other != p && otherIdx <= 5 {
				h := int(math.Ceil(other.state.Health))
				entries = append(entries, ScoreEntry{
					Name:     other.name,
					Score:    h,
					Icon:     "⚔️",
					Progress: other.state.Health / 20.0,
				})
				otherIdx++
			}
		}

		footerMap = map[string]any{
			"server": r.Name,
			"mode":   strings.ToUpper(r.Mode),
			"dim":    strings.ToUpper(r.root().ID),
		}
		if p != nil {
			footerMap["ping"] = p.ping
		}
	}

	// 3. 插件化渲染扩展 (api.ScoreboardRendering)
	// 允许插件拦截、接管或重写计分板标题、条目与页脚
	rawEntries := make([]any, len(entries))
	for i, e := range entries {
		rawEntries[i] = map[string]any{
			"name":     e.Name,
			"score":    e.Score,
			"icon":     e.Icon,
			"badge":    e.Badge,
			"color":    e.Color,
			"rank":     e.Rank,
			"progress": e.Progress,
		}
	}
	renderData := map[string]any{
		"objective": activeObjName,
		"title":     title,
		"entries":   rawEntries,
		"footer":    footerMap,
		"enabled":   true,
		"ping":      0,
		"online":    len(r.Peers),
	}
	if p != nil {
		renderData["ping"] = p.ping
	}
	ev := s.emit(api.ScoreboardRendering, r, p, renderData)
	if ev.Cancelled {
		return map[string]any{
			"t":       "scoreboard",
			"enabled": false,
		}
	}
	if en, ok := renderData["enabled"].(bool); ok && !en {
		return map[string]any{
			"t":       "scoreboard",
			"enabled": false,
		}
	}
	if customTitle, ok := renderData["title"].(string); ok && customTitle != "" {
		title = customTitle
	}
	if customObj, ok := renderData["objective"].(string); ok && customObj != "" {
		activeObjName = customObj
	}
	if customFooter, ok := renderData["footer"].(map[string]any); ok && customFooter != nil {
		footerMap = customFooter
	}
	if parsed := parseScoreEntries(renderData["entries"]); len(parsed) > 0 {
		entries = parsed
	}

	return map[string]any{
		"t":         "scoreboard",
		"enabled":   true,
		"objective": activeObjName,
		"title":     title,
		"slot":      "sidebar",
		"entries":   entries,
		"footer":    footerMap,
	}
}

func parseScoreEntries(val any) []ScoreEntry {
	if val == nil {
		return nil
	}
	var out []ScoreEntry
	switch list := val.(type) {
	case []ScoreEntry:
		return list
	case []any:
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, scoreEntryFromMap(m))
			}
		}
	case []map[string]any:
		for _, m := range list {
			out = append(out, scoreEntryFromMap(m))
		}
	}
	return out
}

func scoreEntryFromMap(m map[string]any) ScoreEntry {
	var e ScoreEntry
	if n, ok := m["name"].(string); ok {
		e.Name = n
	}
	if s, ok := m["score"].(float64); ok {
		e.Score = int(s)
	} else if s, ok := m["score"].(int); ok {
		e.Score = s
	}
	if ic, ok := m["icon"].(string); ok {
		e.Icon = ic
	}
	if bg, ok := m["badge"].(string); ok {
		e.Badge = bg
	}
	if c, ok := m["color"].(string); ok {
		e.Color = c
	}
	if rk, ok := m["rank"].(float64); ok {
		e.Rank = int(rk)
	} else if rk, ok := m["rank"].(int); ok {
		e.Rank = rk
	}
	if pr, ok := m["progress"].(float64); ok {
		e.Progress = pr
	}
	return e
}

// broadcastScoreboard 向房间内所有客户端同步计分板
func (s *Server) broadcastScoreboard(r *Room) {
	if r == nil {
		return
	}
	for p := range r.Peers {
		pkt := s.buildScoreboardPacket(r, p)
		if pkt != nil {
			p.json(pkt)
		}
	}
}

// syncScoreboardPeer 向单个 Peer 发送最新计分板
func (s *Server) syncScoreboardPeer(p *Peer) {
	if p == nil || p.room == nil {
		return
	}
	pkt := s.buildScoreboardPacket(p.room, p)
	if pkt != nil {
		p.json(pkt)
	}
}

// handleScoreboardCommand 处理 /scoreboard 指令
func (s *Server) handleScoreboardCommand(p *Peer, args []string) (bool, bool) {
	r := p.room
	if r == nil {
		return true, false
	}
	if r.scoreboard == nil {
		r.scoreboard = newScoreboard(r)
	}
	sc := r.scoreboard

	if len(args) == 0 {
		p.sys("用法：/scoreboard <objectives|players|toggle>")
		return true, false
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "toggle":
		sc.mu.Lock()
		sc.Enabled = !sc.Enabled
		status := "已开启"
		if !sc.Enabled {
			status = "已隐藏"
		}
		sc.mu.Unlock()
		s.broadcastScoreboard(r)
		p.sys(fmt.Sprintf("计分板显示状态：%s。", status))
		return true, true

	case "objectives":
		if len(args) < 2 {
			p.sys("用法：/scoreboard objectives <add|remove|list|setdisplay>")
			return true, false
		}
		act := strings.ToLower(args[1])
		switch act {
		case "list":
			sc.mu.RLock()
			var list []string
			for _, o := range sc.Objectives {
				list = append(list, fmt.Sprintf("%s (%s)", o.Name, o.DisplayName))
			}
			sc.mu.RUnlock()
			if len(list) == 0 {
				p.sys("当前世界上没有计分项目。")
			} else {
				p.sys("计分项目列表：" + strings.Join(list, "，"))
			}
			return true, true

		case "add":
			if !s.canGameCommand(p, "server.admin") {
				p.sys("创建计分板项目需要 OP 管理员权限。")
				return true, false
			}
			if len(args) < 3 {
				p.sys("用法：/scoreboard objectives add <名称> [准则类型] [显示名称]")
				return true, false
			}
			name := args[2]
			criteria := "dummy"
			if len(args) >= 4 {
				criteria = args[3]
			}
			displayName := name
			if len(args) >= 5 {
				displayName = strings.Join(args[4:], " ")
			}
			if !sc.AddObjective(name, criteria, displayName) {
				p.sys("创建计分项失败：名称不能为空。")
				return true, false
			}
			p.sys(fmt.Sprintf("已成功创建计分项目：%s [%s]。", name, displayName))
			s.broadcastScoreboard(r)
			return true, true

		case "remove":
			if !s.canGameCommand(p, "server.admin") {
				p.sys("移除计分板项目需要 OP 管理员权限。")
				return true, false
			}
			if len(args) < 3 {
				p.sys("用法：/scoreboard objectives remove <名称>")
				return true, false
			}
			if !sc.RemoveObjective(args[2]) {
				p.sys("未找到计分项目：" + args[2])
				return true, false
			}
			p.sys("已移除计分项目：" + args[2])
			s.broadcastScoreboard(r)
			return true, true

		case "setdisplay":
			if !s.canGameCommand(p, "server.admin") {
				p.sys("配置计分板显示槽位需要 OP 管理员权限。")
				return true, false
			}
			if len(args) < 3 {
				p.sys("用法：/scoreboard objectives setdisplay <sidebar|list|belowname> [项目名称]")
				return true, false
			}
			slot := strings.ToLower(args[2])
			objName := ""
			if len(args) >= 4 {
				objName = args[3]
			}
			if !sc.SetDisplay(slot, objName) {
				p.sys("设置显示槽位失败，请确认项目名称正确。")
				return true, false
			}
			if objName == "" {
				p.sys(fmt.Sprintf("已清除槽位 %s 的计分项目显示。", slot))
			} else {
				p.sys(fmt.Sprintf("已将槽位 %s 绑定至计分项目：%s。", slot, objName))
			}
			s.broadcastScoreboard(r)
			return true, true
		}

	case "players":
		if len(args) < 2 {
			p.sys("用法：/scoreboard players <set|add|remove|reset|list>")
			return true, false
		}
		act := strings.ToLower(args[1])
		switch act {
		case "list":
			target := ""
			if len(args) >= 3 {
				target = args[2]
			}
			sc.mu.RLock()
			defer sc.mu.RUnlock()
			if target == "" {
				var holders []string
				seen := make(map[string]bool)
				for _, scores := range sc.Scores {
					for h := range scores {
						if !seen[h] {
							seen[h] = true
							holders = append(holders, h)
						}
					}
				}
				p.sys("被计分玩家/项目：" + strings.Join(holders, "，"))
			} else {
				var details []string
				for o, scores := range sc.Scores {
					if val, ok := scores[target]; ok {
						details = append(details, fmt.Sprintf("%s: %d", o, val))
					}
				}
				p.sys(fmt.Sprintf("%s 的分数：%s", target, strings.Join(details, "，")))
			}
			return true, true

		case "set", "add", "remove":
			if !s.canGameCommand(p, "server.admin") {
				p.sys("修改分数需要 OP 管理员权限。")
				return true, false
			}
			if len(args) < 5 {
				p.sys(fmt.Sprintf("用法：/scoreboard players %s <玩家/词条名> <项目> <分数值>", act))
				return true, false
			}
			holder := args[2]
			objName := args[3]
			val, err := strconv.Atoi(args[4])
			if err != nil {
				p.sys("分数值必须为有效整数。")
				return true, false
			}

			if act == "set" {
				if !sc.SetScore(holder, objName, val) {
					p.sys("修改分数失败，项目不存在：" + objName)
					return true, false
				}
				p.sys(fmt.Sprintf("已将 %s 在 %s 的分数设置为 %d。", holder, objName, val))
			} else if act == "add" {
				newVal, ok := sc.AddScore(holder, objName, val)
				if !ok {
					p.sys("累加分数失败，项目不存在：" + objName)
					return true, false
				}
				p.sys(fmt.Sprintf("已为 %s 在 %s 增加 %d 分，当前分数：%d。", holder, objName, val, newVal))
			} else {
				newVal, ok := sc.AddScore(holder, objName, -val)
				if !ok {
					p.sys("扣减分数失败，项目不存在：" + objName)
					return true, false
				}
				p.sys(fmt.Sprintf("已为 %s 在 %s 扣除 %d 分，当前分数：%d。", holder, objName, val, newVal))
			}
			s.broadcastScoreboard(r)
			return true, true

		case "reset":
			if !s.canGameCommand(p, "server.admin") {
				p.sys("重置分数需要 OP 管理员权限。")
				return true, false
			}
			if len(args) < 3 {
				p.sys("用法：/scoreboard players reset <玩家/词条名> [项目]")
				return true, false
			}
			holder := args[2]
			objName := ""
			if len(args) >= 4 {
				objName = args[3]
			}
			sc.ResetScores(holder, objName)
			p.sys(fmt.Sprintf("已重置 %s 的计分板数据。", holder))
			s.broadcastScoreboard(r)
			return true, true
		}
	}

	p.sys("未知计分板指令，请输入 /scoreboard objectives 或 /scoreboard players。")
	return true, false
}
