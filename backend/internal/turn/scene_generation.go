package turn

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type SceneAttemptReport struct {
	CoreCalls          int
	ContextSupplements int
	ResolutionChecks   int
	Repairs            int
	SelectedEntityIDs  []string
}

// BuildSceneCandidate is the internal quality/compatibility test entry point.
// The application has no route or runtime switch to publish these candidates.
func (s *Service) BuildSceneCandidate(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator) (Output, SceneAttemptReport, error) {
	ctx, cancel := context.WithTimeout(ctx, GenerationTimeBudget)
	defer cancel()
	snapshot, err := s.load(ctx, store, run, generator)
	if err != nil {
		return Output{}, SceneAttemptReport{}, err
	}
	return s.generateSceneCandidate(ctx, store, generator, snapshot, run)
}

// generateSceneCandidate is exercised by the Phase13 candidate tests. Execute
// keeps its current formal path until the quality and compatibility gates pass.
// One generator and deadline own all supplements, checkpoints and corrections.
func (s *Service) generateSceneCandidate(ctx context.Context, store *storage.WorldStore, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run) (Output, SceneAttemptReport, error) {
	var report SceneAttemptReport
	if generator == nil {
		return Output{}, report, ErrModelNotConfigured
	}
	callCtx, cancel := context.WithTimeout(ctx, GenerationTimeBudget)
	defer cancel()
	var intent *TurnIntent
	var parts []InputFragment
	ruleIndex := -1
	if store != nil && run.InputID != "" {
		prepared, found, err := store.ReadActionResolutionForInput(callCtx, run.InputID)
		if err != nil {
			return Output{}, report, err
		}
		if found {
			run.PreparedActionRuleID = prepared.RuleID
		}
	}
	if len(snapshot.Definition.ActionRules) > 0 {
		snapshot.materialReads = newMaterialReadBudget()
		parsed, err := s.resolveIntentStage(callCtx, generator, snapshot, run)
		if err != nil {
			return Output{}, report, err
		}
		intent = &parsed
		parts = parsed.Fragments
		if len(parts) == 0 {
			parts = []InputFragment{{Text: run.Input, ActorID: "player", IntentType: parsed.IntentType, AddresseeID: parsed.AddresseeID, Visibility: parsed.Visibility, WaitMinutes: parsed.WaitMinutes, ActionRuleID: parsed.ActionRuleID}}
		}
		for i, part := range parts {
			if part.ActionRuleID != "" {
				ruleIndex = i
			}
		}
	}
	selected := selectedSceneEntities(snapshot, run)
	if err := validateSceneMemoryOwners(snapshot, selected); err != nil {
		return Output{}, report, err
	}
	snapshot.sceneEntities = selected
	baseMaterial := composeScene(snapshot, run, selected)
	call := NewContextGenerator(s.deps, s.deps.Owner, generator, baseMaterial, snapshot, run, "scene", "", 0, scenePromptVersion).(*ContextGenerator)
	call.composer.Scope.SelectedEntityIDs = slices.Clone(selected)
	_, narrativeTokens := LengthInstruction(snapshot.Narrative)
	outputTokens := structuredTurnOutputTokens + narrativeTokens
	if call.composer.Window != (model.WindowLimits{}) {
		outputTokens = min(outputTokens, call.composer.Window.OutputTokens)
	}
	readIDs := []string{}
	correction := ""
	var prefix []sceneBeat
	var resolution *ActionResolution
	prefixBound := false
	resolutionBlocked := false
	if ruleIndex == 0 {
		prepared, err := prepareActionResolution(callCtx, store, snapshot, sceneRuleRun(run, parts, ruleIndex), parts[ruleIndex].ActionRuleID)
		if err != nil {
			return Output{}, report, err
		}
		resolution = &prepared
	}
	rebuild := func() {
		snapshot.sceneEntities = slices.Clone(selected)
		base := composeScene(snapshot, run, selected)
		material, available := selectStoryMaterials(snapshot, "scene", "", base)
		for _, id := range readIDs {
			if m, ok := available[id]; ok {
				material = appendRequiredMaterial(material, materialSection(m, snapshot.Definition.Revision))
			}
		}
		if prefixBound || resolution != nil {
			material.Required += "\n已验证前段（完整候选必须原样保留）：" + wire.MarshalJSON(prefix) + "\n程序已准备判定（使用此固定结果）：" + wire.MarshalJSON(resolution)
		}
		if intent != nil {
			material.Required += "\n程序解析的固定规则原文片段（保持text、scope和规则绑定）：" + wire.MarshalJSON(parts)
		}
		if resolutionBlocked {
			material.Required += "\n已验证前段未满足所选规则条件，判定未准备；保持前段，保留失败或未执行，不改换规则或强行成功。"
		}
		if correction != "" {
			material.Required += "\n本次完整纠正依据：" + correction
		}
		call.material = material
		call.availableMaterials = available
		call.systemSuffix = strings.TrimPrefix(material.System, base.System)
		call.composer.Scope.SelectedEntityIDs = slices.Clone(selected)
	}
	finish := func(out Output, err error) (Output, SceneAttemptReport, error) {
		report.CoreCalls = call.calls
		report.SelectedEntityIDs = slices.Clone(selected)
		return out, report, err
	}
	for call.calls < 4 {
		if err := callCtx.Err(); err != nil {
			return finish(Output{}, err)
		}
		rebuild()
		response, err := call.generateText(callCtx, model.TextRequest{System: baseMaterial.System, MaxOutputTokens: outputTokens})
		if err != nil {
			return finish(Output{}, err)
		}
		if err := callCtx.Err(); err != nil {
			return finish(Output{}, err)
		}
		if err := model.ValidateTextResponse(model.TextRequest{MaxOutputTokens: outputTokens, MaxResponseBytes: 1 << 20}, response); err != nil {
			return finish(Output{}, err)
		}
		if response.Diagnostic.FinishReason != "" && response.Diagnostic.FinishReason != "stop" {
			return finish(Output{}, model.ErrInvalidTextResponse)
		}
		parsed, validation := decodeSceneResponse(response.Text)
		if validation == nil && parsed.Material != nil {
			if report.ContextSupplements != 0 {
				return finish(Output{}, coordinationInvalid("scene_context_supplement_limit", "needs_material", "one-shared-context-supplement"))
			}
			for _, id := range parsed.Material {
				if _, ok := call.availableMaterials[id]; !ok {
					return finish(Output{}, coordinationInvalid("material_request_unauthorized", "needs_material", "authorized-listed-file-id"))
				}
			}
			report.ContextSupplements++
			readIDs = slices.Clone(parsed.Material)
			continue
		}
		if validation == nil && parsed.Resolution != nil {
			r := parsed.Resolution
			if ruleIndex < 0 || r.InputFragmentIndex != ruleIndex || r.RuleID != parts[ruleIndex].ActionRuleID || resolution != nil || report.ResolutionChecks > 0 {
				return finish(Output{}, coordinationInvalid("scene_resolution_unselected", "needs_resolution", "one-checkpoint-for-program-selected-later-rule"))
			}
			prefixInput := make([]sceneInput, len(parts))
			for i, p := range parts {
				prefixInput[i] = sceneInput{Text: p.Text, IntentType: p.IntentType, AddresseeID: p.AddresseeID, Visibility: p.Visibility, BeatIDs: []string{}, Status: "not_executed", UnexecutedReason: "前段尚未执行", ActionRuleID: p.ActionRuleID}
			}
			for _, beat := range r.PrefixBeats {
				if beat.ActorID == "player" && beat.Attempt != nil {
					i := *beat.Attempt.InputFragmentIndex
					if i >= ruleIndex {
						validation = coordinationInvalid("scene_checkpoint_rule_in_prefix", "prefix_beats", "only-actions-before-the-selected-rule")
						break
					}
					prefixInput[i].BeatIDs = append(prefixInput[i].BeatIDs, beat.LocalID)
				}
			}
			if len(r.PrefixBeats) > 0 && ruleIndex > 0 && len(prefixInput[0].BeatIDs) == 0 {
				prefixInput[0].BeatIDs = []string{r.PrefixBeats[0].LocalID}
			}
			if validation == nil {
				candidate := SceneDraft{InputMap: prefixInput, Beats: r.PrefixBeats, ElapsedMinutes: r.PrefixElapsedMinutes}
				workspace, err := compileSceneWorkspace(snapshot, run, &candidate, selected, sceneLedger(snapshot, selected, call.providedSources), nil, true)
				if err != nil {
					validation = err
				} else {
					preparedSnapshot := snapshot
					preparedSnapshot.Summary.Clock = workspace.Clock
					preparedSnapshot.Positions = workspace.Positions
					preparedSnapshot.States = workspace.States
					preparedSnapshot.Relationships = workspace.Relationships
					preparedSnapshot.Items = workspace.Items
					rule, _ := actionRuleByID(snapshot.Definition, r.RuleID)
					met, _ := evaluateFactConditions(preparedSnapshot, workspace, rule.Conditions, "player")
					report.ResolutionChecks++
					prefix = slices.Clone(r.PrefixBeats)
					prefixBound = true
					if met {
						prepared, err := prepareActionResolution(callCtx, store, preparedSnapshot, sceneRuleRun(run, parts, ruleIndex), r.RuleID)
						if err != nil {
							return finish(Output{}, err)
						}
						resolution = &prepared
					} else {
						resolutionBlocked = true
					}
					continue
				}
			}
		}
		var output Output
		if validation == nil && parsed.Draft != nil {
			if intent != nil {
				validation = validateSceneRuleBinding(parsed.Draft, parts)
			} else {
				for _, part := range parsed.Draft.InputMap {
					if part.ActionRuleID != "" {
						validation = coordinationInvalid("scene_rule_unselected", "input_map.action_rule_id", "program-selected-rule-only")
					}
				}
			}
		}
		if validation == nil && parsed.Draft != nil {
			if len(prefix) > len(parsed.Draft.Beats) || len(prefix) > 0 && !reflect.DeepEqual(prefix, parsed.Draft.Beats[:len(prefix)]) {
				validation = coordinationInvalid("scene_prefix_changed", "beats", "unchanged-validated-prefix")
			} else {
				ledger := sceneLedger(snapshot, selected, call.providedSources)
				output, validation = compileScene(snapshot, run, parsed.Draft, selected, ledger, resolution)
			}
		}
		if validation == nil {
			return finish(output, nil)
		}
		var expansion *sceneActorExpansion
		if errors.As(validation, &expansion) {
			if report.ContextSupplements != 0 {
				return finish(Output{}, coordinationInvalid("scene_context_supplement_limit", "selected_entity_ids", "one-shared-context-supplement"))
			}
			report.ContextSupplements++
			for _, id := range append([]string{expansion.EntityID}, expansion.EntityIDs...) {
				if !slices.Contains(selected, id) {
					selected = append(selected, id)
				}
			}
			if err := validateSceneMemoryOwners(snapshot, selected); err != nil {
				return finish(Output{}, err)
			}
			continue
		}
		var materialExpansion *sceneMaterialExpansion
		if errors.As(validation, &materialExpansion) {
			if report.ContextSupplements != 0 || len(materialExpansion.IDs) > 4 {
				return finish(Output{}, coordinationInvalid("scene_context_supplement_limit", "progress_updates", "one-shared-supplement-at-most-four-files"))
			}
			for _, id := range materialExpansion.IDs {
				if _, ok := call.availableMaterials[id]; !ok {
					return finish(Output{}, ErrContextSourceMissing)
				}
			}
			report.ContextSupplements++
			readIDs = slices.Clone(materialExpansion.IDs)
			continue
		}
		if report.Repairs != 0 {
			return finish(Output{}, validation)
		}
		report.Repairs++
		var failure *GenerationError
		if errors.As(validation, &failure) {
			correction = "code=" + failure.Code + ";field=" + failure.Field + ";expected=" + failure.Expected
		} else {
			correction = "候选没有通过声明合同；重新生成完整候选。"
		}
	}
	return finish(Output{}, coordinationInvalid("scene_call_budget_exhausted", "scene", "at-most-four-core-requests"))
}

