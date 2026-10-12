package game

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type commandDef struct {
	Name        string
	Aliases     []string
	Usage       string
	Description string
	NeedOP      bool
	Survival    bool
}

var commandCatalog = []commandDef{
	{Name: "help", Aliases: []string{"?"}, Usage: "/help [命令]", Description: "查看帮助手册或具体命令用法", NeedOP: false},
	{Name: "gamemode", Aliases: []string{"gm"}, Usage: "/gamemode <survival|creative|adventure|spectator> [玩家]", Description: "切换自己或指定玩家的游戏模式", NeedOP: true, Survival: true},
	{Name: "tp", Aliases: []string{"teleport"}, Usage: "/tp <玩家> 或 /tp [玩家] <目标玩家|x y z>", Description: "传送实体或传送到指定坐标", NeedOP: true},
	{Name: "dimension", Aliases: []string{"dim"}, Usage: "/dimension <overworld|nether|end> [玩家]", Description: "传送到主世界、下界（地狱）或末地的安全出生位置，支持中文维度名", NeedOP: true},
	{Name: "time", Usage: "/time <set|add|query> <day|night|noon|midnight|数值>", Description: "查询或设置世界昼夜时间", NeedOP: true},
	{Name: "weather", Usage: "/weather <clear|rain|thunder> [秒数]", Description: "更改当前世界天气", NeedOP: true, Survival: true},
	{Name: "give", Usage: "/give [玩家] <物品名称或ID> [数量]", Description: "给予自己或指定玩家物品", NeedOP: true, Survival: true},
	{Name: "clear", Usage: "/clear [玩家] [物品] [数量]", Description: "清空背包或清理指定物品", NeedOP: true, Survival: true},
	{Name: "heal", Usage: "/heal [玩家]", Description: "瞬间恢复生命、饱腹度并消除有害负面效果", NeedOP: true, Survival: true},
	{Name: "feed", Usage: "/feed [玩家]", Description: "回满饥饿度与饱和度", NeedOP: true, Survival: true},
	{Name: "kill", Usage: "/kill [玩家]", Description: "消灭指定实体或玩家自尽", NeedOP: false, Survival: true},
	{Name: "xp", Aliases: []string{"experience"}, Usage: "/xp <数量>[L] [玩家]", Description: "给予经验值或经验等级", NeedOP: true, Survival: true},
	{Name: "effect", Usage: "/effect <give|clear> [玩家] [效果] [秒数] [等级]", Description: "赋予或清除药水状态效果", NeedOP: true, Survival: true},
	{Name: "summon", Usage: "/summon <生物类型> [x y z]", Description: "在指定位置召唤生物实体", NeedOP: true, Survival: true},
	{Name: "spawn", Usage: "/spawn", Description: "传送回到当前世界出生点", NeedOP: false},
	{Name: "say", Usage: "/say <消息内容>", Description: "以管理员/服务器广播身份向全服发送公告", NeedOP: true},
	{Name: "op", Usage: "/op <玩家名字或账号ID>", Description: "将玩家提升为管理员 (OP)", NeedOP: true},
	{Name: "deop", Usage: "/deop <玩家名字或账号ID>", Description: "撤销玩家的管理员权限", NeedOP: true},
	{Name: "difficulty", Usage: "/difficulty [peaceful|easy|normal|hard]", Description: "查看或切换世界难度", NeedOP: false, Survival: true},
	{Name: "gamerule", Usage: "/gamerule [规则] [true|false]", Description: "查看或修改世界游戏规则", NeedOP: false, Survival: true},
	{Name: "seed", Usage: "/seed", Description: "查询当前世界的数字种子", NeedOP: false},
	{Name: "worldinfo", Usage: "/worldinfo", Description: "查询当前世界维度、尺寸与运行状态", NeedOP: false},
	{Name: "scoreboard", Usage: "/scoreboard <objectives|players|toggle> ...", Description: "管理计分板项目、显示槽位及玩家分数", NeedOP: false},
	{Name: "anticheat", Usage: "/anticheat [status|reload|toggle|on|off]", Description: "查看或动态调整反作弊开关与参数", NeedOP: true},
	{Name: "balance", Aliases: []string{"bal", "money"}, Usage: "/balance [玩家名]", Description: "查询自己或指定玩家的金币余额", NeedOP: false},
	{Name: "pay", Usage: "/pay <玩家名或ID> <金额>", Description: "向指定玩家转账金币", NeedOP: false},
	{Name: "eco", Usage: "/eco <give|take|set|bal> <玩家> [金额]", Description: "管理员经济管理（加钱/扣钱/设余额）", NeedOP: true},
	{Name: "trade", Usage: "/trade <玩家名或ID> 或 /trade <accept|deny|cancel>", Description: "发起或响应面对面安全交易面板", NeedOP: false},
	{Name: "prefix", Usage: "/prefix [玩家] <前缀文本>", Description: "设置玩家头顶名称前缀（支持颜色代码）", NeedOP: false},
	{Name: "suffix", Usage: "/suffix [玩家] <后缀文本>", Description: "设置玩家头顶名称后缀（支持颜色代码）", NeedOP: false},
	{Name: "title", Usage: "/title [玩家] <图标> [称号文字]", Description: "设置玩家头顶图片称号（支持内置图标或图片URL及文字）", NeedOP: false},
	{Name: "nametag", Usage: "/nametag <clear|info> [玩家]", Description: "查询或重置玩家头顶名牌与称号", NeedOP: false},
}

