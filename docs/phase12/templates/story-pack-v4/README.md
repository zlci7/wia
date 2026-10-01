# Story Pack v4 作者模板

状态：配套[编写合同](../../stages/开发者剧本编写规范与模板.md)的完整文件模板，格式 v4 尚未接入运行时。当前可加载模板为 [Story Pack v3](../story-pack/story.json)。

[story.json](story.json)引用两个地点、一个固定人物、分类材料和持续推进初始配置。复制整个目录后填写世界与人物内容，修改剧本和人物 revision。能力按需配置，现有字段见[编写规范](../../stages/开发者剧本编写规范与模板.md)。

| 文件 | 内容 |
| --- | --- |
| [story.json](story.json) | 身份、展示信息、主角、开场和引用 |
| [materials.json](materials.json) | 材料 ID、用途、权限、关联与正文路径 |
| [world/locations.json](world/locations.json) | 地点与有向路线 |
| [world/background/core.md](world/background/core.md) | 精简公开背景 |
| [world/background/society.md](world/background/society.md) | 详细社会背景 |
| [world/rules/daily-life.md](world/rules/daily-life.md) | 公开世界设定 |
| [world/facts/current-situation.md](world/facts/current-situation.md) | 作者真相 |
| [narrative/conflicts.md](narrative/conflicts.md) | 未解决的发展与压力 |
| [narrative/progression.json](narrative/progression.json) | 初始计划检查与发展引用 |
| [npcs/contact/npc.json](npcs/contact/npc.json) | 固定人物基础配置 |
| [npcs/contact/profile.md](npcs/contact/profile.md) | 本人详细身份与性格 |
| [npcs/contact/knowledge.md](npcs/contact/knowledge.md) | 本人初始知识 |
| [npcs/contact/plans.md](npcs/contact/plans.md) | 本人初始计划 |

初始位置、物品和数值以结构化配置为准，文字材料中的开场事实标注为初始经历。原作参考资料供作者核对，运行时材料按明确引用与知情范围提供。
