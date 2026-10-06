package turn

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func sceneString(s string) *string { return &s }
func sceneIndex(i int) *int        { return &i }
func sceneDialogue(id, actor, content string, basis ...string) sceneBeat {
	return sceneBeat{LocalID: id, Kind: "dialogue", ActorID: actor, Basis: basis, Content: content, Recipients: []string{}, Bystanders: []string{}, Projections: []actionProjection{}, Scope: sceneString("public")}
}
func sceneObservation(id, actor, content string, basis ...string) sceneBeat {
	return sceneBeat{LocalID: id, Kind: "observation", ActorID: actor, Basis: basis, Content: content, Recipients: []string{actor}, Bystanders: []string{}, Projections: []actionProjection{{Recipient: actor, Content: content}}}
}
func sceneComplete(input string, beats ...sceneBeat) *SceneDraft {
	ids := []string{}
	elapsed := 0
	for _, b := range beats {
		ids = append(ids, b.LocalID)
		elapsed = b.OffsetMinutes
	}
	return &SceneDraft{SchemaRevision: sceneDraftRevision, InputMap: []sceneInput{{Text: input, IntentType: "act", Visibility: "public", BeatIDs: slices.Clone(ids), Status: "succeeded"}}, Beats: beats, NarrativeBlocks: []sceneNarrativeBlock{{Text: "你听完解释，继续处理眼前的事情。", BeatIDs: ids}}, ElapsedMinutes: elapsed, Stop: sceneStop{Reason: "completed", Content: "本轮意图已经处理。"}, ProgressUpdates: []sceneProgress{}}
}
func sceneCompileFixture(t *testing.T, s Snapshot, run wiaworld.Run, selected []string, draft *SceneDraft) (Output, error) {
	t.Helper()
	s.sceneEntities = selected
	m, _ := selectStoryMaterials(s, "scene", "", composeScene(s, run, selected))
	_, report, err := (ContextComposer{}).Build(m, m.System, 4096)
	if err != nil {
		return Output{}, err
	}
	return compileScene(s, run, draft, selected, sceneLedger(s, selected, report.SelectedSources), nil)
}

func TestSceneDialogueContinuityAndUnselectedListeners(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "coherent", Input: "我请他们核对登记。"}
	a := sceneDialogue("b1", "npc:a", refusalSpeech, "input:0")
	b := sceneDialogue("b2", "npc:b", "我听到了拒绝，可以先核对登记。", "beat:b1")
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, sceneComplete(run.Input, a, b))
	if err != nil {
		t.Fatal(err)
	}
	var heard bool
	for _, p := range out.Perceptions {
		if p.RecipientID == "npc:c" && strings.Contains(p.Content, refusalSpeech) {
			heard = true
		}
	}
	if !heard {
		t.Fatal("unselected colocated listener lost public hearing")
	}
	for _, e := range out.Events {
		for _, basis := range e.BasisEventIDs {
			if strings.HasPrefix(basis, "beat:") || strings.HasPrefix(basis, "personal:") {
				t.Fatal("temporary source persisted")
			}
		}
	}
	if len(out.Narrative) == 0 || s.Items["unique-tool"].HolderID != "" || len(s.Perceptions["npc:a"]) != 0 {
		t.Fatal("compiler mutated loaded snapshot")
	}
}

func TestScenePrivateSpeechDoesNotAuthorizeOtherActorsOrNarrative(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "private", Input: "我看看他们在忙什么。"}
	a := sceneDialogue("b1", "npc:a", privateSpeech, "input:0")
	a.Scope = sceneString("private")
	a.Recipients = []string{"npc:b"}
	b := sceneDialogue("b2", "npc:b", "我会核对封套。", "beat:b1")
	b.Scope = sceneString("private")
	b.Recipients = []string{"npc:a"}
	seen := sceneObservation("b3", "player", "他们低声交谈，我听不清内容。", "input:0")
	d := sceneComplete(run.Input, a, b, seen)
	d.NarrativeBlocks[0].BeatIDs = []string{"b3"}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Perceptions {
		if (p.RecipientID == "player" || p.RecipientID == "npc:c") && strings.Contains(p.Content, privateSpeech) {
			t.Fatal("private original broadcast")
		}
	}
	d.NarrativeBlocks[0].BeatIDs = []string{"b1"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d); err == nil {
		t.Fatal("private node supported player prose")
	}
	d.NarrativeBlocks[0].BeatIDs = []string{"b3"}
	d.Beats[1].ActorID = "npc:c"
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil {
		t.Fatal("third person used private source")
	}
}

