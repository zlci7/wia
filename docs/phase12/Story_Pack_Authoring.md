# Local story packs

## Files and startup

A pack is one directory containing `story.json`, referenced `npcs/*.json`, and optional `assets/` images. The bundled examples are [Lantern Dusk](../../runtime/internal/storyapp/packs/lantern-dusk/story.json) and [Orbital Repair](../../runtime/internal/storyapp/packs/orbital-repair/story.json).

The default catalog is `%LOCALAPPDATA%\WorldIsAgent\story-app\story-packs`. On the first launch with no catalog directory, the runtime installs the bundled examples. Existing directories are preserved. Author changes become available after restarting the runtime. A different catalog can be selected with `-story-packs <directory>` or `WIA_STORY_PACKS`.

```powershell
go run ./runtime/cmd/server -story-packs ./runtime/internal/storyapp/packs
```

The catalog root contains pack directories, not a single `story.json`. A malformed pack is excluded from new games and reported on the homepage; other packs and saved worlds remain available. An empty catalog supports saved worlds only.

## Story definition

The embedded [story schema](../../runtime/internal/storyapp/schemas/story.schema.json) is enforced at load time. Unknown fields, duplicate JSON keys and null values are rejected. Optional fields are omitted rather than set to null. Cross-file identities, locations, plot dependencies and audiences are validated separately.

| Field | Meaning |
| --- | --- |
| `schema_version` | Format version, currently `1` |
| `game_id`, `revision` | Stable pack identity and immutable content revision |
| `mode` | Author-selected `guided` or `open`; players cannot override it |
| `title`, `description`, `gameplay`, `background` | Public catalog and introductory material |
| `rules` | World rules shared with the coordinator, narrator and NPCs; do not include secrets |
| `author_facts` | Coordinator-only author material; not shared NPC knowledge |
| `player` | Default `name`, `profile`, public `requirements`, and `editable` flag |
| `opening`, `initial_location`, `clock` | Player-visible opening, location ID, and time such as `第 1 日 09:00` |
| `locations` | IDs, public names/descriptions and connection IDs; narrative context, not a deterministic navigation engine |
| `npcs` | Relative NPC definition files |
| `bystanders` | Public background people; no independent NPC agents |
| `cover`, `cover_alt` | Optional local image reference under `assets/` and alternative text |
| `plot` | Existing conditional plot: revision, facts and ordered nodes |
| `defaults` | Partial narrative settings and shared behavior policies |

Omitted player values use the application defaults (旅人, the standard profile, editable). Omitted narrative options inherit the application settings; declared options override them. Effective settings and their origin are saved with the world. Empty shared behavior policies follow the application default; explicit policies remain world-owned.

Plot nodes contain `id`, `after`, optional non-negative `at_minute`, `condition`, `development`, `audience`, and optional `terminal`. Dependencies refer to earlier nodes. Conditions and developments use the existing M2 coordinator; schema validity does not prove that a story is playable or has a reachable ending. Dynamic event-generation settings are not accepted in this format.

Limits include 32 locations, 16 important NPCs, 40 bystanders and 64 plot nodes. `story.json` is at most 256 KiB, each NPC file 64 KiB. Covers support PNG/JPEG, at most 4 MiB and 4096 × 4096 pixels. References must resolve inside the pack; remote URLs, traversal and external links are rejected. The server exposes only the selected cover, never the whole directory.

## NPC definition

The [NPC schema](../../runtime/internal/storyapp/schemas/npc.schema.json) covers `definition_id`, `revision`, `entity_id`, `name`, `role`, `appearance`, `profile`, `knowledge`, `initial_concerns`, and `initial_location`.

`entity_id` starts with `npc:` and identifies a world-local person. `definition_id` and `revision` identify the frozen template. Different games can reuse local IDs. Name, role and appearance are public; profile, knowledge and concerns are private role material. Relationship facts belong in author facts or the knowledge of the people authorized to know them. Knowledge should describe what the person actually knows, rather than disclose a secret by saying they do not know it.

Initial concerns are starting motivations, not permanent tasks. Runtime perceptions, decisions, memories and corrections belong to the created world, not to the pack files.

## Versioning and saves

Change `revision` whenever content or referenced assets change. The runtime persistently binds `(game_id, revision)` to the normalized content digest; reusing a revision for different content makes that candidate unavailable. JSON formatting and object-key order do not affect the digest.

New games pin the revision shown in the form. A revision conflict preserves the form and requests explicit review of the current version. Unknown creation results retain the original request identity for a safe retry.

Each world owns its definition snapshot and cover. Continuing a save uses that snapshot even when the pack changes or disappears. Save-as copies the snapshot, cover, events and memory; deleting the source does not invalidate the copy. Legacy saves use their existing persisted definitions and mode, without injecting a current pack's motives or future events.

## HTTP contract

All routes use the existing local session and ownership checks.

| Route | Response / request |
| --- | --- |
| `GET /api/v1/games` | `{ games, issues }`; public catalog and field-oriented loading errors |
| `GET /api/v1/games/{game}` | `{ game }`; public metadata and actual revision |
| `GET /api/v1/games/{game}/cover?revision=...` | Validated image for the exact revision |
| `GET /api/v1/worlds/{world}/game` | `{ game }`; saved public metadata |
| `GET /api/v1/worlds/{world}/cover` | World-owned image |
| `POST /api/v1/worlds` | `game_id`, `expected_revision`, `request_key`, optional `name`, `player_name`, `player_profile`, `activate` |

Creation returns `{ world }` with HTTP 201. The same owner/key/payload returns the original result; a changed payload is an idempotency conflict. A stale revision returns 409, invalid input returns 400, and an unavailable game returns 404. A replay is resolved before consulting the current catalog. World lists span all games; activation, copying, deletion, memory and corrections resolve the game through the owned world record.

## Current examples

Lantern Dusk is a guided investigation using the existing plot nodes. Orbital Repair is a different-theme open interaction sample with two NPCs and two locations. A complete guided ending package and persistent AI-generated open-world events are separate subsequent deliveries.
