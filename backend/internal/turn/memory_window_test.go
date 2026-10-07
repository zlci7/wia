package turn

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
)

func TestMemoryWindowFitsDigestAndActualSystemPreservingSources(t *testing.T) {
	tail := []memory.MemorySource{}
	for i := 0; i < 6; i++ {
		tail = append(tail, memory.MemorySource{ID: "memory:" + string(rune('a'+i)), RunID: "run:" + string(rune('a'+i)), Content: strings.Repeat("一段完整的旧经历", 70)})
	}
	m := MemoryContext{Digest: memory.MemoryDigest{Scope: "player", Revision: 2, Content: strings.Repeat("已确认的长期约定", 150)}, Tail: tail, Archive: tail}
	base := Material{System: "本轮规则", Required: "本轮事件正文与场景来源", RequiredSources: []string{"current:event", "scene:source"}}
	material := withLongMemory(base, Snapshot{InputBudgetTokens: 20000, LongMemory: map[string]MemoryContext{"player": m}}, "player", "")
	actualSystem := material.System + strings.Repeat("附加字段合同与修复要求", 120)
	minimal := renderMemoryWindow(base, m, "player", tail[len(tail)-1:], "", nil, memory.MemoryRecordsText)
	input, _, _ := contextInput(minimal, nil)
	limit := model.FramedTextInputTokens(model.TextRequest{System: actualSystem, Input: input}) + 50
	composer := ContextComposer{Window: model.WindowLimits{ContextTokens: limit + 512, OutputTokens: 512}}
	req, report, err := composer.Build(material, actualSystem, 512)
	if err != nil {
		t.Fatalf("digest plus newest group fits, but request failed: %v", err)
	}
	if !report.WindowShrunk || !report.RequiredComplete || report.InputTokens > limit {
		t.Fatalf("window was not fitted: %+v", report)
	}
	for _, id := range []string{"current:event", "scene:source", "digest:player:2", "memory:f"} {
		if !slices.Contains(report.SelectedSources, id) {
			t.Errorf("missing required provenance %q: %v", id, report.SelectedSources)
		}
	}
	if !strings.Contains(req.Input, m.Digest.Content) || !strings.Contains(req.Input, base.Required) {
		t.Fatal("required digest or current-turn facts were truncated")
	}
	if len(base.RequiredSources) != 2 || base.RequiredSources[0] != "current:event" {
		t.Fatal("window fitting mutated base provenance")
	}
	// If the digest and newest group cannot fit, the request must fail explicitly.
	_, _, err = (ContextComposer{Window: model.WindowLimits{ContextTokens: 1024, OutputTokens: 512}}).Build(material, actualSystem, 512)
	if !errors.Is(err, ErrContextCapacity) {
		t.Fatalf("irreducible required context: %v", err)
	}
}

// R14: when the whole request does not fit, the recent-experience window must fall back
// to fewer complete groups instead of failing the turn.
func TestRecentWindowShrinksToFitTheWholeRequest(t *testing.T) {
	// Small enough that the character-count rule keeps every group, so the token budget
	// is what has to reduce the window.
	groups := [][]memory.MemorySource{}
	for i := 0; i < memory.TargetRecentGroups; i++ {
		record := memory.MemorySource{ID: "memory:" + string(rune('a'+i)), RunID: "run-" + string(rune('a'+i)), Content: strings.Repeat("已发生的经历", 100)}
		groups = append(groups, []memory.MemorySource{record})
	}
	tail := memory.FlattenGroups(groups)
	if len(memory.MemoryGroups(tail)) != memory.TargetRecentGroups {
		t.Fatalf("fixture groups: %d", len(memory.MemoryGroups(tail)))
	}
	if got := len(memory.MemoryRecordsText(tail)); got > memory.RecentWindowChars {
		t.Fatalf("fixture must fit the size rule so the budget is what binds: %d", got)
	}
	// A budget that fits the base prompt and every group, measured rather than
	// estimated, so the initial window is the whole recent history.
	base := model.FramedTextInputTokens(model.TextRequest{System: "规则", Input: "本轮职责与刺激：玩家输入"})
	testBudget := base + model.FramedTextInputTokens(model.TextRequest{Input: memory.MemoryRecordsText(tail)}) + 1024
	material := withLongMemory(Material{
		Required: "本轮职责与刺激：玩家输入",
		System:   "规则",
	}, Snapshot{InputBudgetTokens: testBudget, LongMemory: map[string]MemoryContext{"player": {Tail: tail}}}, "player", "")
	if material.Bounded == nil {
		t.Fatal("a long-memory material must be able to bound itself")
	}
	// Each record carries its own identifier, so counting them counts the retained
	// groups without depending on the record text.
	groupsIn := func(m Material) int {
		count := 0
		for _, id := range m.RequiredSources {
			if strings.HasPrefix(id, "memory:") {
				count++
			}
		}
		return count
	}
	if groupsIn(material) != memory.TargetRecentGroups {
		t.Fatalf("the initial window should carry every group, got %d", groupsIn(material))
	}
	// Through the composer with a window that cannot hold every group: the projection
	// must present a smaller window instead of the turn failing.
	window := model.WindowLimits{ContextTokens: testBudget + 512, OutputTokens: 512}
	composer := ContextComposer{Window: window}
	req, _, err := composer.Build(material, "规则", 512)
	if err != nil {
		t.Fatalf("a reducible window still failed: %v", err)
	}
	if !strings.Contains(req.Input, "本轮职责与刺激：玩家输入") {
		t.Fatal("the required current-turn material was lost")
	}
	if !strings.Contains(req.Input, groups[len(groups)-1][0].Content[:24]) {
		t.Fatal("shrinking removed the newest group")
	}
	if _, err := model.ValidateTextRequest(req); err != nil {
		t.Fatal(err)
	}
}

