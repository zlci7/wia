package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
)

var errReferenceRecorded = errors.New("reference recorded without world commit")

type creationReference struct {
	Format        string               `json:"format"`
	Reasoning     model.ReasoningMode  `json:"reasoning"`
	InputHash     string               `json:"input_hash"`
	SystemHash    string               `json:"system_hash"`
	InputEstimate int                  `json:"input_estimate"`
	OutputLimit   int                  `json:"output_limit"`
	ElapsedMS     int64                `json:"elapsed_ms"`
	Text          string               `json:"text"`
	Diagnostic    model.TextDiagnostic `json:"diagnostic"`
	ErrorCode     string               `json:"error_code,omitempty"`
}

type referenceGenerator struct {
	provider model.TextGenerator
	window   model.WindowLimits
	format   string
	result   creationReference
	request  model.TextRequest
}

func (g *referenceGenerator) ModelWindow() model.WindowLimits { return g.window }
func (g *referenceGenerator) TextReasoningReserve() int       { return 0 }
func (g *referenceGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: `{"intent_type":"speak","addressee_id":"","visibility":"public","action_rule_id":"","wait_minutes":0}`}, nil
	}
	if g.result.Format != "" {
		return model.TextResponse{}, errors.New("reference attempted extra provider call")
	}
	g.request = req
	if g.format == "narrative" {
		if head, _, ok := strings.Cut(req.System, "\nJSON字段合同"); ok {
			req.System = head
		}
		req.System += "\n本次为纯正文创作参照：直接输出完整玩家可读故事，承接原文意图与人物互动。不要输出JSON、事件节点、幕后分析或结构报告。重要选择留给玩家。"
	}
	req.Reasoning = model.ReasoningOff
	req.ReasoningReserveTokens = 0
	stream := false
	req.Streaming = &stream
	started := time.Now()
	response, err := g.provider.GenerateText(ctx, req)
	inputHash, systemHash := sha256.Sum256([]byte(req.Input)), sha256.Sum256([]byte(req.System))
	g.result = creationReference{Format: g.format, Reasoning: req.Reasoning, InputHash: hex.EncodeToString(inputHash[:]), SystemHash: hex.EncodeToString(systemHash[:]), InputEstimate: model.FramedTextInputTokens(req), OutputLimit: req.TotalOutputTokens(), ElapsedMS: time.Since(started).Milliseconds(), Text: response.Text, Diagnostic: response.Diagnostic}
	if err != nil {
		g.result.ErrorCode = model.TextErrorCode(err)
		var failure *model.TextCallError
		if errors.As(err, &failure) {
			g.result.Diagnostic = failure.Diagnostic
		}
	}
	return response, errReferenceRecorded
}

// This opt-in study performs exactly two provider calls and never submits a turn.
func TestCoCreativeReferenceLive(t *testing.T) {
	if os.Getenv("WIA_LIVE_REFERENCE") != "1" {
		t.Skip("explicit live reference opt-in required")
	}
	config := os.Getenv("WIA_LIVE_MODEL_CONFIG")
	output := os.Getenv("WIA_LIVE_EVIDENCE")
	if config == "" || output == "" {
		t.Fatal("model config and new evidence directory required")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("evidence directory must be new")
	}
	provider, settings, err := llm.NewProviderFromConfigFile(config)
	if err != nil {
		t.Fatal("configured provider unavailable")
	}
	if settings.Provider != "deepseek" {
		t.Fatal("study requires the currently authorized DeepSeek provider")
	}
	text, ok := provider.(model.TextGenerator)
	if !ok {
		t.Fatal("provider does not generate text")
	}
	window := model.WindowLimits{}
	if p, ok := provider.(model.WindowProvider); ok {
		window = p.ModelWindow()
	}
	results := []creationReference{}
	g := &referenceGenerator{provider: text, window: window}
	a := newTestApp(t, g)
	world := createPackWorld(t, a, "mist-embers")
	path, _, err := a.worldRecord(t.Context(), world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := readContextSnapshot(t, a, world.WorldID)
	run := insertSceneCandidateRun(t, store, snapshot, "reference", "我先不急着答应，问马丁刚才停顿的那次最后见面究竟发生了什么：诺拉那天说了什么、去了哪里，近来有无反常。")
	run.AddresseeID = ""
	for _, format := range []string{"narrative", "structured"} {
		g.format, g.result = format, creationReference{}
		if format == "narrative" {
			_, _, err = a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
		} else {
			_, err = g.GenerateText(t.Context(), g.request)
		}
		if !errors.Is(err, errReferenceRecorded) {
			t.Fatalf("reference request did not run: %v", err)
		}
		if !phase13WorldUnchanged(snapshot, readContextSnapshot(t, a, world.WorldID)) {
			t.Fatal("study modified world")
		}
		results = append(results, g.result)
		t.Logf("format=%s elapsed_ms=%d input=%d output=%d reasoning=%d error=%s", format, g.result.ElapsedMS, g.result.Diagnostic.InputTokens, g.result.Diagnostic.OutputTokens, g.result.Diagnostic.ReasoningTokens, g.result.ErrorCode)
		if g.result.ErrorCode != "" {
			break
		}
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.MarshalIndent(struct {
		Model   string              `json:"model"`
		Input   string              `json:"input"`
		Results []creationReference `json:"results"`
	}{settings.Model, "连续追问开场", results}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "reference.json"), encoded, 0644); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[len(results)-1].ErrorCode != "" {
		t.Error("live reference incomplete; evidence records the provider failure")
	} else if results[0].InputHash != results[1].InputHash {
		t.Error("reference inputs were not frozen")
	}
}
