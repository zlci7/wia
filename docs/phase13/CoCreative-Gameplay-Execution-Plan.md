# WIA co-creative gameplay execution plan

Date: 2026-10-07. Status: planned. Inspection baseline: `a7f4f90`.

This execution unit covers a lightweight, continuous story-creation prototype for the current *Mist Embers* story pack. Completion requires readable live-model play sessions with coherent character interaction, meaningful creative development and preserved player choices. Formal runtime migration is a separate delivery boundary.

Canonical responsibilities and constraints remain in [Architecture](../ARCHITECTURE.md), [Product](../phase12/产品说明.md) and [Technical contracts](../phase12/总体技术方案.md). Implementation status belongs in [Delivery status](../phase12/开发状态.md). Existing live evidence is recorded in [Continuation verification](../summary/phase13/连续预检与容量-验收.md).

## 1. Execution scope

| Included | Delivery |
| --- | --- |
| Explicit model reasoning controls | Request-level thinking selection for diagnostic and creation calls; measured parameter choice |
| One scene-creation responsibility | Related characters, interactions, outcomes and player prose created together |
| Lightweight response contract | Narrative and only the changes or notes needed to continue the next turn |
| Relevant context composition | Story facts, character motives, recent conversation and selected older experiences |
| Per-turn creative authorization | Internal request option `allow_plot_advance` |
| Story material refinement | Fixed facts, motivations and creative latitude expressed separately in existing pack materials |
| Continuous interactive evaluation | A local internal session driver, live transcripts and measured results |

UI changes, save migration, save/load acceptance, formal application routing, expanded economic mechanics, fixed-rule integration, long-history infrastructure and additional playable stories are deferred. Existing user data and configuration are preserved. Existing delivered capabilities and verification records retain their recorded status.

The full Phase13 compatibility matrix is outside this prototype's acceptance scope. Repository checks applicable to changed code still run. A successful prototype does not establish formal migration, persistence compatibility or full-world-mechanics support.

## 2. Creation loop

```text
Player input + allow_plot_advance
  -> prepare relevant story material and current session context
  -> one scene-creation request
  -> validate the complete response
  -> accept narrative, necessary scene changes and continuity notes
  -> assemble the next turn from that accepted context
```

Implementation refactors the existing candidate creation responsibility in `backend/internal/turn`. Provider access, material selection, `ContextComposer`, existing memory structures and location rules are reused. The internal session driver is a test/development entry point, not another production runtime or memory service. Existing `Service.Execute` remains the formal application entry point during this unit.

Raw-prose generation is an experimental reference in the test harness. The retained creation implementation has one response contract. Replaced candidate-generation logic and obsolete contract consumers are removed or revised in their implementation unit; obsolete code is recoverable through Git.

Accepted development-session context may remain in memory for this evaluation. No fabricated `Output`, transaction result or save record is generated to present the prototype as formally integrated.

## 3. Request and response boundary

### Request

- Preserve the player's original input.
- Accept `allow_plot_advance` as an explicit boolean for each turn.
- Use request-level reasoning selection for the creator and diagnostic probes.
- Keep transport selection independent from reasoning selection.
- Retain applicable existing input, combined-output, deadline and cancellation limits.

The controlled baseline uses `allow_plot_advance=false`. A second session exercises enabled advancement. Production UI defaults are outside this unit.

### Minimal response

| Field | Meaning |
| --- | --- |
| `narrative` | Required, complete player-readable scene |
| `scene_changes` | Optional actual changes needed to establish the next scene, using existing identities and locations |
| `continuity_notes` | Optional consequential discoveries, statements, commitments or pending matters, with their knowledge owners |

Exact Go types are frozen in U2 before implementation. They contain no per-sentence beat graph, verbatim input-copy requirement, mandatory world-review report or duplicated full dialogue. Changes and notes are omitted when unnecessary.

Scene changes use the existing position authority; presence is derived from accepted positions. New background details can be described in prose without creating important character instances or map entities. Numeric resource changes and fixed-rule outcomes are not claimed by this prototype.

Continuity notes distinguish observed results, character statements, hypotheses and unfinished commitments. A character's statement does not become objective truth solely because it appears in a note. Consequential private speech retains its required wording and actual recipients. Program-generated sequence and source identity remain outside the creator's bookkeeping task.

