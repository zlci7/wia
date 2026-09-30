# WIA 文档

本目录只保留两类内容：**当前架构**与**Phase12 工作文档**。

## 当前架构

- [WIA 1.0 架构收敛方案](ARCHITECTURE.md)：当前架构、模块职责与依赖规则，以及已执行的迁移顺序。R0–R3 与 R5 已完成，R6 文档重组尚未开始。

## Phase12 工作文档

从 [Phase12 文档入口](phase12/README.md) 进入，其中保存产品范围、跨阶段技术合同、阶段方案与验证证据。

## 维护规则

- 架构与依赖规则只写入 [ARCHITECTURE.md](ARCHITECTURE.md)。
- 产品目标只写入[产品说明](phase12/产品说明.md)。
- 跨阶段不变量只写入[总体技术方案](phase12/总体技术方案.md)。
- 阶段专属的数据结构、接口、迁移与测试写入对应阶段方案。
- 实际进度只更新[开发状态](phase12/开发状态.md)；测试证据写入对应阶段验收记录。

## 已移除的历史文档

Game-native Agent Runtime 阶段的文档（`phase01`-`phase11`、`summary/`、`archive/`、`pro/`、`adapter/`、`development/`、`superpowers/`）已从本仓库删除。该产品的原始仓库是 `world-is-agent`，其文档与代码完整保留在那里；本仓库的 Git 历史也保留了这批文件。当前仓库只讲 WIA 叙事产品。
