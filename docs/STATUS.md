# 当前状态

更新时间：2026-09-27。

仓库当前交付 Phase12 M1「核心游玩与可靠存档」。玩家可以从本地 Runtime 进入《暮灯镇的失踪信使》，连接真实 Provider 后连续游玩，并在独立世界之间读取、另存和继续。

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

长期摘要/检索、行动建议、内容编辑与导入、统一纠正、多用户真实登录、跨设备服务端、附件恢复和发行包不属于 M1 当前交付。真实模型的人物自然度、等待体验和连续游玩质量单独记录；现有短样本不能证明长程稳定，结构化生成失败也不归为可接受的创作偏差。

详细阶段记录：[Phase12 M1 验收记录](phase12/acceptance/M1.md)。

### NPC autonomy and behavior policies

Phase12 stores private initial concerns with newly created worlds; existing worlds keep their own character data. Story settings expose optional coordination, narration and shared NPC policies. Custom text replaces its default policy, explicit story options take precedence, and settings retain world/epoch isolation and copy semantics. Deterministic, browser and three-turn real-model evidence is recorded in [M1 acceptance](phase12/acceptance/M1.md). Long-term concerns and conditional world progression remain M2 work.
