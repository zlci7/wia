package turn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

func TestRelationshipProposalContextKeepsExactAllowedPairs(t *testing.T) {
	snapshot, _ := ownershipNPCFixture()
	snapshot.Relationships = []wiaworld.Relationship{{SubjectID: "npc:a", TargetID: "player", RelationType: "trust"}, {SubjectID: "npc:a", TargetID: "player", RelationType: "regard"}, {SubjectID: "npc:a", TargetID: "npc:b", RelationType: "trust"}}
	snapshot.Sources = map[string]SourceMetadata{}
	for _, id := range []string{"source:a", "source:b", "source:c"} {
		snapshot.Sources[id] = SourceMetadata{ID: id}
		snapshot.Perceptions["npc:a"] = append(snapshot.Perceptions["npc:a"], wiaworld.Perception{SourceEventID: id, SourceType: "action_succeeded", Content: "本人亲历"})
	}
	snapshot.AppliedRelationshipSources = map[string]bool{"source:a\x00npc:a\x00player\x00trust": true, "source:c\x00npc:a\x00npc:b\x00trust": true}
	options := relationshipProposalOptions(snapshot, "npc:a")
	var groups []struct {
		SourceIDs []string `json:"source_ids"`
		Relations []struct {
			TargetID     string `json:"target_id"`
			RelationType string `json:"relation_type"`
		} `json:"relations"`
	}
	if err := json.Unmarshal([]byte(relationshipProposalContext(options)), &groups); err != nil {
		t.Fatal(err)
	}
	want, got := map[string]bool{}, map[string]bool{}
	for _, option := range options {
		want[option.SourceID+"\x00"+option.TargetID+"\x00"+option.RelationType] = true
	}
	for _, group := range groups {
		for _, source := range group.SourceIDs {
			for _, relation := range group.Relations {
				key := source + "\x00" + relation.TargetID + "\x00" + relation.RelationType
				if got[key] {
					t.Fatalf("duplicate permitted combination: %q", key)
				}
				got[key] = true
			}
		}
	}
	if !reflect.DeepEqual(got, want) || len(got) != 7 {
		t.Fatalf("allowed relationship combinations changed: got=%v want=%v", got, want)
	}
	if empty := relationshipProposalContext(nil); empty != "[]" {
		t.Fatalf("empty options: %s", empty)
	}
}

func TestMistEmbersFourthInputSegmentPreservesConfirmedExperienceWithinBudget(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := pack.Definition
	snapshot := Snapshot{Definition: def, Characters: def.Characters, Narrative: wiaworld.DefaultNarrativeSettings(), Summary: wiaworld.WorldSummary{GameID: "mist-embers", Clock: "第 1 日 09:15"}, SceneVersion: 5, Positions: def.InitialLocations, Sources: map[string]SourceMetadata{}, Perceptions: map[string][]wiaworld.Perception{}, LongMemory: map[string]MemoryContext{}}
	snapshot.SceneViews = initialSceneViews(snapshot)
	owner := "npc:tailor"
	for _, relation := range def.RelationDefinitions {
		for _, target := range append([]wiaworld.Character{{EntityID: "player"}}, def.Characters...) {
			if target.EntityID != owner {
				snapshot.Relationships = append(snapshot.Relationships, wiaworld.Relationship{SubjectID: owner, TargetID: target.EntityID, RelationType: relation.ID, SourceEvent: "opening", Version: 1})
			}
		}
	}
	var archive []memory.MemorySource
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("run_59686cd9aa00f43b1991928e:action:%d:result:1:projection:%s", i, owner)
		text := fmt.Sprintf("已提交经历%d：%s", i, strings.Repeat("玩家询问女儿的名字与去向，父亲交代争执、委托条件与自己尚不知道的事情。", 3))
		snapshot.Sources[id] = SourceMetadata{ID: id, Actor: owner, Kind: "npc_action_result", RunID: "previous", Seq: int64(i)}
		snapshot.Perceptions[owner] = append(snapshot.Perceptions[owner], wiaworld.Perception{SourceEventID: id, SourceType: "action_succeeded", Content: text})
		archive = append(archive, memory.MemorySource{ID: fmt.Sprintf("perception:%d", i), EventID: id, RunID: "previous", Seq: int64(i), Content: text})
	}
	snapshot.LongMemory[owner] = MemoryContext{Archive: archive, Tail: archive}
	for part := 1; part <= 3; part++ {
		for i := 1; i <= 4; i++ {
			id := fmt.Sprintf("run_ef72e900e909180918d6aab6:part:%d:player-action:result:%d:projection:%s", part, i, owner)
			text := fmt.Sprintf("第%d段的获准结果%d：%s", part, i, strings.Repeat("本人已经听到玩家的私密委托，并确认先前交谈、离开和准备查看订单簿的实际进展。", 3))
			snapshot.Sources[id] = SourceMetadata{ID: id, Actor: "player", Kind: "player_action_result", RunID: "current", Stage: 3}
			snapshot.Perceptions[owner] = append(snapshot.Perceptions[owner], wiaworld.Perception{SourceEventID: id, SourceType: "action_succeeded", Content: text})
		}
	}
	var character wiaworld.Character
	for _, c := range snapshot.Characters {
		if c.EntityID == owner {
			character = c
		}
	}
	material := composeNPC(snapshot, def, character, "", "observe", StageInput{PlayerPerception: "查看订单簿", SourceEventIDs: []string{"current-input"}}, "", 1)
	material, _ = selectStoryMaterials(snapshot, "npc", owner, material)
	request, report, err := (ContextComposer{Window: model.WindowLimits{ContextTokens: 131072, OutputTokens: 12800}, ReasoningReserve: 8192}).Build(material, material.System, structuredTurnOutputTokens)
	t.Logf("input=%d relation_options=%d", report.InputTokens, len(relationshipProposalOptions(snapshot, owner)))
	if err != nil {
		t.Fatalf("fourth segment exceeds unchanged input budget: tokens=%d: %v", report.InputTokens, err)
	}
	for _, p := range snapshot.Perceptions[owner] {
		if !strings.Contains(request.Input, p.Content) {
			t.Fatalf("confirmed experience omitted: %s", p.SourceEventID)
		}
	}
	if request.MaxInputTokens != 12000 || !report.RequiredComplete {
		t.Fatalf("required-content budget changed: %+v", report)
	}
}
