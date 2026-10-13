# 自然生物生成规则

本实现以 Spigot/CraftBukkit **26.3** 为参考。核对的官方仓库版本：CraftBukkit `c4db4c3`，Spigot `bed90fc`；上游 Mojang 26.3 服务端 SHA-1 `33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c`。生物群系选种数据来自官方服务端 `minecraft:gameplay/natural_mob_spawns`，见 `internal/game/spigot_spawn_tables.json`。

参考源码：

- [CraftBukkit NaturalSpawner 补丁](https://hub.spigotmc.org/stash/projects/SPIGOT/repos/craftbukkit/browse/nms-patches/net/minecraft/world/level/NaturalSpawner.patch)：分类限制、各分类间隔、可取消生成事件以及取消后不增加数量。
- [Spigot 自然生成范围补丁](https://hub.spigotmc.org/stash/projects/SPIGOT/repos/spigot/browse/CraftBukkit-Patches/0009-Fix-Mob-Spawning-Relative-to-View-Distance.patch)：区块中心与玩家的距离、配置范围和视距。
- [Spigot 官方配置说明](https://www.spigotmc.org/wiki/spigot-configuration/)及 CraftBukkit `src/main/resources/configurations/bukkit.yml`：默认基准怪物 70、动物 10、范围 6、间隔 1/400 ticks。
- [Mojang 官方 26.3 版本元数据](https://piston-meta.mojang.com/v1/packages/702fe59163c6ee6578607daa85811d9bc9c7cc40/26.3.json)：上游 Monster、NaturalSpawner、SpawnPlacements、Mob 和维度/生物群系数据。

## 白天与光照

Spigot 并不是“白天禁止一切怪物”。**晴天露天不应自然刷新普通敌对生物；白天足够黑暗的洞穴、封闭房间可以刷新。** 夜里刷出的怪物、从洞穴走出的怪物、命令、生成蛋与刷怪笼产生的怪物，不是新一次露天自然生成。

光照由 Go 服务端计算，与网页亮度、Gamma 设置无关。透明玻璃不会遮断天光；树叶、水等按光照阻力衰减；天光会从开口侧面传播。所有已提交地形里的光源都会参与检查，包含原始地形、玩家编辑、插件模型光源以及红石灯的当前供电状态。未加载区块不会因为生成或光照查询被强制加载。

主世界普通怪物的规则按上游分成三步：

1. 原始天光不能大于本次随机的 `0..31`。
2. 方块光必须为 **0**。
3. 经太阳角度、雨天/雷暴处理后的局部光，不能大于本次随机的 `0..7`。雷暴采用上游的天光扣减 10，因此雷暴白天刷怪可以是正常行为。

下界普通怪物使用维度方块光阈值 15、局部光阈值 7；烈焰人使用 SpawnPlacements 注册的任意光照条件，但必须来自有效要塞选种表。末地怪物使用方块光 0、局部光上限 15。普通动物需对应生物群系的选种表、草地以及至少 9 级光；狼按上游独立地面标签，还可使用已实现的雪块与砂土；并非所有生物群系的草地都自然刷新牛羊。

实体脚部、头部和完整身体必须有空间，液体、玻璃实体碰撞、不可生成地面和其他实体的 AABB 都会被检查。

## 数量、范围和频率

| 配置 | 新默认值 | 含义 |
|---|---:|---|
| `MOB_MONSTER_LIMIT` | 70 | 周期自然怪物基准，0 停止此分类周期生成 |
| `MOB_ANIMAL_LIMIT` | 10 | 周期自然动物基准，0 停止此分类周期生成 |
| `MOB_SPAWN_RANGE` | 6 区块 | 对尝试生成的区块中心做圆形距离检查，并受视距限制；允许 1..8 |
| `MOB_MONSTER_INTERVAL` | 1 tick | 周期怪物生成间隔；0 停止 |
| `MOB_ANIMAL_INTERVAL` | 400 ticks | 周期动物生成间隔，20 ticks/秒下为 20 秒；0 停止 |
| `MOB_SPAWN_ATTEMPTS` | 0 | 0 使用所有符合范围条件的已加载区块；1..4096 为显式的额外采样预算 |
| `MOB_SPAWNS_PER_TICK` | 0 | 0 按分类、局部容量和小群限制；1..4096 为显式的额外宿主帧成功预算 |
| `MOB_GENERATE_CHUNK_ANIMALS` | true | 新提交主世界区块的初始动物生成，可单独关闭 |
| `MOB_TOTAL_LIMIT` | 1024 | 所有来源合计的单维度安全上限，1..4096；这是 WebCraft 的保护限制 |

**全局分类上限 = floor(分类基准 × 已加载的自然生成统计区块数 / 289)。** 统计使用玩家周围至多 8 区块的自然生成票据范围，并对重叠玩家区域去重；尝试范围另受 `mob-spawn-range` 限制。不能把刷怪范围 6 直接当成统计区块只有 169 个。一个玩家完整加载 17×17 区块时，默认周期自然上限是怪物 70、动物 10；加载少、世界边界或刚加入时会更低。多个相隔较远的玩家可以扩大全局预算。

上限还要通过上游的玩家局部容量检查，默认局部怪物 70、动物 10，避免某个玩家的满额农场单独阻止另一个远处玩家。上下界、末地分别统计。分类统计包括已加载实体中的命令/生成蛋/刷怪笼等来源，显式持久实体不计入自然容量；远处未加载的实体不会挤占自然分类预算。所有保存的生物仍计入独立安全上限。

自然候选位置距离最近有效玩家及世界出生点必须超过 24 格，距离最近有效玩家不超过 128 格。死去、断开的玩家和旁观者不会触发自然生成；创造模式玩家可以触发。`doMobSpawning=false` 停止周期和初始动物生成，和平难度不产生普通自然怪物。

每个符合条件的区块按地形高度随机抽取 X/Y/Z，进行最多三组尝试，按生物群系权重选择物种与群体数量，再逐个检查。主世界末影人恢复到上游较低权重，不再按维度自动删除已存在的末影人。狼只会由有狼的生物群系表选中。玩家随意铺下界砖不会在要塞外制造烈焰人/凋灵骷髅的自然刷新区域。

新生成区块的初始动物使用上游约 10% 的群体彩票，原因 `chunk_generation`。它与每 400 ticks 的周期补充不同，**不受周期动物上限 10 或玩家 24 格排除圈限制**，仍须通过位置、碰撞、生成事件和 1024 安全上限。旧区块加载、旧有限地形的区块化以及存档恢复不会重复执行这一步；初始生成可通过专用配置关闭。

Go 宿主仍为 10 Hz，每帧自然生成执行两个 Minecraft 逻辑 ticks，因此自然生成的周期/随机消失机会按 20 ticks/秒结算；网络同步和其余 AI 仍按现有宿主节奏运行。显式配置旧预算 32/4 时仍保留该保护，只有新默认 0/0 解除这两个原来人为限制。

## 消失与保存

普通非持久怪物超出 128 格后尝试立即消失；32 格外闲置超过 600 Minecraft ticks 后，按当前上游每逻辑 tick `1/800` 概率尝试消失（每宿主帧最多两次机会）。回到 32 格内重置闲置时间。无人可用于距离判定时暂停；被动动物及已有特殊实体沿用远距保留。取消清除不发送成功事件。

不调整存档格式，不删除、截断已有生物、账号或玩家物品。1024 是新增时的运行保护，旧存档仍按已有 4096 绝对校验上限恢复；降低配置不会清空已有世界。

## 插件事件与现有引擎边界

继续使用 API v1 `entity.spawn.before/entity.spawned`、`entity.despawn.before/entity.despawned`。before 可取消，无新增可修改字段，Monitor 只读。自然生成审计新增 `biome/skyLight/blockLight/localLight/minecraftTick/spawningRules`，并保留原因、操作者、目标、数量、容量、持久状态及原有物品快照。SDK `Event.MobLifecycle()`、`GET /api/events`、`GET /api/server` 和 `world-policy` 示例同步发布这些规则；取消或提交前复验失败，不增加实体数量/ID，不发完成事件。

这是按 Spigot 流程实现**现有引擎支持的实体与地形**，不能声称整个 Minecraft 已完全一致：当前高度仍为 0..127，生物群系由 WebCraft 的旧有限/无限生成器映射，水生、蝙蝠、史莱姆等尚无实体实现，也没有原版全部结构、群系或特殊刷怪器。官方选种表里未实现的物种会放弃该次尝试，不能替换成骷髅或给其他物种重新分配权重；对应分类当前不会自然生成。刷怪笼沿用已有的独立规则，未随这次周期自然生成重写。
