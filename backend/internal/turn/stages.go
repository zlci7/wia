package turn

// This file owns the stage implementations the pipeline calls: what the player meant,
// what each character decided, and what the player is told. They live here because a
// stage is a turn operation — it composes material, sends it, validates the answer and
// hands the result to the next stage through Output. What a stage still needs from the
// application is one logger line per stage (Host.LogStage) and the turn's frozen input
// before the stages begin; only application-owned stage logging stays on Host.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func normalizeNPCActionIntent(value string) string {
	action := wire.Clean(value)
	if action == "" {
		return ""
	}
	normalized := strings.Trim(action, "。；;，, ")
	for _, passive := range []string{
		"观察", "继续观察", "保持观察", "留意", "继续留意", "注视", "继续注视",
		"观察异常", "保持警惕", "提高警惕", "等待", "继续等待", "维持原位", "留在原位",
		"保持沉默", "继续沉默", "不动", "没有行动", "无行动",
	} {
		if normalized == passive {
			return ""
		}
	}
	return action
}

// The output budget and prompt versions of the stages that live here. A prompt version
// changes when the prompt does, so it belongs with the stage that sends it.
const (
	structuredTurnOutputTokens = 4096

	intentPromptVersion    = "story.intent.v10"
	npcPromptVersion       = "story.npc.v17"
	narrationPromptVersion = "story.narration.v12"
)

type narrativeResult struct {
	Narrative string `json:"narrative"`
}

// ErrInvalidRequest marks turn input that cannot be honoured before any model call: a run
// addressed to a character who is not in the scene. It is a turn error rather than an
// application one because the turn is what reads the run.
var ErrInvalidRequest = errors.New("invalid turn request")

// ErrModelNotConfigured marks a stage that was asked to run without a generator. The turn
// cannot decide that for itself — the application owns model configuration — so it reports
// the condition instead of guessing at a reply.
var ErrModelNotConfigured = errors.New("model not configured")

// CharacterSpeakingExamples reads the samples a frozen definition recorded for one
// character, so the running turn uses the world's own version.
func CharacterSpeakingExamples(characters []wiaworld.Character, entityID string) ([]string, bool) {
	for _, character := range characters {
		if character.EntityID == entityID {
			if len(character.SpeakingExamples) == 0 {
				return nil, false
			}
			return character.SpeakingExamples, true
		}
	}
	return nil, false
}

func FindSceneCharacter(characters []wiaworld.Character, id string) (wiaworld.Character, bool) {
	for _, character := range characters {
		if character.InScene && character.EntityID == strings.TrimSpace(id) {
			return character, true
		}
	}
	return wiaworld.Character{}, false
}

func parseNarrativeText(text string) (string, error) {
	text = wire.Clean(text)
	if text == "" {
		return "", fmt.Errorf("%w: narrative is empty", ErrGenerationFailed)
	}
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) < 3 || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			return "", fmt.Errorf("%w: narrative has an incomplete code fence", ErrGenerationFailed)
		}
		text = wire.Clean(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	if strings.HasPrefix(text, "{") {
		if err := ValidateStrictJSON([]byte(text)); err != nil {
			return "", fmt.Errorf("%w: narrative JSON is invalid: %v", ErrGenerationFailed, err)
		}
		var legacy narrativeResult
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&legacy); err != nil {
			return "", fmt.Errorf("%w: narrative JSON does not match the legacy wrapper", ErrGenerationFailed)
		}
		text = wire.Clean(legacy.Narrative)
	}
	if text == "" {
		return "", fmt.Errorf("%w: narrative is empty", ErrGenerationFailed)
	}
	return text, nil
}

