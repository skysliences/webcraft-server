# 方块注册表和 16 位协议

客户端 hello 保留 `v:4`，增加 `blockProtocol:2`。服务端 welcome 返回协商版本及 `registry:{version:1,entries:[...]}`；客户端必须先校验并安装注册表再加载区块。旧客户端未声明版本时继续使用原包，有扩展注册项的服务器要求升级客户端。

新帧：32 区块增量（与 12 同头部，记录为 index:u16 + ID:u16 + flags:u8）；33 创造同步（kind:u8 后 key:u32 + ID:u16）；34 无限地形（与 13 同头部和坐标，RLE 为 count:u16 + ID:u16）；35 创造回滚（kind + key:u32 + ID:u16）；36 创造编辑（kind + key:u32 + ID:u16）。全部小端。旧帧 2/3/4/12/13 的字段长度保持不变。JSON 放置、确认、物品和事件中的数字 ID 已经无损支持 65535。

WASM 新 API：`block_id_max()`、`register_block16(id,model)`、`load_chunk16(ci)`（ebuf 中 32768 个小端 u16）、`copy_box16(x,y,z,w,h,d)`（ebuf 中小端 u16，Y/Z/X 顺序，与旧 copy_box 一致）。`get_block`、`set_block`、`raycast` 返回真实注册 ID；旧 copy_box / apply_chunk 等字节接口保留给 API v1。内部模型缓存只供原有网格、碰撞和光照算法使用，不是游戏状态或存档 ID。Worker 同步注册项和成功的 16 位加载/编辑，并保留版本检查。

生成器原有 base 使用字节存储，以保留旧 MCC、CRC 和生成结果；任何自定义 ID 存在区块 edits 的 u16 字段，地形发包时覆盖 base 并编码为 u16。已有 JSON 与二进制 MCC 都继续读取，不需要重编号或改写所有区块。registry.json 随备份恢复，数值 ID 不回收。新增格式前必须保存原文件备份，当前升级没有修改世界或 MCC 的格式版本。

容量是一个共享的 16 位物品/方块命名空间；不是同时支持 65535 个方块再加 65535 个物品。渲染和碰撞使用模板，任意上传纹理/模型不是本接口的能力。