// nearestPlayer returns the closest player to p (or p itself if alone).
func (s *Server) nearestPlayer(p *Peer) *Peer {
	if p == nil || p.room == nil {
		return p
	}
	peers := worldPeers(p.room)
	if len(peers) <= 1 {
		return p
	}
	var best *Peer
	bestDist := math.MaxFloat64
	for _, o := range peers {
		if o == p {
			continue
		}
		dist := math.Hypot(o.state.X-p.state.X, o.state.Z-p.state.Z)
		if dist < bestDist {
			bestDist = dist
			best = o
		}
	}
	if best != nil {
		return best
	}
	return p
}

// handleFullCommand provides a comprehensive, Spigot-grade command dispatcher.
func (s *Server) handleFullCommand(p *Peer, name string, parts []string) (handled bool, success bool) {
	r := p.room
	args := parts[1:]
	isOp := s.canGameCommand(p, "server.admin")

	needOwner := func() bool {
		if !isOp {
			p.sys("执行此命令需要管理员 (OP) 权限。")
			return false
		}
		return true
	}
	survivalOnly := func() bool {
		if !r.usesSurvival() {
			p.sys("此命令仅在生存模式世界中有效。")
			return false
		}
		return true
	}

	switch strings.ToLower(name) {
	case "dimension", "dim":
		return true, s.dimensionCommand(p, args)
	case "help", "?":
		if len(args) == 0 {
			// 列出所有可用命令
			var userCmds, opCmds []string
			for _, def := range commandCatalog {
				if def.NeedOP {
					opCmds = append(opCmds, "/"+def.Name)
				} else {
					userCmds = append(userCmds, "/"+def.Name)
				}
			}
			p.sys("=== WebCraft 帮助手册 ===")
			p.sys("常用指令：" + strings.Join(userCmds, "  "))
			if isOp {
				p.sys("管理指令：" + strings.Join(opCmds, "  "))
			}
			p.sys("输入 /help <命令名> 查看特定命令的详细语法。")
			return true, true
		}
		target := strings.TrimPrefix(strings.ToLower(args[0]), "/")
		for _, def := range commandCatalog {
			match := def.Name == target
			if !match {
				for _, a := range def.Aliases {
					if a == target {
						match = true
						break
					}
				}
			}
			if match {
				p.sys(fmt.Sprintf("[%s] %s", def.Name, def.Description))
				p.sys("语法：" + def.Usage)
				if len(def.Aliases) > 0 {
					p.sys("别名：/" + strings.Join(def.Aliases, ", /"))
				}
				if def.NeedOP {
					p.sys("权限：需要 OP 管理员权限")
				} else {
					p.sys("权限：所有玩家均可使用")
				}
				return true, true
			}
		}
		p.sys("未找到该命令的帮助信息：" + target)
		return true, false

	case "time":
		if len(args) < 1 {
			p.sys(fmt.Sprintf("当前世界时间：%d 刻。用法：/time set <day|night|noon|midnight|数值> 或 /time query 或 /time add <数值>", r.root().Time%24000))
			return true, true
		}
		sub := strings.ToLower(args[0])
		switch sub {
		case "query":
			cur := r.root().Time
			p.sys(fmt.Sprintf("世界总运行时间：%d 刻，当日时刻：%d 刻。", cur, cur%24000))
			return true, true
		case "set":
			if !needOwner() {
				return true, false
			}
			if len(args) < 2 {
				p.sys("用法：/time set <day|night|noon|midnight|数值>")
				return true, false
			}
			targetTime := uint64(0)
			switch strings.ToLower(args[1]) {
			case "day":
				targetTime = 1000
			case "noon":
				targetTime = 6000
			case "night":
				targetTime = 13000
			case "midnight":
				targetTime = 18000
			default:
				n, err := strconv.ParseUint(args[1], 10, 64)
				if err != nil {
					p.sys("时间格式错误，必须为数字或 day|night|noon|midnight。")
					return true, false
				}
				targetTime = n % 24000
			}
			root := r.root()
			days := root.Time / 24000
			root.Time = days*24000 + targetTime
			root.Dirty = true
			for peer := range root.Peers {
				peer.json(map[string]any{"t": "time", "time": root.Time})
			}
			p.sys(fmt.Sprintf("已将世界时间设置为 %d 刻。", targetTime))
			return true, true
		case "add":
			if !needOwner() {
				return true, false
			}
			if len(args) < 2 {
				p.sys("用法：/time add <刻数>")
				return true, false
			}
			n, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				p.sys("时间增量必须为有效数字。")
				return true, false
			}
			root := r.root()
			root.Time += n
			root.Dirty = true
			for peer := range root.Peers {
				peer.json(map[string]any{"t": "time", "time": root.Time})
			}
			p.sys(fmt.Sprintf("已向时间推进 %d 刻，当前为 %d 刻。", n, root.Time%24000))
			return true, true
		default:
			p.sys("用法：/time set|add|query")
			return true, false
		}

	case "clear":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		targets := []*Peer{p}
		argStart := 0
		if len(args) > 0 && (strings.HasPrefix(args[0], "@") || s.findPlayer(r, args[0]) != nil) {
			targets = s.targets(p, args[0])
			argStart = 1
		}
		if len(targets) == 0 {
			p.sys("找不到匹配的玩家。")
			return true, false
		}
		filterID := 0
		if len(args) > argStart {
			filterID = s.itemByCommandName(args[argStart])
			if filterID <= 0 {
				p.sys("未知物品：" + args[argStart])
				return true, false
			}
		}
		clearedTotal, ok := s.clearCommandInventories(p, targets, filterID)
		if !ok {
			commandRestoreFailure(p, "clear")
			return true, false
		}
		if filterID > 0 {
			p.sys(fmt.Sprintf("已从 %d 位玩家背包中清除了 %d 个指定物品。", len(targets), clearedTotal))
		} else {
			p.sys(fmt.Sprintf("已清空 %d 位玩家的背包物品。", len(targets)))
		}
		return true, true

	case "heal":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		targets := []*Peer{p}
		if len(args) > 0 {
			targets = s.targets(p, args[0])
		}
		if len(targets) == 0 {
			p.sys("未找到目标玩家。")
			return true, false
		}
		for _, t := range targets {
			if t.state == nil || t.state.Dead {
				continue
			}
			if !s.restoreCommandPlayer(p, t, true) {
				commandRestoreFailure(p, "heal")
				return true, false
			}
		}
		p.sys(fmt.Sprintf("已为 %d 位玩家恢复满状态。", len(targets)))
		return true, true

	case "feed":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		targets := []*Peer{p}
		if len(args) > 0 {
			targets = s.targets(p, args[0])
		}
		if len(targets) == 0 {
			p.sys("未找到目标玩家。")
			return true, false
		}
		for _, t := range targets {
			if t.state == nil || t.state.Dead {
				continue
			}
			if !s.restoreCommandPlayer(p, t, false) {
				commandRestoreFailure(p, "feed")
				return true, false
			}
		}
		p.sys(fmt.Sprintf("已为 %d 位玩家回满饱腹度。", len(targets)))
		return true, true

	case "say":
		if !needOwner() {
			return true, false
		}
		if len(args) == 0 {
			p.sys("用法：/say <消息内容>")
			return true, false
		}
		text := strings.Join(args, " ")
		broadcastWorld(r, map[string]string{"t": "sys", "m": fmt.Sprintf("[服务器] %s", text)}, nil)
		return true, true

	case "spawn":
		if !survivalOnly() {
			return true, false
		}
		spawn := s.spawnPoint(r)
		if !s.teleport(p, float64(spawn[0])+.5, float64(spawn[1]), float64(spawn[2])+.5, "command") {
			return true, false
		}
		p.sys("已传送回世界出生点。")
		return true, true

	case "op", "deop":
		if !needOwner() {
			return true, false
		}
		if len(args) != 1 {
			p.sys(fmt.Sprintf("用法：/%s <玩家名字或账号ID>", name))
			return true, false
		}
		u := s.accountByName(args[0])
		if u == nil {
			for peer := range r.Peers {
				if strings.EqualFold(peer.name, args[0]) || peer.sid == args[0] {
					u = &account{ID: peer.sid, Name: peer.name}
					break
				}
			}
		}
		if u == nil && len(args[0]) >= 12 && sidRE.MatchString(args[0]) {
			u = &account{ID: args[0]}
		}
		if u == nil {
			p.sys("找不到该账号或玩家，请确认名称或 ID 正确。")
			return true, false
		}
		next := s.copySettings()
		grant := name == "op"
		if grant {
			next.Operators[u.ID] = true
		} else {
			delete(next.Operators, u.ID)
		}
		if err := s.updateSettings(next, s.commandActor(p), name); err != nil {
			p.sys("更新权限失败：" + err.Error())
			return true, false
		}
		actionText := "设为管理员 (OP)"
		if !grant {
			actionText = "撤销管理员权限"
		}
		displayName := u.Name
		if displayName == "" {
			displayName = u.ID
		}
		p.sys(fmt.Sprintf("已成功对 %s %s。", displayName, actionText))
		// 刷新在线玩家权限
		for peer := range r.Peers {
			if peer.sid == u.ID {
				s.sendPermissions(peer)
			}
		}
		return true, true

	case "summon":
		if !needOwner() || !survivalOnly() {
			return true, false
		}
		if len(args) < 1 {
			p.sys("用法：/summon <生物类型> [x] [y] [z]")
			return true, false
		}
		typ := strings.ToLower(args[0])
		x, y, z := p.state.X, p.state.Y, p.state.Z
		if len(args) >= 4 {
			var ok1, ok2, ok3 bool
			x, ok1 = parseCoord(args[1], p.state.X)
			y, ok2 = parseCoord(args[2], p.state.Y)
			z, ok3 = parseCoord(args[3], p.state.Z)
			if !ok1 || !ok2 || !ok3 {
				p.sys("坐标格式错误。")
				return true, false
			}
		}
		if _, err := s.spawnMob(p.room, p, typ, x, y, z, "command"); err != nil {
			p.sys("生成失败：" + err.Error())
			return true, false
		}
		p.sys(fmt.Sprintf("已在 (%.1f, %.1f, %.1f) 召唤生物：%s", x, y, z, typ))
		return true, true

	case "scoreboard":
		return s.handleScoreboardCommand(p, args)

	case "anticheat":
		return s.handleAnticheatCommand(p, args)

	case "balance", "bal", "money", "pay", "eco":
		return s.handleEconomyCommands(p, strings.ToLower(name), parts)

	case "trade":
		return s.handleTradeCommand(p, parts)

	case "prefix", "suffix", "title", "nametag":
		return s.handleNametagCommands(p, strings.ToLower(name), parts)
	}

	return false, false
}
