# Agent 运行与记忆机制资料评估

资料日期：2026-10-06—2026-10-07。范围为本地源码对照与 WIA Phase13 设计取舍，未运行参考项目、真实模型或性能测试。

## 1. 资料基线

| 项目 | 本地身份 | 结论范围 |
| --- | --- | --- |
| SillyTavern | 1.18.0，`51ad27fb86d39a3daca3adaa970375c9670c12df` | 原生单卡/群聊、上下文、摘要/检索、变量与保存；不包含第三方状态插件 |
| pi | `086c32e74530564922d011ade23ff582c9d63116` | agent-loop、会话投影、压缩及新 AgentHarness 脚手架 |
| DeepSeek Harness | 0.1.0-rc.7 的本地解压源码，无 Git 身份 | 默认工具循环、上下文、会话/压缩/持久化与恢复；插件和示例分别核查 |
| Hermes Agent | pyproject.toml 为 0.20.1，本地解压源码，无 Git 身份 | 内置笔记、会话检索、摘要与维护；Mem0 适配范围分别核查，不评价外部服务实际质量 |
| WIA | `2cb51c1` 的实现与 Phase13 待实施方案 | 现有 Turn、memory、Context、世界状态与提交；设计不作为已交付能力 |

DeepSeek Harness 核心源码 SHA-256：

| 路径 | 摘要 |
| --- | --- |
| `packages/core/agent-loop/src/agent.ts` | `E775E59F3761240EE571A9B997D0D29DEB97A283B6C2FC3A071091B2743D22B4` |
| `packages/compaction/compaction-basic/src/index.ts` | `D902D83329A1EDA4ED7E29AA459C1A9663A19D5B7D95C76AF118CB2C510A2EDD` |
| `packages/core/session/src/repair.ts` | `ECA9E728DBB2AE8CD51D1E163385E6A20B0399DFA82DFA6230F2EEEFDFDD8FD8` |

这些身份标记本次材料，不宣称是各项目最新发布版本。

Hermes 核心源码 SHA-256：

| 路径 | 摘要 |
| --- | --- |
| `tools/memory_tool.py` | `A92CF9227EFDAB5B5E6C0D5604C5ADAE4DB830A20BEDDB624C8B2167C8054AC7` |
| `tools/session_search_tool.py` | `83539013026E16815A52FD6EE920592E9648EF35FD855F922190ED7E5A76749F` |
| `agent/context_compressor.py` | `42F68B134376BCABBE99EEA77BD5895379337A2C5AE23C23CFB7E674A5484175` |

## 2. 实际机制与边界

### SillyTavern

程序先装配角色、预设、历史、世界书与扩展资料，再生成并保存聊天。单张世界卡可以让一个模型统一扮演多个人物；群聊由程序选择成员后逐个调用。世界书支持规则激活、递归扫描和预算，摘要与 vectors 扩展能补充旧聊天或文档；不是所有材料全量注入。

变量、宏、工具调用和自动化扩展可以构建复杂玩法。原生聊天保存有完整性检查和原子文件写入，但这些路径没有 WIA 的统一钱物、位置、个人经历来源与世界版本提交语义。社区状态插件可能增强它，本次没有将这些能力归入原生实现。

代码位置：`public/script.js:4231`、`public/scripts/group-chats.js:1050`、`world-info.js:4624`、`extensions/vectors/index.js:776`、`variables.js:48`、`src/endpoints/chats.js:457`。

### pi

成熟 agent-loop 在模型调用前先执行 transformContext，再执行 convertToLlm，工具参数由程序校验；工具结果回填后继续生成。应用负责决定哪些领域材料进入上下文，核心没有自动提供 NPC 知识或世界状态。

编码会话支持最新摘要、近期保留记录、分支投影与原始 JSONL 条目。压缩保留工具调用/结果完整性，并依据容量和预留决定何时触发；摘要模板面向编码任务，不能直接用作人物记忆。

