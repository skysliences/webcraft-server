package game

import (
	"math"

	api "mcraft/backend/pkg/plugin"
)

// ---------------------------------------------------------------------------
// Enchanting table (port of src/client/js/rules/enchant.js) and the gameplay
// effects of enchantments: protection, sharpness, efficiency, silk touch,
// fortune, unbreaking.
// ---------------------------------------------------------------------------

type javaRandom struct{ s int64 }

const jrMul = 0x5deece66d
const jrMask = (1 << 48) - 1

func newJavaRandom(seed int64) *javaRandom { r := &javaRandom{}; r.setSeed(seed); return r }
func (r *javaRandom) setSeed(seed int64)   { r.s = (seed ^ jrMul) & jrMask }
func (r *javaRandom) next(bits uint) int32 {
	r.s = (r.s*jrMul + 0xb) & jrMask
	return int32(r.s >> (48 - bits))
}
func (r *javaRandom) nextInt(n int) int {
	if n <= 0 {
		return 0
	}
	if n&-n == n {
		return int((int64(n) * int64(r.next(31))) >> 31)
	}
	for {
		bits := int(r.next(31))
		val := bits % n
		if bits-val+(n-1) >= 0 && bits-val+(n-1) <= math.MaxInt32 {
			return val
		}
	}
}
func (r *javaRandom) nextFloat() float32 { return float32(r.next(24)) / 16777216 }

type enchDef struct {
	Key, Name   string
	Weight, Max int
	Type        string
	Min         func(l int) int
	MaxPower    func(l int) int
	Group       string
}

func lin(a, b int) func(int) int { return func(l int) int { return a + b*(l-1) } }
func plusN(minf func(int) int, n int) func(int) int {
	return func(l int) int { return minf(l) + n }
}
func sup50(l int) int           { return 51 + 10*l }
func once(n int) func(int) int  { return func(int) int { return n } }

var enchantments = func() []enchDef {
	d := func(key, name string, w, max int, typ string, min func(int) int, maxP func(int) int, group string) enchDef {
		return enchDef{key, name, w, max, typ, min, maxP, group}
	}
	return []enchDef{
		d("protection", "保护", 10, 4, "armor", lin(1, 11), plusN(lin(1, 11), 11), "prot"),
		d("fire_protection", "火焰保护", 5, 4, "armor", lin(10, 8), plusN(lin(10, 8), 8), "prot"),
		d("feather_falling", "摔落缓冲", 5, 4, "feet", lin(5, 6), plusN(lin(5, 6), 6), ""),
		d("blast_protection", "爆炸保护", 2, 4, "armor", lin(5, 8), plusN(lin(5, 8), 8), "prot"),
		d("projectile_protection", "弹射物保护", 5, 4, "armor", lin(3, 6), plusN(lin(3, 6), 6), "prot"),
		d("respiration", "水下呼吸", 2, 3, "head", lin(10, 10), plusN(lin(10, 10), 30), ""),
		d("aqua_affinity", "水下速掘", 2, 1, "head", once(1), once(41), ""),
		d("thorns", "荆棘", 1, 3, "chest", lin(10, 20), sup50, ""),
		d("sharpness", "锋利", 10, 5, "weapon", lin(1, 11), plusN(lin(1, 11), 20), "dmg"),
		d("smite", "亡灵杀手", 5, 5, "weapon", lin(5, 8), plusN(lin(5, 8), 20), "dmg"),
		d("bane_of_arthropods", "节肢杀手", 5, 5, "weapon", lin(5, 8), plusN(lin(5, 8), 20), "dmg"),
		d("knockback", "击退", 5, 2, "weapon", lin(5, 20), sup50, ""),
		d("fire_aspect", "火焰附加", 2, 2, "weapon", lin(10, 20), sup50, ""),
		d("looting", "抢夺", 2, 3, "weapon", lin(15, 9), sup50, ""),
		d("efficiency", "效率", 10, 5, "digger", lin(1, 10), sup50, ""),
		d("silk_touch", "精准采集", 1, 1, "digger", once(15), sup50, "silk"),
		d("unbreaking", "耐久", 5, 3, "breakable", lin(5, 8), sup50, ""),
		d("fortune", "时运", 2, 3, "digger", lin(15, 9), sup50, "silk"),
		d("power", "力量", 10, 5, "bow", lin(1, 10), plusN(lin(1, 10), 15), ""),
		d("punch", "冲击", 2, 2, "bow", lin(12, 20), plusN(lin(12, 20), 25), ""),
		d("flame", "火矢", 2, 1, "bow", once(20), once(50), ""),
		d("infinity", "无限", 1, 1, "bow", once(20), once(50), ""),
	}
}()

