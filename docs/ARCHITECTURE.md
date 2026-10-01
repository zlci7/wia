# WIA 1.0 架构收敛方案

状态：**主体收敛已完成**。R0–R3 与 R5 已执行，`storyapp` 和旧 Game Runtime 已删除；R6 文档重组尚未开始。实际证据见 [Phase12 开发状态](phase12/开发状态.md)，本文件保留迁移顺序作为历史决策记录。

本文取代此前"阶段式"文档对架构的描述。现有 `docs/phase12/` 继续作为产品范围、数据合同与历史验证的证据；其中与本文冲突的架构描述以本文为准。文档重组在 R6 完成。

## 1. 完成标准

这次收敛是否成功，只由三条可检查的结果判定：

1. **打开仓库根目录，能立即判断这是 WIA 叙事产品，不是 Game Runtime。**
2. **打开 `backend/internal/turn/service.go`，能在十分钟内看懂一轮故事如何运行。**
3. **想找 Agent、Memory、Context、World、Storage、Model 时，每种能力只有一个地方。**

三条都做到，即使总行数没有明显下降，收敛也算成功；只搬了目录而三条没做到，就是白做。

## 2. 为什么重构

收敛前仓库同时存在两代产品：Game-native Agent Runtime（Agent / Context / Memory / Task / Tool / gRPC / Protocol / Adapter / Gateway）与 WIA 叙事应用（Story / NPC / Plot / Memory / Creator / Web）。当时同名能力各有一套：旧 context 与新上下文构建、旧 memory 与新记忆投影、旧 agent runtime 与叙事协调、旧 gRPC 与新 HTTP。

收敛解决的是同一能力存在两个权威来源的问题。当前叙事职责归入 `turn`、`memory`、`content` 与应用服务层 `app`。

删除前核实的依赖事实：

- `storyapp` 与 `storyapi` **对旧运行时零引用**；旧包只被旧入口 `runtime/cmd/server` 及它们彼此引用。
- 旧包（`agent`、`gateway`、`task`、`tool`、`context`、`memory`、`httpapi`、`session`、`trace`、`traceview`、`bootstrap`、`browser`、`dataroot`、`definition`、`protocol/`）合计约 **7 万行**，与叙事链路无关。
- 当时唯一的交叉依赖是 `runtime/config`：`storyapp/app.go` 引用了它的 `WriteFile`，而它又引用 `agent` 与 `definition`。这条依赖已由 `atomicfile` 取代后随旧包删除。

## 3. 定位

> **WIA 是一个 AI 驱动的叙事角色扮演应用。**

不再以 Game-native Agent Runtime 作为代码架构中心。**Agent 是 WIA 的内部能力，不是产品本身。** 产品形态是"开发者准备世界与人物，玩家直接进入、自由行动"的开放型叙事。

以后读代码只有一个入口：**Turn Engine 是绝对主流程。**

## 4. 当前目录

```text
wia/
├── backend/
│   ├── cmd/wia/main.go
│   └── internal/
│       ├── storyapi/     HTTP 接口与请求适配
│       ├── app/          应用用例、生命周期、世界操作与最终提交
│       ├── turn/         Turn Engine：冻结输入、上下文材料、人物决策与一轮故事主流程
│       ├── world/        领域模型与纯规则
│       ├── memory/       一个角色经历过什么、现在提供什么
│       ├── plot/         世界事件与时间如何向前发展
│       ├── story/        冻结的运行定义
│       ├── model/        中立的模型请求、响应与预算合同
│       ├── llm/          Provider 配置与 OpenAI / DeepSeek 接入
│       ├── storage/      世界库 schema、具名读写与事务原语
│       ├── wire/         无领域含义的基础原语（文本规范、时间戳、JSON、标识生成）
│       └── content/      开发者内容工具与内置剧本包
├── frontend/             Vue 前端（原 console/web）
├── docs/
├── scripts/
├── go.mod
└── README.md
```

- **不以包数量作为架构指标。** 判断标准是"是否对应一个稳定职责"。代码少时并入相邻模块即可。
- 模块分三层：核心业务（`turn`、`world`、`memory`、`plot`、`story`）、基础设施（`model`、`storage`、`storyapi`）、产品外围（`content`、`app`）。人物决策与 purpose 驱动的上下文装配都属于一轮故事，代码量不足以证明需要独立 package，因此保留在 `turn`。
- 内置剧本位于 `backend/internal/content/packs/lantern-dusk`、`orbital-repair` 与 `mist-embers`；开发者本地草稿、外部剧本包与已发布修订属于用户数据目录。

