package storyapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/model"
)

func policyRequest(epoch int64, p BehaviorPolicies) UpdateNarrativeSettingsRequest {
	s := defaultNarrativeSettings()
	return UpdateNarrativeSettingsRequest{Perspective: s.Perspective, Length: s.Length, Detail: s.Detail, PlayerElaboration: s.PlayerElaboration, NPCInitiative: s.NPCInitiative, ExpectedContextEpoch: epoch, Policies: &p}
}

func TestBehaviorPoliciesReachOnlyTheirModelCalls(t *testing.T) {
	g := &scriptedGenerator{}
	a := newTestApp(t, g)
	ctx := context.Background()
	w, err := a.createFixtureWorld(ctx, "策略", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	p := BehaviorPolicies{NPC: "POLICY_NPC_ONLY", Coordination: "POLICY_COORD_ONLY", Narration: "POLICY_TEXT_ONLY"}
	_, updated, err := a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, p))
	if err != nil {
		t.Fatal(err)
	}
	logger := &recordingLogger{}
	a.logger = logger
	run, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "policy", Input: "我沉默，不回复", ExpectedContextEpoch: updated.ContextEpoch})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("%+v", done)
	}
	g.mu.Lock()
	requests := append([]string{}, g.requests...)
	g.mu.Unlock()
	counts := map[string]int{}
	for _, req := range requests {
		purpose := "intent"
		if strings.Contains(req, "你是一个重要 NPC") {
			purpose = "npc"
		}
		if strings.Contains(req, "你是场景协调 Agent") {
			purpose = "coordination"
		}
		if strings.Contains(req, "你是玩家正文 Agent") {
			purpose = "narration"
		}
		counts[purpose]++
		for target, marker := range map[string]string{"npc": p.NPC, "coordination": p.Coordination, "narration": p.Narration} {
			if strings.Contains(req, marker) != (target == purpose) {
				t.Fatalf("policy scope %s/%s", target, purpose)
			}
		}
		if purpose != "intent" && !strings.Contains(req, behaviorContract) {
			t.Fatal("fixed priority contract missing")
		}
		if purpose == "npc" && strings.Contains(req, DefaultBehaviorPolicies().NPC) {
			t.Fatal("default NPC policy was appended to custom")
		}
		if purpose == "coordination" && strings.Contains(req, DefaultBehaviorPolicies().Coordination) {
			t.Fatal("default coordination policy was appended")
		}
		if purpose == "narration" && (strings.Contains(req, narrativePacingInstruction()) || !strings.Contains(req, "第二人称")) {
			t.Fatal("policy replacement or explicit option missing")
		}
	}
	for _, purpose := range []string{"intent", "npc", "coordination", "narration"} {
		if counts[purpose] == 0 {
			t.Fatal("missing purpose", purpose)
		}
	}
	if !strings.Contains(logger.String(), "policy_revision=") || strings.Contains(logger.String(), p.NPC) {
		t.Fatal("unsafe or missing policy diagnostics")
	}
}

func TestBehaviorPoliciesPersistIsolateCopyAndRestart(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "策略", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.createFixtureWorld(ctx, "另一世界", "guided", "旅人", "", false)
	if err != nil {
		t.Fatal(err)
	}
	p := BehaviorPolicies{NPC: "主动但不抢话", Coordination: "依情境协调", Narration: "简明收尾"}
	_, updated, err := a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, p))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, BehaviorPolicies{})); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale policy accepted", err)
	}
	if readContextSnapshot(t, a, other.WorldID).Narrative.Policies != (BehaviorPolicies{}) {
		t.Fatal("cross world policies")
	}
	status, _ := a.Status(ctx)
	op, err := a.SaveAs(ctx, w.WorldID, "策略分支", "policy-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); op.Status != "ready" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		op, err = a.CopyOperation(ctx, op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Status != "ready" {
		t.Fatal(op.Status)
	}
	if _, _, err = a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(updated.ContextEpoch, BehaviorPolicies{})); err != nil {
		t.Fatal(err)
	}
	root := a.DataRoot()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if readContextSnapshot(t, reopened, op.TargetWorldID).Narrative.Policies != p {
		t.Fatal("copied custom policy not preserved")
	}
	restored := readContextSnapshot(t, reopened, w.WorldID).Narrative
	if restored.Policies != (BehaviorPolicies{}) {
		t.Fatal("reset not preserved")
	}
	_, revision := behaviorPolicy(restored, "npc")
	if revision != BehaviorPolicyVersion+":npc" {
		t.Fatal(revision)
	}
}

func TestBehaviorLegacyPreferenceAndCorruption(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, _ := a.createFixtureWorld(ctx, "旧设置", "guided", "旅人", "", true)
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if _, err = store.db.Exec("UPDATE meta SET value=? WHERE key='narrative_custom_instruction'", "旧写作偏好"); err != nil {
		t.Fatal(err)
	}
	s, err := loadNarrativeSettings(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Policies.Narration, "旧写作偏好") || s.CustomInstruction != "" {
		t.Fatal("preference lost or duplicated")
	}
	_, _, err = a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, s.Policies))
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := metaGet(ctx, store.db, "narrative_custom_instruction")
	if legacy != "" {
		t.Fatal("two writing policy sources")
	}
	if _, err = store.db.Exec("UPDATE meta SET value='invalid-json' WHERE key='behavior_policies'"); err != nil {
		t.Fatal(err)
	}
	if _, err = loadNarrativeSettings(ctx, store.db); err == nil {
		t.Fatal("corrupt policy silently replaced with defaults")
	}
}

func TestBehaviorPoliciesLimitsBusyAndCapacity(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{delay: 100 * time.Millisecond})
	w, _ := a.createFixtureWorld(ctx, "限制", "guided", "旅人", "", true)
	for _, text := range []string{strings.Repeat("字", 4001), "bad\x00policy"} {
		if _, _, err := a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, BehaviorPolicies{NPC: text})); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("bad policy accepted", err)
		}
	}
	if _, _, err := a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(0, BehaviorPolicies{})); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("missing editing epoch")
	}
	run, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "busy", Input: "我沉默"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(w.ContextEpoch, BehaviorPolicies{})); !errors.Is(err, ErrWorldBusy) {
		t.Fatal("changed during run", err)
	}
	waitRun(t, a, w.WorldID, run.RunID)
	s := contextFixture()
	s.Narrative = defaultNarrativeSettings()
	s.Narrative.Policies.NPC = strings.Repeat("策略", 2000)
	m := composeNPC(s, lanternDefinition(), s.Characters[0], "", "act", npcStageInput{PlayerPerception: "我沉默"}, "", 1)
	c := ContextComposer{Window: model.WindowLimits{ContextTokens: 1024, OutputTokens: 512}}
	if _, _, err = c.Build(m, m.System, 512); !errors.Is(err, ErrContextCapacity) {
		t.Fatal("required policy was truncated", err)
	}
}