func enchFits(typ string, it Item) bool {
	switch typ {
	case "armor":
		return it.Armor != nil
	case "feet", "head", "chest":
		return it.Armor != nil && it.Armor.Slot == typ
	case "weapon":
		return it.Tool != nil && it.Tool.Type == "sword"
	case "digger":
		return it.Tool != nil && (it.Tool.Type == "pickaxe" || it.Tool.Type == "shovel" || it.Tool.Type == "axe")
	case "breakable":
		return it.Durability > 0
	case "bow":
		return it.Key == "bow"
	}
	return false
}

func (s *Server) canEnchant(v *Stack) bool {
	if v == nil || v.Count != 1 || len(v.Ench) > 0 {
		return false
	}
	it, ok := s.rules.byID[v.ID]
	return ok && it.Durability > 0 && it.Stack == 1
}

func tableLevel(rng *javaRandom, slot, power, ench int) int {
	if ench <= 0 {
		return 0
	}
	power = min(power, 15)
	j := rng.nextInt(8) + 1 + (power >> 1) + rng.nextInt(power+1)
	switch slot {
	case 0:
		return max(j/3, 1)
	case 1:
		return j*2/3 + 1
	}
	return max(j, power*2)
}

type enchPick struct {
	def   *enchDef
	level int
}

func enchCandidates(it Item, power int) []enchPick {
	var out []enchPick
	for i := range enchantments {
		e := &enchantments[i]
		if !enchFits(e.Type, it) {
			continue
		}
		for l := e.Max; l >= 1; l-- {
			if power >= e.Min(l) && power <= e.MaxPower(l) {
				out = append(out, enchPick{e, l})
				break
			}
		}
	}
	return out
}

func enchWeighted(rng *javaRandom, list []enchPick) enchPick {
	total := 0
	for _, x := range list {
		total += x.def.Weight
	}
	w := rng.nextInt(total)
	for _, x := range list {
		if w -= x.def.Weight; w < 0 {
			return x
		}
	}
	return list[0]
}

func (s *Server) enchantList(id, level int, rng *javaRandom) [][2]any {
	it := s.rules.byID[id]
	ench := it.Ench
	var out []enchPick
	if ench <= 0 {
		return nil
	}
	level = level + 1 + rng.nextInt((ench>>2)+1) + rng.nextInt((ench>>2)+1)
	f := (rng.nextFloat()+rng.nextFloat() - 1) * float32(0.15)
	level = max(1, int(math.Round(float64(float32(level)+float32(level)*f))))
	list := enchCandidates(it, level)
	if len(list) == 0 {
		return nil
	}
	out = append(out, enchWeighted(rng, list))
	for rng.nextInt(50) <= level {
		last := out[len(out)-1].def
		var keep []enchPick
		for _, x := range list {
			if x.def != last && !(last.Group != "" && last.Group == x.def.Group) {
				keep = append(keep, x)
			}
		}
		list = keep
		if len(list) == 0 {
			break
		}
		out = append(out, enchWeighted(rng, list))
		level /= 2
	}
	res := make([][2]any, len(out))
	for i, x := range out {
		res[i] = [2]any{x.def.Key, x.level}
	}
	return res
}

func (s *Server) tableOffers(v *Stack, power int, seed int64) ([3]int, [3]any) {
	var levels [3]int
	var hints [3]any
	if !s.canEnchant(v) {
		return levels, hints
	}
	ench := s.rules.byID[v.ID].Ench
	rng := newJavaRandom(seed)
	for i := 0; i < 3; i++ {
		levels[i] = tableLevel(rng, i, power, ench)
		if levels[i] < i+1 {
			levels[i] = 0
		}
	}
	for i := 0; i < 3; i++ {
		if levels[i] == 0 {
			continue
		}
		rng.setSeed(int64(int32(seed + int64(i))))
		list := s.enchantList(v.ID, levels[i], rng)
		if len(list) > 0 {
			hints[i] = list[rng.nextInt(len(list))]
		}
	}
	return levels, hints
}

