package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
)

// generatedTarget is the decode target for the diagnostic cases: a scene string and a
// list of outcomes, which is all the local schema has to name for those codes.
type generatedTarget struct {
	Scene    string             `json:"scene"`
	Outcomes []generatedOutcome `json:"outcomes"`
}

type generatedOutcome struct {
	Recipients []string `json:"recipients"`
}

// recordingLogger keeps what a turn logged so a test can assert on the diagnostic line
// without reading the prompt it came from.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestGeneratedJSONDiagnosticIsSafeAndSpecific(t *testing.T) {
	for _, tc := range []struct{ response, code, field, expected string }{
		{`{"scene": {"PRIVATE":"secret"}}`, "json_field_type", "scene", "string"},
		{`{"outcomes": [{"recipients":"PRIVATE"}]}`, "json_field_type", "outcomes.recipients", "array"},
		{`{"PRIVATE_FIELD":"secret"}`, "json_unknown_field", "", `{"scene":string,"outcomes":[{"recipients":[string]}]}`},
		{`{"scene":"PRIVATE`, "json_syntax_invalid", "", ""},
		{`{}`, "json_required_field_missing", "scene", ""},
		{`{"scene":null}`, "json_required_field_null", "scene", ""},
	} {
		var result generatedTarget
		required := []string(nil)
		if tc.code == "json_required_field_missing" || tc.code == "json_required_field_null" {
			required = []string{"scene"}
		}
		err := DecodeGeneratedJSON(tc.response, &result, nil, required)
		var detail *GenerationError
		if !errors.As(err, &detail) || !errors.Is(err, ErrGenerationFailed) || detail.Code != tc.code || detail.Field != tc.field || detail.Expected != tc.expected {
			t.Fatalf("%+v", err)
		}
		logger := &recordingLogger{}
		g := &ContextGenerator{logger: logger}
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

func TestIntentValueDiagnosticsAreSafe(t *testing.T) {
	for _, tc := range []struct{ text, field string }{
		{`{"intent_type":"PRIVATE","visibility":"public"}`, "intent_type"},
		{`{"intent_type":"act","visibility":"PRIVATE_VALUE"}`, "visibility"},
		{`{"intent_type":"act","visibility":"public","wait_minutes":121}`, "wait_minutes"},
	} {
		var result TurnIntent
		err := DecodeGeneratedJSON(tc.text, &result, nil, nil)
		var detail *GenerationError
		if !errors.As(err, &detail) || detail.Field != tc.field || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal(err)
		}
	}
}

func TestJSONRepairReceivesSafeFieldTypeWithinExistingAttemptLimit(t *testing.T) {
	g := &jsonRepairDiagnosticGenerator{}
	var result generatedTarget
	repairs, err := GenerateJSONMetrics(context.Background(), g, "规则", "材料", &result, 4096, "scene")
	if err != nil || repairs != 1 || len(g.requests) != 2 || result.Scene != "安静的大厅" {
		t.Fatalf("%d %+v %v", repairs, result, err)
	}
	rule := g.requests[1].System
	if !strings.Contains(rule, "field=scene expected=string") || strings.Contains(rule, "PRIVATE") {
		t.Fatal(rule)
	}
}
