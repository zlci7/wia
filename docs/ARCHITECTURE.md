# WIA 1.0 架构收敛方案

状态：**待执行**。本文定义目标架构与迁移顺序，不代表任何一步已经完成。执行进度与实际证据记录在开发状态类文档中。

本文取代此前"阶段式"文档对架构的描述。现有 `docs/phase12/` 继续作为产品范围、数据合同与历史验证的证据；其中与本文冲突的架构描述以本文为准。文档重组在 R6 完成。

## 1. 完成标准

这次收敛是否成功，只由三条可检查的结果判定：

1. **打开仓库根目录，能立即判断这是 WIA 叙事产品，不是 Game Runtime。**
2. **打开 `backend/internal/turn/service.go`，能在十分钟内看懂一轮故事如何运行。**
3. **想找 Agent、Memory、Context、World、Storage、Model 时，每种能力只有一个地方。**

三条都做到，即使总行数没有明显下降，收敛也算成功；只搬了目录而三条没做到，就是白做。

## 2. 为什么重构

仓库里同时存在两代产品：Game-native Agent Runtime（Agent / Context / Memory / Task / Tool / gRPC / Protocol / Adapter / Gateway）与 WIA 叙事应用（Story / NPC / Plot / Memory / Creator / Web）。结果是同名能力各有一套：旧 context 与新上下文构建、旧 memory 与新记忆投影、旧 agent runtime 与 `storyapp` 自己协调 Agent、旧 gRPC 与新 HTTP。

这不是代码量问题，是**同一件事有两个权威来源**。叙事功能事实上已经长成独立产品内核（`storyapp` 94 个文件、约 2.2 万行，其中 `run.go` 约 1.5 千行），却仍被放在旧 Runtime 的目录与命名之下。

已核实的依赖事实：

- `storyapp` 与 `storyapi` **对旧运行时零引用**；旧包只被旧入口 `runtime/cmd/server` 及它们彼此引用。
- 旧包（`agent`、`gateway`、`task`、`tool`、`context`、`memory`、`httpapi`、`session`、`trace`、`traceview`、`bootstrap`、`browser`、`dataroot`、`definition`、`protocol/`）合计约 **7 万行**，与叙事链路无关。
- 唯一交叉依赖是 `runtime/config`：`storyapp/app.go` 引用了它的 `WriteFile`，而它又引用 `agent` 与 `definition`。这根绳子必须在删除旧包之前切断。

## 3. 定位

> **WIA 是一个 AI 驱动的叙事角色扮演应用。**

不再以 Game-native Agent Runtime 作为代码架构中心。**Agent 是 WIA 的内部能力，不是产品本身。** 产品形态是"开发者准备世界与人物，玩家直接进入、自由行动"的开放型叙事。

以后读代码只有一个入口：**Turn Engine 是绝对主流程。**

## 4. 目标目录

```text
wia/
├── backend/
│   ├── cmd/wia/main.go
│   └── internal/
│       ├── api/          HTTP 接口与请求适配
│       ├── turn/         Turn Engine：一轮故事的主流程
│       ├── world/        领域模型与纯规则
│       ├── agent/        人物决策（Character Agent）
│       ├── context/      某次模型调用应该看到什么
│       ├── memory/       一个角色经历过什么、现在提供什么
│       ├── plot/         世界事件与时间如何向前发展
│       ├── model/        模型调用统一入口与 Provider
│       ├── storage/      全部持久化
│       └── content/      开发者内容工具（剧本包、草稿、发布、导入导出）
├── frontend/             Vue 前端（原 console/web）
├── stories/              随发布交付的示例剧本包
├── docs/
├── scripts/
├── go.mod
└── README.md
```

- **不以包数量作为架构指标。** 判断标准是"是否对应一个稳定职责"。代码少时并入相邻模块即可。
- 模块分三层：核心业务（`turn`、`world`、`agent`、`context`、`memory`、`plot`）、基础设施（`model`、`storage`、`api`）、产品外围（`content`）。
- `stories/` 只放**随发布交付的示例剧本包**；开发者本地草稿与已发布修订属于用户数据目录，不进入仓库。

