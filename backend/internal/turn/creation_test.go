package turn

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

type creationTestGenerator struct {
	requests []model.TextRequest
	answer   func(context.Context, model.TextRequest, int) (model.TextResponse, error)
}

func TestCreationContinuityReservesPersonalFactsWithoutQueryOverlap(t *testing.T) {
	s := newCreationTestSession(t)
	s.recordCreation("player", "old", "npc:smith", "commitment", "后天到柜台取修好的桅杆收据")
	s.recordCreation("npc:merchant", "private", "npc:merchant", "statement", "商人私下准备转卖货物")
	for i := 0; i < 8; i++ {
		_, err := s.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(creationTestJSON(`{"narrative":"工匠解释普通工序。"}`)), CreationOptions{Input: "请解释加工步骤。"})
		if err != nil {
			t.Fatal(err)
		}
	}
	material, _ := s.creationMaterial(CreationOptions{Input: "今天的天气怎么样？"})
	if !strings.Contains(material.Required, "后天到柜台取修好的桅杆收据") || !strings.Contains(material.Required, "commitment") || strings.Contains(material.Required, "商人私下准备转卖货物") {
		t.Fatal("continuity or ownership lost")
	}
	for i := 0; i < 30; i++ {
		s.recordCreation("player", "long", "", "observed", strings.Repeat("长篇记录", 200))
	}
	sections := s.creationContinuity([]string{"player"}, nil)
	tokens := 0
	for _, section := range sections {
		tokens += model.FramedTextInputTokens(model.TextRequest{Input: section.Text})
	}
	if len(sections) > 8 || tokens > 1200 {
		t.Fatal("unbounded personal continuity")
	}
}

func TestCreationFourStartsFitContinuousBudget(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range pack.Definition.StartingOptions {
		t.Run(option.ID, func(t *testing.T) {
			def, err := story.WithStartingOption(pack.Definition, option.ID)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewCreationSession(def)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				g := &creationTestGenerator{answer: func(_ context.Context, req model.TextRequest, _ int) (model.TextResponse, error) {
					if req.MaxInputTokens != 12000 || model.FramedTextInputTokens(req) > 12000 {
						t.Fatal("input budget")
					}
					if i == 0 && !strings.Contains(req.Input, option.Opening) {
						t.Fatal("opening missing")
					}
					if !strings.Contains(req.Input, option.Player.Profile) {
						t.Fatal("identity missing")
					}
					speaker := "player"
					for _, id := range creationEntities(s.snapshot, "") {
						if id != "player" {
							speaker = id
							break
						}
					}
					text, _ := json.Marshal(CreationScene{Narrative: strings.Repeat("人物根据已有经历回答，并说明一个合理的合作条件。", 62), Changes: &CreationChanges{Positions: def.InitialLocations, ElapsedMinutes: 3}, Notes: []CreationNote{{Kind: "commitment", Content: "明天下午再次谈合作，尚未付款", Recipients: []string{"player"}, SpeakerID: "player"}, {Kind: "statement", Content: strings.Repeat("本人说明了工作流程、协作条件和目前的困难。", 7), Recipients: []string{"player"}, SpeakerID: speaker}}})
					return model.TextResponse{Text: string(text)}, nil
				}}
				_, err := s.Interact(t.Context(), New(nil, Deps{}), g, CreationOptions{Input: "我先聊眼前的事情，问问接下来怎样合作。", Reasoning: model.ReasoningLow})
				if err != nil {
					t.Fatalf("turn %d: %v", i+1, err)
				}
			}
			if len(s.PersonalSources("player")) != 6*4 {
				t.Fatal("accepted records lost")
			}
		})
	}
}

func (g *creationTestGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, request)
	return g.answer(ctx, request, len(g.requests))
}

