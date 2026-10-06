package turn

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type sceneMaterialExpansion struct{ IDs []string }

func (e *sceneMaterialExpansion) Error() string {
	return "selected world assessment requires frozen material"
}

type sceneWorldObject struct {
	Type, ID    string
	MaterialIDs []string
	Node        *plot.Node
	Generated   int
	Basis       string
	GateChecked bool
	GateMet     bool
	GateSources []string
}

type sceneWorldCompiler struct {
	base          Snapshot
	draft         *SceneDraft
	consumed      map[string]bool
	processed     map[int]bool
	worldSelected *sceneWorldObject
	worldConsumed map[string]bool
	stimulated    map[string]bool
}

func newSceneWorldCompiler(snapshot Snapshot, draft *SceneDraft, out *Output) *sceneWorldCompiler {
	if snapshot.Plot != nil {
		p := snapshot.PlotProgress
		p.Nodes = map[string]plot.NodeState{}
		for id, state := range snapshot.PlotProgress.Nodes {
			state.Evidence = slices.Clone(state.Evidence)
			p.Nodes[id] = state
		}
		out.PlotProgress = &p
	}
	if snapshot.Definition.EventGeneration != nil {
		p := snapshot.GeneratedEvents
		p.Active = slices.Clone(p.Active)
		for i := range p.Active {
			p.Active[i].State.Evidence = slices.Clone(p.Active[i].State.Evidence)
		}
		out.GeneratedEvents = &p
	}
	return &sceneWorldCompiler{base: snapshot, draft: draft, consumed: map[string]bool{}, processed: map[int]bool{}, worldConsumed: map[string]bool{}, stimulated: map[string]bool{}}
}

func (w *sceneWorldCompiler) selectObject(snapshot Snapshot, out Output) *sceneWorldObject {
	if snapshot.Definition.Progression != nil && w.worldConsumed["open"] {
		snapshot.Definition.Progression, snapshot.Plot = nil, nil
	}
	object := selectSceneWorld(snapshot, out)
	if object == nil || (object.Type == "legacy_node" || object.Type == "generated_event") && w.worldConsumed["event"] {
		return nil
	}
	return object
}

func sceneWorldWorking(snapshot Snapshot, out Output) Snapshot {
	snapshot.Summary.Clock = out.Clock
	snapshot.Positions, snapshot.States, snapshot.Relationships, snapshot.Items = out.Positions, out.States, out.Relationships, out.Items
	snapshot.OpenProgress = out.OpenProgress
	if out.PlotProgress != nil {
		snapshot.PlotProgress = *out.PlotProgress
	}
	if out.GeneratedEvents != nil {
		snapshot.GeneratedEvents = *out.GeneratedEvents
	}
	return snapshot
}

// Selection reuses current progression ordering and semantic basis, against the
// actual working clock and positions rather than the forecast window.
func selectSceneWorld(snapshot Snapshot, out Output) *sceneWorldObject {
	working := sceneWorldWorking(snapshot, out)
	minute, err := plot.ClockMinute(out.Clock)
	if err != nil {
		return nil
	}
	if def := snapshot.Definition.Progression; def != nil && out.OpenProgress != nil {
		for _, schedule := range def.ExternalSchedules {
			if schedule.AtMinute <= minute && out.OpenProgress.ExternalApplied[schedule.ID] == "" {
				return &sceneWorldObject{Type: "external_schedule", ID: schedule.ID, MaterialIDs: []string{schedule.MaterialID}}
			}
		}
		start := 0
		for i, d := range def.Developments {
			if d.ID == out.OpenProgress.DevelopmentCursor {
				start = (i + 1) % len(def.Developments)
			}
		}
		for offset := range len(def.Developments) {
			d := def.Developments[(start+offset)%len(def.Developments)]
			if !developmentRelevant(working, out.Events, d) {
				continue
			}
			basis := developmentBasis(working, out, d)
			if out.OpenProgress.DevelopmentChecks[d.ID] != basis {
				return &sceneWorldObject{Type: "development", ID: d.ID, MaterialIDs: d.MaterialIDs, Basis: basis}
			}
		}
	}
	if snapshot.Definition.Progression == nil {
		if node, due, ok := NextPlotNode(working); ok && minute >= due {
			return &sceneWorldObject{Type: "legacy_node", ID: node.ID, Node: &node}
		}
	}
	if index, due, ok := NextGeneratedEvent(working); ok && minute >= due {
		if _, authorDue, author := NextPlotNode(working); author && minute >= authorDue {
			return nil
		}
		e := working.GeneratedEvents.Active[index]
		return &sceneWorldObject{Type: "generated_event", ID: e.Node.ID, Node: &e.Node, Generated: index}
	}
	return nil
}