## 5. 模块职责与依赖规则

| 模块 | 只负责 | 明确不负责 |
| --- | --- | --- |
| `world` | 领域结构与纯规则：世界、场景、人物、玩家、地点、事件、感知、游戏时间 | 不访问数据库、不调用模型、不懂 HTTP |
| `agent` | 人物决策：据本人定义、记忆、感知与获准信息决定说话、行动、保持沉默 | 不写数据库、不直接读全局世界状态 |
| `context` | 组装"这次调用该看到什么"：接收者、用途、阶段、来源授权、预算、必需与可选材料 | 不写数据库、**不执行模型调用** |
| `memory` | 一个角色经历过什么、本轮该提供哪些记忆材料：来源、近期尾部、整理、检索 | 不裁定事件是否发生、不管人物在哪、不管世界时间 |
| `plot` | 世界事件与时间如何向前发展：时间推进、条件判断、计划与事实、生成边界 | 不决定人物怎么回应、不负责叙述 |
| `turn` | 编排：加载、意图、人物阶段、协调、世界推进、叙述、提交 | 不放具体实现细节 |
| `model` | 统一调用入口、用途标记、用量与超时、Provider | 不理解剧情，不知道这是 NPC 还是记忆 |
| `storage` | 持久化、事务、迁移、存档复制 | 不生成剧情、不做业务判断 |
| `api` | HTTP 合同、请求校验、错误映射 | 不直接调用 Provider、不写业务规则 |
| `content` | 开发者内容工具：项目、草稿、校验、发布、导入导出 | 不进入正常 Turn 主链 |

### 5.1 运行流程（谁在什么时候调用模型）

```text
Turn
 ├─ Context Build          （只产材料，不调用模型）
 ├─ Character Agent ──────→ Model
 ├─ Host：协调 ───────────→ Model
 ├─ Host：正文 ───────────→ Model
 ├─ Memory（维护时）────────→ Model
 └─ Plot（需要语义生成时）──→ Model
```

### 5.2 Package 依赖

```text
                  api
                   │
                   ▼
                 turn ──────────────┐
       ┌───────────┼────────────┐   │
       ▼           ▼            ▼   ▼
     agent       memory        plot  storage
       │           │            │
       └─────┬─────┴──────┬─────┘
             ▼            ▼
           context       world
             │
             ▼
           model（仅请求类型）
```

**硬约束：**

```text
world      不依赖 model / storage / api / agent / context / memory / plot
context    不写数据库、不执行模型调用（可依赖 model 的请求类型）
agent      不写数据库
model      不理解剧情
storage    不生成剧情
api        不直接调用 Provider
content    不进入正常 Turn 主链
turn       是唯一同时使用 context / agent / memory / plot / storage 的模块
```

`world` 是**底层语言，不是总服务**。所有模块都依赖它，它不依赖任何模块。

**`world` 只放领域概念，不放"恰好都被用到"的结构体。** 适合：`WorldID`、`EntityID`、`LocationID`、`Character`、`Player`、`Scene`、`Event`、`Perception`、`GameTime`、`Location`。不适合：HTTP 请求/响应结构（属 `api`）、数据库行结构（属 `storage`）、模型输出结构（属 `agent`/`content`）。

> **API DTO、Persistence Row、Model Output 不进入 `world`；在各自边界显式映射。** 否则一年后 `world` 会变成全项目公共 `types.go`。

**`content` 与游玩内核必须隔离。** 依赖方向是 `content → 发布的 StoryDefinition → 世界创建`，不是 `turn ↔ content`。即使整个内容工具被删除，玩家仍能加载 `stories/foo/` 正常游玩。这是架构验收条件之一。

## 6. Turn Engine

### 6.1 薄编排，不是第二个巨型文件

`turn/service.go` 只负责编排，读起来是一条线：

```go
func (s *Service) Run(ctx context.Context, req Request) (Result, error) {
    state    := s.load(ctx, req)
    intent   := s.resolveIntent(ctx, state, req)
    stages   := s.runCharacterStages(ctx, state, intent)
    resolved := s.coordinate(ctx, state, intent, stages)
    world    := s.advanceWorld(ctx, state, resolved)
    script   := s.narrate(ctx, state, resolved, world)
    return s.commit(ctx, state, script)
}
```

