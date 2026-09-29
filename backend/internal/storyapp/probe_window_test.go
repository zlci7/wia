package storyapp

import (
	"strings"
	"testing"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

// R14: when the whole request does not fit, the recent-experience window must fall back
// to fewer complete groups instead of failing the turn.
func TestRecentWindowShrinksToFitTheWholeRequest(t *testing.T) {
	// Small enough that the character-count rule keeps every group, so the token budget
	// is what has to reduce the window.
	groups := [][]memorymodel.MemorySource{}
	for i := 0; i < targetRecentGroups; i++ {
		record := memorymodel.MemorySource{ID: "memory:" + string(rune('a'+i)), RunID: "run-" + string(rune('a'+i)), Content: strings.Repeat("已发生的经历", 100)}
		groups = append(groups, []memorymodel.MemorySource{record})
	}
	tail := flattenGroups(groups)
	if len(memoryGroups(tail)) != targetRecentGroups {
		t.Fatalf("fixture groups: %d", len(memoryGroups(tail)))
	}
	if got := len(memoryRecordsText(tail)); got > recentWindowChars {
		t.Fatalf("fixture must fit the size rule so the budget is what binds: %d", got)
	}
	// A budget that fits the base prompt and every group, measured rather than
	// estimated, so the initial window is the whole recent history.
	base := framedContextTokens(model.TextRequest{System: "规则", Input: "本轮职责与刺激：玩家输入"})
	testBudget := base + framedContextTokens(model.TextRequest{Input: memoryRecordsText(tail)}) + 2*budgetHeadroomTokens
	material := withLongMemory(contextMaterial{
		Required: "本轮职责与刺激：玩家输入",
		System:   "规则",
	}, turn.Snapshot{InputBudgetTokens: testBudget, LongMemory: map[string]turn.MemoryContext{"player": {Tail: tail}}}, "player", "")
	if material.Bounded == nil {
		t.Fatal("a long-memory material must be able to bound itself")
	}
	// Each record carries its own identifier, so counting them counts the retained
	// groups without depending on the record text.
	groupsIn := func(m contextMaterial) int {
		count := 0
		for _, id := range m.RequiredSources {
			if strings.HasPrefix(id, "memory:") {
				count++
			}
		}
		return count
	}
	if groupsIn(material) != targetRecentGroups {
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
	tail := []memorymodel.MemorySource{}
	for i := 0; i < targetRecentGroups; i++ {
		tail = append(tail, memorymodel.MemorySource{ID: "memory:" + string(rune('a'+i)), RunID: "run-" + string(rune('a'+i)), Content: strings.Repeat("经历记录", 60)})
	}
	groupsIn := func(budget int) int {
		material := withLongMemory(contextMaterial{Required: "本轮", System: "规则"}, turn.Snapshot{InputBudgetTokens: budget, LongMemory: map[string]turn.MemoryContext{"player": {Tail: tail}}}, "player", "")
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
	if generous != targetRecentGroups {
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
	shrunk := withLongMemory(contextMaterial{Required: "本轮", System: "规则"}, turn.Snapshot{InputBudgetTokens: 20000, LongMemory: map[string]turn.MemoryContext{"player": {Tail: tail}}}, "player", "")
	reduced, changed := shrunk.Bounded(recentWindowMinTokens + 200)
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
	single := withLongMemory(contextMaterial{Required: "本轮职责与刺激：玩家输入"}, turn.Snapshot{LongMemory: map[string]turn.MemoryContext{"player": {Tail: []memorymodel.MemorySource{{ID: "memory:only", Content: long}}}}}, "player", "")
	if single.Bounded == nil {
		t.Fatal("expected a bounding hook")
	}
	if _, changed := single.Bounded(100); changed {
		t.Fatal("a single group must not be reported as reduced")
	}
}