func sceneWorldWindow(snapshot Snapshot) Section {
	minute, _ := plot.ClockMinute(snapshot.Summary.Clock)
	window := map[string]any{"forecast_end_minute": minute + PlotTimeLimit(snapshot), "actual_selection": "按节点实际时间、工作位置、状态、当前轮转确认；预备条目不授予提前执行资格"}
	if def := snapshot.Definition.Progression; def != nil {
		window["development_candidates"] = def.Developments
		window["external_candidates"] = def.ExternalSchedules
	}
	plans := newContextTable("id", "owner_id", "next_check", "last_check", "status")
	owners := plot.DuePlanOwners(snapshot.OpenProgressPlans(), minute+PlotTimeLimit(snapshot), 2)
	for _, plan := range snapshot.OpenProgressPlans() {
		if slices.Contains(owners, plan.OwnerID) {
			plans.add(plan.ID, plan.OwnerID, plan.NextCheck, plan.LastCheck, plan.Status)
		}
	}
	window["plan_candidates"] = plans
	var sources []string
	if node, due, ok := NextPlotNode(snapshot); ok {
		id := "definition:" + snapshot.Plot.Revision + ":" + node.ID
		window["legacy_node"] = map[string]any{"node": node, "due": due, "source_id": id, "author_facts": snapshot.Plot.Facts}
		sources = append(sources, id)
	}
	if index, due, ok := NextGeneratedEvent(snapshot); ok {
		e := snapshot.GeneratedEvents.Active[index]
		id := "definition:generated.v1:" + e.Node.ID
		window["generated_event"] = map[string]any{"event": e, "due": due, "source_id": id}
		sources = append(sources, id, e.StartID)
	}
	window["event_policy"] = snapshot.Definition.EventGeneration
	return Section{Name: "scene_world_window", Text: "世界评估候选窗口：" + wire.MarshalJSON(window), Sources: sources}
}