func (s *Server) shelfPower(r *Room, x, y, z int) int {
	shelf := s.id("Bookshelf")
	get := func(a, b, c int) int { return s.block(r, a, b, c) }
	n := 0
	for k := -1; k <= 1; k++ {
		for l := -1; l <= 1; l++ {
			if (k == 0 && l == 0) || get(x+l, y, z+k) != 0 || get(x+l, y+1, z+k) != 0 {
				continue
			}
			for j := 0; j < 2; j++ {
				if get(x+l*2, y+j, z+k*2) == shelf {
					n++
				}
				if l != 0 && k != 0 {
					if get(x+l*2, y+j, z+k) == shelf {
						n++
					}
					if get(x+l, y+j, z+k*2) == shelf {
						n++
					}
				}
			}
		}
	}
	return n
}

func (s *Server) enchSeed(p *Peer) int64 {
	e := p.ext()
	if e.EnchSeed == 0 {
		e.EnchSeed = int64(int32(s.rng.Uint32()))
		if e.EnchSeed == 0 {
			e.EnchSeed = 1
		}
	}
	return e.EnchSeed
}

func (s *Server) sendEnchOffer(p *Peer) {
	if p.window == nil || p.kind() != "ench" {
		return
	}
	x, y, z := coords(p.window.Key)
	levels, hints := s.tableOffers(p.window.Slots[0], s.shelfPower(p.room, x, y, z), s.enchSeed(p))
	p.json(map[string]any{"t": "eoffer", "w": p.window.ID, "l": levels, "h": hints})
}

func (s *Server) enchantClick(p *Peer, m Message) {
	if p.window == nil || p.kind() != "ench" || m.W != p.window.ID || m.I < 0 || m.I > 2 {
		return
	}
	item, lapis := p.window.Slots[0], p.window.Slots[1]
	x, y, z := coords(p.window.Key)
	seed := s.enchSeed(p)
	levels, _ := s.tableOffers(item, s.shelfPower(p.room, x, y, z), seed)
	cost, level := m.I+1, levels[m.I]
	creative := s.isCreative(p)
	nl := 0
	if lapis != nil && lapis.ID == lapisID {
		nl = lapis.Count
	}
	e := p.ext()
	if level <= 0 || !creative && (nl < cost || e.XPLevel < level) {
		s.sendEnchOffer(p)
		return
	}
	list := s.enchantList(item.ID, level, newJavaRandom(int64(int32(seed+int64(m.I)))))
	if len(list) == 0 {
		return
	}
	out := copyStack(item)
	out.Ench = map[string]int{}
	for _, kv := range list {
		out.Ench[kv[0].(string)] = kv[1].(int)
	}
	data := map[string]any{"item": itemSnapshot(item), "result": itemSnapshot(out), "cost": cost, "level": level}
	data["reason"]="enchant";data["x"],data["y"],data["z"]=x,y,z;data["lapis"]=itemSnapshot(lapis)
	if !s.allowed(api.ItemEnchanting,p.room,p,data){return}
	var xp *experienceChange;if !creative {xp=s.prepareExperience(p,cost,true,"enchant_cost");if xp==nil{return}}
	if !s.allowed(api.InventoryChanging, p.room, p, data) {
		return
	}
	p.window.Slots[0] = out
	if !creative {
		p.window.Slots[1] = sized(lapis, nl-cost)
		s.commitExperience(p,xp)
	}
	e.EnchSeed = int64(int32(s.rng.Uint32())) | 1
	p.room.Dirty = true
	s.emit(api.ItemEnchanted,p.room,p,data);s.emit(api.InventoryChanged,p.room,p,data)
	soundAt(p.room, "block.enchantment_table.use", float64(x)+.5, float64(y)+.5, float64(z)+.5, nil)
	s.inventory(p, 0, 0)
	s.sendEnchOffer(p)
}

// ---------------------------------------------------------------------------
// Enchantment effects
// ---------------------------------------------------------------------------

func enchOf(v *Stack, key string) int {
	if v == nil || v.Ench == nil {
		return 0
	}
	return v.Ench[key]
}

func (s *Server) armorEnch(p *Peer, key string) int {
	n := 0
	if p.state.Inv == nil {
		return 0
	}
	for _, v := range p.state.Inv.Slots[36:40] {
		n += enchOf(v, key)
	}
	return n
}

