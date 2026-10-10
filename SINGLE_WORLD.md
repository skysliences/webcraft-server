# 单世界游戏服务器

每个 Go 游戏服进程与 DATA_DIR 默认运行一个主世界，其下界和末地共享世界种子、玩家身份与运营权限。不同游戏服使用独立数据目录、端口和核心账号注册的 server ID；玩家的核心账号 ID 不变，各服保存自己的进度。

空数据目录启动时自动生成一个主世界，使用 WORLD_NAME（默认“世界”）和 WORLD_MODE（默认 survival，可设 creative），支持按需无限区块。生成的 ID 写入 `world-server.json`，重启继续使用它。WORLD_NAME/MODE 只影响新世界，不覆盖旧世界名称、模式或进度。

旧数据目录可设 `WORLD_ID=ti9bwes4ryguhkra` 选择已有主世界。未指定时优先已有绑定，再选择旧默认世界或唯一存档；多个非默认存档必须指定 ID。绑定文件损坏或选中的存档缺失会停止启动，避免错误生成新世界覆盖进度。调整已有绑定之前自动备份旧绑定文件。

其余存档及其区块留在磁盘上，单世界进程不加载、迁移或对外开放它们。要运行另一存档，由服主停止进程后配置 WORLD_ID，或将完整存档复制到独立 DATA_DIR 启动另一服务器；不要同时让两个进程写同一个目录。网页不再提供在当前游戏服创建第二个世界的入口，POST /api/rooms 返回 409。API v1 的旧列表字段保持兼容，GET /api/server 提供公开的 singleWorld、online、主世界 id/name/mode、joinPath 和核心 issuer/serverId，不包含账号、房主密钥或会话。玩家在统一网站输入私人 Go 游戏服地址，网页无凭据发现其主世界，并留在当前网站进入 `/r/ID?server=Go地址`。旧游戏网页收藏通过 `/server-info.json` 发现公开 Go 地址；旧世界链接会以该目标服的主世界为准。无效地址、未登记身份、离线或未知发行方会明确显示错误，不能回退到本机服务器。统一网站不依赖任何私人服显示首页，见 [CLIENT.md](CLIENT.md)。

GET /api/server 提供公开的 singleWorld、online、主世界 id/name/mode 和 joinPath，不包含账号、房主密钥或会话。前端 /server-info.json 仅向它自己配置的 Go 后端读取公开状态。输入游戏网页 IP 后，浏览器打开目标网页的 /#join-server，由该网页查询自己的主世界并进入 /r/ID；不会沿用发起连接网页的后端配置。旧的 /#local-servers 和已归档世界链接会进入该服务器正在运行的主世界。游戏服无法访问时明确显示失败，不回退到其他服务器。

备份包含全部存档和 world-server.json，恢复时校验主世界存在，并通过插件的 server.world.select.before 保护恢复提交。原文件及预恢复备份仍保留。核心账号服务的数据应独立备份，游戏服备份不包含核心密码或签名私钥。

仅为迁移工具和旧 API v1 多世界测试保留 LEGACY_MULTI_WORLD=true；正式服务器默认 false。插件事件语义见 PLUGINS.md。
