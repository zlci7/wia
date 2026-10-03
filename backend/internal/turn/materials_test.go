package turn

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func materialTestSnapshot() Snapshot {
	return Snapshot{Definition: story.Definition{Revision: "rev.1", Materials: []story.Material{
		{ID: "common", Purpose: "background", Visibility: "public", Delivery: "core", Summary: "Common", Body: "COMMON_WORLD"},
		{ID: "own", Purpose: "npc_knowledge", Visibility: "owner", OwnerID: "npc:a", Delivery: "core", Summary: "Own", Body: "OWN_KNOWLEDGE"},
		{ID: "secret", Purpose: "author_facts", Visibility: "author", Delivery: "on_demand", Summary: "SECRET_DIRECTORY", Body: "AUTHOR_SECRET"},
		{ID: "foreign", Purpose: "npc_knowledge", Visibility: "owner", OwnerID: "npc:b", Delivery: "on_demand", Summary: "FOREIGN_DIRECTORY", Body: "FOREIGN_SECRET"},
		{ID: "details", Purpose: "background", Visibility: "public", Delivery: "on_demand", KnownTo: []string{"npc:a"}, Summary: "Detailed public record", Body: "READ_DETAIL_BODY"},
		{ID: "unknown-public", Purpose: "background", Visibility: "public", Delivery: "on_demand", Summary: "UNKNOWN_DIRECTORY", Body: "UNKNOWN_DETAIL"},
	}}, materialReads: newMaterialReadBudget()}
}

func TestMaterialDirectoryAndBodyShareKnowledgeAuthorization(t *testing.T) {
	snapshot := materialTestSnapshot()
	material, directory := selectStoryMaterials(snapshot, "npc", "npc:a", Material{System: "S", Required: "request"})
	request, _, err := (ContextComposer{}).Build(material, material.System, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET_DIRECTORY", "AUTHOR_SECRET", "FOREIGN_DIRECTORY", "FOREIGN_SECRET", "UNKNOWN_DIRECTORY", "UNKNOWN_DETAIL"} {
		if strings.Contains(request.Input, secret) {
			t.Fatalf("unauthorized material %s leaked", secret)
		}
	}
	if !strings.Contains(request.Input, "OWN_KNOWLEDGE") || !strings.Contains(request.Input, "COMMON_WORLD") || len(directory) != 1 || directory["details"].ID == "" {
		t.Fatal("authorized core or readable directory missing")
	}
	if strings.Contains(request.Input, "READ_DETAIL_BODY") {
		t.Fatal("unrelated detailed text was eagerly read")
	}
	if strings.Contains(request.System, "needs_material") == false {
		t.Fatal("typed material request contract missing")
	}
}

func TestPlayerMaterialKnowledgeAppliesToDirectoryBodyAndReadRequests(t *testing.T) {
	for _, purpose := range []string{"intent", "narration"} {
		t.Run(purpose, func(t *testing.T) {
			snapshot := materialTestSnapshot()
			snapshot.Positions = map[string]string{"player": "office"}
			snapshot.Definition.Materials = append(snapshot.Definition.Materials,
				story.Material{ID: "archive", Purpose: "location_lore", Visibility: "public", Delivery: "on_demand", LocationIDs: []string{"archive-room"}, Summary: "REMOTE_ARCHIVE_INDEX", Body: "REMOTE_ARCHIVE_BODY"})
			material, directory := selectStoryMaterials(snapshot, purpose, "player", Material{System: "S", Required: "archive"})
			request, _, err := (ContextComposer{}).Build(material, material.System, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"UNKNOWN_DIRECTORY", "UNKNOWN_DETAIL", "REMOTE_ARCHIVE_INDEX", "REMOTE_ARCHIVE_BODY", "SECRET_DIRECTORY", "FOREIGN_DIRECTORY"} {
				if strings.Contains(request.Input, text) {
					t.Fatalf("unacquired player material leaked: %s", text)
				}
			}
			if len(directory) != 0 || !strings.Contains(request.Input, "COMMON_WORLD") {
				t.Fatal("player directory or common knowledge is incorrect")
			}
			base := &materialTestGenerator{responses: []string{`{"needs_material":["archive"]}`}}
			call := NewContextGenerator(Deps{}, "owner", base, material, snapshot, wiaworld.Run{}, purpose, "player", 1, "T")
			var result struct {
				Answer string `json:"answer"`
			}
			if err := GenerateJSON(context.Background(), call, "S", "request", &result, 100, "answer"); err == nil {
				t.Fatal("unacquired material read was accepted")
			}
			for _, request := range base.requests {
				if strings.Contains(request.Input, "REMOTE_ARCHIVE_BODY") {
					t.Fatal("unacquired body supplied during material read")
				}
			}
			for _, acquisition := range []string{"initial", "observation", "source"} {
				known := snapshot
				switch acquisition {
				case "initial":
					known.Definition.Materials = append([]story.Material{}, snapshot.Definition.Materials...)
					known.Definition.Materials[len(known.Definition.Materials)-1].KnownTo = []string{"player"}
				case "observation":
					known.Positions = map[string]string{"player": "archive-room"}
				case "source":
					known.PerceivedSources = map[string]map[string]bool{"player": {"material:rev.1:archive": true}}
				}
				knownMaterial, knownDirectory := selectStoryMaterials(known, purpose, "player", Material{System: "S", Required: "archive"})
				knownRequest, _, err := (ContextComposer{}).Build(knownMaterial, knownMaterial.System, 100)
				if err != nil || knownDirectory["archive"].ID == "" || !strings.Contains(knownRequest.Input, "REMOTE_ARCHIVE_BODY") {
					t.Fatalf("authorized %s material missing: %v", acquisition, err)
				}
			}
		})
	}
}