实现下沉到同包其他文件：`intent.go`、`stage.go`、`coordinate.go`、`narrate.go`、`commit.go`、`host_prompt.go`、`types.go`。

**主流程集中，不等于所有实现集中。** 把 `storyapp/run.go` 改名为 `turn/service.go` 不算解决问题。

### 6.2 World 变更预约

**模型调用期间不持有 SQL 事务，但必须持有 World 级逻辑预约。** 只靠最终版本校验不够：玩家连点两次时，两个 Turn 会各自花掉一整轮模型费用、让 NPC 基于同一个旧世界思考、在界面上同时出现两个进行中的运行，最后其中一个以版本冲突失败。

因此：

> **一个 Turn 在开始模型工作前取得 World 变更预约，持续到完成、失败或取消；该预约是逻辑边界，不是长时间持有的数据库锁。**

```text
World mutation reservation
      │
      ├── Load snapshot
      ├── Model calls          （无 SQL 事务）
      ├── Stages
      ├── Coordinate
      └── short DB transaction
             ↓
          release
```

- 同一个 `world_id` 同时只允许一个修改权威状态的 Turn；重复请求返回明确的忙碌错误。
- 读取历史、回顾与检索可以并发。
- 建议、摘要等派生任务可以并发，但**只能通过 basis/version 校验发布，不能绕过 Turn 修改权威状态**。
- 纠正、正式 Turn、删除、设置更新、内容发布等可能改变权威状态的操作共享同一 World 变更边界。

这套语义在现有实现中已经存在（活跃运行检查与明确的忙碌错误，覆盖回合、纠正、设置、建议、另存等入口），总体技术方案称为"故事单写者预约"。**收敛要做的是继承它并给它准确的名字，不是重新发明。** 现状与该合同不一致的地方在 R0 记录，并在 R2 修正。

### 6.3 事务边界

顺序固定：

```text
读取快照（无事务）
    ↓
模型调用（无事务；可能几十秒）
    ↓
开事务 → 校验 context_epoch 与 scene_version → 写入 → 提交 → 释放预约
```

**不允许实现成"开事务 → 调模型 → 提交"。** 并发陈旧回合在提交时被拒绝，而不是靠长时间持锁。

### 6.4 Agent 划分

- **Character Agent**（`agent/`）：决定人物这一步做什么。
- **场景主持（Host）**：协调实际结果、环境、背景人物、空间与世界变化，产出最终正文。**它是 Turn Engine 的智能部分，放 `turn/`。**
- 协调与叙述是两个不同职责，分文件实现（`coordinate.go` / `narrate.go`），不合并。

不再保留 `AgentSession`、`ExecutionLane`、`AgentTurn`、`Tool`、`Task`、`Runtime` 作为 WIA 主抽象。

### 6.5 Character 与 World 并发合同

删掉 `ExecutionLane` 之后，它的并发语义必须留下来，否则很容易写出同一个角色在同一阶段被调用两次：

1. **同一个 `world_id` 同时只允许一个修改权威故事状态的 Turn**（由 6.2 的预约保证）。
2. **同一 Stage 中，每个 `entity_id` 最多产生一个 Character Decision。** 不同角色可以基于**同一个冻结快照**并行决策。
3. **同一个角色的下一次决策只能发生在后续 Stage。** 前一 Stage 必须先完成协调，并形成新的获准感知；角色不能在同一 Stage 内基于自己的中间输出再次决策。
4. **派生任务（摘要、建议）可以异步，但只能通过 basis/version 校验发布，不能绕过 Turn 修改权威状态。**

```text
World A
  ├─ Turn 1
  │    ├─ Stage 1      沈岚 ┐
  │    │               铁杉 ┼─ 并行（同一冻结快照）
  │    │               丙   ┘
  │    ├─ Host 协调
  │    └─ Stage 2      沈岚 ┐
  │                    丙   ┘
  ├─ Commit（短事务）
  └─ Turn 2
```

