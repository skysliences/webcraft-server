# WebCraft Game Server（体素游戏专用服务端）

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org)

> **开源声明**：本项目为开源项目 (Public Repository)，采用 MIT 许可证。

WebCraft Game Server 是采用 Go 语言构建的高性能体素沙盒多人游戏服务器。具备强大的实时房间管理、多维度世界流式加载、红石逻辑模拟、A* 3D 生物寻路、方块事件调度与类似 Spigot 的原生插件扩展系统。

---

## 🌟 核心特性

- **高性能多人体素引擎**：
  - 基于 Go 语言轻量协程与二进制流式压缩，单服支持百人同屏畅玩。
  - 增量区块传输（Chunk Streaming），动态 LOD 视野剔除。
- **完整 Minecraft 核心玩法仿真**：
  - **红石自动化系统**：红石线信号衰减、红石火把反向门、中继器延时与锁存、活塞推拉方块、红石块与压力板。
  - **3D 体素 A\* 生物寻路**：僵尸、苦力怕等生物支持绕过坑洞、攀爬阶梯、绕行障碍追逐目标。
  - **真实物理与重力流体**：沙子/沙砾下落、水/岩浆流动与碰撞、方块破坏掉落物自然抛物线重力下坠。
  - **统一 RandomTick 调度**：农作物生长、冰雪消融、树叶枯萎、火势蔓延。
- **Spigot 兼容的插件生态**：
  - 提供强大的 Go 插件 SDK（`pkg/plugin`）。
  - 支持 `.before` 意图拦截（可阻止与修改参数）与事件提交完成监听。
  - 物品完整元数据系统（Lore、属性修饰符、命名空间自定义数据跟随合并与持久化）。
- **去中心化微服务架构**：
  - 游戏服务器通过 Ed25519 权威公钥校验玩家身份，无需直连集中式用户数据库，天生支持私服与公服联邦接入。
- **开箱即用的运维 CLI**：
  - 内置 `mcraftctl` 命令行工具，提供控制台监控、世界快照备份、权限变更与热重载。

---

## 🏗️ 架构概览

```mermaid
flowchart TD
    Client["WebCraft Web 客户端 (浏览器)"]
    Account["WebCraft Account Service (身份认证中心)"]
    Server["WebCraft Game Server (开源游戏服)"]
    Plugin["独立插件进程 (Go SDK Plugin)"]

    Client -- "1. 登录并获取签名 Proof" --> Account
    Client -- "2. WebSocket 加入房间携带 Proof" --> Server
    Server -- "3. 本地公钥无锁验签" --> Server
    Server <--> "4. JSON-RPC 事件与批处理" <--> Plugin
```

---

## 🛠️ 快速启动

### 1. 编译构建
```bash
# 编译游戏主程序与运维管理工具
go build -o bin/mcraft ./cmd/mcraft
go build -o bin/mcraftctl ./cmd/mcraftctl

# 编译示例插件 (可选)
go build -o plugins/welcome/welcome ./examples/welcome
```

### 2. 启动服务
```bash
# 复制配置文件
cp deployment/server.env.example server.env

# 启动游戏服务器 (默认监听 8081 端口)
./deployment/start.sh
```

---

## ⚙️ 核心环境变量（server.env）

| 配置项 | 默认值 | 作用说明 |
| :--- | :--- | :--- |
| `PORT` | `8081` | 游戏服务 WebSocket 与 API 端口 |
| `DEFAULT_MODE` | `survival` | 默认游戏模式 (`survival` / `creative`) |
| `SINGLE_WORLD` | `true` | 是否开启单世界归档模式 |
| `AUTH_PUBLIC_KEY` | `""` | 信任的 WebCraft 账号服务 Ed25519 公钥 (用于微服务验签) |
| `AUTH_ISSUER_URL` | `https://login.ccraft.app` | 默认账号认证服务地址 |
| `PLUGIN_DIR` | `./plugins` | 插件加载目录 |

---

## 📚 详细技术文档

- 📘 [插件开发指南与事件目录 (PLUGINS.md)](PLUGINS.md)
- 📗 [控制台指令与运维手册 (OPERATIONS.md)](OPERATIONS.md)
- 📙 [物品系统与元数据规范 (ITEMS.md)](ITEMS.md)
- 📕 [单世界归档与多维度管理 (SINGLE_WORLD.md)](SINGLE_WORLD.md)

---

## 📄 开源许可证

本项目采用 [MIT License](LICENSE) 协议开源，欢迎社区提交 Issue 与 Pull Request！
