# Story packs and multi-story runtime verification

Date: 2026-09-28. Scope: unit ① of the [story-pack plan](../WIA_Phase12_剧本配置与事件生成设计.md). This is a local-playtest delivery; user experience acceptance is pending.

## Delivered behavior

- Local JSON story/NPC definitions validated by embedded JSON Schemas and world-local reference checks.
- Two independently themed examples: guided Lantern Dusk and open Orbital Repair. Mode belongs to the author.
- Public catalog, story details, default protagonists and controlled PNG/JPEG cover routes.
- Revision-pinned, idempotent creation; independent definition and cover snapshots; multi-story activation, saves, copying, deletion and correction.
- Invalid candidates are isolated. Saved worlds continue when source packs are absent or changed.
- New-story drafts retain the original revision and request payload after uncertain responses. Late responses respect the originating editing session.

## Deterministic and engineering evidence

`packs_test.go` covers schema and unknown fields, null/duplicate keys, invalid locations and connections, path traversal, remote references, duplicate NPC IDs, invalid defaults, formatting-stable digests, version conflict, idempotent creation, changed-payload rejection, default player values, public projections, same-revision content changes, absent catalogs, cover copying after source deletion, two-theme continuous interaction and captured requests for all four model purposes. Correction tests reuse NPC IDs across games and verify that correction and legacy fallback remain world-local.

Existing plot, context, memory, correction, retry, transaction and lifecycle tests remain in the regression suite. Test-only fixtures retain legacy mode variants without allowing player overrides in the public API. HTTP tests cover public catalog redaction, stale revisions and rejection of mode overrides.

Frontend checks add revision-conflict draft retention, uncertain creation replay and late creation response isolation. The suite contains 27 settings/recovery checks plus 4 memory checks.

Passed: full Go regression; related storyapp/storyapi race tests; vet for storyapp/storyapi/server; frontend mechanism tests, type check and production build. The runtime executable is built with the production web assets.

## Browser evidence

Driver: gstack `/browse`, headless Chromium fallback. Target: disposable local HTTP fixture; existing user worlds and model settings were not changed.

Verified: two-story homepage, public details, immutable type and revision in the creation form, default Orbital Repair protagonist, successful opening with the correct NPCs/location, save-as confirmation remaining in the original world, and a return to Lantern Dusk showing only its saves. Homepage and play layout were inspected at 375×812, 768×1024 and 1280×720. No page console errors were reported.

Local visual evidence: `.gstack/pack-home-{mobile,tablet,desktop}.png` and `.gstack/pack-play-{mobile,tablet,desktop}.png`. These are local QA artifacts, not packaged application assets. A real mobile keyboard was not tested.

## Real-model evidence

Opt-in `TestPackRealModel`, using the existing DeepSeek `deepseek-v4-flash` configuration and isolated worlds. Configuration and credentials were not copied into evidence or modified.

| Story | Turn | Result | Duration |
| --- | --- | --- | --- |
| Lantern Dusk | Greeting, stopping to rest | Completed | 19.4 s |
| Lantern Dusk | Look outside, ask about rooms | Completed | 28.9 s |
| Orbital Repair | Ask engineer about the shift | Completed | 44.5 s |
| Orbital Repair | Ask dispatcher about supplies after restart with an empty catalog | Completed | 34.6 s |

All four turns completed. The station retained its characters and earlier events after restart without consulting a source pack. The engineer disclosed the inspection issue in her own public response; subsequent knowledge was therefore authorized. The final turn repeated an equipment-experience question. Occasional prose additions, repetition and latency remain model-quality limitations; this small sample is not a long-term reliability estimate.

## Experience path and remaining scope

Start the rebuilt runtime, choose either story, inspect its public introduction and create a world using defaults. Return home to switch stories. Save-as from the play bar and confirm that the copy is independent. File authors can follow [the authoring guide](../Story_Pack_Authoring.md) to edit a pack, increment its revision and restart.

Unit ② complete guided routes/endings, unit ③ persistent generated events and unit ④ expanded long-memory/recovery evaluation remain subsequent work. The open station sample is an interaction baseline, not a claim of completed generated-event gameplay. M3 and M4 remain unstarted.