// resolveTurnIntent decides what the player is trying to do and who it addresses, with one
// bounded repair.
func (a *Service) resolveTurnIntent(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run) (TurnIntent, int, error) {
	participants := InScene(snapshot.Characters)
	explicitRecipient := wire.Clean(run.AddresseeID)
	if explicitRecipient != "" {
		if _, ok := FindSceneCharacter(participants, explicitRecipient); !ok {
			return TurnIntent{}, 0, ErrInvalidRequest
		}
	}
	material := composeIntent(snapshot, run)
	generator = a.generator(generator, material, snapshot, run, "intent", "player", 0, intentPromptVersion)
	input := material.Required
	var intent TurnIntent
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	checkRecipient := func() error {
		if len(intent.Fragments) > 0 {
			if explicitRecipient != "" {
				intent.Fragments[0].AddresseeID = explicitRecipient
			}
			if err := validateInputFragments(snapshot, run, &intent); err != nil {
				return err
			}
			// Segment eligibility is checked against its ordered working state.
			// Aggregate metadata describes the opening segment.
			first := intent.Fragments[0]
			intent.IntentType, intent.AddresseeID, intent.Visibility = first.IntentType, first.AddresseeID, first.Visibility
			intent.WaitMinutes, intent.ActionRuleID = first.WaitMinutes, first.ActionRuleID
			return nil
		}
		if explicitRecipient != "" {
			intent.AddresseeID = explicitRecipient
		}
		if intent.AddresseeID != "" {
			if _, ok := FindSceneCharacter(participants, intent.AddresseeID); !ok {
				return &GenerationError{Code: "json_field_value", Field: "addressee_id", Expected: "listed-in-scene-important-character-or-empty", Cause: ErrGenerationFailed}
			}
		}
		if intent.Visibility == "private" && intent.AddresseeID == "" {
			return &GenerationError{Code: "json_field_value", Field: "visibility", Expected: "public-when-addressee-is-empty", Cause: ErrGenerationFailed}
		}
		intent.ActionRuleID = wire.Clean(intent.ActionRuleID)
		if run.PreparedActionRuleID != "" {
			intent.ActionRuleID = run.PreparedActionRuleID
			if intent.IntentType != "act" && intent.IntentType != "observe" {
				intent.IntentType = "act"
			}
		}
		if intent.ActionRuleID != "" {
			rule, ok := actionRuleByID(snapshot.Definition, intent.ActionRuleID)
			if !ok || (intent.IntentType != "act" && intent.IntentType != "observe") {
				return &GenerationError{Code: "json_field_value", Field: "action_rule_id", Expected: "applicable-listed-action-rule-or-empty", Cause: ErrGenerationFailed}
			}
			if met, _ := evaluateFactConditions(snapshot, Output{Positions: snapshot.Positions, States: snapshot.States, Relationships: snapshot.Relationships, Items: snapshot.Items}, rule.Conditions, "player"); !met {
				return &GenerationError{Code: "json_field_value", Field: "action_rule_id", Expected: "rule-with-satisfied-program-preconditions", Cause: ErrGenerationFailed}
			}
		}
		return validateInputFragments(snapshot, run, &intent)
	}
	repairCount, err := GenerateJSONCheckedMetrics(callCtx, generator, material.System, input, &intent, structuredTurnOutputTokens, []string{"addressee_id"}, []string{"intent_type", "addressee_id", "visibility"}, checkRecipient)
	if err != nil {
		return TurnIntent{}, repairCount, err
	}
	return intent, repairCount, nil
}

// definitionFor is the definition a turn runs against: the character roster comes from
// the world, while dialogue samples come from the definition this world froze when it
// started, never from the currently installed story. A later revision must not change how
// an existing save's characters speak, and a world started before samples existed keeps
// none rather than silently adopting a newer template.
func definitionFor(snapshot *Snapshot) story.Definition {
	def := snapshot.Definition
	frozen := def.Characters
	def.Characters = snapshot.Characters
	for index := range def.Characters {
		if samples, ok := CharacterSpeakingExamples(frozen, def.Characters[index].EntityID); ok {
			def.Characters[index].SpeakingExamples = samples
		}
	}
	return def
}

// resolveIntentStage decides what the player is trying to do and who it addresses.
func (a *Service) resolveIntentStage(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run) (TurnIntent, error) {
	intentStarted := time.Now()
	intent, intentRepairs, err := a.resolveTurnIntent(ctx, generator, snapshot, run)
	if err != nil {
		return TurnIntent{}, AtStage(StageIntent, err)
	}
	a.host.LogStage(snapshot.Summary.WorldID, run, StageIntent, "resolve_intent", "", 0, intentPromptVersion, nil, intent.AddresseeID, intentRepairs, time.Since(intentStarted))
	return intent, nil
}

