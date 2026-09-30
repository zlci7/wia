package turn

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func ownershipNPCFixture() (Snapshot, story.Definition) {
	characters := []wiaworld.Character{
		{EntityID: "npc:a", DefinitionID: "a", Name: "甲", Role: "掌柜", Profile: "谨慎", InitialConcerns: "守住约定", InScene: true},
		{EntityID: "npc:b", DefinitionID: "b", Name: "乙", Role: "旅人", Profile: "沉静", InitialConcerns: "查明来意", InScene: true},
	}
	definition := story.Definition{Summary: story.Summary{ID: "fixture", Mode: "open"}, Background: "雨夜客栈", Characters: characters}
	snapshot := Snapshot{
		Summary:     wiaworld.WorldSummary{WorldID: "world", GameID: "fixture", Clock: "第 1 日 19:00"},
		Definition:  definition,
		Narrative:   wiaworld.DefaultNarrativeSettings(),
		Characters:  append([]wiaworld.Character{}, characters...),
		Perceptions: map[string][]wiaworld.Perception{},
		Memories:    map[string][]wiaworld.Memory{},
	}
	return snapshot, definition
}

func TestRecallBudgetPreservesRequiredAndCausalGroups(t *testing.T) {
	archive := []memory.MemorySource{{ID: "attempt", Seq: 1, RunID: "old", Content: "铜钥匙" + strings.Repeat("长篇旧经历", 15000)}, {ID: "result", Seq: 2, RunID: "old", Content: "尝试失败"}}
	material := withRecall(Material{System: "NPC", Required: "本轮刺激与完整近期经历", RequiredSources: []string{"current"}}, memoryProjection{Context: MemoryContext{Archive: archive}}, "铜钥匙")
	req, report, err := (ContextComposer{}).Build(material, material.System, 100)
	if err != nil {
		t.Fatalf("optional recall blocked current turn: %v", err)
	}
	if !strings.Contains(req.Input, "本轮刺激与完整近期经历") || strings.Contains(req.Input, "长篇旧经历") || strings.Contains(req.Input, "尝试失败") {
		t.Fatal("required or causal group boundary violated")
	}
	if report.ExcludedSources != 2 || !strings.Contains(req.Input, "预算") {
		t.Fatal("recall exclusion not reported", report)
	}
}

func TestMemoryTailIsRequiredAndSearchBounded(t *testing.T) {
	var archive []memory.MemorySource
	for i := 1; i <= 20; i++ {
		archive = append(archive, memory.MemorySource{ID: fmt.Sprint(i), Seq: int64(i), RunID: fmt.Sprint(i / 2), Content: "旧码头的铜钥匙"})
	}
	if got := memory.SearchMemory(archive, "铜钥匙", 5); len(got) != 5 || got[0].Seq != 20 {
		t.Fatal(got)
	}
	snapshot := Snapshot{LongMemory: map[string]MemoryContext{"player": {Tail: []memory.MemorySource{{Content: strings.Repeat("完整经历", 15000)}}}}}
	material := withLongMemory(Material{System: "test", Required: "current"}, snapshot, "player", "")
	if _, _, err := (ContextComposer{}).Build(material, material.System, 100); !errors.Is(err, ErrContextCapacity) {
		t.Fatal("silently truncated recent history", err)
	}
}

func TestWindowGroupsExitWholeBeforeCapacityFailure(t *testing.T) {
	tail := make([]memory.MemorySource, 0, 4)
	for group := 1; group <= 4; group++ {
		tail = append(tail, memory.MemorySource{ID: fmt.Sprintf("record:%02d", group), Seq: int64(group), RunID: fmt.Sprintf("run:%02d", group), Content: fmt.Sprintf("第%d轮%s", group, strings.Repeat("已提交经历", 500))})
	}
	snapshot := Snapshot{LongMemory: map[string]MemoryContext{"player": {Archive: tail, Tail: tail}}}
	material := withLongMemory(Material{System: "职责", Required: strings.Repeat("本轮必需资料", 1000)}, snapshot, "player", "")
	req, report, err := (ContextComposer{}).Build(material, material.System, 1024)
	if err != nil {
		t.Fatalf("window did not degrade: %v", err)
	}
	if report.Excluded == 0 || !report.RequiredComplete || !strings.Contains(req.Input, tail[len(tail)-1].ID) {
		t.Fatalf("whole-group degradation failed: %+v", report)
	}
	oversized := Snapshot{LongMemory: map[string]MemoryContext{"player": {Archive: []memory.MemorySource{{ID: "only", Seq: 1, RunID: "run:1", Content: strings.Repeat("单组超长经历", 20000)}}, Tail: []memory.MemorySource{{ID: "only", Seq: 1, RunID: "run:1", Content: strings.Repeat("单组超长经历", 20000)}}}}}
	tooLarge := withLongMemory(Material{System: "职责", Required: "本轮"}, oversized, "player", "")
	if _, _, err := (ContextComposer{}).Build(tooLarge, tooLarge.System, 1024); !errors.Is(err, ErrContextCapacity) {
		t.Fatalf("oversized single group must fail loudly: %v", err)
	}
}

