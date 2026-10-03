# World Is Agent

World Is Agent (WIA) is a local AI narrative role-playing application. Developers prepare a world and its characters; players enter, act freely, and continue a persistent story in their browser. Important characters have their own knowledge, memories, motives and decisions.

The initial story, **《雾都余烬》**, takes place in a finite Backlund neighborhood after the first Lord of the Mysteries novel, with the Church of the Fool already established. It includes four fixed important characters, seven places and an original cracked silver mirror. Players can accept a commission, investigate, refuse, leave or pursue their own interests. This is an internal playtest build.

## Start locally

Requirements: Windows and Go 1.25+. Building the browser workspace also requires Node.js 20+.

```powershell
cd D:\data\project\game-agent\wia
.\scripts\start-phase12.ps1 -Rebuild
```

For subsequent launches:

```powershell
.\scripts\start-phase12.ps1
```

The source launcher reads the repository's current initial pack. `-StoryPacks <directory>` or `WIA_STORY_PACKS` selects a developer-managed catalog. Existing saves continue with their frozen definitions, independently of the current catalog.

The application opens a local browser. If opening fails, use the `local client` URL printed at startup. Configure DeepSeek or OpenAI through the model settings before submitting an action. Credentials remain in local secret storage and are excluded from story saves and normal settings responses.

The default data root is `%LOCALAPPDATA%\WorldIsAgent`; `-DataRoot` or `WIA_DATA_ROOT` selects another directory. Each world has its own SQLite database. Save As creates an independent world slot.

After building the browser workspace, a standalone executable can also be built:

```powershell
go build -o wia-runtime.exe ./backend/cmd/wia
.\wia-runtime.exe
```

A fresh standalone installation seeds the initial pack. An existing developer catalog remains under its owner's control.

## Current capabilities

- Story Pack v4 supports separate structured files and permission-scoped materials. Saves freeze the complete package. Models receive relevant material and can request bounded additional reads.
- Current character plans persist and are reconsidered when in-game time reaches their review point. Decisions use current facts and each character's authorized experiences.
- Mixed input executes in its original order against one working state. The final world update commits atomically.
- Private conversations, NPC private replies and action results have individual recipient projections. Corrections protect sources already consumed by persistent facts.
- Authoritative positions determine location, nearby destinations and the people present. State, directed relationships and uniquely owned items share the turn commit. Existing fixed percentile rules remain available.
- The reading workspace shows authored world dates and action input, with character, cash, possessions, known places and people available on demand. Automatic saves, independent branches, settings, memory review and correction remain available. Generation can be cancelled; failed input remains available for retry.

Developers edit v4 packages as files using the [authoring contract and template](docs/phase12/stages/开发者剧本编写规范与模板.md). Import, publication and export preserve the file package. The structured content editor explicitly rejects formats it cannot fully represent.

## Validation

Engineering tests, browser checks, real-model observations and user acceptance are recorded separately in the [current status](docs/phase12/开发状态.md), [closed-loop verification record](docs/phase12/records/开放世界闭环-验收记录.md) and [world information verification record](docs/phase12/records/世界信息与阅读界面-验收记录.md). The world, perception and workspace modules have engineering evidence; continuous real-model play is under verification. Provider timeouts, generation failures and semantic deviations remain practical limitations. Desktop browser mobile viewports do not replace physical-phone testing.

## Documentation

- [Architecture and dependency rules](docs/ARCHITECTURE.md)
- [Documentation index](docs/README.md)
- [Phase12 index](docs/phase12/README.md)
- [Product scope](docs/phase12/产品说明.md)
- [Cross-stage contracts](docs/phase12/总体技术方案.md)
- [Player workspace design](DESIGN.md)

## License

[MIT License](LICENSE). The bundled display font retains its [SIL Open Font License](frontend/public/fonts/OFL.txt).