// A smaller budget must produce a strictly smaller window, and a material with a single
// group must report failure rather than silently dropping the newest context.
func TestWindowBudgetIsWhatReducesTheGroups(t *testing.T) {
	tail := []memory.MemorySource{}
	for i := 0; i < memory.TargetRecentGroups; i++ {
		tail = append(tail, memory.MemorySource{ID: "memory:" + string(rune('a'+i)), RunID: "run-" + string(rune('a'+i)), Content: strings.Repeat("经历记录", 60)})
	}
	groupsIn := func(budget int) int {
		material := withLongMemory(Material{Required: "本轮", System: "规则"}, Snapshot{InputBudgetTokens: budget, LongMemory: map[string]MemoryContext{"player": {Tail: tail}}}, "player", "")
		count := 0
		for _, id := range material.RequiredSources {
			if strings.HasPrefix(id, "memory:") {
				count++
			}
		}
		return count
	}
	generous := groupsIn(20000)
	tight := groupsIn(1200)
	if generous != memory.TargetRecentGroups {
		t.Fatalf("a generous budget must keep every group, got %d", generous)
	}
	if tight >= generous {
		t.Fatalf("a tight budget must reduce the window: %d vs %d", tight, generous)
	}
	if tight < 1 {
		t.Fatal("a tight budget must still keep the newest group")
	}
	// Shrinking must render from the parts: the digest header appears once and the source
	// list describes what was included.
	shrunk := withLongMemory(Material{Required: "本轮", System: "规则"}, Snapshot{InputBudgetTokens: 20000, LongMemory: map[string]MemoryContext{"player": {Tail: tail}}}, "player", "")
	reduced, changed := shrunk.Bounded(712, shrunk.System)
	if !changed {
		t.Fatal("the window did not shrink")
	}
	if count := strings.Count(reduced.Required, "已提交的连续个人回顾"); count != 1 {
		t.Fatalf("the digest header appears %d times after shrinking", count)
	}
	included := 0
	for _, id := range reduced.RequiredSources {
		if strings.HasPrefix(id, "memory:") {
			included++
		}
	}
	if included >= generous || included < 1 {
		t.Fatalf("the source list was not recomputed: %d", included)
	}
	for _, id := range reduced.RequiredSources {
		if strings.HasPrefix(id, "memory:") && !strings.Contains(reduced.Required, id) {
			t.Fatalf("a reported source is not in the text: %q", id)
		}
	}
	long := strings.Repeat("唯一的一组", 4000)
	single := withLongMemory(Material{Required: "本轮职责与刺激：玩家输入"}, Snapshot{LongMemory: map[string]MemoryContext{"player": {Tail: []memory.MemorySource{{ID: "memory:only", Content: long}}}}}, "player", "")
	if single.Bounded == nil {
		t.Fatal("expected a bounding hook")
	}
	if _, changed := single.Bounded(100, single.System); changed {
		t.Fatal("a single group must not be reported as reduced")
	}
}

func TestBoundedMemoryRebuildRecomputesRecall(t *testing.T) {
	archive := []memory.MemorySource{{ID: "memory:old", Seq: 1, RunID: "run-old", Content: "copper key"}}
	for i := 0; i < memory.TargetRecentGroups; i++ {
		archive = append(archive, memory.MemorySource{
			ID:      "memory:recent-" + string(rune('a'+i)),
			Seq:     int64(i + 2),
			RunID:   "run-recent-" + string(rune('a'+i)),
			Content: strings.Repeat("recent experience", 80),
		})
	}
	material := withLongMemory(
		Material{Required: "current turn", System: "rules"},
		Snapshot{InputBudgetTokens: 20000, LongMemory: map[string]MemoryContext{"player": {Archive: archive, Tail: archive[1:]}}},
		"player",
		"copper",
	)
	reduced, changed := material.Bounded(712, material.System)
	if !changed {
		t.Fatal("the recent window did not shrink")
	}
	if len(reduced.RecallSources) != 1 || reduced.RecallSources[0] != "memory:old" {
		t.Fatalf("recall sources after rebuild = %v", reduced.RecallSources)
	}
	found := false
	for _, section := range reduced.Optional {
		found = found || section.Name == "memory_recall" && strings.Contains(section.Text, "copper key")
	}
	if !found {
		t.Fatal("bounded rebuild lost the recalled group")
	}
}
