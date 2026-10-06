package turn

import (
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	wiaworld "gameagent/backend/internal/world"
)

func TestSceneExplicitPersonalSourcesRecallWithoutKeywordMatch(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "recall-ref", Input: "我继续办理手续。"}
	for _, owner := range []string{"npc:a", "npc:b"} {
		archive := []memory.MemorySource{
			{Scope: owner, ID: "old-attempt", EventID: owner + ":old-attempt", RunID: "old", Seq: 1, Content: owner + "答应周二送来封套。"},
			{Scope: owner, ID: "old-result", EventID: owner + ":old-result", RunID: "old", Seq: 2, Content: owner + "未能按时送来，封套尚待送达。"},
			{Scope: owner, ID: "new", EventID: owner + ":new", RunID: "new", Seq: 3, Content: "今天晴朗。"},
		}
		s.LongMemory[owner] = MemoryContext{Archive: archive, Tail: archive[2:], Digest: memory.MemoryDigest{Scope: owner, Revision: 1, Through: 2, Sources: []string{"old-attempt", "old-result"}, Content: "尚待履约。", States: []memory.SubjectiveState{}}}
	}
	m := s.LongMemory["npc:a"]
	m.Digest.States = []memory.SubjectiveState{{Kind: "commitment", Content: "送来封套", Sources: []string{"old-attempt"}}}
	s.LongMemory["npc:a"] = m
	s.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "plan", OwnerID: "npc:b", Content: "完成交接", Status: "active", SourceIDs: []string{"npc:b:old-result"}}}}
	selected := []string{"player", "npc:a", "npc:b"}
	material := composeScene(s, run, selected)
	req, report, err := (ContextComposer{}).Build(material, material.System, 4096)
	if err != nil {
		t.Fatal(err)
	}
	ledger := sceneLedger(s, selected, report.SelectedSources)
	for _, owner := range []string{"npc:a", "npc:b"} {
		for _, id := range []string{"old-attempt", "old-result"} {
			alias := scenePersonalID(owner, id)
			if !slices.Contains(report.SelectedSources, alias) || strings.Count(req.Input, "["+alias+"；") != 1 {
				t.Fatalf("complete old group not supplied once for %s: %s", owner, id)
			}
			canonical, err := ledger.resolve(owner, []string{alias}, false)
			if err != nil || !slices.Equal(canonical, []string{owner + ":" + id}) {
				t.Fatalf("owner source mapping lost: %v %v", canonical, err)
			}
		}
	}
	if _, err := ledger.resolve("npc:a", []string{scenePersonalID("npc:b", "old-attempt")}, false); err == nil {
		t.Fatal("source lookup borrowed another owner's original words")
	}
	if len(s.LongMemory["npc:a"].Archive) != 3 || s.LongMemory["npc:a"].Digest.Through != 2 {
		t.Fatal("context recall mutated persistent coverage")
	}
}

func TestSceneRecallDeduplicatesBacklogAndKeepsLimitedReport(t *testing.T) {
	archive := []memory.MemorySource{{Scope: "npc:a", Seq: 1, ID: "old", RunID: "old", Content: "答应送来封套。"}, {Scope: "npc:a", Seq: 2, ID: "result", RunID: "old", Content: "未能送达。"}, {Scope: "npc:a", Seq: 3, ID: "new", RunID: "new", Content: "晴朗。"}}
	m := MemoryContext{Archive: archive, Tail: archive}
	material := renderMemoryWindow(Material{}, m, "npc:a", archive[2:], "没有匹配", []string{"old"})
	req, report, err := (ContextComposer{}).Build(material, material.System, 128)
	if err != nil || strings.Count(req.Input, "[old；") != 1 || strings.Count(req.Input, "[result；") != 1 || report.RecallIncluded != 2 {
		t.Fatalf("backlog duplicated referenced group: %+v %v", report, err)
	}
	material = withRecall(Material{}, memoryProjection{Context: m, References: []string{"absent"}}, "没有匹配")
	if material.RecallLimited || len(material.RecallSources) != 0 {
		t.Fatal("not found was confused with limited search")
	}
}
