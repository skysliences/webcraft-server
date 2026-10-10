# 自定义物品、伤害与插件扩展

核心采用 `ItemStack + ItemMeta`：物品编号、数量、耐久与名称、Lore、属性和自定义数据一起保存。参考 Paper 的 [ItemStack 数据组件](https://docs.papermc.io/paper/dev/data-component-api/) 和 [Persistent Data Container](https://docs.papermc.io/paper/dev/pdc/) 的职责划分，本项目提供自己的 Go API，不兼容 Bukkit Java 插件或完整 Minecraft 组件格式。

## 可运行示例

```sh
npm run plugins:build
npm start
```

登录并进入自己创建的**生存世界**，聊天输入 `/itemdemo`。领取的赤月吸血剑基础攻击 7，主手属性增加 3；`[吸血]` Lore 开启示例插件的吸血逻辑，恢复量为实际扣血的 25%，过量攻击不会按标称伤害恢复。守望者胸甲装备到胸部后增加 4 点生命上限、2 点护甲；插件读取 `demo:fire_resistance` 自定义数据来取消熔岩伤害。生命上限提高不会凭空补血，正常恢复会填满新增生命。

选中物品后可用房主命令：

```text
/itemlore clear
/itemlore [吸血] 命中恢复生命
/itemattr attack_damage 4
/itemattr max_health 6 mainhand
/itemdata demo:lifesteal 0.5
```

`itemlore` 追加一行，`clear` 清空；`itemattr` 添加或替换 `demo:edit_<属性>` 修饰符，保留其他修饰符；`itemdata` 修改命名空间键，值支持 JSON 数字、布尔、对象和数组。背包提示显示自定义名称、Lore、属性和无法破坏状态；Lore 支持基础 `§` 颜色。示例代码位于 `examples/custom-items/main.go`。

Lore 默认只有显示意义。核心不解析“吸血”“攻击力”等文案；开发者在事件里读取 Lore 或 `CustomData` 实现效果。稳定的业务标识建议放在命名空间自定义数据中，避免改名、翻译或文本冲突改变效果。

## Go 物品 API

```go
weapon := &plugin.ItemStack{
    ID: 276, Count: 1,
    Meta: &plugin.ItemMeta{
        DisplayName: "我的剑",
        Lore: []string{"[吸血]", "插件自定义描述"},
        Attributes: []plugin.AttributeModifier{{
            ID: "myplugin:power", Attribute: plugin.AttackDamage,
            Amount: 5, Operation: plugin.Add, Slot: "mainhand",
        }},
        CustomData: map[string]any{"myplugin:lifesteal": 0.25},
        Unbreakable: true,
    },
}
c.GiveStack(e, weapon)
held := e.Player.Item(e.Player.Selected) // 完整、深拷贝快照
if held != nil {
    if held.Meta == nil { held.Meta = &plugin.ItemMeta{} }
    held.Meta.Lore = []string{"新的 Lore"}
    c.SetItemMeta(e, e.Player.Selected, held.Meta)
}
c.DropStack(e.World.ID, weapon, x, y, z)
```

现有 `GiveItem` 和 `DropItem` 继续创建普通物品。`SetItemMeta` 只改变名称、Lore、属性和自定义数据，不改变编号、数量和耐久；指定玩家背包槽 0–44，不能修改合成结果槽。动作延迟到当前游戏操作结束后执行，使用原始快照比较，物品移动、被消耗或耐久变化时放弃更新；插件应监听 `item.meta.changed` 确认提交。不同元数据不合并；拆分深拷贝元数据。移动、容器、地面掉落、多人拾取、游标、恢复队列和重启恢复均保留元数据。普通合成/熔炼消耗材料并生成配方产物，不自动继承材料的自定义效果。

属性当前支持 `minecraft:attack_damage`、`minecraft:attack_speed`、`minecraft:armor`、`minecraft:max_health`。它们在服务端用于玩家攻击、生物攻击、防御、冷却、回血、重生和上限约束。属性在相应装备栏生效，放在普通背包格内不生效。装备栏为 `mainhand`、`offhand`、`head`、`chest`、`legs`、`feet`、`armor`、`any`；同一属性下同 ID 修饰符只算一次。

运算依次为：基础值加所有 `add`；对这个结果加各 `multiply_base` 的倍数；再依次乘以每项 `1 + multiply_total`。属性在普通物品默认值上叠加。攻击伤害限制 0–1000、攻速 0.1–20 次/秒、护甲 0–30（现有减伤按最多 20 护甲计算）、生命上限 1–200。取下装备后生命超过新上限时截断；不会算作受伤，也不会产生吸血。`player.attributes.changed` 是已提交装备变化后的观察事件。

名称最多 80 字，Lore 最多 32 行、每行 160 字；属性最多 32 项、自定义键最多 64 个；整体元数据最多 8 KiB。属性 ID 和自定义键要求 `namespace:key`，修饰符数值必须有限且在 -1000–1000 内。当前不是完整 Minecraft 属性集，移动速度、击退抗性、NBT、富文本组件、附魔和装备模型自定义尚未接入。

## 伤害事件

玩家近战流程：`player.attack.before` → 目标的 `player.damage.before` 或 `entity.damage.before` → 致命时的死亡前事件 → 扣血 → 受伤/死亡完成事件 → 工具耐久事件 → `player.attacked`。任一攻击、伤害或死亡前事件取消，不扣血、不消耗攻击耐久、不发送攻击完成事件。生物攻击使用 `entity.attack.before` / `entity.attacked`，玩家快照为目标。插件 `DamagePlayer`、`DamageEntity` 与环境伤害走相同受伤/死亡入口。

每个攻击、伤害和死亡事件带 `e.Damage`：

- `Cause`：`player_attack`、`mob_attack`、`fall`、`lava`、`drowning`、`void`、`starvation`、`sunlight`、`plugin` 或 `environment`。
- `Attacker`、`Target`：类型、独立的玩家/实体 ID、名字或生物种类、坐标和操作前生命；环境没有攻击者。
- `Weapon`：攻击时的完整物品快照，包含元数据；攻击后的耐久变化不会改写这个快照。
- `RawAmount`：护甲处理前伤害；`Armor`：参与本次防御的护甲值；`Amount`：当前阶段伤害。
- `HealthBefore`、`HealthAfter`、`AppliedAmount`：操作前后生命和实际损失。完成事件才有真实 `AppliedAmount`，生命不足时小于伤害值。

修改伤害使用 `e.Data["amount"]`；`e.Number("amount")` 读取当前监听阶段数值。监听优先级间会更新 `e.Damage.Amount`。伤害限制 0–1000，0 表示不结算。取消致命伤害保留原生命；完成事件只在提交后发出。旧 `data.amount/reason/health/message` 和 `Player.Inventory [][]int` 仍保留，完整物品新增到 `Player.Items` / `CursorItem`；`e.Item("stack")` 可读取事件物品快照。

```go
p.On(plugin.PlayerDamaging, plugin.Subscription{IgnoreCancelled: true},
    func(c *plugin.Context, e *plugin.Event) error {
        if e.Damage != nil && e.Damage.Cause == "fall" {
            e.Data["amount"] = e.Number("amount") * 0.5
        }
        return nil
    })
p.On(plugin.PlayerAttacked, plugin.Subscription{},
    func(c *plugin.Context, e *plugin.Event) error {
        if e.Damage != nil && e.Damage.AppliedAmount > 0 {
            c.Heal(e, e.Damage.AppliedAmount * 0.25)
        }
        return nil
    })
```

回血使用 `player.heal.before`（可取消/修改 `amount`）和 `player.healed`；自然恢复与插件回血都会触发。工具耐久使用 `item.durability.before`（可取消/修改整数 `amount`）和 `item.durability.changed`，包括 `damageBefore`、`damageAfter`、`broken`；无法破坏物品不扣耐久。元数据使用 `item.meta.before` / `item.meta.changed`，带槽位与完整 `before/after`。核心和插件动作都触发相应事件，周期性造成伤害的插件应避免再次响应自身的 `source=plugin:<id>`，防止递归。

## 协议与存档

插件 SDK API 仍为 v1，增加字段不破坏已有字段。客户端物品元组扩展为 `[id,count,damage,null,meta]`；普通物品仍是原来三项。地面实体保留原二进制生成格式，通过紧随其后的 `item_meta` JSON 消息给视距内玩家同步元数据。拾取数量和移除继续使用原实体消息。

世界存档 v6 读取 v1–v5 普通物品；首次保存前保留 `.json.pre-item-meta-vN.bak`。无效元数据的存档停止加载并保留原文件。测试使用隔离世界与账号，不替换用户进度。

```sh
npm run backend:test
npm run items:verify
npm run plugins:smoke
```

多人同时修改源码时，可用 `node scripts/run-backend.mjs --no-build` 启动已经验证的 `backend/bin/mcraft`，前端仍用 `npm run start:frontend`。此方式不会编译新改动；完成并行开发后再用常规启动命令构建和验证合并后的源码。本次运行构建的验证记录、版本和数据备份路径见 `artifacts/items-validated-build.json`。
