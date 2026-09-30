package turn

import (
	"context"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

// intentEnumGenerator answers the intent stage with a fixed first response and a valid one
// afterwards, so a test can see exactly when a repair happened.
type intentEnumGenerator struct {
	requests []model.TextRequest
	invalid  bool
	first    string
}

func (g *intentEnumGenerator) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, r)
	if len(g.requests) == 1 || g.invalid {
		if g.first != "" {
			return model.TextResponse{Text: g.first}, nil
		}
		return model.TextResponse{Text: `{"intent_type":"wait","addressee_id":"","visibility":"public","wait_minutes":30}`}, nil
	}
	return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public","wait_minutes":30}`}, nil
}

// intentScene is one important character in the scene, which is all the recipient check
// needs: an addressee who is not in this list is refused and repaired.
func intentScene() Snapshot {
	return Snapshot{
		Summary:    wiaworld.WorldSummary{WorldID: "w1", GameID: "harbor", Clock: "第 1 日 08:00"},
		Characters: []wiaworld.Character{{EntityID: "npc:innkeeper", Name: "沈岚", Role: "客栈老板", InScene: true}},
	}
}

func TestIntentRecipientUsesBoundedRepair(t *testing.T) {
	for _, first := range []string{
		`{"intent_type":"speak","addressee_id":"npc:boatman","visibility":"public"}`,
		`{"intent_type":"speak","addressee_id":"","visibility":"private"}`,
	} {
		g := &intentEnumGenerator{first: first}
		service := New(&rosterHost{}, Deps{})
		intent, repairs, err := service.resolveTurnIntent(context.Background(), g, intentScene(), wiaworld.Run{RunID: "recipient", Input: "请船夫帮伤者上船"})
		if err != nil || repairs != 1 || len(g.requests) != 2 || intent.AddresseeID != "" {
			t.Fatalf("repairs=%d calls=%d err=%v", repairs, len(g.requests), err)
		}
	}
}

func TestIntentEnumsUseOneBoundedFormatRepair(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		g := &intentEnumGenerator{invalid: invalid}
		var result TurnIntent
		repairs, err := GenerateJSONMetrics(context.Background(), g, "规则", "等半小时", &result, 4096, "intent_type", "addressee_id", "visibility")
		if (err != nil) != invalid || repairs != 1 || len(g.requests) != 2 {
			t.Fatalf("invalid=%t repairs=%d calls=%d err=%v", invalid, repairs, len(g.requests), err)
		}
		if !strings.Contains(g.requests[1].System, "field=intent_type expected=speak|observe|act") {
			t.Fatal("missing local enum contract")
		}
		if !invalid && (result.IntentType != "act" || result.WaitMinutes != 30) {
			t.Fatal(result)
		}
	}
}
