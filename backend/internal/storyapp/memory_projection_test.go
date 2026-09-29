package storyapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

func historyRecords(groups, perGroup int, content func(group, index int) string) []memorymodel.MemorySource {
	var out []memorymodel.MemorySource
	seq := int64(0)
	for group := 1; group <= groups; group++ {
		for index := 1; index <= perGroup; index++ {
			seq++
			out = append(out, memorymodel.MemorySource{
				ID:      fmt.Sprintf("record:%02d:%d", group, index),
				Seq:     seq,
				RunID:   fmt.Sprintf("run:%02d", group),
				Content: content(group, index),
			})
		}
	}
	return out
}

// A world whose digest never caught up supplies a bounded window instead of its
// whole history, and a short history still travels complete.
func TestUnsummarizedHistorySuppliesBoundedWindow(t *testing.T) {
	archive := historyRecords(30, 1, func(group, _ int) string { return fmt.Sprintf("第%d轮已提交经历", group) })
	block, backlog, supplied := projectRecentExperience(archive)
	if len(memoryGroups(block)) != targetRecentGroups || len(backlog) != 30-targetRecentGroups {
		t.Fatalf("window=%d backlog=%d", len(memoryGroups(block)), len(backlog))
	}
	if len(supplied) != targetRecentGroups {
		t.Fatalf("supplied=%d", len(supplied))
	}
	newest := archive[len(archive)-1]
	if !supplied[newest.ID] || !strings.Contains(memoryRecordsText(block), newest.ID) {
		t.Fatal("newest committed experience left the window")
	}
	if supplied[backlog[0][0].ID] {
		t.Fatal("backlog reported as supplied")
	}
	if len(archive) != 30 {
		t.Fatal("projection must not mutate the stored history")
	}
	if complete, rest, _ := projectRecentExperience(historyRecords(2, 1, func(int, int) string { return "短经历" })); len(rest) != 0 || len(complete) != 2 {
		t.Fatal("short history should stay complete")
	}
}

// The supplied window must fit the request, whole groups leave the window first,
// older unsummarized groups stay retrievable, and the request states the boundary.
func TestUnderWindowHistoryKeepsBacklogRetrievable(t *testing.T) {
	archive := historyRecords(12, 1, func(group, _ int) string {
		if group == 3 {
			return "第3轮：答应在旧码头归还铜钥匙"
		}
		return fmt.Sprintf("第%d轮：河面传来船笛声", group)
	})
	snapshot := turn.Snapshot{
		PlayerName: "旅人",
		Definition: story.Definition{Background: "旧渡口客栈的雨夜"},
		Summary:    wiaworld.WorldSummary{WorldID: "world_projection", GameID: GameID, Clock: "第 1 日 19:00"},
		Messages:   []wiaworld.Message{{MessageID: "msg:1", Kind: "narrative", Content: "雨声敲着窗沿。"}},
		LongMemory: map[string]turn.MemoryContext{"player": {Archive: archive, Tail: archive}},
	}
	material := composeSuggestions(snapshot)
	if !strings.Contains(material.Required, "本次提供最近") || !strings.Contains(material.Required, "尚未整理") {
		t.Fatal("degradation was not stated to the model")
	}
	if !strings.Contains(material.Required, archive[len(archive)-1].ID) {
		t.Fatal("newest group missing from required material")
	}
	for _, record := range archive[:len(archive)-targetRecentGroups] {
		if strings.Contains(material.Required, record.ID) {
			t.Fatal("backlog group entered required material", record.ID)
		}
		if !wiaworld.ContainsID(material.DeclinedSources, record.ID) || wiaworld.ContainsID(material.RecallSources, record.ID) {
			t.Fatal("backlog group must be reported as declined, not as retrieved", record.ID)
		}
	}
	req, report, err := (turn.ContextComposer{}).Build(material, material.System, 1024)
	if err != nil {
		t.Fatalf("unsummarized history blocked the call: %v", err)
	}
	if !report.RequiredComplete || report.RecallIncluded+report.RecallExcluded == 0 {
		t.Fatalf("report lost the window accounting: %+v", report)
	}
	for _, id := range material.RequiredSources {
		if strings.HasPrefix(id, "record:") && !strings.Contains(req.Input, id) {
			t.Fatal("required source missing from input", id)
		}
	}
	// Retrieval starts from a fresh request that supplied nothing: the older
	// unsummarized agreement must come back, and the window accounting must not
	// silently consume it.
	recalled := withRecall(turn.Material{System: "NPC", Required: "本轮"}, memoryProjection{context: snapshot.LongMemory["player"]}, "铜钥匙")
	found := false
	for _, section := range recalled.Optional {
		if section.Name == "memory_recall" && strings.Contains(section.Text, "铜钥匙") {
			found = true
		}
	}
	if !found {
		t.Fatal("older unsummarized agreement was not retrievable")
	}
	// An in-turn appended query must reach a group the window left out while still
	// skipping the groups this request already supplies.
	_, _, supplied := projectRecentExperience(archive)
	appended := withRecall(turn.Material{System: "NPC", Required: "本轮"}, memoryProjection{context: snapshot.LongMemory["player"], supplied: supplied}, "铜钥匙")
	recalledBacklog, recalledSupplied := false, false
	for _, section := range appended.Optional {
		if section.Name != "memory_recall" {
			continue
		}
		recalledBacklog = recalledBacklog || strings.Contains(section.Text, "铜钥匙")
		recalledSupplied = recalledSupplied || strings.Contains(section.Text, archive[len(archive)-1].ID)
	}
	if !recalledBacklog || recalledSupplied {
		t.Fatalf("appended recall boundary wrong: backlog=%t supplied=%t", recalledBacklog, recalledSupplied)
	}
}