当前阶段不需要 `map[AgentSessionKey]*ExecutionLane`。将来真要做"离场 NPC 独立后台持续行动"时，再引入一个很薄的按 `world_id + entity_id` 键控的执行器即可。

### 6.6 Model 与 purpose

统一入口 `Generator`（`Generate(ctx, Request) (Response, error)`）。业务侧调用带用途标记：

```go
type Call struct {
    Purpose Purpose
    Request Request
}
```

例如 `character_decision`、`scene_narration`、`memory_digest`、`memory_retrieval`、`suggestion`、`event_generation`，日志里可直接看到 `purpose=character_decision recipient=shenlan`。

**`purpose` 属于 ModelService，不属于 Provider。** Provider 不必知道这是 NPC 还是记忆，否则 OpenAI / DeepSeek 实现会开始理解业务概念。

```text
业务 purpose → ModelService → Provider 中立请求 → OpenAI / DeepSeek
```

### 6.7 Context 用 purpose 驱动一个入口

`context` 只回答一句话：**这次模型调用应该看到什么。**

```go
composer.Build(BuildRequest{
    Purpose:   CharacterDecision,
    Recipient: characterID,
    World:     snapshot,
    Stage:     stage,
})
```

不再对外暴露 `BuildNPC()`、`BuildNarrator()`、`BuildMemory()`、`BuildSuggestion()` 这类会持续增长的函数集合。现有实现中值得保留的部分——接收者、用途、阶段、epoch、场景版本、来源授权、token 预算、必需与可选材料——继续沿用。

### 6.8 Prompt 跟职责走

人物决策在 `agent/character_prompt.go`，场景主持与正文在 `turn/host_prompt.go`、`turn/narration_prompt.go`，记忆整理在 `memory/digest_prompt.go`，事件生成在 `plot/event_prompt.go`。

**不要建 `model/prompts.go` 集中堆放。** Prompt 是业务逻辑的一部分。

## 7. 存储

### 7.1 本次只统一代码责任

**本轮不改存储语义。** 保留 `app.db` + `worlds/<id>/world.db`，把全部持久化代码集中到 `backend/internal/storage/`（`app.go`、`world.go`、`copy.go`、`schema.go`、`sqlite.go`）。

效果是"**代码上只有一个 Storage 模块，物理上仍是多数据库**"。

### 7.2 为什么本轮不做单库合并

1. **会削弱一层真实的安全边界。** 现状"一个世界一个文件"顺带保证写错查询也不会串档。单库后 `WHERE world_id = ?` 从性能条件变成正确性与隔离条件，漏一个就是串存档。
2. **另存不再是一致性复制。** 需要逐表克隆并重新绑定 world_id，比整体复制更容易出错。
3. **这是与"去掉旧架构包袱"无关的独立大型存储变更。** 同时做目录重构、包重构、模块合并、Agent 重构与持久化迁移，测试失败时无法判断原因。

**未来何时再考虑：** 进入服务端与多用户阶段时重新设计持久化；本地仍是 SQLite + 世界库、服务器用 PostgreSQL + `world_id`，两者不必物理相同。

### 7.3 标识

**继续只用 `WorldID`，不引入第二个标识。**

> 一个 `WorldID` 表示一份独立、持续推进、可另存的世界状态。

它**不与文件路径绑定**。代码里 `type WorldID string` 即可。现在的物理实现是 `world.db`，将来换 PostgreSQL 不影响它的语义；文档不把"一个世界 = 一个数据库文件"写成契约。

## 8. 执行顺序

分阶段迁移，每阶段有独立验收标准，未通过不进入下一阶段。

### R0：冻结行为基线

在动任何代码之前记录当前行为：后端测试、前端构建、核心回合、私聊与旁观、记忆整理与检索、存档另存/读取/恢复、开放事件生成、并发拒绝（同一世界重复运行）。记录实际命令与实际输出。

同时建立**测试保留表**，对每个现有测试标注处置：

```text
旧测试 → [删除：仅验证已废弃 Game Runtime 行为]
        [迁移：仍是 WIA 需要的合同]
        [替代：已有等价新测试覆盖]
```