type materialTestGenerator struct {
	mu        sync.Mutex
	responses []string
	requests  []model.TextRequest
}

func (g *materialTestGenerator) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r)
	if len(g.responses) == 0 {
		return model.TextResponse{}, errors.New("unexpected model call")
	}
	text := g.responses[0]
	g.responses = g.responses[1:]
	return model.TextResponse{Text: text}, nil
}

func TestTypedMaterialReadResumesOneCandidateWithinExistingBudget(t *testing.T) {
	base := &materialTestGenerator{responses: []string{`{"needs_material":["details"]}`, `{"answer":"ready"}`}}
	snapshot := materialTestSnapshot()
	call := NewContextGenerator(Deps{}, "owner", base, Material{System: "S", Required: "request"}, snapshot, wiaworld.Run{}, "npc", "npc:a", 1, "T")
	var result struct {
		Answer string `json:"answer"`
	}
	if err := GenerateJSON(context.Background(), call, "S", "request", &result, 100, "answer"); err != nil {
		t.Fatal(err)
	}
	if len(base.requests) != 2 || strings.Contains(base.requests[0].Input, "READ_DETAIL_BODY") || !strings.Contains(base.requests[1].Input, "READ_DETAIL_BODY") || result.Answer != "ready" {
		t.Fatal("material read did not precede the single final candidate")
	}
	if base.requests[1].MaxInputTokens != 12000 || !strings.Contains(base.requests[0].System, "needs_material") {
		t.Fatal("material read changed the input budget or omitted its protocol")
	}
}

func TestMaterialReadRejectsForeignUnknownAndRepeatedRequests(t *testing.T) {
	for _, responses := range [][]string{{`{"needs_material":["foreign"]}`}, {`{"needs_material":["missing"]}`}, {`{"needs_material":["details"]}`, `{"needs_material":["details"]}`}} {
		base := &materialTestGenerator{responses: responses}
		call := NewContextGenerator(Deps{}, "owner", base, Material{System: "S", Required: "request"}, materialTestSnapshot(), wiaworld.Run{}, "npc", "npc:a", 1, "T")
		_, err := call.GenerateText(context.Background(), model.TextRequest{System: "S", MaxOutputTokens: 100})
		if err == nil {
			t.Fatal("invalid material request was accepted")
		}
	}
}

func TestMaterialReadQuotaUsesStableIDsAfterParallelBarrier(t *testing.T) {
	budget := newMaterialReadBudget()
	group := newMaterialReadGroup([]string{"npc:c", "npc:b", "npc:a"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range []string{"npc:c", "npc:b", "npc:a"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := budget.request(ctx, ContextScope{Purpose: "npc", Recipient: id, Stage: 1}, group)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			results[id] = allowed
			mu.Unlock()
		}()
	}
	wg.Wait()
	if !results["npc:a"] || !results["npc:b"] || results["npc:c"] {
		t.Fatalf("quota depended on arrival order: %v", results)
	}
	if _, err := budget.request(ctx, ContextScope{Purpose: "npc", Recipient: "npc:a", Stage: 1}, group); err == nil {
		t.Fatal("a recreated generator acquired a second read in the same purpose")
	}
	if allowed, err := budget.request(ctx, ContextScope{Purpose: "plot", Recipient: "coordinator", Stage: 4}, nil); err != nil || allowed {
		t.Fatal("whole-turn quota was exceeded")
	}
}

func TestRequiredMaterialsSurviveRecentWindowRebuild(t *testing.T) {
	material := Material{System: "S", Required: "old", Bounded: func(int, string) (Material, bool) { return Material{System: "S", Required: "bounded"}, true }}
	selected, _ := selectStoryMaterials(materialTestSnapshot(), "npc", "npc:a", material)
	request, report, err := (ContextComposer{}).Build(selected, selected.System, 100)
	if err != nil || !report.WindowShrunk || !strings.Contains(request.Input, "COMMON_WORLD") || !strings.Contains(request.Input, "OWN_KNOWLEDGE") || !strings.Contains(request.Input, "details") {
		t.Fatalf("material lost in rebuild: %v", err)
	}
}

func TestOversizedRequestedMaterialFailsBeforeFinalModelCall(t *testing.T) {
	snapshot := materialTestSnapshot()
	snapshot.Definition.Materials[4].Body = strings.Repeat("长", 60000)
	base := &materialTestGenerator{responses: []string{`{"needs_material":["details"]}`}}
	call := NewContextGenerator(Deps{}, "owner", base, Material{System: "S", Required: "request"}, snapshot, wiaworld.Run{}, "npc", "npc:a", 1, "T")
	_, err := call.GenerateText(context.Background(), model.TextRequest{System: "S", MaxOutputTokens: 100})
	if !errors.Is(err, ErrContextCapacity) || len(base.requests) != 1 {
		t.Fatalf("oversized required material called model or failed unclearly: %v", err)
	}
}