The narrative itself remains part of recent context. Notes retain consequential information; they do not replace the complete recent exchange.

### Validation and failure

Validate complete response format, recognized fields, valid references, declared creative scope and supported scene changes. JSON output mode may assist syntax when supported; it does not replace local checks or prevent output truncation.

The programmer retains final application of accepted changes. A failed or cancelled response leaves the previously accepted session context intact. Partial streamed content is provisional.

An eligible format or reference failure has at most one technical correction within the existing limits. Token exhaustion, deterministic capacity errors and recurring service failures are diagnosed before further paid calls. Narrative repetition or weak pacing is reviewed as a quality defect, not automatically retried by another model agent.

A shared creator can see multiple characters' materials. Typed ownership checks do not prove that generated prose preserves every secret. Privacy and motivation fidelity require live interaction review.

## 4. Creative behavior and story separation

### Advancement disabled

Fully develop the current interaction, let characters respond to each other, and complete ordinary steps the player has already chosen. Preserve immediate consequences and actual elapsed circumstances. Keep the scene focused on the current intent.

### Advancement enabled

Allow characters to continue relevant interaction, introduce matters of their own, reveal appropriate information and create a plausible new situation within the story's creative scope. Stop at a meaningful endpoint or a new important player decision. A quiet or unresolved scene remains valid when supported by the situation.

Both modes preserve the player's authority over important commitments, dangerous choices and new destinations. NPC-to-NPC exchanges can contain several natural turns without inventing new decisions for the player. Existing actions and consequences continue in both modes; the option controls discretionary creative advancement, not a frozen world.

### Story material

The current pack remains `backend/internal/content/packs/mist-embers`. Its existing materials express:

1. Established facts and important truths.
2. Character identity, motives, information and current concerns.
3. Creative latitude: plausible interaction, investigation, complications and optional developments.

`narrative/developments.md` supplies creative latitude; related profile and knowledge files retain character differences. These are material selections within the existing format, not a new story-pack schema. They describe pressure and possibilities rather than a mandatory route or predetermined success.

New creative facts accepted within that scope become continuity evidence for later turns. The creator need not cite a prewritten plot node for every new detail. It preserves established truths and distinguishes background invention from a discovered fact.

Generic creator code and prompts contain no story-specific names, locations, factions or calendar values. An offline content-injection test checks this separation without developing another playable story.

## 5. Context composition

Use this order through the existing composition entry point:

1. Short generic creation responsibilities, output contract and player-choice boundary.
2. Relevant fixed story facts and necessary world rules.
3. Relevant characters' identities, motives and separately labelled knowledge.
4. Complete recent exchanges and accepted current situation.
5. Selected older experiences, outstanding commitments and relevant clues.
6. Original player input and this turn's advancement option.

Load relevant existing material before the ordinary creation call. Necessary material expansion remains bounded by the applicable authorization and request limits. Unrelated city lore, every character's full history and all mechanics are not mandatory context.

Character memories remain independently scoped data inside a shared request. Retrieval does not create a per-character model call. Accepted situation and recent experience take precedence over initial descriptions; subjective notes cannot overwrite fixed truths.

## 6. Work units and gates

| Unit | Work | Completion gate | Estimate |
| --- | --- | --- | --- |
| U1: model control and reference | Record HEAD and existing limits; implement explicit request-level thinking controls; compare raw prose and the existing contract with thinking disabled | Exact settings, full result, reasoning/result usage and latency recorded; control wiring tested | About 1 h |
| U2: lightweight creator | Freeze minimal types; refactor candidate generation and validation; remove required beat bookkeeping and repeated creative calls | A complete accepted scene; a rejected result leaves context intact; ordinary turn uses one core call | 2–3 h |
| U3: material and authorization | Compose relevant context; refine current pack material; implement per-turn advancement behavior in the internal request | Both modes work; character differences and fixed facts retained; engine/story separation verified | 1–2 h |
| U4: continuous play and closure | Run two live sessions, review defects, make targeted corrections, run applicable repository checks and package evidence | Transcripts and measurements support the stated gameplay conclusion; limitations recorded | 2–3 h |

