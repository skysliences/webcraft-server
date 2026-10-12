package game

import (
	"fmt"
	"strings"

	api "mcraft/backend/pkg/plugin"
)

// NametagInfo 保存玩家头顶名牌与称号的运行时与持久化状态
type NametagInfo struct {
	Prefix         string `json:"prefix,omitempty"`         // 头顶名称前缀（如 §6[VIP]§r ）
	Suffix         string `json:"suffix,omitempty"`         // 头顶名称后缀（如  §a[Lv.99]§r）
	DisplayName    string `json:"displayName,omitempty"`    // 自定义显示名字
	NameColor      string `json:"nameColor,omitempty"`      // 名字颜色代码
	Title          string `json:"title,omitempty"`          // 顶部称号文本（如 ★ 屠龙勇士 ★）
	TitleColor     string `json:"titleColor,omitempty"`     // 称号文本颜色（如 #f1c40f 或 §6）
	TitleIcon      string `json:"titleIcon,omitempty"`      // 称号图片图标（内置如 crown, dragon, vip 等，或图片URL）
	TitleAnimation string `json:"titleAnimation,omitempty"` // 称号动效
}

// Clone 制作深拷贝副本
func (n *NametagInfo) Clone() *NametagInfo {
	if n == nil {
		return &NametagInfo{}
	}
	cp := *n
	return &cp
}

// getPlayerNametag 获取玩家当前的头顶名牌数据
func (s *Server) getPlayerNametag(p *Peer) *NametagInfo {
	if p == nil {
		return &NametagInfo{}
	}
	if p.nametag != nil {
		return p.nametag
	}
	// 从数据库读取持久化数据
	if s.economyDB != nil && p.sid != "" {
		if disk, err := s.economyDB.GetNametag(p.sid); err == nil && disk != nil {
			p.nametag = disk
			return p.nametag
		}
	}
	p.nametag = &NametagInfo{}
	return p.nametag
}

// renderPlayerNametag 触发生命周期渲染事件，允许插件拦截并修改前缀、后缀与图片称号
func (s *Server) renderPlayerNametag(p *Peer) *NametagInfo {
	info := s.getPlayerNametag(p)
	if p == nil || p.room == nil {
		return info
	}

	renderData := map[string]any{
		"prefix":         info.Prefix,
		"suffix":         info.Suffix,
		"displayName":    info.DisplayName,
		"nameColor":      info.NameColor,
		"title":          info.Title,
		"titleColor":     info.TitleColor,
		"titleIcon":      info.TitleIcon,
		"titleAnimation": info.TitleAnimation,
		"player":         p.name,
		"id":             p.id,
		"account":        p.sid,
		"isOp":           p.owner || s.isOperator(p.sid),
	}

	ev := s.emit(api.PlayerNametagRendering, p.room, p, renderData)
	if ev != nil && !ev.Cancelled {
		if prefix, ok := renderData["prefix"].(string); ok {
			info.Prefix = prefix
		}
		if suffix, ok := renderData["suffix"].(string); ok {
			info.Suffix = suffix
		}
		if displayName, ok := renderData["displayName"].(string); ok {
			info.DisplayName = displayName
		}
		if nameColor, ok := renderData["nameColor"].(string); ok {
			info.NameColor = nameColor
		}
		if title, ok := renderData["title"].(string); ok {
			info.Title = title
		}
		if titleColor, ok := renderData["titleColor"].(string); ok {
			info.TitleColor = titleColor
		}
		if titleIcon, ok := renderData["titleIcon"].(string); ok {
			info.TitleIcon = titleIcon
		}
		if titleAnim, ok := renderData["titleAnimation"].(string); ok {
			info.TitleAnimation = titleAnim
		}
	}

	return info
}

// setPlayerNametag 设置玩家头顶名牌与称号并同步广播
func (s *Server) setPlayerNametag(p *Peer, newInfo *NametagInfo, persist bool) {
	if p == nil {
		return
	}
	if newInfo == nil {
		newInfo = &NametagInfo{}
	}
	p.nametag = newInfo.Clone()

	if persist && s.economyDB != nil && p.sid != "" {
		_ = s.economyDB.SaveNametag(p.sid, p.name, p.nametag)
	}

	s.broadcastPlayerNametag(p)

	if p.room != nil {
		s.emit(api.PlayerNametagChanged, p.room, p, map[string]any{
			"prefix":         p.nametag.Prefix,
			"suffix":         p.nametag.Suffix,
			"displayName":    p.nametag.DisplayName,
			"nameColor":      p.nametag.NameColor,
			"title":          p.nametag.Title,
			"titleColor":     p.nametag.TitleColor,
			"titleIcon":      p.nametag.TitleIcon,
			"titleAnimation": p.nametag.TitleAnimation,
		})
	}
}

// broadcastPlayerNametag 向当前世界所有玩家广播该玩家的最新名牌与称号
func (s *Server) broadcastPlayerNametag(p *Peer) {
	if p == nil || p.room == nil {
		return
	}
	info := p.nametag
	if info == nil {
		info = &NametagInfo{}
	}
	msg := map[string]any{
		"t":              "nametag",
		"id":             p.id,
		"prefix":         info.Prefix,
		"suffix":         info.Suffix,
		"displayName":    info.DisplayName,
		"nameColor":      info.NameColor,
		"title":          info.Title,
		"titleColor":     info.TitleColor,
		"titleIcon":      info.TitleIcon,
		"titleAnimation": info.TitleAnimation,
	}
	broadcastWorld(p.room, msg, nil)
}

