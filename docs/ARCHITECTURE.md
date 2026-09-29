# WIA 1.0 架构收敛方案

状态：**待执行**。本文定义目标架构与迁移顺序，不代表任何一步已经完成。执行进度与实际证据记录在开发状态类文档中。

本文取代此前"阶段式"文档对架构的描述。现有 `docs/phase12/` 各篇继续作为产品范围、数据合同与历史验证的证据；其中与本文冲突的架构描述以本文为准。文档本身的重组在 R6 完成。

## 1. 完成标准

这次收敛是否成功，只由三条可检查的结果判定：

1. **打开仓库根目录，能立即判断这是 WIA 叙事产品，不是 Game Runtime。** 根目录不再同时出现 `runtime/`、`protocol/`、`console/` 这类由两个产品叠加产生的并列结构。
2. **打开 `backend/internal/turn/service.go`，能在十分钟内看懂一轮故事如何运行。** 主流程在一个文件里顺着读到底，不需要跳十几个文件才能拼出执行顺序。
3. **想找 Agent、Memory、Context、World、Storage、Model 时，每种能力只有一个地方。** 不存在"旧的一套 + 新的一套"。

如果三条都做到，即使总行数没有明显下降，架构收敛也算成功；如果只搬了目录而三条没做到，那就是白做。

## 2. 为什么重构

仓库里同时存在两代产品：

```text
旧：Game-native Agent Runtime
    Agent / Context / Memory / Task / Tool
    gRPC / Protocol / Adapter / Gateway

新：WIA 叙事应用
    Story / NPC / Plot / Memory / Creator / Web
```

结果是同名能力各有一套：

```text
旧 context        + 新 storyapp 的上下文构建
旧 memory         + 新 storyapp 的记忆投影
旧 agent runtime  + storyapp 自己协调 Agent
旧 protocol/gRPC  + 新 HTTP Web
```

这不是代码量问题，是**同一件事有两个权威来源**的问题。叙事功能事实上已经长成独立产品内核（`storyapp` 94 个文件、约 2.2 万行，其中 `run.go` 约 1.5 千行），却仍被放在旧 Runtime 的目录与命名之下。

已核实的依赖事实：

- `storyapp` 与 `storyapi` **对旧运行时零引用**；旧包只被旧入口 `runtime/cmd/server` 及它们彼此引用。
- 旧包（`agent`、`gateway`、`task`、`tool`、`context`、`memory`、`httpapi`、`session`、`trace`、`traceview`、`bootstrap`、`browser`、`dataroot`、`definition`、`protocol/`）合计约 **7 万行**，与叙事链路无关。
- 唯一交叉依赖是 `runtime/config`：`storyapp/app.go` 引用了它的 `WriteFile`，而它又引用 `agent` 与 `definition`。这一根绳子必须在删除旧包之前切断。

## 3. 定位

> **WIA 是一个 AI 驱动的叙事角色扮演应用。**

不再以 Game-native Agent Runtime 作为代码架构中心。**Agent 是 WIA 的内部能力，不是产品本身。** 产品形态是"开发者准备世界与人物，玩家直接进入、自由行动"的开放型叙事。

架构图随之变为：

```text
Frontend
   ↓ HTTP
API
   ↓
Turn Engine
   ├─ Agent（人物决策 / 场景主持）
   ├─ Context
   ├─ Memory
   └─ Plot / World
   ↓
Model
   ↓
Storage
```

以后读代码只有一个入口：**Turn Engine 是绝对主流程。**

## 4. 目标目录

```text
wia/
├── backend/
│   ├── cmd/
│   │   └── wia/
│   │       └── main.go
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
│   ├── ARCHITECTURE.md   本文
│   ├── TURN.md           一轮故事到底发生什么
│   ├── DATA_MODEL.md     领域模型与持久化结构
│   ├── MEMORY_AND_CONTEXT.md
│   └── DEVELOPMENT.md
├── scripts/
├── go.mod
└── README.md
```

说明：

- **不以包数量作为架构指标。** 判断标准是"是否对应一个稳定职责"。`config` 这类代码少时并入 app/bootstrap 即可；`suggestion` 若增长可以独立；`auth` 在服务端阶段很可能值得独立。
- 模块分三层：核心业务（`turn`、`world`、`agent`、`context`、`memory`、`plot`）、基础设施（`model`、`storage`、`api`）、产品外围（`content`）。
- `stories/` 只放**随发布交付的示例剧本包**。开发者本地草稿与已发布修订属于用户数据目录，不进入仓库。

