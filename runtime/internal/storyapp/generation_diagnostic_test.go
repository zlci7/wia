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
