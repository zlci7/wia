package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

const maxStudyCalls = 32

type studyInput struct {
	Input   string `json:"input"`
	Advance bool   `json:"allow_plot_advance"`
}

type studyCall struct {
	Text          string               `json:"text,omitempty"`
	ElapsedMS     int64                `json:"elapsed_ms"`
	FirstTextMS   *int64               `json:"first_text_ms,omitempty"`
	InputEstimate int                  `json:"input_estimate"`
	OutputLimit   int                  `json:"output_limit"`
	Diagnostic    model.TextDiagnostic `json:"diagnostic"`
	ErrorCode     string               `json:"error_code,omitempty"`
}

type studyTurn struct {
	Input         studyInput          `json:"request"`
	Reasoning     model.ReasoningMode `json:"reasoning"`
	Streaming     bool                `json:"streaming"`
	ElapsedMS     int64               `json:"elapsed_ms"`
	Calls         []studyCall         `json:"calls"`
	Result        turn.CreationResult `json:"result"`
	AcceptedState turn.CreationStatus `json:"accepted_state"`
	ErrorCode     string              `json:"error_code,omitempty"`
}

type studyRecord struct {
	Model    string      `json:"model"`
	Revision string      `json:"story_revision"`
	Turns    []studyTurn `json:"turns"`
}

type studyGenerator struct {
	provider model.TextGenerator
	window   model.WindowLimits
	reserve  int
	used     int
	limit    int
	calls    []studyCall
}

func (g *studyGenerator) ModelWindow() model.WindowLimits { return g.window }
func (g *studyGenerator) TextReasoningReserve() int       { return g.reserve }
func (g *studyGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	if g.used >= g.limit {
		return model.TextResponse{}, errors.New("study_call_limit")
	}
	g.used++
	started := time.Now()
	call := studyCall{InputEstimate: model.FramedTextInputTokens(request), OutputLimit: request.TotalOutputTokens()}
	observe := request.OnDelta
	if request.Streams() {
		request.OnDelta = func(delta model.TextDelta) {
			if delta.Text != "" && call.FirstTextMS == nil {
				ms := time.Since(started).Milliseconds()
				call.FirstTextMS = &ms
			}
			if observe != nil {
				observe(model.TextDelta{Text: delta.Text})
			}
		}
	}
	response, err := g.provider.GenerateText(ctx, request)
	call.ElapsedMS, call.Diagnostic, call.Text = time.Since(started).Milliseconds(), response.Diagnostic, response.Text
	if err != nil {
		call.ErrorCode = model.TextErrorCode(err)
		var failure *model.TextCallError
		if errors.As(err, &failure) {
			call.Diagnostic = failure.Diagnostic
		}
	}
	g.calls = append(g.calls, call)
	return response, err
}

func modelConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("WIA_MODEL_CONFIG")); value != "" {
		return value
	}
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	return filepath.Join(root, "WorldIsAgent", "story-app", "config", "model.json")
}

