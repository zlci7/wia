package turn

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestWorldResultRetainsValidatedEventBases(t *testing.T) {
	output := Output{}
	result := plotResolution{Status: "occurred", Content: "An external change occurred.", SourceIDs: []string{"old-event", "current-event", "material:revision:development", "definition:legacy:node", "fact:item:mirror", "old-event"}}
	_, err := New(&rosterHost{}, Deps{}).publishPlotResolution(context.Background(), nil, Snapshot{}, wiaworld.Run{RunID: "current"}, "world-result", result, &output)
	if err != nil || len(output.Events) != 1 || !slices.Equal(output.Events[0].BasisEventIDs, []string{"old-event", "current-event"}) {
		t.Fatalf("world result lost event bases or treated materials as event foreign keys: %+v %v", output.Events, err)
	}
}

func TestNPCDecisionRetainsRecallAndDigestEventBases(t *testing.T) {
	for _, kind := range []string{"recall", "digest"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, def := ownershipNPCFixture()
			owner := snapshot.Characters[0].EntityID
			context := MemoryContext{}
			for i := 0; i < 9; i++ {
				context.Archive = append(context.Archive, memory.MemorySource{Scope: owner, ID: fmt.Sprintf("perception:%d", i), EventID: fmt.Sprintf("historical:%d", i), Seq: int64(i + 1), RunID: "old", Content: "铜钥匙的旧线索"})
				context.Digest.Sources = append(context.Digest.Sources, fmt.Sprintf("perception:%d", i))
			}
			query := "铜钥匙"
			if kind == "digest" {
				context.Digest.Scope, context.Digest.Revision, context.Digest.Content = owner, 1, "Earlier clues remain relevant."
				query = "current stimulus"
			}
			snapshot.LongMemory = map[string]MemoryContext{owner: context}
			snapshot.Sources = map[string]SourceMetadata{"current": {ID: "current"}}
			g := &materialTestGenerator{responses: []string{wire.MarshalJSON(NPCDecision{Silent: true, ActionIntent: "Follow up the remembered clue."})}}
			decisions := map[string]NPCDecision{}
			err := New(&rosterHost{}, Deps{}).decideNPCs(t.Context(), g, snapshot, def, wiaworld.Run{RunID: "new"}, "", "observe", map[string]StageInput{owner: {NewStimulus: query, SourceEventIDs: []string{"current"}}}, nil, decisions, 1)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "recall" && !strings.Contains(g.requests[0].Input, "铜钥匙的旧线索") {
				t.Fatal("test did not supply the recalled history")
			}
			var output Output
			appendNPCDecisionOutput(&output, wiaworld.Run{RunID: "new"}, snapshot.Characters[0], decisions[owner], nil, "current", 1, 1)
			for i := 0; i < 9; i++ {
				if !slices.Contains(output.Events[0].BasisEventIDs, fmt.Sprintf("historical:%d", i)) {
					t.Fatalf("%s basis missing, including evidence beyond the plan limit: %+v", kind, output.Events[0])
				}
			}
		})
	}
}