// When the newest groups together do not fit, the composer drops whole older
// groups; if even the smallest required material does not fit, it still fails.
func TestWindowGroupsExitWholeBeforeCapacityFailure(t *testing.T) {
	base := turn.Material{System: "职责", Required: strings.Repeat("本轮必需资料", 1000)}
	tail := historyRecords(4, 1, func(group, _ int) string {
		return fmt.Sprintf("第%d轮%s", group, strings.Repeat("已提交经历", 500))
	})
	snapshot := turn.Snapshot{
		PlayerName: "旅人",
		Definition: story.Definition{Background: "背景"},
		Summary:    wiaworld.WorldSummary{WorldID: "world_window", Clock: "第 1 日 19:00"},
		LongMemory: map[string]turn.MemoryContext{"player": {Archive: tail, Tail: tail}},
	}
	material := withLongMemory(base, snapshot, "player", "")
	req, report, err := (turn.ContextComposer{}).Build(material, material.System, 1024)
	if err != nil {
		t.Fatalf("window did not degrade: %v", err)
	}
	if report.Excluded == 0 || !report.RequiredComplete {
		t.Fatalf("expected whole-group exclusion: %+v", report)
	}
	newest := tail[len(tail)-1]
	if !strings.Contains(req.Input, newest.ID) {
		t.Fatal("newest required group was dropped")
	}
	oversized := turn.Snapshot{
		PlayerName: "旅人",
		Definition: story.Definition{Background: "背景"},
		Summary:    wiaworld.WorldSummary{WorldID: "world_oversized", Clock: "第 1 日 19:00"},
		LongMemory: map[string]turn.MemoryContext{"player": {
			Archive: []memorymodel.MemorySource{{ID: "only", Seq: 1, RunID: "run:1", Content: strings.Repeat("单组超长经历", 20000)}},
			Tail:    []memorymodel.MemorySource{{ID: "only", Seq: 1, RunID: "run:1", Content: strings.Repeat("单组超长经历", 20000)}},
		}},
	}
	tooLarge := withLongMemory(turn.Material{System: "职责", Required: "本轮"}, oversized, "player", "")
	if _, _, err := (turn.ContextComposer{}).Build(tooLarge, tooLarge.System, 1024); !errors.Is(err, turn.ErrContextCapacity) {
		t.Fatalf("oversized single group must fail loudly: %v", err)
	}
}

