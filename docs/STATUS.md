# 当前状态

更新时间：2026-09-28。

Local story packs and multi-story saves are available. The runtime validates JSON schemas and references, pins author-selected modes and revisions, and stores independent definition/cover snapshots. The inn and orbital-station samples share the existing runtime. Evidence and remaining scope: [Story Packs](phase12/acceptance/Story_Packs.md).

The local playtest build includes Phase12 M1 and M2. Players can run conditional story events, continue personal memories, inspect sources and correct records while keeping independent worlds and original prose. User experience acceptance remains separate from engineering verification.

## 已验证

| 范围 | 状态 |
| --- | --- |
| Go 全仓测试 | 通过：`go test ./... -count=1` |
| M1 故事运行时 | 通过：私聊隔离、公开阶段反应、严格 JSON、幂等、事务、取消、重试、复制与跨世界隔离测试 |
| HTTP 入口 | 通过：loopback 限制、会话交换、公开人物投影、世界/回合/另存路由测试 |
| 并发安全 | 通过：`go test -race ./runtime/internal/storyapp ./runtime/internal/storyapi -count=1` |
| 静态检查 | 通过：`go vet ./runtime/internal/storyapp ./runtime/internal/storyapi ./runtime/cmd/server` |
| 玩家工作台 | 通过：`npm run type-check`、`npm run build` |
| 正文分页 | 通过：默认最新 100 条、双向排他游标、非法参数、并发追加/删除、账户/剧本/世界隔离与空名称存档测试 |
| 浏览器交互 | 临时世界与受控模型下验证阅读锚点、增量补齐、草稿隔离、响应式布局、首次连接、弹窗错误与重试；详见 M1 记录 |
| 手机软键盘 | 待实机验证；手机尺寸与缩短视口已模拟检查 |
| 可执行入口 | 正式构建在独立临时数据根启动；首页和脚本资源 HTTP 200，不改动个人存档 |
| 上下文构建 | 来源与场景权限、历史窗口、完整组裁剪、超限及修复再校验、复制重启专项通过；缺失来源阻止生成但允许阅读历史 |
| 设置编辑并发 | 8 项设置逻辑回归及真实 HTTP 浏览器验证通过，覆盖旧版本冲突、跨存档失效和迟到返回 |
| 真实模型连续游玩 | DeepSeek 上下文专项最新五轮 5/5；连续试验共 18/19 完成，含一次协调失败。叙事偏差与长程质量仍需验收 |

## Turn recovery and diagnostics

- Requests have bounded client waits. Uncertain submissions retain their original idempotency key and payload; polling reconciles accepted work without automatically replaying actions.
- Model-call and JSON-validation diagnostics are written to rotating, content-free `story-app/logs/runtime.log` files. Provider failures, timeouts, incomplete output and schema failures are distinguished.
- NPC pending actions are separated from spoken dialogue. Coordination supports sourced `not_executed` outcomes for overlapping or unnecessary proposals.
- This focused evaluation completed 6 of 7 real-model turns; the latest three-turn sequence completed 3/3. A coordination-format failure and remaining prose continuity deviations are documented in the M1 acceptance record. Frontend recovery/settings checks pass 19/19.

## M1 能力

- 每个世界一个 SQLite 数据库，保存正文、事件、人物感知、人物记忆和世界时间。
- 两名重要 NPC 并行决策；公共回应触发沉默人物的下一阶段反应。
- 私聊原文进入实际接收者、玩家正文和获准协调调用；旁观人物仅获得自己的感知。NPC 回复按公开对白处理。
- ContextComposer 统一四类调用的来源、阶段、场景视图与预算，提供不含正文的构建诊断；场景视图随回合原子保存。
- 玩家输入、生成状态、取消、失败重试、请求幂等、活动版本和存档复制具备明确状态。
- 本地浏览器会话、首次模型连接验证、玩家输入、自动保存、存档列表、新开一局和另存分支均有正式入口。
- 顶栏与输入区持续可用，历史分页保留阅读位置，人物栏可选择交谈对象；故事设置默认仅展示互动风格与回复长度。
- 当前页面会话内按存档保留草稿、对象和阅读锚点；发送与各弹窗分别显示错误，迟到请求不覆盖其他存档。

## 已知范围

Action suggestions, full content editing/import, server accounts, cross-device access, attachment recovery and release packaging remain M3/M4 work. M2 memory/correction mechanisms are implemented. Real-model naturalness, latency and continuity are evaluated separately; short successful sequences do not establish a stable failure rate.

详细阶段记录：[Phase12 M1 验收记录](phase12/acceptance/M1.md)。

### NPC autonomy and behavior policies

Phase12 stores private initial concerns with newly created worlds; existing worlds keep their own character data. Story settings expose optional coordination, narration and shared NPC policies. Custom text replaces its default policy, explicit story options take precedence, and settings retain world/epoch isolation and copy semantics. Deterministic, browser and three-turn real-model evidence is recorded in [M1 acceptance](phase12/acceptance/M1.md). M2 digests maintain scoped beliefs, relationships, concerns and commitments.

### M2 world progression and memory

Conditional nodes, bounded waiting, resolved interventions and recipient-scoped scenes commit atomically with each turn. Supported reasoning models reserve internal reasoning separately from visible output. Guided waiting reached all three plot times. Open-mode waiting and post-event dialogue completed four turns in five attempts, continuing to 20:05 after the final node. A separate ten-turn historical fixture plus real-model continuation verified compaction, a scoped correction, rebuild, copy and restart. A complete real-model rescue route remains unverified; the latest open-mode sample included a roughly 147-second turn.

Each recipient has a continuous source index, a versioned digest and a complete recent tail. Search is scope-limited, and NPC-requested recall is bounded to two queries. Corrections preserve original records, advance the context epoch and use durable rebuild jobs. Required rebuilds block new generation and copying while preserving reading access; failed jobs can be retried.

The browser “回顾与纠正” view supports paging, spoiler-gated author records, frozen editing versions and uncertain-request recovery. Frontend checks pass 24 settings/recovery cases and four memory-session cases. Real-model failures and quality limits remain in [M2 verification](phase12/acceptance/M2.md); M2 is not a stable-release claim.