例如取消、模型失败、模型配置、数据根、SQLite 失败、幂等、密钥、重试、关闭等行为，虽然原测试命名属于旧 Runtime，但 WIA 仍可能需要。**不能因为删了旧包就把对应质量保护一起删掉。**

**没有 R0，不开始搬目录。** 后续每一步都以此判断"行为是否零变化"。

### R1：仓库形态（只改物理位置）

```text
runtime/            → backend/
console/web         → frontend/
backend/cmd/server  → backend/cmd/wia
```

**先整体搬 `runtime → backend`，不在 R1 修改任何内部 package 边界。**

原因是一个真实的编译限制：Go 的 `internal` 规则要求导入方位于 `internal` 的父目录之下。当前 module 是 `gameagent`，`runtime/internal/storyapi` 导入 `runtime/internal/storyapp`。若只把 `storyapi` 搬到 `backend/internal/api` 而 `storyapp` 留在 `runtime/`，导入方就跑出了 `runtime/` 父目录，**直接编译不过**。整体平移则完全保持这条规则。

`backend/cmd/server → backend/cmd/wia` 是同一棵树内的重命名，安全。

前端搬迁同时处理静态资源嵌入与构建产物路径。

**验收标准：产品行为零变化。** 测试结果与 R0 基线一致，前端构建与静态服务照旧。此时 `backend/internal/` 仍然很脏，这正是 R2–R5 要解决的。

### R2：World 与 Turn 主流程

建立 `world/` 与 `turn/`，先把领域类型与 Run 编排从 `storyapp` 抽出，让 `turn/service.go` 成为主入口，并在 R2 补齐 6.2 的预约语义与 6.3 的事务边界。

验收标准：**打开这个文件能在十分钟内看懂一轮故事。**

### R3：Context / Memory / Agent / Plot / Content 拆分

按 `context` → `agent` → `memory` → `plot` → `content` 顺序抽取，每抽一个就删除 `storyapp` 中对应旧实现。**不建兼容包装层。**

**`storyapp` 不只包含这五项**，因此迁移前先按下面的表逐项确定归属；执行中出现表外文件时先补表再动手，不临时决定。

| 当前 `storyapp` 内容 | 新归属 |
| --- | --- |
| `run.go` 主流程 | `turn/` |
| 场景与阶段上下文装配 | `turn/` + `context/` |
| 人物提示词与决策 | `agent/` |
| ContextComposer、材料、预算 | `context/` |
| 长期记忆、整理、检索、纠正 | `memory/` |
| 剧情、生成事件、世界时间 | `plot/` |
| 领域类型（世界、人物、场景、事件、感知） | `world/` |
| 存储、schema、存档复制 | `storage/` |
| 内容、导入导出、发布、资源 | `content/` |
| 剧本包与定义加载 | `content/` 负责加载，`world/` 提供领域结构 |
| 行动建议 | 先放 `turn/suggestion.go`，增长后再独立 |
| 用量与模型诊断 | `model/` |
| 应用启停与装配 | composition root；必要时再形成薄 `app/` |
| 路人提升 | 已移出产品范围；若保留必须给定明确归属，**不能留在孤儿 `storyapp`** |

**R3 的结束条件是：`storyapp` 每一项职责都有明确新 owner，目录清空并删除。** 不是"四个核心模块抽完了就宣布它消失"。

### R4：Storage

把所有 SQL、schema、迁移、事务、存档复制、AppStore 与 WorldStore 收拢进 `storage/`。**保留 `app.db + N world.db`，只改代码边界。**

### R5：删除旧 Game Runtime

删除 `protocol/`、`gateway/`、`tool/`、`task/`、旧 `agent`、旧 `context`、旧 `memory`、旧 `httpapi`、旧入口，以及 `scenarios/` 中不属于 WIA 的内容。

**删除测试的条件不是"它属于旧包"，而是"被迁移行为的覆盖没有下降"。** 依据 R0 的测试保留表：仅验证已废弃行为的测试可删；仍覆盖 WIA 复用能力的必须迁移或由等价新测试替代后再删；已有新版覆盖的才可以随旧包一起删除。