func TestComposeNPCScopesInitialConcern(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	snapshot.Characters[0].InitialConcerns = "ONLY_FIRST_CHARACTER_CONCERN"
	own := composeNPC(snapshot, definition, snapshot.Characters[0], "", "act", StageInput{PlayerPerception: "我沉默"}, "", 1)
	other := composeNPC(snapshot, definition, snapshot.Characters[1], "", "act", StageInput{PlayerPerception: "我沉默"}, "", 1)
	if !strings.Contains(own.Required, "ONLY_FIRST_CHARACTER_CONCERN") || strings.Contains(other.Required, "ONLY_FIRST_CHARACTER_CONCERN") {
		t.Fatal("concern scope")
	}
}

func TestComposeNPCAutonomousSilentActionHasIndependentChannel(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	material := composeNPC(snapshot, definition, snapshot.Characters[0], "", "act", StageInput{PlayerPerception: "我沉默片刻，不回复"}, "", 1)
	if strings.Contains(material.Required, "基于玩家本轮输入作出一次自然决定") || !strings.Contains(material.Required, "处理自己的事务") {
		t.Fatal("reactive-only task")
	}
	if strings.Contains(material.System, "甲") || strings.Contains(material.System, "乙") {
		t.Fatal("character content in generic system")
	}
}

func TestComposeNPCIgnoresForeignPrivateMaterial(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	snapshot.Summary.Scene = "不应共享的全知旧场景"
	snapshot.Memories["npc:a"] = []wiaworld.Memory{{SourceEventID: "secret", Content: "SECRET_MEMORY"}}
	snapshot.SceneViews = []SceneView{{Recipient: "player", Version: 1, Content: "公开"}, {Recipient: "npc:a", Version: 1, Content: "PRIVATE_SCENE"}, {Recipient: "npc:b", Version: 1, Content: "乙可见情境"}}
	material := composeNPC(snapshot, definition, snapshot.Characters[1], "npc:a", "speak", StageInput{PlayerPerception: "看见交谈，但未听清"}, "", 1)
	req, _, err := (ContextComposer{}).Build(material, material.System, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET_MEMORY", "PRIVATE_SCENE", "不应共享的全知旧场景"} {
		if strings.Contains(req.Input, secret) {
			t.Fatalf("unexpected source %s", secret)
		}
	}
}

func TestInitialSceneViewsUseOnlyAuthorizedRecords(t *testing.T) {
	snapshot, _ := ownershipNPCFixture()
	snapshot.Summary.TurnSeq = 10
	snapshot.Events = []wiaworld.Event{{EventID: "public-result", EventType: "turn_settled", Content: "茶在桌上。"}, {EventID: "secret", EventType: "npc_action_result", Content: "隐藏信件。"}}
	snapshot.Perceptions["npc:a"] = []wiaworld.Perception{{SourceEventID: "public-result", SourceType: "action_succeeded", Content: "茶在桌上。"}}
	snapshot.SceneViews = initialSceneViews(snapshot)
	for _, id := range []string{"player", "npc:a"} {
		if SceneFor(snapshot, id) != "茶在桌上。" {
			t.Fatalf("%s: %s", id, SceneFor(snapshot, id))
		}
	}
}

func TestComposeNPCRequiredPolicyCannotBeTruncated(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	snapshot.Narrative.Policies.NPC = strings.Repeat("策略", 2000)
	material := composeNPC(snapshot, definition, snapshot.Characters[0], "", "act", StageInput{PlayerPerception: "我沉默"}, "", 1)
	composer := ContextComposer{Window: model.WindowLimits{ContextTokens: 1024, OutputTokens: 512}}
	if _, _, err := composer.Build(material, material.System, 512); !errors.Is(err, ErrContextCapacity) {
		t.Fatal("required policy was truncated", err)
	}
}

func TestComposeNPCIncludesSpeakingExamples(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	definition.Characters[0].SpeakingExamples = []string{"例句甲", "例句乙"}
	material := composeNPC(snapshot, definition, definition.Characters[0], "player", "speak", StageInput{PlayerPerception: "我问他灯的事。"}, "", 1)
	for _, example := range definition.Characters[0].SpeakingExamples {
		if !strings.Contains(material.Required, example) {
			t.Fatalf("the prompt dropped a speaking example %q", example)
		}
	}
	if !strings.Contains(material.Required, "只作语气与用词参考") {
		t.Fatal("the prompt must state that samples are style, not events")
	}
}

func TestUnaddressedNPCUsesContextualInitiativeAndPassiveIntentIsDropped(t *testing.T) {
	snapshot, definition := ownershipNPCFixture()
	material := composeNPC(snapshot, definition, snapshot.Characters[0], "", "act", StageInput{PlayerPerception: "走进大门看看"}, "", 1)
	for _, want := range []string{"按情境主动", "可以沉默，也可以在规则允许时主动介入", "普通进入、环顾和走动不要求每个在场人物都回应", "继续观察", "不属于 action_intent"} {
		if !strings.Contains(material.Required, want) {
			t.Fatalf("NPC prompt missing %q: %s", want, material.Required)
		}
	}
	for _, input := range []string{"保持观察", "观察异常", "继续等待。", "提高警惕", "维持原位"} {
		if normalizeNPCActionIntent(input) != "" {
			t.Fatalf("passive intent survived: %q", input)
		}
	}
	if got := normalizeNPCActionIntent("走到门边关上门"); got != "走到门边关上门" {
		t.Fatalf("state-changing action was dropped: %q", got)
	}
}