// runCharacterStages runs the two character decision stages and keeps the public
// reply log and the merged decisions on the output for the coordination stage.
func (a *Service) runCharacterStages(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, participants []wiaworld.Character, perceptText map[string]string, stageOneInputs map[string]StageInput, output *Output) error {
	def := definitionFor(snapshot)

	playerEventID := inputPrefix(run) + ":input"
	decisions := make(map[string]NPCDecision)
	if err := a.decideNPCs(ctx, generator, *snapshot, def, run, intent.AddresseeID, intent.IntentType, stageOneInputs, nil, decisions, 1); err != nil {
		return AtStage(StageNPC, err)
	}
	output.speechAudience = append([]string{"player"}, CharacterIDs(participants)...)
	for _, bystander := range snapshot.BystanderRefs {
		output.speechAudience = append(output.speechAudience, bystander.BystanderID)
	}
	for _, character := range participants {
		decision := decisions[character.EntityID]
		appendNPCDecisionOutput(output, run, character, decision, participants, playerEventID, snapshot.SceneVersion, 1)
	}
	snapshot.OpenProgress = cloneOpenProgress(output.OpenProgress)

	// Replies become stage-two stimuli only for the people who heard them.
	stageTwoInputs := PublicReplyStageInputs(output.Perceptions, perceptText, 1)
	if len(stageTwoInputs) > 0 {
		priorTurn := make(map[string]string, len(stageTwoInputs))
		for characterID := range stageTwoInputs {
			priorTurn[characterID] = fmt.Sprintf("第一阶段自己的决定：%s", FormatSelfDecision(decisions[characterID]))
		}
		followDecisions := make(map[string]NPCDecision)
		if err := a.decideNPCs(ctx, generator, *snapshot, def, run, intent.AddresseeID, intent.IntentType, stageTwoInputs, priorTurn, followDecisions, 2); err != nil {
			return AtStage(StageNPC, err)
		}
		for _, character := range participants {
			decision, ok := followDecisions[character.EntityID]
			if !ok {
				continue
			}
			input := stageTwoInputs[character.EntityID]
			sourceEventID := playerEventID
			if len(input.SourceEventIDs) > 0 {
				sourceEventID = input.SourceEventIDs[0]
			}
			appendNPCDecisionOutput(output, run, character, decision, participants, sourceEventID, snapshot.SceneVersion, 2)
			decisions[character.EntityID] = MergeNPCDecision(decisions[character.EntityID], decision)
		}
	}
	output.Decisions = decisions
	return nil
}

// narrateStage renders the player-facing prose and closes the turn with the
// settled scene projection.
//
// What the player can perceive comes from the output, where scene resolution left it,
// rather than from an argument: narration is two stages away from the place those events
// were gathered.
func (a *Service) narrateStage(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, recipient string, private bool, output *Output) error {
	def := definitionFor(snapshot)
	visibleEvents := output.VisibleEvents

	playerProjection := RenderVisibleProjection(visibleEvents, snapshot.Characters)
	narrationStarted := time.Now()
	result, narrationRepairs, err := a.narrateVisible(ctx, generator, *snapshot, run, def, recipient, intent.IntentType, visibleEvents, private, output.Clock, output.Scene, output.SceneCharacters)
	if err != nil {
		return AtStage(StageNarration, err)
	}
	a.host.LogStage(snapshot.Summary.WorldID, run, StageNarration, "render_player_text", "scene", 0, narrationPromptVersion, EventIDs(visibleEvents), recipient, narrationRepairs, time.Since(narrationStarted))
	output.Narrative = result.Narrative
	settledStage := 3
	for _, event := range output.Events {
		if event.Stage >= 4 {
			settledStage = 7
		}
	}
	output.Events = append(output.Events, wiaworld.Event{EventID: run.RunID + ":outcome", EventType: "turn_settled", ActorID: "scene", Content: playerProjection, RunID: run.RunID, Stage: settledStage, SceneVersion: output.SceneVersion, SourceType: "scene", CreatedAt: time.Now().UTC()})
	return nil
}

