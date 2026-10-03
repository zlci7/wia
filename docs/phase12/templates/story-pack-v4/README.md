# Story Pack v4 作者模板

配套[编写合同](../../stages/开发者剧本编写规范与模板.md)的 Story Pack v4 完整文件模板。加载和验证状态见[开发状态](../../开发状态.md)；[Story Pack v3](../story-pack/story.json)保留旧格式示例。

[story.json](story.json)引用两个地点、一个固定人物、分类材料和持续推进初始配置。复制整个目录后填写世界与人物内容，修改剧本和人物 revision。能力按需配置，现有字段见[编写规范](../../stages/开发者剧本编写规范与模板.md)。

开场日期使用世界年月日与时分，模板中的配置为：

```json
{
  "schema_version": 4,
  "requires": { "spatial": 1, "progression": 1, "world_info": 1 },
  "calendar": { "kind": "gregorian" },
  "clock": "2026-01-01 09:00"
}
```

`calendar.era` 可填写世界内的纪元名称；日期按公历规则计算。`player.known_locations` 可填写主角开场已知的公开地点 ID。题材、年份与地名由剧本配置，运行界面在角色、背包、地图和人物栏目提供获准资料。

| 文件 | 内容 |
| --- | --- |
| [story.json](story.json) | 身份、展示信息、主角、历法、开场日期和引用 |
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

现金与随身物品按需接入既有 `state`、`items` 能力及对应文件：现金使用带 `currency` 的非负整数资源状态，物品填写真实初始归属。模板未配置的资产保持未配置状态，具体字段见[世界信息编写合同](../../stages/开发者剧本编写规范与模板.md#世界信息配置)。

新世界冻结当前剧本和开场条件；旧存档沿用自己的冻结定义与运行值。修改开场日期、币制或初始资产只影响新世界，旧相对时间存档显示“日期未配置 · 时分”。