## 5. 模块职责与依赖规则

| 模块 | 只负责 | 明确不负责 |
| --- | --- | --- |
| `world` | 领域结构与纯规则：世界、场景、人物、玩家、地点、事件、感知、游戏时间 | 不访问数据库、不调用模型、不懂 HTTP |
| `memory` | 一个角色经历过什么、本轮该提供哪些记忆材料：来源、近期尾部、整理、检索 | 不裁定事件是否发生、不管人物在哪、不管世界时间 |
| `plot` | 世界事件与时间如何向前发展：时间推进、条件判断、计划与事实、生成边界 | 不决定人物怎么回应、不负责叙述 |
| `story` | 世界创建时冻结的运行定义：人物模板、地点、剧情与设置 | 不加载外部包、不访问数据库、不执行回合 |
| `turn` | 加载冻结输入、按用途组装上下文、人物决策、协调、世界推进、叙述与输出 | 不管理应用生命周期、不处理 HTTP |
| `model` / `llm` | 中立的请求响应、容量与错误合同 / Provider 配置和协议接入 | 不理解剧情，不知道这是 NPC 还是记忆 |
| `storage` | 持久化、事务、迁移、存档复制 | 不生成剧情、不做业务判断 |
| `wire` | 无领域含义的基础原语：文本规范、时间戳、JSON 编码、标识生成 | 不放领域概念、不做业务判断、不依赖任何业务包 |
| `storyapi` | HTTP 合同、请求校验、错误映射 | 不直接调用 Provider、不写业务规则 |
| `content` | 开发者内容工具：项目、草稿、校验、发布、导入导出 | 不进入正常 Turn 主链 |
| `app` | 应用用例、世界生命周期、回合准入与最终提交、纠正与重建作业、建议状态、模型配置和计量 | 不重复实现人物决策、上下文装配或内容发布 |

### 5.1 运行流程（谁在什么时候调用模型）

```text
app.SubmitRun → app.runWorker
 ├─ turn.Service.Execute
 │   ├─ Load Snapshot + Memory
 │   │   └─ Memory Maintenance ─→ Model（需要整理时；发布摘要使用短事务）
 │   ├─ Intent ───────────────→ Model
 │   ├─ Character Decisions ──→ Model
 │   ├─ Coordination / Plot ──→ Model
 │   ├─ Narration ────────────→ Model
 │   └─ Record Experience → Output
 └─ app.commitTurn → 校验运行状态及版本 → 原子写入
```

### 5.2 Package 依赖

#### 回合内的可选能力

`turn` 使用现有冻结能力清单接入状态、关系、物品与判定。模块通过具名函数和类型协作，保持单一主流程与应用层最终事务。

| 能力 | 实现入口 | 输入与输出 |
| --- | --- | --- |
| 状态更新 | `turn/state_effects.go` | 定义、当前值、模型或规则变化 → 新值与变化记录 |
| 有向关系 | `turn/relationships.go` | 本人的已知经历与本人提案 → 有向关系变化 |
| 物品归属 | `turn/items.go` | 实例当前归属与模型转移结果 → 唯一新归属与转移记录 |
| 固定判定与投掷 | `turn/rules.go` | 选中规则、稳定输入及快照 → 可复用判定结果、可选状态效果 |
| 条件读取 | `turn/fact_conditions.go` | 已有世界事实与显式条件 → 是否满足及证据 |
| 能力接入 | `turn/capabilities.go`、`turn/mechanics.go` | 装配各能力的上下文合同，隔离应用整组候选 |
| 状态展示 | `frontend/src/components/StatePanel.vue` | 服务端公开投影 → 状态名称、值与单位 |

普通行动的因果、物品转移方式和日常状态变化由模型判断；程序保证身份引用、类型、声明范围、来源与提交一致性。作者明确选择固定规则或数值预算时执行对应合同。场内与场外协调使用相同能力入口，各次模型调用获得独立完整的材料。状态展示不访问模型，不解析正文，不参与判定。

各能力不直接调用彼此的持久化操作。模型调用位于事务外；判定准备保留短事务，世界后果随应用层最终事务提交。没有判定规则时仍能更新状态，没有状态效果时仍能进行投掷。