func creationTestDefinition() story.Definition {
	return story.Definition{
		Revision: "harbor.v1", Clock: "2040-06-12 23:58", Background: "海港的航船维修与商人协商",
		Opening: "你在船坞听工匠解释修理进度。",
		Locations: []story.Location{
			{ID: "dock", Name: "船坞", Connections: []string{"inn"}, Public: true},
			{ID: "inn", Name: "客栈", Connections: []string{"dock"}, Public: true},
			{ID: "island", Name: "孤岛", Connections: []string{}, Public: true},
		},
		InitialLocations: map[string]string{"player": "dock", "npc:smith": "dock", "npc:merchant": "inn"},
		Characters: []wiaworld.Character{
			{EntityID: "npc:smith", Name: "工匠", Profile: "要求先支付工钱", Knowledge: "工匠私有信息"},
			{EntityID: "npc:merchant", Name: "商人", Profile: "急于离港", Knowledge: "商人私有信息"},
		},
		Materials: []story.Material{
			{ID: "smith-truth", Purpose: "author_facts", Visibility: "author", EntityIDs: []string{"npc:smith"}, Body: "修船的固定真相"},
			{ID: "inn-lore", Purpose: "location_lore", Visibility: "public", Summary: "客栈正文", LocationIDs: []string{"inn"}, Body: "客栈相关正文"},
			{ID: "island-lore", Purpose: "location_lore", Visibility: "public", Summary: "孤岛目录", LocationIDs: []string{"island"}, Body: "无关孤岛正文"},
		},
	}
}

func newCreationTestSession(t *testing.T) *CreationSession {
	t.Helper()
	session, err := NewCreationSession(creationTestDefinition())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func creationFixedGenerator(text string) *creationTestGenerator {
	return &creationTestGenerator{answer: func(context.Context, model.TextRequest, int) (model.TextResponse, error) {
		return model.TextResponse{Text: text}, nil
	}}
}

func creationTestJSON(text string) string {
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		panic(err)
	}
	changes, ok := object["scene_changes"].(map[string]any)
	if !ok {
		changes = map[string]any{}
		object["scene_changes"] = changes
	}
	if _, ok := changes["positions"]; !ok {
		changes["positions"] = map[string]string{"player": "dock", "npc:smith": "dock", "npc:merchant": "inn"}
	}
	data, _ := json.Marshal(object)
	return string(data)
}

func TestCreationAcceptsWholeSceneAndPersonalRecords(t *testing.T) {
	session := newCreationTestSession(t)
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"工匠低声答应明早检查桅杆。","scene_changes":{"elapsed_minutes":5},"continuity_notes":[{"kind":"commitment","content":"明早检查桅杆","recipients":["player","npc:smith"],"speaker_id":"npc:smith"}]}`))
	off := false
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{
		Input: "我私下请工匠明早检查桅杆。", AllowPlotAdvance: true, Reasoning: model.ReasoningOff, Streaming: &off,
	})
	if err != nil || result.Report.CoreCalls != 1 || result.Report.Repairs != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if session.Status().Clock != "2040-06-13 00:03" || session.Status().Turn != 1 {
		t.Fatal(session.Status())
	}
	if len(session.PersonalSources("npc:smith")) != 1 || len(session.PersonalSources("npc:merchant")) != 0 || len(session.PersonalSources("player")) != 3 {
		t.Fatal("personal record ownership lost")
	}
	req := generator.requests[0]
	if !req.JSON || req.Reasoning != model.ReasoningOff || req.Streams() || !strings.Contains(req.Input, `"allow_plot_advance":true`) ||
		!strings.Contains(req.Input, "工匠私有信息") || strings.Contains(req.Input, "商人私有信息") ||
		!strings.Contains(req.Input, "修船的固定真相") || strings.Contains(req.Input, "无关孤岛正文") {
		t.Fatalf("wrong context or options: %s", req.Input)
	}
	result.Scene.Notes[0].Content = "外部修改"
	status := session.Status()
	status.Positions["player"] = "island"
	sources := session.PersonalSources("npc:smith")
	sources[0].Content = "外部修改"
	if session.Status().Positions["player"] != "dock" || session.PersonalSources("npc:smith")[0].Content != "明早检查桅杆" {
		t.Fatal("accepted state aliases caller data")
	}
}

func TestCreationMovementCarriesCurrentSceneAndDestinationPeople(t *testing.T) {
	session := newCreationTestSession(t)
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"你与工匠到客栈，商人抬头回应。","scene_changes":{"elapsed_minutes":8,"positions":{"player":"inn","npc:smith":"inn","npc:merchant":"inn"}}}`))
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "和工匠去客栈见商人。"})
	if err != nil || !slices.Contains(result.Report.SelectedEntityIDs, "npc:merchant") || !strings.Contains(generator.requests[0].Input, "商人私有信息") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	generator = creationFixedGenerator(creationTestJSON(`{"narrative":"商人回应你们的追问。","scene_changes":{"positions":{"player":"inn","npc:smith":"inn","npc:merchant":"inn"}}}`))
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续问刚才说的细节。"})
	if err != nil || !strings.Contains(generator.requests[0].Input, `"player":"inn"`) ||
		!strings.Contains(generator.requests[0].Input, "你与工匠到客栈") || strings.Contains(generator.requests[0].Input, "开场：") {
		t.Fatalf("current scene did not supersede opening: %v", err)
	}
}