func sceneRuleRun(run wiaworld.Run, parts []InputFragment, index int) wiaworld.Run {
	if len(parts) > 1 {
		run.InputPart = index + 1
	}
	run.Input = parts[index].Text
	return run
}

func validateSceneRuleBinding(draft *SceneDraft, parts []InputFragment) error {
	if len(draft.InputMap) != len(parts) {
		return coordinationInvalid("scene_rule_input_changed", "input_map", "same-program-parsed-original-fragments")
	}
	for i, part := range parts {
		actual := draft.InputMap[i]
		bindings := []struct {
			name string
			want any
			got  any
		}{
			{"text", part.Text, actual.Text},
			{"intent_type", part.IntentType, actual.IntentType},
			{"wait_minutes", part.WaitMinutes, actual.WaitMinutes},
			{"action_rule_id", part.ActionRuleID, actual.ActionRuleID},
			{"addressee_id", part.AddresseeID, actual.AddresseeID},
			{"visibility", part.Visibility, actual.Visibility},
		}
		for _, binding := range bindings {
			if binding.want != binding.got {
				expected := "unchanged-original-fragment"
				if binding.name != "text" {
					expected = wire.MarshalJSON(binding.want)
				}
				return coordinationInvalid("scene_rule_binding_changed", fmt.Sprintf("input_map[%d].%s", i, binding.name), expected)
			}
		}
	}
	return nil
}