```text
               storyapi
                   │
                   ▼
                  app
                   │
                   ▼
                 turn ──────────────┐
       ┌───────────┼────────────┐   │
       ▼           ▼            ▼   ▼
     memory       story         plot  storage
       │           │            │
       └───────────┴──────┬─────┘
                          ▼
                        world

                 turn ───────────→ model
```

**硬约束：**

```text
world      不依赖 model / storage / storyapi / turn / memory / plot
turn 的上下文装配与人物决策不写数据库；输入加载经 storage 读取，来源元数据查询仍使用受控 SQL
model      不理解剧情
storage    不生成剧情
storyapi   不直接调用 Provider
content    不进入正常 Turn 主链
wire       只依赖标准库；不放领域概念
plot       不 import content：剧情规则是权威，内容包只是调用方
story      只依赖 world / plot；不得长出 Service / Loader / Repository
turn       是唯一回合主流程；人物决策、上下文材料、memory / plot / storage 的交接都在这里
app        拥有应用用例与跨领域事务，不复制人物决策、上下文装配或内容发布
```

**四层归属**：

```text
content  owns external package representation   （JSON/schema/assets/digest/import/export，可删除）
story    owns normalized immutable runtime definition
world    owns mutable / committed world vocabulary
plot     owns world-progression rules
turn     consumes story + world + plot
```

依赖方向：

```text
content ─────→ story ←───── turn
   │              │
   └────→ plot ←──┘
                  │
                world
```

**为什么运行定义单独成层**：`World` 是已经运行起来的世界状态，`Story Definition` 是这个世界依据什么剧本规则初始化与运行——像类定义与对象实例的关系。把 `gameDefinition` 放 `content` 会让 `turn → content`，使内容工具成为游玩内核的 runtime dependency，与"删掉整个内容工具后玩家仍能游玩"冲突；放 `world` 则会形成 `world → plot`，破坏"`world` 只依赖标准库"。

**Pack DTO 与 Runtime Definition 必须真正分开。** `story.Definition` 不得引用 `content.PackLocation` / `content.PackBystander` / `content.GameSummary`，否则等于 `story → content` 绕回去。`content.Load` 产出 `content.LoadedPack`（含 cover、digest、root、raw pack、assets），其中**包含**一份 `story.Definition`；`turn` 只拿 `pack.Definition`。运行定义与外部格式初始可能字段相同，但职责不同，格式一旦长出地区层级或 v3 归一化就会分化。

**`content` 与 `plot` 的分工**：`content` 负责**内容包是否合法**（JSON 与 schema、字段类型、文件大小、资源路径、剧本包引用的 NPC 与地点是否存在），`plot` 负责**剧情定义本身是否合法**（节点、依赖、时间边界、条件、状态枚举）。依赖方向是 `content → plot` 与 `turn → plot`，**`plot` 永不 import `content`**。

若让 `content` 自己实现依赖与时间校验，运行时的 `plot` 又理解一遍，就会出现"发布时允许、运行时拒绝"或"运行时支持新规则、`content` 忘了同步"——违背"每种能力只能有一套"。

校验按问题拆开，调用方只问它需要的那一个：

```go
plot.ValidateDefinition(def)          // 发布/加载剧本包：剧情图本身是否合法
plot.ValidateProgress(def, progress)  // 运行时读档：进度是否与定义一致
plot.Validate(def, progress)          // 两者都要时
```

**不要为了校验定义而伪造一个进度对象**——那正是拆分的理由。

`plot` 返回自己的 `ErrInvalidDefinition` / `ErrInvalidProgress`，不返回上层错误。同一个剧情校验失败，在发布入口是"内容非法"、在运行入口是"存档不可读"、在 HTTP 层是某个状态码，**映射属于各入口**。

`world` 是**底层语言，不是总服务**。所有模块都依赖它，它不依赖任何模块。

`wire` 是**命名了领域概念之后剩下的东西**。分界：领域概念（`Character`、`Event`、`GameTime`）与读它们的纯规则进 `world`；文本清洗、时间戳、JSON 编码、标识生成这类没有领域含义的原语进 `wire`。把 `cleanText` 放进 `world` 会让只想清洗字符串的包必须依赖领域类型所在的包。

**`world` 只放领域概念，不放"恰好都被用到"的结构体。** 适合：`WorldID`、`EntityID`、`LocationID`、`Character`、`Player`、`Scene`、`Event`、`Perception`、`GameTime`、`Location`。不适合：HTTP 请求/响应结构（属 `api`）、数据库行结构（属 `storage`）、模型输出结构（属 `agent`/`content`）。