## 5. 模块职责与依赖规则

职责表：

| 模块 | 只负责 | 明确不负责 |
| --- | --- | --- |
| `world` | 领域结构与纯规则：世界、场景、人物、玩家、地点、事件、感知、游戏时间 | 不访问数据库、不调用模型、不懂 HTTP |
| `agent` | 人物决策：据本人定义、记忆、感知与获准信息决定说话、行动、保持沉默 | 不写数据库、不直接读全局世界状态 |
| `context` | 组装"这次调用该看到什么"：接收者、用途、阶段、来源授权、预算、必需与可选材料 | 不写数据库、不调用模型 |
| `memory` | 一个角色经历过什么，本轮该提供哪些记忆材料：来源、近期尾部、整理、检索 | 不裁定事件是否发生、不管人物在哪、不管世界时间 |
| `plot` | 世界事件与时间如何向前发展：时间推进、条件判断、计划与事实、生成边界 | 不决定人物怎么回应、不负责叙述 |
| `turn` | 编排：加载、意图、人物阶段、协调、世界推进、叙述、提交 | 不放具体实现细节 |
| `model` | 统一调用入口、用途标记、用量与超时、Provider | 不理解剧情，不知道这是 NPC 还是记忆 |
| `storage` | 持久化、事务、迁移、存档复制 | 不生成剧情、不做业务判断 |
| `api` | HTTP 合同、请求校验、错误映射 | 不直接调用 Provider、不写业务规则 |
| `content` | 开发者内容工具：项目、草稿、校验、发布、导入导出 | 不进入正常 Turn 主链 |

**依赖规则比目录名字更重要，以下按硬约束执行：**

```text
world      不依赖 model / storage / api / agent / context / memory / plot
context    不写数据库、不调用模型
agent      不写数据库
model      不理解剧情
storage    不生成剧情
api        不直接调用 Provider
content    不进入正常 Turn 主链
```

依赖方向：

```text
                  api
                   │
                   ▼
                 turn
       ┌───────────┼────────────┐
       ▼           ▼            ▼
     agent       memory        plot
       │           │            │
       └─────┬─────┴──────┬─────┘
             ▼            ▼
           context       world
             │
             ▼
           model

 turn / memory / content
             │
             ▼
         storage
```

`world` 是**底层语言，不是总服务**。所有模块都依赖它，它不依赖任何模块，否则很快形成循环。

**`content` 与游玩内核必须隔离。** 依赖方向是 `content → 发布的 StoryDefinition → 世界创建`，不是 `turn ↔ content`。即使整个内容工具明天被删除，玩家仍能加载 `stories/foo/` 正常游玩。这是架构验收条件之一。

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

具体实现下沉到同包其他文件：

```text
turn/
├── service.go      # 一眼看懂主流程
├── intent.go
├── stage.go
├── coordinate.go
├── narrate.go
├── commit.go
├── host_prompt.go  # 场景主持的 prompt
└── types.go
```

**主流程集中，不等于所有实现集中。** 如果重构只是把 `storyapp/run.go` 的大文件改名为 `turn/service.go`，问题没有解决。

### 6.2 事务边界必须写死

模型调用不在事务内。顺序固定为：

```text
读取快照（无事务）
    ↓
模型调用（无事务；可能几十秒）
    ↓
开事务 → 校验 context_epoch 与 scene_version → 写入 → 提交
```

并发陈旧回合在提交时被拒绝，而不是靠长时间持锁。这条经验来自现有实现，必须原样保留，不允许实现成"开事务 → 调模型 → 提交"。

### 6.3 Agent 划分

WIA 只有一种真正的 Agent：

- **Character Agent**（`agent/`）：决定人物这一步做什么。
- **场景主持（Host）**：协调实际结果、环境、背景人物、空间与世界变化，产出最终正文。

**场景主持本质上就是 Turn Engine 的智能部分，因此放在 `turn/`，不放进 `agent/`。** 协调结果与正文叙述是两个不同职责，分文件实现（`coordinate.go` / `narrate.go`），不合并——过去把角色决定、结果协调与正文混在一起正是缺陷来源。

不再保留 `AgentSession`、`ExecutionLane`、`AgentTurn`、`Tool`、`Task`、`Runtime` 作为 WIA 的主抽象；它们是为"任意游戏 Adapter 都能接"服务的。

### 6.4 Model 与 purpose

统一入口：

