# World Is Agent

World Is Agent 是一个本机运行的 AI 叙事游戏。玩家在浏览器中进入预置调查冒险，与两名有独立经历的重要人物和场景路人互动；每个世界单独保存正文、事件、人物感知与记忆。

当前交付为 Phase12 M1：核心游玩与可靠存档。正式入口是本地 Runtime 和玩家工作台，不依赖游戏 Adapter 或外部游戏运行时。

## 开始使用

运行环境：Windows、Go 1.25+；首次从源码构建工作台还需要 Node.js 20+。

```powershell
cd D:\data\project\game-agent\world-is-agent
.\scripts\start-phase12.ps1 -Rebuild
```

日常启动已经构建好的工作台：

```powershell
.\scripts\start-phase12.ps1
```

需要重新安装前端依赖并构建工作台时使用 `-Rebuild`，也可以使用 `-True`。Runtime 会打开本地浏览器并打印带会话令牌的工作台地址。若没有自动打开，复制日志中的 `local client` 地址到浏览器即可。

首次进入可先浏览剧本和已有故事，开始新故事或发送输入前再填写 DeepSeek 或 OpenAI 的 API Key。Runtime 会先验证连接，再将凭据保存在本机数据目录的 secrets 文件中；普通配置、页面响应和故事存档都不包含 API Key。

也可以先构建可执行文件：

```powershell
go build -o wia-runtime.exe ./runtime/cmd/server
.\wia-runtime.exe
```

通过 `-data-root` 指定数据目录，或使用 `WIA_DATA_ROOT`。默认数据目录是 `%LOCALAPPDATA%\WorldIsAgent`。每个世界位于独立的 SQLite 数据库中；另存会创建新的 world_id，读取后继续写入所选世界。

## M1 可体验内容

- 预置故事《暮灯镇的失踪信使》，支持流程型与开放型开局。
- 两名重要 NPC：客栈老板沈岚、佣兵铁杉；开场保留十名场景路人。
- NPC 并行决策、公开回应后的下一阶段反应、按人物分开的感知和记忆。
- 私下交谈的旁观隔离：授权人物看到原文，其他人物只看到交谈迹象。
- 自动保存、世界列表、显式读取、新开一局、另存为独立分支。
- 生成状态、取消、失败输入保留、显式重试、幂等请求和版本冲突保护。
- 本地回环 HTTP 会话、模型连接引导和响应式玩家工作台。
- 持久导航、独立正文滚动与底部输入区、历史分页、阅读位置恢复，以及按存档隔离的页面会话草稿。
- 点击人物指定交谈对象、三档互动风格与按需展开的故事设置、就地错误与存档操作反馈。

## 当前验证

阶段工程验证记录在 [Phase12 M1 验收记录](docs/phase12/acceptance/M1.md)；阶段状态见 [Phase12 开发状态](docs/phase12/WIA_Phase12_开发状态.md)。

已完成自动化回归、race 检查、静态检查、前端类型检查与生产构建，并使用隔离服务验证本地会话、状态接口和正式页面。本轮可执行文件构建通过，独立启动探测受执行环境策略限制。真实模型连续游玩需要在工作台提供可用的 Provider API Key。

已用临时世界完成桌面、平板和手机视口的浏览器交互验证；模型采用测试替身。实体手机软键盘与最终游玩体验仍待验证。

后续阶段再加入长期整理、建议、内容编辑、统一纠正、多用户服务端与完整发布包。

## 相关文档

- [系统架构](ARCHITECTURE.md)
- [公开状态](docs/STATUS.md)
- [Phase12 产品说明](docs/phase12/WIA_产品说明_v1.0.md)
- [Phase12 技术方案](docs/phase12/WIA_Phase12_技术方案_v1.0.md)
- [Phase12 开发执行指南](docs/phase12/WIA_Phase12_开发执行指南.md)

## 许可证

[MIT License](LICENSE)
