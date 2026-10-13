# 村庄与废弃矿井

客户端本地世界和服务器共用 `internal/worldgen/source/structures.c` 的结构规则。它参考 Minecraft 的区域随机选址、群系筛选、带权建筑池和矿井分支拼装方式，是适配本项目方块及地形的独立实现；不承诺与原版相同种子得到相同结构，也没有复制原版建筑模板。

## 内容

- 五种村庄风格：平原橡木、沙漠砂岩、热带草原、针叶林云杉、雪原。包含水井、道路、住宅、农田、图书室、铁匠房、床、照明、村民和战利品箱。
- 每 32 × 32 区块区域挑选一个村庄候选位置，保留 8 区块间隔，再检查群系、地势、道路坡度与建筑地基。道路和建筑可以跨区块。
- 废弃矿井按每区块 0.4% 的起点概率，在地下拼装中心房间、支撑廊道、岔路及下降通道；最多 48 个部件、5 层分支。包含木板地面、栅栏支柱、木梁、方向正确的断续铁轨、蛛网和战利品箱。
- 本项目暂无金合欢木与陶瓦，草原风格使用橙色墙面和现有木材；暂无专用针叶林地形，以森林或寒冷森林选择对应风格。矿井宝箱使用普通箱子，未包含运输矿车宝箱或洞穴蜘蛛刷怪笼。

Minecraft 官方的拼装思路说明：[Jigsaw structures](https://learn.microsoft.com/en-us/minecraft/creator/documents/structures/introductiontojigsawstructures?view=minecraft-bedrock-stable)；[Structure sets](https://learn.microsoft.com/en-us/minecraft/creator/reference/content/jigsawreference/examples/jigsawcomponents/structure_set?view=minecraft-bedrock-stable)。这里采用自己的固定整数散列和建筑模板，不依赖 Mojang 随机数实现。

## 生效与存档

本地新世界带 `structuresVersion: 1`，WASM 原生区块生成、碰撞及渲染线程均启用同一版本。已有本地世界没有此标记，继续使用旧地形，避免重建地形覆盖原有建筑。要在本地体验结构，请新建世界。

服务器重新构建并启动后，无限主世界的未生成区块使用 `noise-v1-structures-v1`。已有区块直接读取存档，有限旧世界的原始区块、下界和末地继续原有流程。服务器与本地原有地形及种子编码不同，因此共用的是结构规则，不保证两种世界的坐标相同。

服务器生成工作在线程独享的 WASM 实例里完成，方块、轨道状态、宝箱及待生成村民先进入区块快照，再由主流程提交。村民仍经过既有插件生成事件。结构不读取邻区块的已生成内容，因此加载顺序不会影响结果。

本地村民及宝箱在附近区块首次进入模拟时初始化，并随 `structurePopulation` 一起保存。重进不会补充已拿走的战利品或复活已死亡的居民。挖掘、开箱路径也会先确保初始化。轨道方向会在重新进入世界时恢复。

## 构建

正常运行及 Go 编译使用仓库内已生成的 WASM，不需要 C 工具链。修改 C 源码后，在相邻的 `webcraft-client` 仓库运行：

```sh
WASI_CLANG=/path/to/wasi-sdk/bin/clang npm run structures:build
npm run structures:verify
npm run build:frontend
```

需要 WASI SDK 和 PATH 中的 WABT `wasm2wat` / `wat2wasm`。构建脚本同时更新服务器 `internal/worldgen/structures.wasm`、客户端 `src/client/engine.wasm` 和 `public/engine.wasm`。客户端保留 `src/structures/engine-base.wasm` 作为链接输入，并校验其散列；更换基础引擎时需要同步检查适配器的函数及内存地址。

服务器检查：

```sh
go test ./internal/worldgen
go test ./internal/game -run 'TestStructureDeterminism|TestBackgroundVillageMetadataAndProtectedSpawnCommit|TestVillageGenerationIntegrity|TestVillageChest'
go build ./...
```

客户端验证脚本覆盖五种真实群系、主引擎与共享程序一致性、真实渲染 Worker 一致性、旧世界保持不变、区块顺序、玩家修改、战利品和村民持久化、铁轨方向。种子 `1234` 的本地示例区块：平原 `(51,94)`、沙漠 `(101,85)`、草原 `(166,112)`、针叶林 `(114,95)`、雪原 `(210,79)`、矿井 `(46,4)`；区块坐标乘 16 即对应方块坐标附近。