更新公开事实源：`README.md`、`AGENTS.md`、脚本与构建入口。

这些包的删除是**被依赖关系证明过安全的**：叙事链路对它们零引用。原始 Game Runtime 已独立保存在 `world-is-agent` 仓库，WIA 的 Git 历史也保留了它们。

### R6：文档重组

把 `docs/` 重组为主题式：

```text
docs/
├── ARCHITECTURE.md          本文：架构与依赖规则
├── TURN.md                  一轮故事到底发生什么
├── DATA_MODEL.md            领域模型与持久化结构
├── MEMORY_AND_CONTEXT.md    记忆与上下文合同
├── SPATIAL_MODEL.md         空间模型
└── DEVELOPMENT.md           开发与验证方式
```

`docs/phase12/` 转为历史记录，不再作为架构与空间模型的正式依据。

### LOC：空间系统

**空间是 WIA 核心世界模型，不是内容创作工具的子问题**，因此不继续依附 Phase12 M3。R6 完成后再做区域 / 地点 / 当前位置 / 连接，正式依据写在 `docs/SPATIAL_MODEL.md`。

原因：先把桌子收拾干净，再往上加新的核心领域状态。否则会同时改目录、改位置语义、改数据库 schema，评审无法判断缺陷来自架构迁移还是空间逻辑。

现有未决问题清单（协调字段形状、哪些实体可移动、校验失败处置、事务边界、旧档迁移归属、另存是否带位置、谁知道谁的位置、位置与纠正重建的关系、开销测量、是否触发事件生成）作为输入保留，**在 `SPATIAL_MODEL.md` 中重新定稿，而不是直接沿用旧阶段文档的结论**。

## 9. 本轮明确不做

- **不合并 `app.db` 与 `N world.db` 为单一数据库**（见 7.2）。
- **不重写已验证的存储语义**（世界分库、另存一致性、导出与恢复）。
- **不引入兼容包装层或双实现。**
- **不为了架构美观新建包。**
- **不在本轮加入空间系统、流程型剧情或新的玩家侧创作能力。**
- **不引入第二个世界标识**（见 7.3）。
- **不改已发布的兼容标识**（Go module 路径、proto 包名等），除非单独授权。

## 10. 决策记录

| 决策 | 取舍 |
| --- | --- |
| 从 Game Runtime 架构独立 | Agent 降为内部能力，换取主流程可读、能力单一来源 |
| Turn Engine 为唯一主流程 | 牺牲实现自由度，换"一轮故事可在一个文件读懂" |
| `turn/service.go` 保持薄编排 | 牺牲"所有逻辑集中"的便利，换可读性与可测试性 |
| 保留 World 变更预约 | 牺牲并发自由度，换取不浪费模型费用、不重复思考、界面不出现双运行 |
| 同一 Stage 每角色一次决策 | 牺牲单阶段内迭代，换取决策依据始终来自冻结快照 |
| 模型调用在事务外 | 牺牲"事务包裹一切"的简单心智，换不会长事务锁库 |
| Context 用 purpose 驱动单一入口 | 牺牲显式命名的直观，换不会随用途增长而膨胀的接口 |
| Host 归属 `turn` 而非 `agent` | 承认协调是回合引擎的智能部分，避免 Agent 概念再次膨胀 |
| `world` 只放领域概念 | 牺牲"共用结构体"的便利，换不会退化成公共 `types.go` |
| 只用 `WorldID` | 放弃"存档槽"这个更精确的名字，换取概念唯一、不与文件绑定 |
| 本轮保留多数据库 | 牺牲单库的管理便利，换隔离、另存一致性与迁移风险可控 |
| R1 只改物理位置 | 牺牲一次到位，换取搬迁阶段可证明"行为零变化" |
| 先重构再上空间系统 | 牺牲一点功能推进速度，换缺陷可归因 |

## 11. 规则速查

1. **是不是 WIA 真正需要的？** 只为"未来也许能接游戏"的抽象不做。
2. **这个能力是不是已经有一套？** Context / Memory / Agent 绝不允许出现第二套。
3. **一次 Turn 能不能从一个文件顺着读到底？** 不能，说明架构又开始碎了。