// appendNPCDecisionOutput turns one character's decision into the events, perceptions,
// memories and public reply the rest of the turn reads.
func appendNPCDecisionOutput(output *Output, run wiaworld.Run, character wiaworld.Character, decision NPCDecision, participants []wiaworld.Character, defaultSourceEventID string, sceneVersion int64, stage int) string {
	start := len(output.Events)
	applyPlanUpdates(output, run, character.EntityID, decision, stage)
	sourceEventID := defaultSourceEventID
	if decision.ActionIntent != "" {
		actionEventID := fmt.Sprintf("%s:%s:action:%d", inputPrefix(run), character.EntityID, stage)
		output.Events = append(output.Events, wiaworld.Event{EventID: actionEventID, EventType: "npc_action_intent", ActorID: character.EntityID, TargetID: decision.ActionTargetID, Content: decision.ActionIntent, RunID: run.RunID, Stage: stage, SceneVersion: sceneVersion, SourceType: "npc_intent", ProjectionParentID: defaultSourceEventID, CreatedAt: time.Now().UTC()})
		sourceEventID = actionEventID
	}
	var reply string
	if decision.Speech != "" {
		var speechID string
		speechID, reply = appendNPCSpeech(output, run, character, decision, participants, sceneVersion, stage)
		if decision.ActionIntent == "" {
			sourceEventID = speechID
		}
	}
	if decision.Memory != "" {
		output.Memories = append(output.Memories, wiaworld.Memory{RecipientID: character.EntityID, Kind: "character_judgment", Content: wire.Clean(decision.Memory), SourceEventID: sourceEventID, CreatedAt: time.Now().UTC()})
	}
	for i := start; i < len(output.Events); i++ {
		if output.Events[i].EventType == "npc_speech" || output.Events[i].EventType == "npc_action_intent" {
			output.Events[i].BasisEventIDs = eventBasisSources(append([]string{defaultSourceEventID}, decision.sourceEventIDs...))
		}
	}
	return reply
}

// decideNPCs runs one bounded round of character decisions.
func (a *Service) decideNPCs(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, def story.Definition, run wiaworld.Run, recipient, intentType string, inputs map[string]StageInput, priorTurn map[string]string, decisions map[string]NPCDecision, stage int) error {
	if generator == nil {
		return ErrModelNotConfigured
	}
	npcCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var decisionMu sync.Mutex
	readers := []string{}
	for _, character := range InScene(snapshot.Characters) {
		if _, ok := inputs[character.EntityID]; ok {
			readers = append(readers, character.EntityID)
		}
	}
	snapshot.materialGroup = newMaterialReadGroup(readers)
	for _, character := range InScene(snapshot.Characters) {
		character := character
		stageInput, present := inputs[character.EntityID]
		if !present {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer snapshot.materialGroup.finish(character.EntityID)
			material := composeNPC(snapshot, def, character, recipient, intentType, stageInput, priorTurn[character.EntityID], stage)
			callGenerator := a.generator(generator, material, snapshot, run, "npc", character.EntityID, stage, npcPromptVersion)
			input := material.Required
			var decision NPCDecision
			started := time.Now()
			callCtx, callCancel := context.WithTimeout(npcCtx, 60*time.Second)
			defer callCancel()
			required := []string{"speech", "action_intent", "silent", "memory"}
			if snapshot.Definition.Capabilities["relations"] == 1 {
				required = append(required, "relationship_proposals")
			}
			checkDecision := func() error {
				decision.ActionTargetID = wire.Clean(decision.ActionTargetID)
				if decision.ActionTargetID != "" && decision.ActionTargetID != "player" {
					found := false
					for _, target := range snapshot.Characters {
						found = found || target.EntityID == decision.ActionTargetID
					}
					if !found {
						return coordinationInvalid("npc_action_target_invalid", "action_target_id", "defined-important-character-or-player")
					}
				}
				if err := validatePlanUpdates(snapshot, character.EntityID, &decision, callGenerator.(*ContextGenerator)); err != nil {
					return err
				}
				if err := validateNPCSpeech(snapshot, character.EntityID, &decision); err != nil {
					return err
				}
				return validateRelationshipProposals(snapshot, character.EntityID, &decision)
			}
			repairCount, err := GenerateJSONCheckedMetrics(callCtx, callGenerator, material.System, input, &decision, structuredTurnOutputTokens, []string{"speech", "action_intent", "memory"}, required, checkDecision)
			for recall := 0; err == nil && wire.Clean(decision.RecallQuery) != ""; recall++ {
				if recall >= 2 || len([]rune(decision.RecallQuery)) > 256 {
					err = ErrGenerationFailed
					break
				}
				material = withRecall(material, memoryProjection{Context: snapshot.LongMemory[character.EntityID]}, decision.RecallQuery)
				material.Required += fmt.Sprintf("\n已完成第%d次只读检索；命中材料按因果组纳入预算，已在近期经历或此前检索中的内容不重复添加。参考检索预算说明，缺少材料不等于事情未发生。最多两次，随后根据已获准资料完成决定。", recall+1)
				callGenerator = a.generator(generator, material, snapshot, run, "npc", character.EntityID, stage, npcPromptVersion)
				decision = NPCDecision{}
				var repairs int
				repairs, err = GenerateJSONCheckedMetrics(callCtx, callGenerator, material.System, material.Required, &decision, structuredTurnOutputTokens, []string{"speech", "action_intent", "memory"}, required, checkDecision)
				repairCount += repairs
			}
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
			decision.Speech = wire.Clean(decision.Speech)
			decision.ActionIntent = normalizeNPCActionIntent(decision.ActionIntent)
			decision.Memory = wire.Clean(decision.Memory)
			for index := range decision.RelationshipProposals {
				proposal := &decision.RelationshipProposals[index]
				proposal.TargetID = wire.Clean(proposal.TargetID)
				proposal.RelationType = wire.Clean(proposal.RelationType)
				proposal.SourceID = wire.Clean(proposal.SourceID)
			}
			if decision.Speech == "" {
				decision.Silent = true
			}
			if decision.Silent {
				decision.Speech = ""
			}
			decision.sourceEventIDs = decisionEventSources(snapshot, character.EntityID, stageInput.SourceEventIDs, callGenerator.(*ContextGenerator).providedSources)
			a.host.LogStage(snapshot.Summary.WorldID, run, StageNPC, "npc_decision", character.EntityID, inputStage(run, stage), npcPromptVersion, stageInput.SourceEventIDs, recipient, repairCount, time.Since(started))
			decisionMu.Lock()
			decisions[character.EntityID] = decision
			decisionMu.Unlock()
		}()
	}
	wg.Wait()
	return firstErr
}