```go
type Generator interface {
    Generate(ctx context.Context, req Request) (Response, error)
}
```

业务侧调用带用途标记：

```go
type Call struct {
    Purpose Purpose
    Request Request
}
```

例如 `character_decision`、`scene_narration`、`memory_digest`、`memory_retrieval`、`suggestion`、`event_generation`，日志里可以直接看到 `purpose=character_decision recipient=shenlan`。

**`purpose` 属于 ModelService，不属于 Provider。** Provider 不必知道这是 NPC 还是记忆，否则 OpenAI / DeepSeek 实现会开始理解业务概念。依赖方向固定为：

```text
业务 purpose → ModelService → Provider 中立请求 → OpenAI / DeepSeek
```

### 6.5 Context 用 purpose 驱动一个入口

`context` 只回答一句话：**这次模型调用应该看到什么。** 对外是一个 purpose 驱动的入口，内部再按用途选择构建逻辑：

```go
composer.Build(BuildRequest{
    Purpose:   CharacterDecision,
    Recipient: characterID,
    World:     snapshot,
    Stage:     stage,
})
```

不再对外暴露 `BuildNPC()`、`BuildNarrator()`、`BuildMemory()`、`BuildSuggestion()` 这类会持续增长的函数集合。现有实现中值得保留的部分——接收者、用途、阶段、epoch、场景版本、来源授权、token 预算、必需与可选材料——继续沿用。

### 6.6 Prompt 跟职责走

Prompt 属于谁就放在谁那里：人物决策在 `agent/character_prompt.go`，场景主持与正文在 `turn/host_prompt.go`、`turn/narration_prompt.go`，记忆整理在 `memory/digest_prompt.go`，事件生成在 `plot/event_prompt.go`。

**不要建 `model/prompts.go` 集中堆放。** Prompt 本身就是业务逻辑的一部分，集中堆放会让"到底是 Context、Prompt 还是 Model 的问题"更难排查。

## 7. 存储

### 7.1 本次只统一代码责任

**本轮不改存储语义。** 保留现有的：

```text
app.db
worlds/
  <world-a>/world.db
  <world-b>/world.db
```

把全部持久化代码集中到 `backend/internal/storage/`：

```text
storage/
├── app.go
├── world.go
├── copy.go
├── schema.go
└── sqlite.go
```

效果是"**代码上只有一个 Storage 模块，物理上仍是多数据库**"。

### 7.2 为什么本轮不做单库合并

单库在表与连接管理上确实更简单，但代价与本轮目标冲突：

1. **会削弱一层真实的安全边界。** 现状"一个世界一个文件"顺带保证写错查询也不会串档。单库后 `WHERE world_id = ?` 从性能条件变成正确性与隔离条件，所有查询（事件、感知、摘要、纠正、消息、回合、场景、剧情、人物）都必须永远带上它，漏一个就是串存档。
2. **另存不再是一致性复制。** 需要逐表克隆并重新绑定 world_id，比现在的整体复制更容易出错。
3. **这是与"去掉旧架构包袱"无关的独立大型存储变更。** 同时做目录重构、包依赖重构、Context/Memory 合并、Agent 重构和持久化模型迁移，最后测试失败时很难判断是搬包引起的还是 DB 模型变化引起的。

**未来何时再考虑：** 进入服务端与多用户阶段时重新设计持久化，很可能本地仍是 SQLite + 世界库、服务器用 PostgreSQL + `world_id`，两者不必物理相同。

### 7.3 存档语义与命名

"一个世界是一份存档"这个语义**必须保留**；要去掉的只是"一份存档必须等于一个数据库文件"的隐含假设。领域模型里用 `WorldSaveID` 这类名字表达"存档槽"，避免后来者把它理解为文件标识。

另存、导出与恢复的合同不变：另保存档是独立完整的世界，导出是可携带归档，失败时不留半个世界。

## 8. 执行顺序

分阶段迁移，不做一次性重写。每阶段有独立验收标准，未通过不进入下一阶段。

### R0：冻结行为基线

在动任何代码之前，记录当前行为基线：后端测试、前端构建、核心回合、私聊与旁观、记忆整理与检索、存档另存/读取/恢复、开放事件生成。记录实际命令与实际结果。

**没有 R0，不开始搬目录。** 后续每一步都以此判断"行为是否零变化"。

### R1：仓库形态

只做位置与命名：`console/web → frontend`、`runtime/cmd/server → backend/cmd/wia`、`runtime/internal/storyapi → backend/internal/api`。