func loadStudyInputs(path string) ([]studyInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := turn.ValidateStrictJSON(data); err != nil {
		return nil, err
	}
	var inputs []studyInput
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inputs); err != nil {
		return nil, err
	}
	if len(inputs) == 0 || len(inputs) > 15 {
		return nil, errors.New("study requires 1..15 inputs")
	}
	for _, input := range inputs {
		if strings.TrimSpace(input.Input) == "" {
			return nil, errors.New("empty study input")
		}
	}
	return inputs, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	packPath := flag.String("pack", "internal/content/packs/mist-embers", "剧本包目录")
	configPath := flag.String("model-config", modelConfigPath(), "现有模型配置路径")
	inputPath := flag.String("inputs", "", "连续测试输入JSON文件；省略时交互输入")
	recordPath := flag.String("record", "", "新建结果JSON文件路径")
	reasoningFlag := flag.String("reasoning", "off", "off、low或high")
	streaming := flag.Bool("stream", false, "观察候选流式片段")
	advance := flag.Bool("advance", false, "允许本轮主动推进")
	callLimit := flag.Int("max-calls", maxStudyCalls, "本次付费请求上限（1～32）")
	flag.Parse()
	reasoning := model.ReasoningMode(*reasoningFlag)
	if reasoning == model.ReasoningDefault || !reasoning.Valid() || *callLimit < 1 || *callLimit > maxStudyCalls {
		return errors.New("参数无效：思考模式为off/low/high，请求上限为1～32")
	}
	var inputs []studyInput
	if *inputPath != "" {
		var err error
		inputs, err = loadStudyInputs(*inputPath)
		if err != nil {
			return fmt.Errorf("测试输入无效：%w", err)
		}
	}
	pack, err := content.Load(*packPath)
	if err != nil {
		return fmt.Errorf("剧本加载失败：%w", err)
	}
	session, err := turn.NewCreationSession(pack.Definition)
	if err != nil {
		return fmt.Errorf("情境初始化失败：%w", err)
	}
	provider, config, err := llm.NewProviderFromConfigFile(*configPath)
	if err != nil {
		return errors.New("模型配置不可用，请检查WIA正式配置或指定-model-config")
	}
	text, ok := provider.(model.TextGenerator)
	if !ok || config.Provider == "" || config.Provider == "fake" {
		return errors.New("连续游玩需要现有真实模型配置")
	}
	generator := &studyGenerator{provider: text, limit: *callLimit}
	if p, ok := provider.(model.WindowProvider); ok {
		generator.window = p.ModelWindow()
	}
	if p, ok := provider.(model.TextReasoningProvider); ok {
		generator.reserve = p.TextReasoningReserve()
	}
	var file *os.File
	if *recordPath != "" {
		file, err = os.OpenFile(*recordPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return errors.New("结果文件必须是可创建的新文件")
		}
		defer file.Close()
	}
	record := studyRecord{Model: config.Model, Revision: pack.Definition.Revision, Turns: []studyTurn{}}
	save := func() error {
		if file == nil {
			return nil
		}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		return file.Sync()
	}
	if err := save(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	service := turn.New(nil, turn.Deps{})
	fmt.Printf("%s · %s\n%s\n", pack.Definition.Summary.Title, session.Status().Clock, pack.Definition.Opening)
	fmt.Println("内存会话；/advance on|off、/stream on|off、/reasoning off|low|high、/quit。")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 32<<10)
	index := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var input studyInput
		if *inputPath != "" {
			if index == len(inputs) {
				return nil
			}
			input = inputs[index]
			index++
			fmt.Printf("\n[%d] %s\n", index, input.Input)
		} else {
			fmt.Print("\n行动> ")
			if !scanner.Scan() {
				return scanner.Err()
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "/quit" {
				return nil
			}
			if strings.HasPrefix(line, "/") {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					switch {
					case parts[0] == "/advance" && (parts[1] == "on" || parts[1] == "off"):
						*advance = parts[1] == "on"
					case parts[0] == "/stream" && (parts[1] == "on" || parts[1] == "off"):
						*streaming = parts[1] == "on"
					case parts[0] == "/reasoning" && model.ReasoningMode(parts[1]).Valid() && parts[1] != "":
						reasoning = model.ReasoningMode(parts[1])
					default:
						fmt.Println("命令无效。")
					}
				} else {
					fmt.Println("命令无效。")
				}
				continue
			}
			if line == "" {
				continue
			}
			input = studyInput{line, *advance}
		}
		generator.calls = nil
		if *streaming {
			fmt.Println("生成候选片段（完整校验后接受）：")
		}
		started := time.Now()
		result, err := session.Interact(ctx, service, generator, turn.CreationOptions{
			Input: input.Input, AllowPlotAdvance: input.Advance, Reasoning: reasoning, Streaming: streaming,
			OnDelta: func(delta model.TextDelta) {
				if *streaming && delta.Text != "" {
					fmt.Print(delta.Text)
				}
			},
		})
		item := studyTurn{Input: input, Reasoning: reasoning, Streaming: *streaming, ElapsedMS: time.Since(started).Milliseconds(),
			Calls: generator.calls, Result: result, AcceptedState: session.Status()}
		if err != nil {
			item.ErrorCode = turn.ErrorCode(err)
		}
		record.Turns = append(record.Turns, item)
		if saveErr := save(); saveErr != nil {
			return saveErr
		}
		if err != nil {
			return fmt.Errorf("\n本轮未接受，保留第%d轮情境；error=%s", session.Status().Turn, item.ErrorCode)
		}
		fmt.Printf("\n%s\n[%s · %s · %.3fs · 核心调用%d · 纠正%d]\n", result.Scene.Narrative, session.Status().Clock,
			session.Status().Positions["player"], float64(item.ElapsedMS)/1000, result.Report.CoreCalls, result.Report.Repairs)
	}
}