// protectionScale reduces damage by armor enchantments (EPF, max 20 → 80%).
func (s *Server) protectionScale(p *Peer, amount float64, ctx *api.DamageContext) float64 {
	if p.state.Inv == nil || ctx == nil {
		return amount
	}
	cause := ctx.Cause
	if cause == "void" || cause == "starvation" {
		return amount
	}
	epf := 0
	for _, v := range p.state.Inv.Slots[36:40] {
		if v == nil || v.Ench == nil {
			continue
		}
		epf += v.Ench["protection"]
		switch cause {
		case "fire", "lava", "burn":
			epf += 2 * v.Ench["fire_protection"]
		case "fall":
			epf += 3 * v.Ench["feather_falling"]
		case "explosion":
			epf += 2 * v.Ench["blast_protection"]
		case "projectile":
			epf += 2 * v.Ench["projectile_protection"]
		}
	}
	if resist := s.effectAmp(p, "resistance"); resist >= 0 {
		amount *= math.Max(0, 1-.2*float64(resist+1))
	}
	return amount * (1 - float64(min(epf, 20))/25)
}

func (s *Server) attackBonusExt(p *Peer) float64 {
	b := 0.
	if l := enchOf(s.heldItem(p), "sharpness"); l > 0 {
		b += .5*float64(l) + .5
	}
	if a := s.effectAmp(p, "strength"); a >= 0 {
		b += 3 * float64(a+1)
	}
	if a := s.effectAmp(p, "weakness"); a >= 0 {
		b -= 4
	}
	return b
}

// digScale multiplies the expected mining time.
func (s *Server) digScale(p *Peer, b int) float64 {
	if s.isCreative(p) {
		return 0
	}
	f := 1.
	v, it := s.heldStack(p)
	if l := enchOf(v, "efficiency"); l > 0 && it.Tool != nil {
		speed := math.Max(1, it.Tool.Speed)
		if s.rules.mine(b, v.ID).Ticks < s.rules.mine(b, 0).Ticks {
			f *= speed / (speed + float64(l*l+1))
		}
	}
	if a := s.effectAmp(p, "haste"); a >= 0 {
		f /= 1 + .2*float64(a+1)
	}
	return f
}

var silkBlocks = map[string]bool{"Glass": true, "Grass Block": true, "Coal Ore": true, "Diamond Ore": true, "Lapis Lazuli Ore": true, "Spark Ore": true, "Under Quartz Ore": true, "Oak Leaves": true, "Birch Leaves": true, "Spruce Leaves": true, "Stone": true, "Bookshelf": true, "Snow Block": true, "Clay": true, "Melon": true, "Ender Chest": true, "Iron Ore": true, "Gold Ore": true}

var fortuneBlocks = map[string]bool{"Coal Ore": true, "Diamond Ore": true, "Lapis Lazuli Ore": true, "Spark Ore": true, "Under Quartz Ore": true, "Iron Ore": true, "Gold Ore": true}

func (s *Server) enchDrops(p *Peer, b int, drops Stacks) Stacks {
	if s.isCreative(p) {
		return nil
	}
	held := s.heldItem(p)
	name := s.nameOf(b)
	if enchOf(held, "silk_touch") > 0 && silkBlocks[name] && s.rules.mine(b, held.ID).Ticks >= 0 {
		if id, ok := s.rules.byName[name]; ok && s.rules.byID[id].ID == id && s.rules.byID[id].Stack > 0 {
			return Stacks{stack(id, 1, 0)}
		}
	}
	if l := enchOf(held, "fortune"); l > 0 && fortuneBlocks[name] {
		mult := max(1, s.rng.Intn(l+2))
		for _, d := range drops {
			if d != nil {
				d.Count = min(d.Count*mult, s.rules.max(d.ID))
			}
		}
	}
	return drops
}

func (s *Server) unbreakingWear(p *Peer, v *Stack, amount int) int {
	if s.isCreative(p) {
		return 0
	}
	l := enchOf(v, "unbreaking")
	if l <= 0 {
		return amount
	}
	n := 0
	for i := 0; i < amount; i++ {
		if s.rng.Intn(l+1) == 0 {
			n++
		}
	}
	return n
}

func init() {
	onMessage("ench", func(s *Server, p *Peer, m Message) bool {
		s.enchantClick(p, m)
		return true
	})
}