func requireSceneWorldMaterials(snapshot Snapshot, object *sceneWorldObject, ledger *SourceLedger) error {
	missing := []string{}
	for _, id := range object.MaterialIDs {
		m, ok := story.MaterialByID(snapshot.Definition, id)
		if !ok {
			return ErrContextSourceMissing
		}
		if _, provided := ledger.Author[m.SourceID(snapshot.Definition.Revision)]; !provided {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return &sceneMaterialExpansion{IDs: missing}
	}
	return nil
}

func (w *sceneWorldCompiler) authorizeBeat(snapshot Snapshot, out Output, beat sceneBeat, ledger *SourceLedger) error {
	if beat.Kind != "world_change" {
		return nil
	}
	for _, p := range w.draft.ProgressUpdates {
		if !slices.Contains(p.BeatIDs, beat.LocalID) || p.Type == "personal_plan" {
			continue
		}
		if p.Status == "deferred" || p.OffsetMinutes != beat.OffsetMinutes || w.consumed[p.Type+":"+p.ID] {
			return coordinationInvalid("scene_world_beat_invalid", "progress_updates.beat_ids", "current-selected-nondeferred-assessment")
		}
		object := w.worldSelected
		if object == nil {
			object = w.selectObject(snapshot, out)
		}
		if object == nil || object.Type != p.Type || object.ID != p.ID {
			return coordinationInvalid("scene_world_unselected", "world_change", "actually-selected-world-object")
		}
		if err := requireSceneWorldMaterials(snapshot, object, ledger); err != nil {
			return err
		}
		if object.Node != nil {
			met, evidence := object.GateMet, object.GateSources
			if !object.GateChecked {
				met, evidence = evaluateFactConditions(snapshot, out, object.Node.Requirements, "")
			}
			if !met && len(object.Node.Requirements) > 0 {
				return coordinationInvalid("scene_world_condition_unmet", "world_change", "no-occurrence-before-structured-conditions")
			}
			object.GateChecked, object.GateMet, object.GateSources = true, met, evidence
			for _, recipient := range beat.Recipients {
				if !slices.Contains(object.Node.Audience, recipient) {
					return coordinationInvalid("scene_world_audience_invalid", "recipients", "selected-node-audience")
				}
			}
		}
		w.worldSelected = object
		return nil
	}
	if offer := w.draft.EventOffer; offer != nil && slices.Contains(offer.InitialBeatIDs, beat.LocalID) {
		return w.validateOfferTrigger(snapshot, out, ledger)
	}
	return coordinationInvalid("scene_world_beat_unbound", "world_change", "selected-progress-or-qualified-event-offer")
}

func (w *sceneWorldCompiler) applyReady(snapshot *Snapshot, out *Output, run wiaworld.Run, ledger *SourceLedger, done map[string]bool, plansSeen map[string]bool) error {
	current, _ := plot.ClockMinute(out.Clock)
	start, _ := plot.ClockMinute(w.base.Summary.Clock)
	for index, p := range w.draft.ProgressUpdates {
		if w.processed[index] || p.OffsetMinutes > current-start {
			continue
		}
		ready := true
		for _, id := range p.BeatIDs {
			ready = ready && done[id]
		}
		if !ready {
			continue
		}
		if p.OffsetMinutes != current-start {
			return coordinationInvalid("scene_progress_time_invalid", "progress_updates.offset_minutes", "assessment-at-actual-working-node-time")
		}
		key := p.Type + ":" + p.ID
		if w.consumed[key] {
			return coordinationInvalid("scene_progress_duplicate", "progress_updates", "one-assessment-per-object")
		}
		owner, author := "world", true
		var plan *wiaworld.PersonalPlan
		if p.Type == "personal_plan" {
			if expansion := sceneDueOwnerExpansion(*snapshot, out.Clock); expansion != nil {
				return expansion
			}
			for _, original := range w.base.OpenProgressPlans() {
				if original.ID == p.ID {
					copy := original
					plan = &copy
				}
			}
			if plan == nil || !slices.Contains(snapshot.sceneEntities, plan.OwnerID) || plan.Status != "active" || plan.NextCheck > current || plan.LastCheck == current || !slices.Contains(plot.DuePlanOwners(w.base.OpenProgressPlans(), current, 2), plan.OwnerID) {
				return coordinationInvalid("scene_plan_review_unselected", "progress_updates.id", "actually-due-selected-owner-and-plan")
			}
			owner, author = plan.OwnerID, false
		}
		var object *sceneWorldObject
		if plan == nil {
			object = w.worldSelected
			if object == nil {
				object = w.selectObject(*snapshot, *out)
			}
			if object == nil || object.ID != p.ID || object.Type != p.Type {
				return coordinationInvalid("scene_world_unselected", "progress_updates", "actual-world-order-and-relevance")
			}
			if err := requireSceneWorldMaterials(*snapshot, object, ledger); err != nil {
				return err
			}
		}
		basis, err := ledger.resolve(owner, p.Basis, author)
		if err != nil {
			return err
		}
		if err := sceneDurableSources(snapshot, basis); err != nil {
			return err
		}
		rootID := run.RunID + ":world:" + key
		if plan != nil {
			if p.Ending != "" || p.Status == "deferred" && plansSeen[p.ID] {
				return coordinationInvalid("scene_plan_review_conflict", "progress_updates", "consistent-explicit-update-and-review")
			}
			if p.Status == "occurred" {
				w.stimulated[plan.OwnerID] = true
				for i := range out.OpenProgress.Plans {
					currentPlan := &out.OpenProgress.Plans[i]
					if currentPlan.ID == p.ID && currentPlan.Status == "active" && currentPlan.NextCheck <= current && !plansSeen[p.ID] {
						currentPlan.LastCheck, currentPlan.NextCheck = current, current+30
						currentPlan.Version++
					}
				}
			}
		} else {
			if object.Node != nil {
				met, evidence := object.GateMet, object.GateSources
				if !object.GateChecked {
					met, evidence = evaluateFactConditions(*snapshot, *out, object.Node.Requirements, "")
				}
				if len(object.Node.Requirements) > 0 && !met {
					expected := "deferred"
					if object.Node.OnUnmet == "skip" {
						expected = "skipped"
					}
					if p.Status != expected || len(p.BeatIDs) > 0 || p.Ending != "" {
						return coordinationInvalid("scene_world_condition_unmet", "progress_updates", "structured-condition-result")
					}
				}
				// Structured condition evidence is supplied by the program. It is
				// recorded at the gate before an occurrence changes its own premise.
				basis = append(basis, evidence...)
			}
			if err := w.applyWorldProgress(out, run, p, object, basis, current, &rootID); err != nil {
				return err
			}
			if object.Type == "external_schedule" || object.Type == "development" {
				w.worldConsumed["open"] = true
			} else {
				w.worldConsumed["event"] = true
			}
			w.worldSelected = nil
		}
		if p.Status != "deferred" {
			stage := 1
			for index, beat := range w.draft.Beats {
				if done[beat.LocalID] {
					stage = max(stage, index+1)
				}
			}
			event := wiaworld.Event{EventID: rootID, EventType: "world_assessed", ActorID: owner, Content: p.Content, RunID: run.RunID, Stage: stage, SceneVersion: out.SceneVersion, SourceType: "plot_" + p.Status, BasisEventIDs: eventBasisSources(basis), CreatedAt: time.Now().UTC()}
			if object != nil && object.Type == "generated_event" {
				event.ProjectionParentID = w.base.GeneratedEvents.Active[object.Generated].StartID
			}
			out.Events = append(out.Events, event)
			snapshot.Sources[rootID] = SourceMetadata{ID: rootID, Actor: owner, Kind: event.EventType, RunID: run.RunID, Stage: event.Stage, SceneVersion: out.SceneVersion}
		}
		w.processed[index], w.consumed[key] = true, true
		snapshot.OpenProgress = cloneOpenProgress(out.OpenProgress)
	}
	return nil
}

func sceneDueOwnerExpansion(snapshot Snapshot, clock string) *sceneActorExpansion {
	minute, err := plot.ClockMinute(clock)
	if err != nil {
		return nil
	}
	missing := []string{}
	for _, owner := range plot.DuePlanOwners(snapshot.OpenProgressPlans(), minute, 2) {
		if !slices.Contains(snapshot.sceneEntities, owner) {
			missing = append(missing, owner)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &sceneActorExpansion{EntityID: missing[0], EntityIDs: missing}
}

func (w *sceneWorldCompiler) applyWorldProgress(out *Output, run wiaworld.Run, p sceneProgress, object *sceneWorldObject, basis []string, minute int, rootID *string) error {
	if p.Ending != "" && (object.Type != "legacy_node" || object.Node == nil || !object.Node.Terminal || p.Status == "deferred" || w.base.Summary.Mode != "guided") {
		return coordinationInvalid("scene_ending_invalid", "progress_updates.ending", "settled-guided-terminal-node")
	}
	switch object.Type {
	case "external_schedule":
		if p.Status != "deferred" {
			out.OpenProgress.ExternalApplied[p.ID] = *rootID
		}
	case "development":
		out.OpenProgress.DevelopmentChecks[p.ID] = object.Basis
		out.OpenProgress.DevelopmentCursor = p.ID
	case "legacy_node", "generated_event":
		state := plot.NodeState{Status: p.Status, Content: p.Content, NextCheck: minute + 1, Evidence: slices.Clone(basis)}
		if p.Status != "deferred" {
			state.EventID = *rootID
		}
		if object.Type == "legacy_node" {
			out.PlotProgress.Nodes[p.ID] = state
			out.PlotProgress.Version++
			if p.Ending != "" {
				out.PlotProgress.Ending = p.Ending
			}
		} else if p.Status == "deferred" {
			out.GeneratedEvents.Active[object.Generated].State = state
		} else {
			out.GeneratedEvents.Active = slices.Delete(out.GeneratedEvents.Active, object.Generated, object.Generated+1)
			out.GeneratedEvents.Completed++
		}
	}
	return nil
}

func (w *sceneWorldCompiler) finish(snapshot Snapshot, out *Output, run wiaworld.Run, ledger *SourceLedger, plansSeen map[string]bool) error {
	if len(w.processed) != len(w.draft.ProgressUpdates) {
		return coordinationInvalid("scene_progress_unapplied", "progress_updates", "all-assessments-bound-to-processed-node-time")
	}
	if expansion := sceneDueOwnerExpansion(snapshot, out.Clock); expansion != nil {
		return expansion
	}
	if w.selectObject(snapshot, *out) != nil {
		return coordinationInvalid("scene_world_assessment_missing", "progress_updates", "actual-selected-world-assessment-including-deferred")
	}
	for _, p := range w.draft.ProgressUpdates {
		if p.Type == "personal_plan" && p.Status == "deferred" && plansSeen[p.ID] {
			return coordinationInvalid("scene_plan_review_conflict", "progress_updates", "deferred-review-without-explicit-plan-update")
		}
	}
	minute, _ := plot.ClockMinute(out.Clock)
	owners := plot.DuePlanOwners(w.base.OpenProgressPlans(), minute, 2)
	for _, p := range w.base.OpenProgressPlans() {
		if p.Status == "active" && p.NextCheck <= minute && p.LastCheck != minute && slices.Contains(owners, p.OwnerID) && !w.consumed["personal_plan:"+p.ID] {
			return coordinationInvalid("scene_plan_review_missing", "progress_updates", "actual-due-selected-plan-review")
		}
	}
	if w.draft.EventOffer != nil {
		return w.publishOffer(snapshot, out, run, ledger)
	}
	return nil
}

func (w *sceneWorldCompiler) validateOfferTrigger(snapshot Snapshot, out Output, ledger *SourceLedger) error {
	offer, policy := w.draft.EventOffer, snapshot.Definition.EventGeneration
	if offer == nil || policy == nil || out.GeneratedEvents == nil || len(out.GeneratedEvents.Active) >= policy.MaxActive || out.GeneratedEvents.LastOfferTurn > 0 && snapshot.Summary.TurnSeq+1-out.GeneratedEvents.LastOfferTurn < policy.CooldownTurns || offer.AfterMinutes < 1 || offer.AfterMinutes > 120 || strings.TrimSpace(offer.Condition) == "" || strings.TrimSpace(offer.Development) == "" || len(offer.InitialBeatIDs) == 0 || !uniqueSceneIDs(offer.InitialBeatIDs) || !slices.Contains(policy.Locations, offer.Location) {
		return coordinationInvalid("scene_event_offer_invalid", "event_offer", "enabled-policy-qualified-cooldown-and-active-capacity")
	}
	working := sceneWorldWorking(snapshot, out)
	if w.worldConsumed["event"] {
		return coordinationInvalid("scene_event_existing_priority", "event_offer", "current-authored-or-existing-event-assessment-consumes-offer-round")
	}
	minute, _ := plot.ClockMinute(out.Clock)
	if _, due, ok := NextPlotNode(working); ok && minute >= due {
		return coordinationInvalid("scene_event_author_priority", "event_offer", "authored-node-before-new-event")
	}
	if _, due, ok := NextGeneratedEvent(working); ok && minute >= due {
		return coordinationInvalid("scene_event_existing_priority", "event_offer", "existing-event-before-new-event")
	}
	var trigger *sceneBeat
	for i, beat := range w.draft.Beats {
		if beat.LocalID == offer.TriggerBeatID {
			trigger = &w.draft.Beats[i]
		}
	}
	if trigger == nil || trigger.Kind != "action_result" || trigger.Status == nil || *trigger.Status != "succeeded" && *trigger.Status != "partial" {
		return coordinationInvalid("scene_event_trigger_invalid", "event_offer.trigger_beat_id", "current-successful-or-partial-action-result")
	}
	if _, err := ledger.resolve("world", []string{"beat:" + trigger.LocalID}, true); err != nil {
		return err
	}
	if offer.Kind == "arrival" {
		found := false
		for _, move := range trigger.Effects.Movements {
			found = found || move.EntityID == trigger.ActorID && move.To == offer.Location
		}
		if !found {
			return coordinationInvalid("scene_event_arrival_invalid", "event_offer.location", "validated-trigger-arrival-location")
		}
	} else if offer.Kind != "significant_change" || len(trigger.Effects.StateEffects)+len(trigger.Effects.ItemTransfers)+len(trigger.Effects.RelationshipEffects) == 0 || snapshot.Definition.Capabilities["spatial"] == 1 && out.Positions[trigger.ActorID] != offer.Location {
		return coordinationInvalid("scene_event_change_invalid", "event_offer.kind", "actual-significant-change-at-trigger-location")
	}
	return nil
}

func (w *sceneWorldCompiler) publishOffer(snapshot Snapshot, out *Output, run wiaworld.Run, ledger *SourceLedger) error {
	if err := w.validateOfferTrigger(snapshot, *out, ledger); err != nil {
		return err
	}
	offer := w.draft.EventOffer
	triggerIDs, _ := ledger.resolve("world", []string{"beat:" + offer.TriggerBeatID}, true)
	premises, roots := []string{}, []string{}
	lastIndex := -1
	for _, id := range offer.InitialBeatIDs {
		found := false
		for index, beat := range w.draft.Beats {
			if beat.LocalID != id {
				continue
			}
			if beat.Kind != "world_change" || !slices.Contains(beat.Basis, "beat:"+offer.TriggerBeatID) {
				return coordinationInvalid("scene_event_initial_invalid", "event_offer.initial_beat_ids", "world-change-with-current-trigger-basis")
			}
			if index <= lastIndex {
				return coordinationInvalid("scene_event_initial_order_invalid", "event_offer.initial_beat_ids", "chronological-world-nodes")
			}
			lastIndex = index
			for _, recipient := range beat.Recipients {
				if recipient != "player" && !slices.Contains(snapshot.Definition.EventGeneration.Participants, recipient) {
					return coordinationInvalid("scene_event_audience_invalid", "recipients", "declared-event-participants")
				}
			}
			root, err := ledger.resolve("world", []string{"beat:" + id}, true)
			if err != nil {
				return err
			}
			roots = append(roots, root...)
			premises = append(premises, beat.Content)
			found = true
		}
		if !found {
			return coordinationInvalid("scene_event_initial_missing", "event_offer.initial_beat_ids", "existing-world-nodes")
		}
	}
	premise := strings.Join(premises, "\n")
	if len([]rune(premise+offer.Condition+offer.Development)) > 4000 {
		return coordinationInvalid("scene_event_content_limit", "event_offer", "at-most-4000-runes")
	}
	key := run.InputID
	if key == "" {
		key = run.RunID
	}
	minute, _ := plot.ClockMinute(w.base.Summary.Clock)
	for _, beat := range w.draft.Beats {
		if beat.LocalID == offer.InitialBeatIDs[0] {
			minute += beat.OffsetMinutes
		}
	}
	endMinute, _ := plot.ClockMinute(out.Clock)
	if minute+offer.AfterMinutes <= endMinute {
		return coordinationInvalid("scene_event_time_crossed", "event_offer.after_minutes", "scene-stops-before-new-event-reassessment")
	}
	event := GeneratedEvent{Node: plot.Node{ID: key, AtMinute: minute + offer.AfterMinutes, After: []string{}, Condition: offer.Condition, Development: offer.Development, Audience: append([]string{"player"}, snapshot.Definition.EventGeneration.Participants...)}, StartID: roots[0], TriggerID: triggerIDs[0], Location: offer.Location, Premise: premise}
	out.GeneratedEvents.Active = append(out.GeneratedEvents.Active, event)
	out.GeneratedEvents.LastOfferTurn = snapshot.Summary.TurnSeq + 1
	out.Events = append(out.Events, wiaworld.Event{EventID: "generated:" + key + ":plan", EventType: "generated_event_plan", ActorID: "world", Content: wire.MarshalJSON(event), RunID: run.RunID, Stage: len(w.draft.Beats) + 1, SceneVersion: out.SceneVersion, SourceType: "author_plan", ProjectionParentID: roots[0], BasisEventIDs: eventBasisSources(append(roots, triggerIDs...)), CreatedAt: time.Now().UTC()})
	return nil
}

func sceneWorldEventID(run wiaworld.Run, index int) string {
	return fmt.Sprintf("%s:scene:%d:world", run.RunID, index+1)
}