// narrateVisible composes the narration material for the player-visible events and asks
// the model for the text.
func (a *Service) narrateVisible(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, def story.Definition, recipient, intentType string, visibleEvents []wiaworld.Event, private bool, clock, scene string, sceneCharacters []string) (narrativeResult, int, error) {
	if generator == nil {
		return narrativeResult{}, 0, ErrModelNotConfigured
	}
	material, maxOutputTokens, err := composeNarration(snapshot, run, def, recipient, intentType, visibleEvents, clock, sceneCharacters)
	if err != nil {
		return narrativeResult{}, 0, err
	}
	stage := 3
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil {
		stage = 7
	}
	generator = a.generator(generator, material, snapshot, run, "narration", "player", stage, narrationPromptVersion)
	input := material.Required
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	narrative, repairCount, err := generateNarrativeText(callCtx, generator, material.System, input, maxOutputTokens)
	if err != nil {
		return narrativeResult{}, repairCount, err
	}
	return narrativeResult{Narrative: narrative}, repairCount, nil
}

func generateNarrativeText(ctx context.Context, generator model.TextGenerator, system, input string, maxOutputTokens int) (string, int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		requestSystem := system
		if attempt > 0 {
			requestSystem += "\n上一次响应不可用。请重新生成，只输出一段完整的故事正文。"
		}
		response, err := generator.GenerateText(ctx, model.TextRequest{System: requestSystem, Input: input, MaxInputTokens: 12000, MaxOutputTokens: maxOutputTokens, MaxResponseBytes: 1 << 20})
		if err != nil {
			if attempt == 0 && errors.Is(err, model.ErrInvalidTextResponse) {
				continue
			}
			return "", attempt, err
		}
		narrative, err := parseNarrativeText(response.Text)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return "", attempt, err
		}
		return narrative, attempt, nil
	}
	return "", 1, ErrGenerationFailed
}

// generator wraps a model generator for one composed call of this turn.
func (s *Service) generator(generator model.TextGenerator, material Material, snapshot Snapshot, run wiaworld.Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	return NewContextGenerator(s.deps, s.deps.Owner, generator, material, snapshot, run, purpose, recipient, inputStage(run, stage), template)
}

// runCharacters has each character in the scene decide what to do. Who is present is
// passed in rather than read again here: presence decides who perceives the player's
// action, and that question is answered by the roster the turn opened with.
func (s *Service) runCharacters(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, output *Output) error {
	return s.runCharacterStages(ctx, generator, snapshot, run, intent, InScene(snapshot.Characters), output.PerceptText, output.StageOneInputs, output)
}

// narrate writes the player-visible text from the events already resolved.
func (s *Service) narrate(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, output *Output) error {
	return s.narrateStage(ctx, generator, snapshot, run, intent, intent.AddresseeID, intent.Private(), output)
}