Estimated first delivery: 6–9 hours. Estimates include routine testing and closure, excluding unavailable-service time and additional style iteration. Reserve the final 1–1.5 hours for review, verification and delivery. An unfinished unit is reported as unfinished when the available work budget ends.

U1 initially compares the same model, story material, frozen input, transport and applicable token ceiling; only the response contract changes. Existing high-effort failure evidence is retained. A low-effort variant is added only when needed to resolve a quality or completion question. Combined output and visible/reasoning usage are reported separately.

U2 and U3 introduce intentional changes to the creative behavior. Their acceptance is continuity, player control and actual text quality, not identical legacy output. The formal migration gates in the broader Phase13 plan apply when that deferred delivery resumes.

## 7. Play sessions and acceptance

Two sessions target 10–15 turns each, within the remaining authorized model budget:

- **Investigation-led:** follow-up questions, a multi-step ordinary action, character refusal, a witnessed disagreement, private conversation and later recall.
- **Advancement-enabled:** repeated enabled turns, an opportunity the player rejects, character initiative, newly created developments and later consequences.

The recorded tailor-shop follow-up supplies a reproducible investigation sample. Branches follow equivalent player intentions; an action that no longer fits a generated branch is not forcibly repeated.

| Check | Required evidence |
| --- | --- |
| Intent fulfilment | The complete chosen intent is addressed; obstacles and unfinished parts are understandable |
| Character interaction | Speakers respond to each other and retain distinct motives; every present character need not speak |
| Creative development | Enabled turns can form plausible new situations and later reuse them without requiring constant twists |
| Continuity | Completed actions remain completed; significant facts, statements and promises are handled consistently |
| Privacy | A bystander does not reproduce an unheard private statement in a later interaction |
| Player control | Important new acceptance, commitments, expenditure and dangerous choices are not invented for the player |
| Pacing | No routine reintroduction, forced waiting ending or mechanical action menu; ordinary steps have meaningful completion |
| Waiting value | Full responses, completion/failure counts, actual latency and reasoning/result token usage recorded |

Technical acceptance requires complete validated responses and stable failure handling. Gameplay acceptance requires both session transcripts, review of the listed situations and no unresolved major fixed-fact, privacy or player-control defect. Every requested turn, failure and technical retry remains in the denominator.

No universal latency target or statistical reliability claim is inferred from these sessions. Shortened samples support only the scenarios actually observed. Fake-model tests establish mechanics, not narrative quality.

## 8. Verification and delivery

Meaningful deterministic tests cover request reasoning controls, advancement propagation, relevant context/owner selection, necessary scene-change application, response validation and unchanged context after rejection. Prompt wording is assessed through generated text rather than mirrored string tests.

For narrative-chain changes, run `go test ./... -count=1`, affected-package `go test -race`, `go vet ./...` and the affected build. Documentation changes require `git diff --check` and local-link verification. No UI work is included, so frontend acceptance is not added to this unit.

Local commits contain complete work units and their directly related tests/materials. Delivery includes:

- The internal continuous-play entry point and exact local invocation.
- Creator, context and current-pack changes.
- Live transcripts with inputs, advancement flags, model settings and outcomes.
- Usage, request counts, first result delta and full-response latency; corrections and failures.
- Test results, local commit list and scoped limitations.

Evidence contains no credentials, machine configuration, actual player saves or raw model reasoning. Internal review is labelled as implementer review unless an independent review actually occurs. Prototype quality and formal application integration are reported separately.

## 9. Defaults and decision boundary

The authorized defaults are the existing provider/model, current story pack, one creator, explicit per-turn advancement, existing context/memory components and a request-level reasoning choice selected from measured results. Technical prompt and field adjustments proceed within this scope without per-item confirmation.

Existing paid-call authorization, pauses, fee limits, timeouts and retry allowances remain authoritative. Planned session length does not grant a higher fee ceiling. Stop paid work when an applicable limit is reached; complete available local work and report missing live evidence. Prior failures and consumed retry allowances remain recorded.

A new model/provider, higher spending ceiling, formal save/runtime migration, UI expansion, dynamic important-character creation or a change to fixed story truths requires a separate scope decision. None is a prerequisite for this execution unit.