// A failed maintenance keeps every record and the real watermark; a later
// successful maintenance resumes from that watermark instead of skipping ahead.
func TestFailedMaintenanceKeepsStoredHistoryAndWatermark(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "整理滞后", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	for i := 1; i <= 12; i++ {
		run := fmt.Sprintf("lag-%02d", i)
		if _, err = store.Database().Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,?,'','',1,'completed','now','now')`, run, run, run); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Database().Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?)`, i+200, "e"+run, "player_attempt", "player", "npc:innkeeper", "铜钥匙", run, 1, 1, "player_private", "now"); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Database().Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,'player_private',?,1,1,'now')`, "npc:innkeeper", "e"+run, fmt.Sprintf("第%d轮承诺在码头归还铜钥匙", i)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := loadTurnSnapshot(ctx, store, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.prepareLongMemory(ctx, store, &snapshot, wiaworld.Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{fail: true}); err != nil {
		t.Fatal(err)
	}
	lagging := snapshot.LongMemory["npc:innkeeper"]
	if lagging.Digest.Through != 0 || len(lagging.Tail) != 12 || len(lagging.Archive) != 12 {
		t.Fatalf("failed maintenance changed stored history: through=%d tail=%d archive=%d", lagging.Digest.Through, len(lagging.Tail), len(lagging.Archive))
	}
	if err = a.prepareLongMemory(ctx, store, &snapshot, wiaworld.Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{}); err != nil {
		t.Fatal(err)
	}
	recovered := snapshot.LongMemory["npc:innkeeper"]
	if recovered.Digest.Through == 0 || recovered.Digest.Through >= 12 {
		t.Fatalf("recovery skipped the real watermark: through=%d", recovered.Digest.Through)
	}
	seen := map[string]bool{}
	for _, record := range recovered.Archive {
		seen[record.ID] = true
	}
	for _, record := range lagging.Archive {
		if !seen[record.ID] {
			t.Fatal("recovered maintenance lost a record", record.ID)
		}
	}
}

// Corrections stay in effect for records the window left out, and recall never
// crosses receivers.
func TestBacklogRecallKeepsCorrectionsAndScope(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "窗口纠正", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	for i := 1; i <= 8; i++ {
		run := fmt.Sprintf("corr-%02d", i)
		if _, err = store.Database().Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,?,'','',1,'completed','now','now')`, run, run, run); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Database().Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?)`, i+300, "e"+run, "player_attempt", "player", "npc:innkeeper", "铜钥匙", run, 1, 1, "player_private", "now"); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Database().Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,'player_private',?,1,1,'now')`, "npc:innkeeper", "e"+run, fmt.Sprintf("第%d轮：约定明早在旧码头归还铜钥匙", i)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := loadTurnSnapshot(ctx, store, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = loadLongMemory(ctx, store, &snapshot); err != nil {
		t.Fatal(err)
	}
	var target memorymodel.MemorySource
	for _, record := range snapshot.LongMemory["npc:innkeeper"].Archive {
		if strings.Contains(record.Content, "第1轮") {
			target = record
		}
	}
	if target.ID == "" || !strings.HasPrefix(target.Kind, "perception:") {
		t.Fatalf("unexpected correction target: %+v", target)
	}
	correction, err := a.Correct(ctx, w.WorldID, memorymodel.CorrectionRequest{
		RequestKey: "corr-window", ExpectedEpoch: w.ContextEpoch, Kind: "perception", Scope: "npc:innkeeper",
		TargetID: target.ID, Replacement: "改为在客栈柜台归还铜钥匙",
	})
	if err != nil {
		t.Fatal(err)
	}
	if correction.Epoch <= w.ContextEpoch {
		t.Fatal("correction did not advance the epoch")
	}
	store2, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Database().Close()
	reloaded, err := loadTurnSnapshot(ctx, store2, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = loadLongMemory(ctx, store2, &reloaded); err != nil {
		t.Fatal(err)
	}
	projection := memoryProjection{context: reloaded.LongMemory["npc:innkeeper"], supplied: map[string]bool{}}
	material := withRecall(turn.Material{System: "NPC", Required: "本轮"}, projection, "铜钥匙")
	req, _, err := (turn.ContextComposer{}).Build(material, material.System, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Input, "客栈柜台") {
		t.Fatal("correction did not apply to a backlog record")
	}
	other := turn.MemoryContext{Archive: []memorymodel.MemorySource{{ID: "other:1", Seq: 1, RunID: "run:other", Content: "铜钥匙在别人手里"}}}
	crossScope := withRecall(turn.Material{System: "NPC", Required: "本轮"}, memoryProjection{context: other, supplied: map[string]bool{}}, "铜钥匙")
	if strings.Contains(anyOptionalText(crossScope), "旧码头") {
		t.Fatal("recall crossed receivers")
	}
}

func anyOptionalText(material turn.Material) string {
	var builder strings.Builder
	for _, section := range material.Optional {
		builder.WriteString(section.Text)
	}
	return builder.String()
}
