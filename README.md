# World Is Agent

World Is Agent 是一个本机运行的 AI 叙事游戏。玩家在浏览器中选择本地剧本，与有独立经历的重要人物和场景路人互动；每个世界单独保存正文、事件、人物感知与记忆。

The current local build includes Phase12 M1 and M2 plus Story Pack v3 Stages A, B, and C: reliable story saves, scoped long-term memory, recoverable corrections, authoritative spatial worlds, author-defined character state, directed relationships, uniquely owned items, program-settled risk, and fact-gated world progression. The browser workspace connects directly to the local Runtime; no game adapter is required. This is an internal-playtest build, not a stable release.

Repository story packs include a guided investigation, an open station with independent event lines, and the v3 spatial sample 《雾都余烬》. V3 worlds freeze their capability manifest and derive the current location, nearby destinations, important characters, and stable bystanders from persisted positions. Existing saves keep their pinned definitions. Use the [M2 story-pack reference](docs/phase12/stages/M2-世界剧情与长期记忆.md#10-本地剧本包参考) for v1/v2 and the [v3 minimal template](docs/phase12/templates/story-pack-v3-minimal/story.json) for the spatial author contract.

## 开始使用

运行环境：Windows、Go 1.25+；首次从源码构建工作台还需要 Node.js 20+。

```powershell
cd D:\data\project\game-agent\wia
.\scripts\start-phase12.ps1 -Rebuild
```

日常启动已经构建好的工作台：

```powershell
.\scripts\start-phase12.ps1
```

需要重新安装前端依赖并构建工作台时使用 `-Rebuild`，也可以使用 `-True`。Runtime 会打开本地浏览器并打印带会话令牌的工作台地址。若没有自动打开，复制日志中的 `local client` 地址到浏览器即可。

首次进入可先浏览剧本和已有故事，开始新故事或发送输入前再填写 DeepSeek 或 OpenAI 的 API Key。Runtime 会先验证连接，再将凭据保存在本机数据目录的 secrets 文件中；普通配置、页面响应和故事存档都不包含 API Key。

After building the web client, you can also build an executable:

```powershell
go build -o wia-runtime.exe ./backend/cmd/wia
.\wia-runtime.exe
```
通过 `-data-root` 指定数据目录，或使用 `WIA_DATA_ROOT`。默认数据目录是 `%LOCALAPPDATA%\WorldIsAgent`。每个世界位于独立的 SQLite 数据库中；另存会创建新的 world_id，读取后继续写入所选世界。

## 可体验内容

- Local JSON packs: guided 《暮灯镇的失踪信使》, open 《远星维修站》, and v3 sample 《雾都余烬》. Authors define each story's mode, supported runtime capabilities, state vocabulary, directed relationship types, and item instances.
- V3 state values, relationship changes, and item transfers are validated against successful action results, applied to one turn working state, and committed atomically with their provenance. The play sidebar shows only server-authorized state and item projections.
- V3 action rules use a durable stable-input preparation record for program-owned percentile results. Story-authored effects and plot gates can read current location, state, relationship, item, and committed rule facts; off-scene NPC movement still follows personal decisions and authoritative routes.
- 两名重要 NPC：客栈老板沈岚、佣兵铁杉；开场保留十名场景路人。
- NPC 并行决策、公开回应后的下一阶段反应、按人物分开的感知和记忆。
- 私下交谈的旁观隔离：授权人物看到原文，其他人物只看到交谈迹象。
- 自动保存、世界列表、显式读取、新开一局、另存为独立分支。
- 生成状态、取消、失败输入保留、显式重试、幂等请求和版本冲突保护。
- 本地回环 HTTP 会话、模型连接引导和响应式玩家工作台。
- 持久导航、独立正文滚动与底部输入区、历史分页、阅读位置恢复，以及按存档隔离的页面会话草稿。
- 点击人物指定交谈对象、三档互动风格与按需展开的故事设置、就地错误与存档操作反馈。
- Conditional world events follow in-game time and resolved interventions. Guided stories can conclude; open stories remain playable after an event ends.
- Personal digests retain a complete recent tail and retrieve older authorized experiences. NPCs do not share a global memory.
- “回顾与纠正” provides player memories and an explicitly spoiler-marked author view. Corrections preserve original prose, use version checks and rebuild affected interpretations with restart recovery.

## 当前验证

Verification evidence: [M1](docs/phase12/records/M1-验收记录.md), [M2](docs/phase12/records/M2-验收记录.md), [M3](docs/phase12/records/M3-验收记录.md), and [phase status](docs/phase12/开发状态.md). Deterministic tests, browser checks, real-model observations and user acceptance are recorded separately.

Full Go regression, relevant race/vet checks, frontend mechanism tests, type checking and production builds pass. The executable starts with an isolated data root and serves both its homepage and bundled client assets. Real-model tests use disposable worlds; occasional generation failures, latency and semantic deviations remain documented limitations.

已用临时世界完成桌面、平板和手机视口的浏览器交互验证；模型采用测试替身。实体手机软键盘与最终游玩体验仍待验证。

M3 已交付行动建议、主角资料与面向开发者的内容工作台；**面向普通玩家的剧本创作、跨剧本主角模板库与游玩中提升路人已按范围变更移出**，写作、导入与导出保留为开发者工具。Story Pack v3 阶段 A 已交付地点层级、权威位置、结构化移动与同场派生；阶段 B 已交付由剧本定义的人物状态、有向关系、物品唯一归属、授权转移、权限投影和持久化；阶段 C 已交付可选程序风险判定、结构化条件事件和有限场外推进。当前状态、物品与判定按[可选能力模块](docs/phase12/stages/模型主导的可选能力模块.md)接入；普通行动及状态变化由模型判断，程序负责一致保存，状态栏独立展示公开投影。真实模型连续游玩和默认内容切换仍待单独验收与授权。M4 覆盖桌面打包、服务器账户与跨设备。仓库形态、Turn 主流程、owner 拆分与旧 Game Runtime 删除已完成，当前架构见 [WIA 1.0 架构收敛方案](docs/ARCHITECTURE.md)。

## 相关文档

- [WIA 1.0 架构收敛方案](docs/ARCHITECTURE.md)
- [文档入口](docs/README.md)
- [Phase12 文档入口](docs/phase12/README.md)
- [Phase12 产品说明](docs/phase12/产品说明.md)
- [Phase12 总体技术方案](docs/phase12/总体技术方案.md)
- [Phase12 开发状态](docs/phase12/开发状态.md)

## 许可证

[MIT License](LICENSE)