本地新 AgentHarness 的 prompt、compact、resume、navigateTree 等接口仍返回 HarnessNotImplemented，已有记录 restore 也被拒绝。独立 session/context 和 compaction 函数存在实现，不能据此宣称新 Harness 的完整执行和恢复已接通。[实际循环](https://github.com/earendil-works/pi/blob/086c32e74530564922d011ade23ff582c9d63116/packages/agent/src/agent-loop.ts)、[新 Harness 接口](https://github.com/earendil-works/pi/blob/086c32e74530564922d011ade23ff582c9d63116/packages/agent/src/harness/agent-harness.ts)。

代码位置：`agent-loop.ts:281–306`、`:381`、`:600–660`；`coding-agent/src/core/session-manager.ts:418–468`、`:1015–1048`；`coding-agent/src/core/compaction/compaction.ts:403`、`:467`；`agent-harness.ts:347–381`。路径均属于对应 pi 包。

### DeepSeek Harness

默认运行是模型—工具循环：请求前接纳输入、记录消息与实际请求头、从 Session 投影历史，生成后执行工具并继续下一 step。动态情境按贡献项排序，仅变化时追加新快照。模型所见可从日志重建的约束由程序检查；日志接受与实际落盘有区别，checkpoint 插件才提供落盘屏障。

压缩保留近期原文和调用/结果配对，摘要关联被替换事件，原日志保持。摘要变小且来源仍一致才接受。源码提供会话日志、压缩和全文检索插件；mcp-memory 是默认关闭的第三方参考配置，不是发行组合中的 NPC 长期记忆模块。

恢复区分 TOOL_NOT_STARTED 与 TOOL_OUTCOME_UNKNOWN。已经开始但结果不明的外部操作，需要核查实际状态；取消和恢复不撤销既有工具副作用。核心没有 WIA 所需的整轮步数预算，repeat-tool-reminder 只是提示，timeout 也依赖工具配合取消。[项目源码](https://github.com/deepseek-ai/deepseek-harness)。

代码位置：`packages/core/agent-loop/src/agent.ts:225–400`、`:407–494`；`runtime-context.ts:34–75`；`core/session/src/repair.ts:91–122`；`session/session-checkpoint-policy/src/index.ts:29–82`；`compaction/compaction-basic/src/region.ts:368–465`；`guard/repeat-tool-reminder/src/index.ts:162–232`；`examples/mcp-memory/README.md:5–31`。

### Hermes Agent

内置 MemoryStore 是小容量的人工式笔记，由模型选择内容并增删改。MEMORY.md/USER.md 默认上限分别为 2,200/1,375 字符；载入时生成稳定的 system snapshot，写入后的 live state 立即保存，但不会自动改变当前会话的系统快照。它不是每个虚构人物的完整事件记忆。

session_search 使用本地 SQLite FTS5，命中后按消息 ID 展开前后原文，也能继续滚动查看。检索包含相关性/时间排序、会话血缘去重及自动化内容降权。实际 discovery 对周边首尾和消息内容使用 1,200/4,000 字符上限，并报告截断；不能将“原文保留”理解为每次检索全文无损输入。中文索引提供可选 CJK 双字 tokenizer，缺少扩展时使用既有回退；不证明该扩展可直接用于 WIA 的 Go/Windows 环境。

摘要使用已完成动作、当前状态、未解决问题等结构，连续更新旧摘要，并提供日期锚定规则。模板面向助手任务与编码，WIA 只采用完成/未完成和时点明确的原则，使用既有个人状态种类与世界时间。笔记 batch 在工作副本上验证最终容量后一次写入；成功返回 done，整理失败计数提供终止反馈，但成功会重置失败计数，不能将其当作整个 Turn 的硬调用上限。

MemoryManager 串行同步外部记忆、处理会话结束与切换顺序，预取等待有上限；中断回合不镜像为已完成对话。常驻笔记的后台回顾另起 AIAgent，默认沿用主模型，可能增加请求与上下文。Mem0 的语义搜索/事实提取属于可选后端；其默认读取只限定 user_id，跨 agent/session 召回是个人助手的设计选择，WIA 必须保留 world/owner 与有效来源边界。

代码位置：`tools/memory_tool.py:148–174`、`:562–670`、`:682–728`；`tools/session_search_tool.py:755–932`；`hermes_state.py:2484–2614`；`agent/context_compressor.py:3887–4131`；`agent/memory_manager.py:675–731`、`:927–971`；`agent/turn_finalizer.py:748–774`；`plugins/memory/mem0/__init__.py:372–378`。路径均在本地 Hermes 源码中。

## 3. WIA 已有基础与必要适配

WIA 已有个人来源流、带连续覆盖水位的摘要、近期完整回合组、有界关键词召回、摘要比较发布与重建任务、Context 预算和选择报告、受限补材、严格 JSON、运行幂等与最终原子提交。核心来源见 [memory 类型](../../../backend/internal/memory/types.go)、[记忆规则](../../../backend/internal/memory/records.go)、[上下文](../../../backend/internal/turn/composer.go)、[回合提交](../../../backend/internal/app/store.go)。

集中输入需要以下适配：

- 多人物先各自投影，再统一合并与预算；当前单人物 withLongMemory 会重置 Optional，不能在同一 Material 上重复调用。
- 个人记录与 digest 归一保留 owner；scene 空 recipient 不能代表所有人物。
- 对白复用程序派生原话投影；行动、观察和外部结果继续提供个人感知差异。
- 补材、判定和纠正共享一个 scene attempt 额度，不叠加多个局部重试循环。
- 冻结材料与运行时人物资料分别装配；资料目录摘要不作为完整事实依据。

现行记忆纠正有明确限制：会破坏当前位置或受保护结构来源的纠正被拒绝。受保护来源包含历史已结算状态、关系与物品变化、固定判定和当前计划。普通未被结构状态消费的文本可以按已有路径纠正；没有任意钱物/位置历史自动重建能力。[纠正限制](../../../backend/internal/app/corrections.go#L116)、[结构来源集合](../../../backend/internal/storage/mechanics.go#L109)。

Hermes 记忆参考的落点是以下现有能力：

- [个人摘要](../../../backend/internal/turn/memory_store.go#L93)已区分尝试、结果和主观判断，保留有效旧状态；Phase13 细化已兑现承诺和世界时间的输入与评估合同，不新建摘要模型职责。
- [旧经历召回](../../../backend/internal/memory/records.go#L127)已返回完整组并报告受限；[汉字查询切分](../../../backend/internal/memory/records.go#L268)已有双字规则。首次迁移强化来源定位与诊断，全文索引按实际漏召回或耗时后置评估。
- [摘要比较发布](../../../backend/internal/turn/memory_store.go#L148)与[重建任务](../../../backend/internal/app/memory_jobs.go#L150)继续校验来源、版本和覆盖。批量整理原则映射到完整 Digest 候选，不增加自由编辑笔记工具。

## 4. 设计取舍

| 机制 | 采用方式 | 范围 |
| --- | --- | --- |
| 小内核与明确模型输入转换 | 现有 Turn 编排，Context 只选择与装配材料 | 首次迁移 |
| 同一模型按需补材并继续 | 一次补充上下文、一次判定检查点、一次纠正，核心最多四次 | 首次迁移 |
| 近期原文与带来源摘要 | 复用 memory，当前关键依据可定位，个人视图分别维护 | 首次迁移 |
| 重点摘要与旧原文分层、命中后定位完整因果组 | 第 6.5.1/6.5.2 节复用 Digest、近期流与 archive，提供去重和受限诊断 | 首次迁移 |
| 已完成/仍有效事项与时间锚定 | 第 6.5.3 节按个人来源表达，使用世界日期，保留不确定性 | 首次迁移 |
| 完整候选整理与有界维护 | 第 6.5.4 节复用既有摘要调用、比较发布和重建任务 | 首次迁移 |
| 稳定职责与动态场景分区 | 现有材料排序、去重、来源选择报告和计量 | 首次迁移 |
| 取消、未完成与提交未知区分 | 沿用 run/attempt、版本校验及先查询后重试 | 首次迁移 |
| 长历史全文索引、语义召回、usage 自适应压缩、steering | 先取得实际漏召回或等待证据，再设计接口与评估 | 后置评估 |
| 全局用户笔记、外部记忆服务、逐 NPC 回顾 Agent | 保留个人经历、现有存储和维护职责，不接入首次迁移 | 不采用 |
| Cordis/Node Harness 接入、通用工具、自修改、默认多 Agent | 不进入游戏内核，保留数据型剧本和当前依赖 | 不采用 |
| 会话树或 JSONL 替换世界库、结构状态历史重建 | 不与集中创作迁移混做 | 独立范围 |

WIA 的区分度应由可见体验验证：普通多步行动能在一轮自然承接，人物经历分别保存并影响后续，日期、位置和钱物可靠持续，失败后安全重试，关闭再打开仍能续玩。它不由框架名称、项目年代或更多 Agent 证明，也不宣称这些能力在酒馆生态中无法实现。

## 5. 开发结论

[Phase13 技术方案](../../phase13/开放叙事与单Agent-技术方案.md)具备 P0/P1 工程准备与 P2 候选原型的设计条件。正式切换需通过机制、来源和旧档回归，以及获准的集中创作质量预检；真实模型暂停时继续候选机制开发，保持正式默认链路。

工程与体验预算沿用[实施计划](../../phase13/实施与验收计划.md#8-工时预估与执行安排)的 14—22 小时估算。范围包括当前内核的必要适配，不包括接入参考框架、建设长历史索引或新增语义记忆服务；工时与剧情质量仍需实际验证。