func TestCreationRejectsMalformedAndUnsupportedChanges(t *testing.T) {
	for name, text := range map[string]string{
		"missing":            `{"scene_changes":{}}`,
		"missing_changes":    `{"narrative":"一"}`,
		"duplicate":          `{"narrative":"一","narrative":"二"}`,
		"unknown":            `{"narrative":"一","balance":300}`,
		"nested_unknown":     `{"narrative":"一","scene_changes":{"gold":300}}`,
		"null":               `{"narrative":null}`,
		"time":               `{"narrative":"一","scene_changes":{"elapsed_minutes":121}}`,
		"teleport":           `{"narrative":"一","scene_changes":{"positions":{"player":"island","npc:smith":"dock"}}}`,
		"missing_position":   `{"narrative":"一","scene_changes":{"positions":{"player":"dock"}}}`,
		"foreign_actor":      `{"narrative":"一","scene_changes":{"positions":{"player":"dock","npc:smith":"dock","npc:merchant":"dock"}}}`,
		"unknown_recipient":  `{"narrative":"一","continuity_notes":[{"kind":"observed","content":"一","recipients":["npc:alien"]}]}`,
		"repeated_recipient": `{"narrative":"一","continuity_notes":[{"kind":"observed","content":"一","recipients":["player","player"]}]}`,
		"untyped_note":       `{"narrative":"一","continuity_notes":[{"kind":"fact","content":"一","recipients":["player"]}]}`,
		"missing_speaker":    `{"narrative":"一","continuity_notes":[{"kind":"statement","content":"一","recipients":["player"]}]}`,
		"trailing":           `{"narrative":"一"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			session := newCreationTestSession(t)
			before := session.Status()
			if name != "missing" && name != "missing_changes" && name != "duplicate" && name != "trailing" {
				text = creationTestJSON(text)
			}
			generator := creationFixedGenerator(text)
			result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "请解释。"})
			if err == nil || result.Report.CoreCalls != 2 || result.Report.Repairs != 1 || !reflect.DeepEqual(before, session.Status()) || len(session.PersonalSources("player")) != 0 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestCreationRepairsOnceAndStopsProviderFailure(t *testing.T) {
	session := newCreationTestSession(t)
	generator := &creationTestGenerator{answer: func(_ context.Context, req model.TextRequest, n int) (model.TextResponse, error) {
		if n == 1 {
			return model.TextResponse{Text: creationTestJSON(`{"narrative":"一","scene_changes":{"elapsed_minutes":-1}}`)}, nil
		}
		if !strings.Contains(req.System, "creation_contract_invalid") || !strings.Contains(req.System, "必须同时包含scene_changes和narrative") || strings.Contains(req.Input, "creation_contract_invalid") {
			t.Fatal("repair lacks safe field diagnosis")
		}
		return model.TextResponse{Text: creationTestJSON(`{"narrative":"有效回应"}`)}, nil
	}}
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续。"})
	if err != nil || result.Report.CoreCalls != 2 || result.Report.Repairs != 1 || session.Status().Turn != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, failure := range []error{model.ErrInvalidTextResponse, context.DeadlineExceeded} {
		generator := &creationTestGenerator{answer: func(context.Context, model.TextRequest, int) (model.TextResponse, error) {
			return model.TextResponse{}, failure
		}}
		before := session.Status()
		result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续。"})
		if !errors.Is(err, failure) || result.Report.CoreCalls != 1 || result.Report.Repairs != 0 || !reflect.DeepEqual(before, session.Status()) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	generator = &creationTestGenerator{answer: func(context.Context, model.TextRequest, int) (model.TextResponse, error) {
		return model.TextResponse{Text: creationTestJSON(`{"narrative":"有效但被截断的消息"}`), Diagnostic: model.TextDiagnostic{FinishReason: "length"}}, nil
	}}
	before := session.Status()
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续。"})
	if !errors.Is(err, model.ErrInvalidTextResponse) || len(generator.requests) != 1 || !reflect.DeepEqual(before, session.Status()) {
		t.Fatal("truncated response accepted or billed for a repair")
	}
}

func TestCreationMissingChangesRepairPreservesFullContract(t *testing.T) {
	session := newCreationTestSession(t)
	generator := &creationTestGenerator{answer: func(_ context.Context, req model.TextRequest, n int) (model.TextResponse, error) {
		if n == 1 {
			return model.TextResponse{Text: `{"narrative":"人物解释合作条件，尚未承诺。"}`}, nil
		}
		if !strings.Contains(req.System, "field=scene_changes") || !strings.Contains(req.System, "json_required_field_missing") || !strings.Contains(req.System, "必须同时包含scene_changes和narrative") {
			t.Fatal("missing required field diagnosis and complete contract")
		}
		return model.TextResponse{Text: creationTestJSON(`{"narrative":"人物说明合作条件，等待玩家选择。"}`)}, nil
	}}
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "说明合作条件。", Reasoning: model.ReasoningLow})
	if err != nil || result.Report.Repairs != 1 || session.Status().Turn != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCreationMaterialSupplementHasOneSharedOpportunity(t *testing.T) {
	session := newCreationTestSession(t)
	generator := &creationTestGenerator{answer: func(_ context.Context, req model.TextRequest, n int) (model.TextResponse, error) {
		if n == 1 {
			return model.TextResponse{Text: `{"needs_material":["inn-lore"]}`}, nil
		}
		if !strings.Contains(req.Input, "客栈相关正文") {
			t.Fatal("authorized supplement missing")
		}
		return model.TextResponse{Text: creationTestJSON(`{"narrative":"工匠描述自己实际知道的客栈情况。"}`)}, nil
	}}
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "询问工匠。"})
	if err != nil || result.Report.CoreCalls != 2 || result.Report.Repairs != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	generator = creationFixedGenerator(`{"needs_material":["inn-lore"]}`)
	before := session.Status()
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "询问工匠。"})
	if err == nil || len(generator.requests) != 2 || !reflect.DeepEqual(before, session.Status()) {
		t.Fatal("repeated material request accepted")
	}
}

func TestCreationCancellationAndBusyPreserveAcceptedState(t *testing.T) {
	session := newCreationTestSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	entered, release := make(chan struct{}), make(chan struct{})
	generator := &creationTestGenerator{answer: func(_ context.Context, request model.TextRequest, _ int) (model.TextResponse, error) {
		if request.OnDelta != nil {
			request.OnDelta(model.TextDelta{Text: "候选片段"})
		}
		close(entered)
		<-release
		return model.TextResponse{Text: creationTestJSON(`{"narrative":"完成的候选正文"}`)}, nil
	}}
	before := session.Status()
	done := make(chan error, 1)
	go func() {
		_, err := session.Interact(ctx, New(nil, Deps{}), generator, CreationOptions{Input: "继续", OnDelta: func(model.TextDelta) {}})
		done <- err
	}()
	<-entered
	_, err := session.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(""), CreationOptions{Input: "另一个输入"})
	if !errors.Is(err, ErrCreationBusy) || !reflect.DeepEqual(before, session.Status()) {
		t.Fatal("concurrent request or partial stream changed state")
	}
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, session.Status()) {
		t.Fatal("cancelled result accepted")
	}
}

func TestCreationOldPromiseRecallsWithoutExtraModelAndOwnsDefinition(t *testing.T) {
	def := creationTestDefinition()
	session, err := NewCreationSession(def)
	if err != nil {
		t.Fatal(err)
	}
	def.InitialLocations["player"] = "island"
	def.Characters[0].Knowledge = "被外部修改"
	for i := 0; i < 10; i++ {
		answer := `{"narrative":"工匠继续解释船体情况。"}`
		if i == 0 {
			answer = `{"narrative":"工匠答应明早检查桅杆。","continuity_notes":[{"kind":"commitment","content":"明早检查桅杆","recipients":["player","npc:smith"],"speaker_id":"npc:smith"}]}`
		}
		_, err := session.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(creationTestJSON(answer)), CreationOptions{Input: "继续核对。"})
		if err != nil {
			t.Fatal(err)
		}
	}
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"工匠记得检查桅杆的约定。"}`))
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "之前明早检查桅杆的约定还成立吗？"})
	if err != nil || len(generator.requests) != 1 || !strings.Contains(generator.requests[0].Input, "明早检查桅杆") ||
		!strings.Contains(generator.requests[0].Input, "commitment") || strings.Contains(generator.requests[0].Input, "被外部修改") ||
		session.Status().Positions["player"] != "dock" {
		t.Fatalf("recall/ownership failure: %v", err)
	}
}

