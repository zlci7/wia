package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

func historyRecords(groups, perGroup int, content func(group, index int) string) []memory.MemorySource {
	var out []memory.MemorySource
	seq := int64(0)
	for group := 1; group <= groups; group++ {
		for index := 1; index <= perGroup; index++ {
			seq++
			out = append(out, memory.MemorySource{
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
	block, backlog, supplied := memory.ProjectRecentExperience(archive)
	if len(memory.MemoryGroups(block)) != memory.TargetRecentGroups || len(backlog) != 30-memory.TargetRecentGroups {
		t.Fatalf("window=%d backlog=%d", len(memory.MemoryGroups(block)), len(backlog))
	}
	if len(supplied) != memory.TargetRecentGroups {
		t.Fatalf("supplied=%d", len(supplied))
	}
	newest := archive[len(archive)-1]
	if !supplied[newest.ID] || !strings.Contains(memory.MemoryRecordsText(block), newest.ID) {
		t.Fatal("newest committed experience left the window")
	}
	if supplied[backlog[0][0].ID] {
		t.Fatal("backlog reported as supplied")
	}
	if len(archive) != 30 {
		t.Fatal("projection must not mutate the stored history")
	}
	if complete, rest, _ := memory.ProjectRecentExperience(historyRecords(2, 1, func(int, int) string { return "短经历" })); len(rest) != 0 || len(complete) != 2 {
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
	material := turn.ComposeSuggestions(snapshot)
	if !strings.Contains(material.Required, "本次提供最近") || !strings.Contains(material.Required, "尚未整理") {
		t.Fatal("degradation was not stated to the model")
	}
	if !strings.Contains(material.Required, archive[len(archive)-1].ID) {
		t.Fatal("newest group missing from required material")
	}
	for _, record := range archive[:len(archive)-memory.TargetRecentGroups] {
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
	snapshot, err := turn.LoadSnapshot(ctx, store, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.turnService().PrepareLongMemory(ctx, store, &snapshot, wiaworld.Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{fail: true}); err != nil {
		t.Fatal(err)
	}
	lagging := snapshot.LongMemory["npc:innkeeper"]
	if lagging.Digest.Through != 0 || len(lagging.Tail) != 12 || len(lagging.Archive) != 12 {
		t.Fatalf("failed maintenance changed stored history: through=%d tail=%d archive=%d", lagging.Digest.Through, len(lagging.Tail), len(lagging.Archive))
	}
	if err = a.turnService().PrepareLongMemory(ctx, store, &snapshot, wiaworld.Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{}); err != nil {
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
	snapshot, err := turn.LoadSnapshot(ctx, store, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = turn.LoadLongMemory(ctx, store, &snapshot); err != nil {
		t.Fatal(err)
	}
	var target memory.MemorySource
	for _, record := range snapshot.LongMemory["npc:innkeeper"].Archive {
		if strings.Contains(record.Content, "第1轮") {
			target = record
		}
	}
	if target.ID == "" || !strings.HasPrefix(target.Kind, "perception:") {
		t.Fatalf("unexpected correction target: %+v", target)
	}
	correction, err := a.Correct(ctx, w.WorldID, memory.CorrectionRequest{
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
	reloaded, err := turn.LoadSnapshot(ctx, store2, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = turn.LoadLongMemory(ctx, store2, &reloaded); err != nil {
		t.Fatal(err)
	}
	hits := memory.SearchMemory(reloaded.LongMemory["npc:innkeeper"].Archive, "铜钥匙", 5)
	if !strings.Contains(memory.MemoryRecordsText(hits), "客栈柜台") {
		t.Fatal("correction did not apply to a backlog record")
	}
	other := []memory.MemorySource{{ID: "other:1", Seq: 1, RunID: "run:other", Content: "铜钥匙在别人手里"}}
	if strings.Contains(memory.MemoryRecordsText(memory.SearchMemory(other, "铜钥匙", 5)), "旧码头") {
		t.Fatal("recall crossed receivers")
	}
}