> **API DTO、Persistence Row、Model Output 不进入 `world`；在各自边界显式映射。** 否则一年后 `world` 会变成全项目公共 `types.go`。

**`content` 与游玩内核必须隔离。** `app` 在世界创建时把内容包转为冻结的 `story.Definition`；正常 Turn 从世界库读取该定义，不访问创作工作区。`turn` 不依赖 `content`。

## 6. Turn Engine

### 6.1 薄编排，不是第二个巨型文件

`turn/service.go` 编排回合生成，顺序如下：

```text
load → resolveIntent → runCharacters → notePlayerAction
     → coordinate（场景协调与世界推进）→ narrate
     → recordPlayerExperience → Output
```

阶段实现位于 `load.go`、`memory_store.go`、`stages.go`、`coordinate.go`、`progression.go` 与 `experience.go`。`Execute` 成功表示产出了待提交结果；`app/run.go` 的 `runWorker` 调用 `app/store.go` 的 `commitTurn` 才完成持久化。

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

- **Character Agent**（`turn/stages.go`）：按每个人物获准的材料决定其说话与行动。
- **场景主持（Host）**：协调实际结果、环境、背景人物、空间与世界变化，产出最终正文。**它是 Turn Engine 的智能部分，放 `turn/`。**
- 协调与叙述分别由 `turn/coordinate.go` 与 `turn/stages.go` 的命名阶段执行。

不再保留 `AgentSession`、`ExecutionLane`、`AgentTurn`、`Tool`、`Task`、`Runtime` 作为 WIA 主抽象。

### 6.5 Character 与 World 并发合同

删掉 `ExecutionLane` 之后，它的并发语义必须留下来，否则很容易写出同一个角色在同一阶段被调用两次：

1. **同一个 `world_id` 同时只允许一个修改权威故事状态的 Turn**（由 6.2 的预约保证）。
2. **同一 Stage 中，每个 `entity_id` 最多产生一个 Character Decision。** 不同角色可以基于**同一个冻结快照**并行决策。
3. **同一个角色的下一次决策只能发生在后续 Stage。** 前一 Stage 必须先完成汇合，再按接收者形成获准感知；角色不能在同一 Stage 内基于自己的中间输出再次决策。
4. **派生任务（摘要、建议）可以异步，但只能通过 basis/version 校验发布，不能绕过 Turn 修改权威状态。**

```text
World A
  ├─ Turn 1
  │    ├─ Stage 1      沈岚 ┐
  │    │               铁杉 ┼─ 并行（同一冻结快照）
  │    │               丙   ┘
  │    ├─ 汇合与接收者感知投影
  │    ├─ Stage 2      沈岚 ┐
  │    │               丙   ┘
  │    └─ 场景协调、世界推进与叙述
  ├─ Commit（短事务）
  └─ Turn 2
```

当前阶段不需要 `map[AgentSessionKey]*ExecutionLane`。将来真要做"离场 NPC 独立后台持续行动"时，再引入一个很薄的按 `world_id + entity_id` 键控的执行器即可。

### 6.6 Model 与 purpose

统一生成接口为 `model.TextGenerator.GenerateText(ctx, TextRequest) (TextResponse, error)`。`turn.ContextGenerator` 在调用前完成材料预算与报告，通过 `ContextScope` 携带用途、接收者、阶段、版本和来源；`app.meteredText` 记录用量。`llm` 提供配置加载及 OpenAI / DeepSeek 协议实现。

业务用途如 `intent`、`npc`、`coordination`、`narration`、`memory_digest` 保留在调用作用域中。Provider 接收中立的 `TextRequest`，不承担人物或记忆规则。

```text
turn.ContextGenerator → app.meteredText → model.TextGenerator → llm Provider
```

### 6.7 Context 用 purpose 驱动一个入口

上下文装配位于 `turn`，只回答一句话：**这次模型调用应该看到什么。** 各用途先形成 `Material`，由 `ContextGenerator` 统一调用 `ContextComposer.Build(material, system, output)` 完成容量校验和来源报告。

接收者、用途、阶段、epoch、场景版本与模板放在 `ContextScope`；必需与可选文本及其来源放在 `Material`。人物决策、协调、正文、摘要和建议共用同一个生产构建入口。

### 6.8 Prompt 跟职责走