前置条件：切断 `runtime/config` 这根交叉依赖——把 `storyapp` 实际使用的部分（模型配置写入）搬入新结构，使叙事链路不再引用旧包。

**不要同时拆 `storyapp`。** 验收标准是**产品行为零变化**：测试结果与 R0 基线一致，前端构建与静态服务照旧。

### R2：领域层与 Turn 主流程

建立 `world/` 与 `turn/`，先把领域类型与 Run 编排从 `storyapp` 抽出，让 `turn/service.go` 成为主入口。

验收标准就是完成标准第 2 条：**打开这个文件能在十分钟内看懂一轮故事。**

### R3：Context / Agent / Memory / Plot

按 `context` → `agent` → `memory` → `plot` 顺序抽取，每抽一个就删除 `storyapp` 中对应旧实现。**不建兼容包装层**，Git 历史就是归档。

最终 `storyapp` 应当消失。

### R4：Storage

把所有 SQL、schema、迁移、事务、存档复制、AppStore 与 WorldStore 收拢进 `storage/`。

**保留 `app.db + N world.db`，只改代码边界。**

### R5：删除旧 Game Runtime

删除 `protocol/`、`gateway/`、`tool/`、`task/`、旧 `agent`、旧 `context`、旧 `memory`、旧 `httpapi`，以及 `scenarios/` 中不属于 WIA 的内容；删除旧入口与旧验收测试。

更新公开事实源：`README.md`、`ARCHITECTURE.md`、`docs/`、`AGENTS.md`、CI 与脚本。

这些包的删除是**被依赖关系证明过安全的**：叙事链路对它们零引用。原始 Game Runtime 已独立保存在 `world-is-agent` 仓库，WIA 的 Git 历史也保留了它们，因此不需要额外打标签。

### R6：文档重组与空间系统

先把 `docs/` 重组为主题式（本文、`TURN.md`、`DATA_MODEL.md`、`MEMORY_AND_CONTEXT.md`、`DEVELOPMENT.md`），把 `docs/phase12/` 转为历史。

**然后再做空间系统**（区域 / 地点 / 当前位置 / 连接）。原因：先把桌子收拾干净，再往上加新的核心领域状态。否则会同时改目录、改位置语义、改数据库 schema，评审无法判断缺陷来自架构迁移还是空间逻辑。

空间系统的未决问题清单见 [M3 技术方案的待定清单](phase12/stages/M3-完整玩家工作台.md)。

## 9. 本轮明确不做

- **不合并 `app.db` 与 `N world.db` 为单一数据库。** 见 7.2。
- **不重写已验证的存储语义**（世界分库、另存一致性、导出与恢复）。
- **不引入兼容包装层或双实现。** 旧实现直接删除。
- **不为了架构美观新建包。** 是否独立取决于是否存在稳定职责。
- **不在本轮加入空间系统、流程型剧情或新的玩家侧创作能力。**
- **不改已发布的兼容标识**（Go module 路径、proto 包名等），除非单独授权。

## 10. 决策记录

| 决策 | 取舍 |
| --- | --- |
| 从 Game Runtime 架构独立 | Agent 降为内部能力，换取主流程可读、能力单一来源 |
| `backend` / `frontend` 分目录 | 牺牲一点历史目录连续性，换根目录语义清晰 |
| Turn Engine 为唯一主流程 | 牺牲实现自由度，换"一轮故事可在一个文件读懂" |
| `turn/service.go` 保持薄编排 | 牺牲"所有逻辑集中"的便利，换可读性与可测试性 |
| Context 用 purpose 驱动单一入口 | 牺牲显式命名的直观，换不会随用途增长而膨胀的接口 |
| 场景主持归属 `turn` 而非 `agent` | 承认协调是回合引擎的智能部分，避免 Agent 概念再次膨胀 |
| 本轮保留多数据库 | 牺牲单库的管理便利，换隔离、另存一致性与迁移风险可控 |
| Storage 只统一代码责任 | 物理结构不变，代价是"模块统一但文件仍多" |
| 先重构再上空间系统 | 牺牲一点功能推进速度，换缺陷可归因 |

## 11. 规则速查

以后新增功能时用三个问题自检：

1. **是不是 WIA 真正需要的？** 只为"未来也许能接游戏"的抽象不做。
2. **这个能力是不是已经有一套？** Context / Memory / Agent 绝不允许出现第二套。
3. **一次 Turn 能不能从一个文件顺着读到底？** 不能，说明架构又开始碎了。