func TestCreationFollowupIncludesLastSpeakerAndPlacesAuthorityAfterHistory(t *testing.T) {
	session := newCreationTestSession(t)
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"工匠答应等你，你独自到客栈。","scene_changes":{"positions":{"player":"inn","npc:smith":"dock","npc:merchant":"inn"}},"continuity_notes":[{"kind":"commitment","content":"工匠答应等你","speaker_id":"npc:smith","recipients":["player"]}]}`))
	_, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "与工匠说完，我独自去客栈。"})
	if err != nil || len(session.PersonalSources("npc:smith")) != 1 {
		t.Fatalf("speaker did not remember own promise: %v", err)
	}
	generator = creationFixedGenerator(creationTestJSON(`{"narrative":"你记得他刚才的约定，商人还在客栈。","scene_changes":{"positions":{"player":"inn","npc:smith":"dock","npc:merchant":"inn"}}}`))
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "他刚才那个约定是什么？"})
	if err != nil || !slices.Contains(result.Report.SelectedEntityIDs, "npc:smith") {
		t.Fatalf("last speaker excluded from follow-up: %v", err)
	}
	request := generator.requests[0].Input
	if strings.LastIndex(request, "权威当前情境") < strings.LastIndex(request, "工匠答应等你，你独自到客栈") ||
		!strings.Contains(request, `"npc:smith":"dock"`) || !strings.Contains(request, `"player":"inn"`) {
		t.Fatal("history displaced authoritative present positions")
	}
}

func TestCreationRecognizesPartialNamesAcrossLocations(t *testing.T) {
	def := creationTestDefinition()
	def.Characters[1].Name = "塔林·远川"
	session, err := NewCreationSession(def)
	if err != nil {
		t.Fatal(err)
	}
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"你决定先了解塔林的来历，尚未和他当面交谈。"}`))
	result, err := session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "塔林是谁？"})
	if err != nil || !slices.Contains(result.Report.SelectedEntityIDs, "npc:merchant") || !strings.Contains(generator.requests[0].Input, "商人私有信息") {
		t.Fatal("partial name excluded relevant character")
	}
}