人物决策、场景主持与正文材料位于 `turn/compose.go`，共享指令位于 `turn/prompt.go`；记忆整理模型合同位于 `turn/memory_store.go`，事件生成合同位于 `turn/progression.go`。`memory` 拥有记录规则，`plot` 拥有剧情纯规则，它们均不调用模型。

**不要建 `model/prompts.go` 集中堆放。** Prompt 是业务逻辑的一部分。

### 6.9 渐进材料与持续世界（规划）

本节为待实现的扩展。数据合同与两个交付模块见[渐进材料与持续世界技术方案](phase12/stages/渐进材料与持续世界-技术方案.md)，作者格式见[剧本编写规范](phase12/stages/开发者剧本编写规范与模板.md)。

| 归属 | 扩展职责 |
| --- | --- |
| `content` | 读取入口、分文件配置和文字材料，校验引用、权限声明与内容摘要，编译运行定义 |
| `story` | 持有不可变材料索引与正文、初始发展和人物计划；保持纯数据与校验边界 |
| `turn` | 按用途、知情范围与相关性选取冻结材料，通过现有 Context 构建入口控制预算；在已有世界阶段调度到期计划、人物决定和结果投影 |
| `world` / `plot` | 持有运行计划、事件和感知的领域词汇及纯规则；保留既有冻结节点的读取语义 |
| `storage` | 读写冻结材料、当前计划、来源依赖及个人投影；不判断剧情 |
| `app` | 管理运行并在原有最终事务中提交全部变化 |

包解析、材料全文冻结与模型输入选择分别处理。Turn 从世界库读取冻结内容，按需装配不访问作者工作目录。材料补充请求仍属于当前用途，只有最终候选形成一次人物决定。

位置、状态、关系和物品继续通过已有能力入口提供最新工作态。世界推进共用人物决策、行动协调与最终提交路径；同一回合保持有界推进，不增加后台模拟器。个人感知共用一套授权与投影合同，正文只消费玩家投影。以上扩展落在现有包内，不增加第二套 Context、事件引擎或创作系统运行依赖。

## 7. 存储

### 7.1 本次只统一代码责任

**存储语义保持不变。** `app.db` 管理应用目录和操作，世界各有独立 `world.db`。`storage` 提供世界库 schema、具名读写与事务原语；应用表 schema、跨领域事务和部分 SQL 仍由 `app`、`content` 持有。

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

**执行中的偏离（记录，不修改原计划结论）**：实际勘察后确认，`turn` 若此刻抽成独立包，需要调用 `storyapp` 约 20 个未导出方法与字段；跨包意味着把这些全部导出并把 `App` 拆成接口，属于深改而非搬移，风险远超一个阶段。因此 R2 拆成两步：

1. **包内完成流水线提取（已完成）**：`executeTurn` 改为按顺序调用命名阶段，各阶段实现各自成函数，主流程可通读；行为零变化。阅读入口此时仍是 `storyapp/run.go`，其中 `executeTurn` 就是那条流水线。
2. **模块边界（已完成）**：编排进入独立 `turn`，领域词汇进入 `world`，冻结运行定义进入 `story`；D2 后输入加载也由 `turn` 持有，`Host` 只剩 `LogStage`。

`world/` 的领域类型已随模块边界一次迁移完成，依赖守卫要求它只使用标准库。

### R3：Context / Memory / Agent / Plot / Content 拆分

本节记录原抽取计划。实际执行按依赖闭包收敛为 `turn`（上下文材料、人物决策、主流程）、`memory`、`plot`、`content`、`story`、`world` 与应用服务层 `app`，并在 D2 删除 `storyapp`。**未建兼容包装层。**

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

### R4：Storage 边界

Storage 已拥有 schema、中性记录、具名读写与事务端口；物理语义保持 `app.db + N world.db`。应用装配仍有 23 处受守卫约束的 `Database()` 逃生口，本轮明确不以归零为目标，也不为归零引入 repository 层。

### R5：删除旧 Game Runtime

已删除 `protocol/`、`gateway/`、`tool/`、`task`、旧 `agent`、旧 `context`、旧 `memory`、旧 `httpapi`、旧入口，以及 `scenarios/` 中不属于 WIA 的内容。

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

**空间是 WIA 核心世界模型。** R6 文档主题化重组保持后置，不阻塞区域、地点、当前位置与连接的产品设计；空间方案定稿后再建立对应正式文档。

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
