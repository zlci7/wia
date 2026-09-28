package storyapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gameagent/runtime/internal/model"
)

func TestGeneratedJSONDiagnosticIsSafeAndSpecific(t *testing.T) {
	for _, tc := range []struct{ response, code, field, expected string }{
		{`{"scene": {"PRIVATE":"secret"}}`, "json_field_type", "scene", "string"},
		{`{"outcomes": [{"recipients":"PRIVATE"}]}`, "json_field_type", "outcomes.recipients", "array"},
		{`{"PRIVATE_FIELD":"secret"}`, "json_unknown_field", "", ""},
		{`{"scene":"PRIVATE`, "json_syntax_invalid", "", ""},
		{`{}`, "json_required_field_missing", "scene", ""},
		{`{"scene":null}`, "json_required_field_null", "scene", ""},
	} {
		var result hostResult
		required := []string(nil)
		if tc.code == "json_required_field_missing" || tc.code == "json_required_field_null" {
			required = []string{"scene"}
		}
		err := decodeGeneratedJSON(tc.response, &result, nil, required)
		var detail *generationJSONError
		if !errors.As(err, &detail) || !errors.Is(err, ErrGenerationFailed) || detail.Code != tc.code || detail.Field != tc.field || detail.Expected != tc.expected {
			t.Fatalf("%+v", err)
		}
		logger := &recordingLogger{}
		g := &contextGenerator{logger: logger}
		g.recordJSONValidation(err)
		if strings.Contains(logger.String()+detail.Error(), "PRIVATE") || !strings.Contains(logger.String(), tc.code) {
			t.Fatal(logger.String())
		}
	}
}

type jsonRepairDiagnosticGenerator struct{ requests []model.TextRequest }

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

func TestIntentRecipientUsesBoundedRepair(t *testing.T) {
	for _, first := range []string{
		`{"intent_type":"speak","addressee_id":"npc:boatman","visibility":"public"}`,
		`{"intent_type":"speak","addressee_id":"","visibility":"private"}`,
	} {
		g := &intentEnumGenerator{first: first}
		a := newTestApp(t, g)
		w := createPackWorld(t, a, GameID)
		s := readContextSnapshot(t, a, w.WorldID)
		intent, repairs, err := a.resolveTurnIntent(context.Background(), g, s, Run{RunID: "recipient", Input: "请船夫帮伤者上船"})
		if err != nil || repairs != 1 || len(g.requests) != 2 || intent.AddresseeID != "" {
			t.Fatalf("repairs=%d calls=%d err=%v", repairs, len(g.requests), err)
		}
	}
}

func TestIntentEnumsUseOneBoundedFormatRepair(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		g := &intentEnumGenerator{invalid: invalid}
		var result turnIntent
		repairs, err := generateJSONMetrics(context.Background(), g, "规则", "等半小时", &result, 4096, "intent_type", "addressee_id", "visibility")
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

func TestIntentValueDiagnosticsAreSafe(t *testing.T) {
	for _, tc := range []struct{ text, field string }{
		{`{"intent_type":"PRIVATE","visibility":"public"}`, "intent_type"},
		{`{"intent_type":"act","visibility":"PRIVATE_VALUE"}`, "visibility"},
		{`{"intent_type":"act","visibility":"public","wait_minutes":121}`, "wait_minutes"},
	} {
		var result turnIntent
		err := decodeGeneratedJSON(tc.text, &result, nil, nil)
		var detail *generationJSONError
		if !errors.As(err, &detail) || detail.Field != tc.field || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal(err)
		}
	}
}

func (g *jsonRepairDiagnosticGenerator) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, r)
	if len(g.requests) == 1 {
		return model.TextResponse{Text: `{"scene":{"PRIVATE":"secret"}}`}, nil
	}
	return model.TextResponse{Text: `{"scene":"安静的大厅"}`}, nil
}

func TestJSONRepairReceivesSafeFieldTypeWithinExistingAttemptLimit(t *testing.T) {
	g := &jsonRepairDiagnosticGenerator{}
	var result hostResult
	repairs, err := generateJSONMetrics(context.Background(), g, "规则", "材料", &result, 4096, "scene")
	if err != nil || repairs != 1 || len(g.requests) != 2 || result.Scene != "安静的大厅" {
		t.Fatalf("%d %+v %v", repairs, result, err)
	}
	rule := g.requests[1].System
	if !strings.Contains(rule, "field=scene expected=string") || strings.Contains(rule, "PRIVATE") {
		t.Fatal(rule)
	}
}
