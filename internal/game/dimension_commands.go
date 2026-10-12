package game

import (
	"fmt"
	"strings"
)

func commandDimension(name string) string {
	switch strings.TrimPrefix(strings.ToLower(name), "minecraft:") {
	case "overworld", "normal", "world", "主世界", "普通世界":
		return "overworld"
	case "nether", "the_nether", "下界", "地狱":
		return "nether"
	case "end", "the_end", "末地":
		return "end"
	}
	return ""
}

func dimensionLabel(dim string) string {
	return map[string]string{"overworld": "主世界", "nether": "下界", "end": "末地"}[dim]
}

// Search actual blocks, including player edits, without clearing existing builds
// or constructing a portal. Nether landings stay below the bedrock ceiling.
func (s *Server) dimensionCommandSpawn(r *Room) ([3]float64, bool) {
	sp := s.spawnPoint(r)
	maxY := 125
	maxRadius := 8
	if r.dimName() == "nether" {
		maxY = 120
		maxRadius = 64
	}
	preferredY := min(max(sp[1], 1), maxY)
	for radius := 0; radius <= maxRadius; radius++ {
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if max(abs(dx), abs(dz)) != radius {
					continue
				}
				x, z := sp[0]+dx, sp[2]+dz
				if !r.inside(x, z) {
					continue
				}
				for distance := 0; distance < maxY; distance++ {
					for _, y := range []int{preferredY + distance, preferredY - distance} {
						if y < 1 || y > maxY || s.block(r, x, y, z) != 0 || s.block(r, x, y+1, z) != 0 {
							continue
						}
						floor := s.block(r, x, y-1, z)
						if s.kind(floor) != 1 || s.nameOf(floor) == "Magma Block" || s.nameOf(floor) == "Cactus" {
							continue
						}
						return [3]float64{float64(x) + .5, float64(y), float64(z) + .5}, true
					}
				}
			}
		}
	}
	return [3]float64{}, false
}

// Commands and plugin actions share landing validation and the protected
// dimension/teleport/transport departure transaction.
func (s *Server) changePlayerDimension(actor, p *Peer, dimension, reason string) bool {
	dim := commandDimension(dimension)
	if dim == "" || p == nil || p.closed || p.room == nil || p.state == nil || p.state.Dead {
		return false
	}
	if !p.room.usesSurvival() {
		p.sys("旧版有限创造世界不支持维度切换，请在生存世界或无限世界使用此命令。")
		return false
	}
	target := s.dimRoom(p.room, dim)
	pos, ok := s.dimensionCommandSpawn(target)
	if !ok {
		p.sys("目标维度出生点附近没有安全落脚位置，请先整理出生区域。")
		return false
	}
	if !s.moveToDimensionWithContext(p, target, pos[0], pos[1], pos[2], true, actor, reason) {
		return false
	}
	e := p.ext()
	e.portalTicks, e.PortalCool = 0, 20
	if e.EndCredits {
		e.EndCredits = false
		p.json(map[string]any{"t": "credits", "on": 0})
	}
	p.json(map[string]any{"t": "portal", "n": 0})
	return true
}

func (s *Server) dimensionCommand(p *Peer, args []string) bool {
	if !s.canGameCommand(p, "server.admin") {
		p.sys("执行此命令需要管理员 (OP) 权限。")
		return false
	}
	if len(args) < 1 || len(args) > 2 || commandDimension(args[0]) == "" {
		p.sys("用法：/dimension <overworld|nether|end> [玩家]；也支持主世界、地狱、末地。")
		return false
	}
	target := p
	if len(args) == 2 && args[1] != "@s" {
		target = s.findPlayer(p.room, args[1])
		if target == nil {
			p.sys("找不到该玩家。")
			return false
		}
	}
	if !s.changePlayerDimension(p, target, args[0], s.commandReason()) {
		p.sys("传送未完成：玩家死亡、目标不可用或操作被插件阻止。")
		return false
	}
	p.sys(fmt.Sprintf("已将 %s 传送到%s。", target.name, dimensionLabel(target.room.dimName())))
	return true
}