func TestSceneLateArrivalAndJustifiedParticipantExpansion(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Locations = []story.Location{{ID: "workplace", Kind: "place", Connections: []string{"dock"}}, {ID: "dock", Kind: "place", Connections: []string{"workplace"}}}
	s.Positions["npc:c"] = "dock"
	s.Characters[2].InScene = false
	run := wiaworld.Run{RunID: "arrival", Input: "我问清情况，再去码头查看。"}
	a := sceneDialogue("b1", "npc:a", refusalSpeech, "input:0")
	move := sceneBeat{LocalID: "b2", Kind: "action_result", ActorID: "player", OffsetMinutes: 5, Basis: []string{"input:0"}, Content: "我沿路抵达码头。", Recipients: []string{"player"}, Bystanders: []string{}, Projections: []actionProjection{{Recipient: "player", Content: "我抵达码头。"}}, Status: sceneString("succeeded"), Attempt: &sceneAttempt{Content: "去码头查看", InputFragmentIndex: sceneIndex(0)}, Effects: sceneEffects{Movements: []sceneMovement{{EntityID: "player", From: "workplace", To: "dock", Route: []string{"workplace", "dock"}}}}}
	c := sceneDialogue("b3", "npc:c", "你来码头找什么？", scenePersonalID("npc:c", "definition:"+s.Definition.Revision+":npc:c"))
	c.OffsetMinutes = 5
	d := sceneComplete(run.Input, a, move, c)
	_, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	var expansion *sceneActorExpansion
	if !errors.As(err, &expansion) || expansion.EntityID != "npc:c" {
		t.Fatalf("arrival missing justified extension: %v", err)
	}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if out.Positions["player"] != "dock" || len(out.PositionChanges) != 1 || out.PositionChanges[0].SourceEventID != "arrival:scene:2:action:result:1" {
		t.Fatal("movement not bound to compiled result")
	}
	for _, p := range out.Perceptions {
		if p.RecipientID == "npc:c" && strings.Contains(p.Content, refusalSpeech) {
			t.Fatal("late participant received earlier speech")
		}
	}
	d.Beats[2].Basis = []string{"beat:b1"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil {
		t.Fatal("late participant used earlier speech")
	}
	if s.Positions["player"] != "workplace" {
		t.Fatal("arrival mutated original position")
	}
}

func TestSceneConflictingItemEffectsRejectWholeCandidate(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "conflict", Input: "我观察他们怎样取工具。"}
	beats := []sceneBeat{}
	for i, actor := range []string{"npc:a", "npc:b"} {
		b := sceneBeat{LocalID: fmt.Sprintf("b%d", i+1), Kind: "action_result", ActorID: actor, Basis: []string{"input:0"}, Content: "拿起唯一工具。", Recipients: []string{"player", actor}, Bystanders: []string{}, Projections: outcomeProjectionFixture("拿起工具。", []string{"player", actor}), Status: sceneString("succeeded"), Attempt: &sceneAttempt{Content: "尝试拿起工具"}, Effects: sceneEffects{ItemTransfers: []sceneItemTransfer{{InstanceID: "unique-tool", FromLocationID: "workplace", ToHolderID: actor}}}}
		beats = append(beats, b)
	}
	if out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, sceneComplete(run.Input, beats...)); err == nil || len(out.ItemTransfers) != 0 || s.Items["unique-tool"].LocationID != "workplace" {
		t.Fatal("conflicting ownership escaped atomic candidate", err)
	}
	beats[1].Status = sceneString("failed")
	beats[1].Effects = sceneEffects{}
	beats[1].Content = "工具已经由甲持有，我没有拿到。"
	beats[1].Projections = outcomeProjectionFixture(beats[1].Content, []string{"player", "npc:b"})
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, sceneComplete(run.Input, beats...))
	if err != nil || len(out.ItemTransfers) != 1 || out.Items["unique-tool"].HolderID != "npc:a" {
		t.Fatalf("valid conflict: %+v %v", out.ItemTransfers, err)
	}
}

func TestSceneCandidateRoundTripUsesStrictProductionDecoder(t *testing.T) {
	d := sceneComplete("我问好。", sceneDialogue("b1", "npc:a", "晚上好。", "input:0"))
	response, err := decodeSceneResponse(wire.MarshalJSON(d))
	if err != nil {
		t.Fatal(err)
	}
	if response.Draft.InputMap[0].Text != d.InputMap[0].Text {
		t.Fatal("candidate round trip lost original input")
	}
}