// handleNametagCommands 处理名牌与称号相关控制台指令
func (s *Server) handleNametagCommands(p *Peer, cmd string, parts []string) (bool, bool) {
	isOp := p == nil || (p != nil && (p.owner || s.isOperator(p.sid)))

	switch cmd {
	case "prefix":
		if len(parts) < 2 {
			if p != nil {
				p.sys("用法: /prefix [玩家] <前缀文本> （支持颜色代码如 §6[VIP]§r ）")
			}
			return true, false
		}
		target := p
		prefixText := ""
		if len(parts) == 2 {
			if target == nil {
				return true, false
			}
			prefixText = parts[1]
		} else {
			if !isOp {
				p.sys("只有管理员可以为其他玩家设置前缀。")
				return true, false
			}
			target = s.findPeerByName(parts[1])
			if target == nil {
				if p != nil {
					p.sys("目标玩家不在线。")
				}
				return true, false
			}
			prefixText = strings.Join(parts[2:], " ")
		}

		info := s.getPlayerNametag(target).Clone()
		info.Prefix = prefixText
		s.setPlayerNametag(target, info, true)
		if p != nil {
			p.sys(fmt.Sprintf("已成功为 %s 设置前缀：%s", target.name, prefixText))
		}
		return true, true

	case "suffix":
		if len(parts) < 2 {
			if p != nil {
				p.sys("用法: /suffix [玩家] <后缀文本> （支持颜色代码如 §a[Lv.99]§r ）")
			}
			return true, false
		}
		target := p
		suffixText := ""
		if len(parts) == 2 {
			if target == nil {
				return true, false
			}
			suffixText = parts[1]
		} else {
			if !isOp {
				p.sys("只有管理员可以为其他玩家设置后缀。")
				return true, false
			}
			target = s.findPeerByName(parts[1])
			if target == nil {
				if p != nil {
					p.sys("目标玩家不在线。")
				}
				return true, false
			}
			suffixText = strings.Join(parts[2:], " ")
		}

		info := s.getPlayerNametag(target).Clone()
		info.Suffix = suffixText
		s.setPlayerNametag(target, info, true)
		if p != nil {
			p.sys(fmt.Sprintf("已成功为 %s 设置后缀：%s", target.name, suffixText))
		}
		return true, true

	case "title":
		if len(parts) < 2 {
			if p != nil {
				p.sys("用法: /title [玩家] <图标> [称号文字] （图标如: crown, dragon, vip, sword, diamond 等或图片URL）")
			}
			return true, false
		}
		target := p
		icon := ""
		titleText := ""
		if len(parts) == 2 {
			// /title <icon>
			if target == nil {
				return true, false
			}
			icon = parts[1]
		} else if len(parts) == 3 && !isOp {
			// 普通玩家设置自己的 title: /title <icon> <text>
			icon = parts[1]
			titleText = parts[2]
		} else {
			// 检查第一个参数是否为在线玩家
			possibleTarget := s.findPeerByName(parts[1])
			if possibleTarget != nil && isOp {
				target = possibleTarget
				icon = parts[2]
				if len(parts) > 3 {
					titleText = strings.Join(parts[3:], " ")
				}
			} else {
				icon = parts[1]
				titleText = strings.Join(parts[2:], " ")
			}
		}

		info := s.getPlayerNametag(target).Clone()
		info.TitleIcon = icon
		info.Title = titleText
		s.setPlayerNametag(target, info, true)
		if p != nil {
			p.sys(fmt.Sprintf("已成功为 %s 设置图片称号 [图标: %s, 称号: %s]", target.name, icon, titleText))
		}
		return true, true

	case "nametag":
		if len(parts) < 2 {
			if p != nil {
				p.sys("用法: /nametag <clear|info> [玩家]")
			}
			return true, false
		}
		sub := strings.ToLower(parts[1])
		target := p
		if len(parts) >= 3 {
			if !isOp {
				p.sys("只有管理员可以查询或重置其他玩家的名牌。")
				return true, false
			}
			target = s.findPeerByName(parts[2])
			if target == nil {
				if p != nil {
					p.sys("目标玩家不在线。")
				}
				return true, false
			}
		}
		if target == nil {
			return true, false
		}

		if sub == "clear" || sub == "reset" {
			info := &NametagInfo{}
			s.setPlayerNametag(target, info, true)
			if p != nil {
				p.sys(fmt.Sprintf("已清空 %s 的头顶名牌与称号。", target.name))
			}
			return true, true
		} else if sub == "info" {
			info := s.getPlayerNametag(target)
			if p != nil {
				p.sys(fmt.Sprintf("玩家 %s 名牌信息: 前缀: [%s], 后缀: [%s], 称号图标: [%s], 称号: [%s]",
					target.name, info.Prefix, info.Suffix, info.TitleIcon, info.Title))
			}
			return true, true
		}
	}

	return false, false
}