func TestCreationUsesExistingNarrativePerspective(t *testing.T) {
	def := creationTestDefinition()
	def.Settings = wiaworld.DefaultNarrativeSettings()
	def.Settings.Perspective = wiaworld.PerspectiveFirstPerson
	session, err := NewCreationSession(def)
	if err != nil {
		t.Fatal(err)
	}
	generator := creationFixedGenerator(creationTestJSON(`{"narrative":"我听完工匠的解释。"}`))
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续解释。"})
	if err != nil || !strings.Contains(generator.requests[0].System, PerspectiveInstruction(def.Settings, "")) {
		t.Fatalf("existing perspective not supplied: %v", err)
	}
}

func TestCreationCapacityFailurePreventsProviderCall(t *testing.T) {
	def := creationTestDefinition()
	def.Characters[0].Profile = strings.Repeat("完整的必需人物资料", 20000)
	session, err := NewCreationSession(def)
	if err != nil {
		t.Fatal(err)
	}
	before := session.Status()
	generator := creationFixedGenerator("")
	_, err = session.Interact(t.Context(), New(nil, Deps{}), generator, CreationOptions{Input: "继续交谈", Reasoning: model.ReasoningOff})
	if !errors.Is(err, ErrContextCapacity) || len(generator.requests) != 0 || !reflect.DeepEqual(before, session.Status()) {
		t.Fatalf("capacity failure reached provider or changed state: %v", err)
	}
}

func TestCreationDestinationUsesDirectedGraph(t *testing.T) {
	graph := map[string][]string{"one": {"two"}, "two": {"three"}, "three": {}, "island": {}}
	if !creationReachable("one", "three", graph) || creationReachable("three", "one", graph) || creationReachable("one", "island", graph) {
		t.Fatal("ending positions bypassed directed reachability")
	}
}
