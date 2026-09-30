package turn

// This file owns the contract between a stage and the model that answers it: the strict
// JSON check, the field contract derived from the target struct, the one repair attempt,
// and the error vocabulary that says which field and which shape was wrong.
//
// It also owns the generator that wraps a model call, because a turn's request is
// composed here and the diagnostic of that request belongs with it. What the wrapper
// still needs from the application — usage metering and a logger — arrives as Deps, so
// that a turn never holds the application it runs inside.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// validateTurnIntent checks and normalizes what a model produced for a player's intent.
//
// The rules live here rather than on the type because the error they return is part of
// the turn's diagnostics: a repair pass needs to know which field was wrong and what was
// expected, and that vocabulary belongs with the rest of the generation errors. The three
// fields are constrained here and nowhere else, so every decode into a TurnIntent gets the
// same rules. Normalizing first means a model answering "Speak" is accepted rather than
// rejected on case.
func validateTurnIntent(intent *TurnIntent) error {
	intent.IntentType = strings.ToLower(wire.Clean(intent.IntentType))
	intent.Visibility = strings.ToLower(wire.Clean(intent.Visibility))
	intent.AddresseeID = wire.Clean(intent.AddresseeID)
	field, expected := "", ""
	switch {
	case intent.IntentType != "speak" && intent.IntentType != "observe" && intent.IntentType != "act":
		field, expected = "intent_type", "speak|observe|act"
	case intent.Visibility != "public" && intent.Visibility != "private":
		field, expected = "visibility", "public|private"
	case intent.WaitMinutes < 0 || intent.WaitMinutes > 120:
		field, expected = "wait_minutes", "integer:0..120"
	}
	if field != "" {
		return &GenerationError{Code: "json_field_value", Field: field, Expected: expected, Cause: ErrGenerationFailed}
	}
	return nil
}

func (g *ContextGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	req, report, err := g.composer.Build(g.material, request.System, request.MaxOutputTokens)
	if g.logger != nil {
		s := report.Scope
		g.logger.Printf("story context recall: world_id=%q run_id=%q purpose=%q recipient=%q included=%d excluded=%d", s.World, s.Run, s.Purpose, s.Recipient, report.RecallIncluded, report.RecallExcluded)
		g.logger.Printf("story context output budget: world_id=%q run_id=%q purpose=%q recipient=%q visible_tokens=%d reasoning_requested=%d reasoning_reserved=%d total_output_tokens=%d", s.World, s.Run, s.Purpose, s.Recipient, report.OutputTokens, report.ReasoningRequested, report.ReasoningReserved, report.TotalOutputTokens)
		g.logger.Printf("story context built: owner_id=%q game_id=%q world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d epoch=%d scene_version=%d template=%q policy_revision=%q sections=%q sources=%d excluded=%d duplicates=%d input_tokens=%d output_tokens=%d required_complete=%t window_known=%t success=%t failure=%q excluded_sources=%d selected_source_ids=%q", s.Owner, s.Game, s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, s.Epoch, s.SceneVersion, s.Template, s.PolicyRevision, strings.Join(report.Sections, ","), report.Sources, report.Excluded, report.Duplicates, report.InputTokens, report.OutputTokens, report.RequiredComplete, report.WindowKnown, err == nil, report.Failure, report.ExcludedSources, strings.Join(report.SelectedSources, ","))
	}
	if err != nil {
		return model.TextResponse{}, err
	}
	g.calls++
	scope := g.composer.Scope
	started := time.Now()
	if g.logger != nil {
		g.logger.Printf("story model call started: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls)
	}
	var response model.TextResponse
	var callErr error
	if g.metered != nil {
		response, callErr = g.metered(ctx, g.TextGenerator, req, scope, report)
	} else {
		response, callErr = g.TextGenerator.GenerateText(ctx, req)
	}
	diagnostic := response.Diagnostic
	var failure *model.TextCallError
	if errors.As(callErr, &failure) {
		diagnostic = failure.Diagnostic
	}
	if g.logger != nil {
		g.logger.Printf("story model call finished: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d elapsed_ms=%d success=%t error_code=%q http_status=%d provider_request_id=%q finish_reason=%q provider_input_tokens=%d provider_output_tokens=%d reasoning_tokens=%d content_chars=%d reasoning_chars=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls, time.Since(started).Milliseconds(), callErr == nil, model.TextErrorCode(callErr), diagnostic.HTTPStatus, model.SafeRequestID(diagnostic.RequestID), model.SafeFinishReason(diagnostic.FinishReason), diagnostic.InputTokens, diagnostic.OutputTokens, diagnostic.ReasoningTokens, diagnostic.ContentChars, diagnostic.ReasoningChars)
	}
	return response, callErr
}

type ContextGenerator struct {
	// metered is how a composed request is sent. It is nil when the generator is not
	// metered, in which case the call goes straight to the wrapped generator.
	//
	// It is the only thing this type ever needed from the application, and holding the
	// whole application for it is what kept the context assemblers from moving anywhere.
	metered       MeteredText
	TextGenerator model.TextGenerator
	composer      ContextComposer
	material      Material
	logger        Logger
	calls         int
}

// NewContextGenerator wraps a model generator so that every call this turn makes is
// composed against the material, budgeted against the model window and logged against
// one scope. owner is the account the world belongs to; it is part of the scope a
// composed request is recorded under.
func NewContextGenerator(deps Deps, owner string, generator model.TextGenerator, material Material, snapshot Snapshot, run wiaworld.Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	window := model.WindowLimits{}
	if provider, ok := generator.(model.WindowProvider); ok {
		window = provider.ModelWindow()
	}
	reasoning := 0
	if provider, ok := generator.(model.TextReasoningProvider); ok {
		reasoning = provider.TextReasoningReserve()
	}
	return &ContextGenerator{metered: deps.Meter, TextGenerator: generator, material: material, logger: deps.Logger, composer: ContextComposer{Scope: ContextScope{Owner: owner, Game: snapshot.Summary.GameID, World: snapshot.Summary.WorldID, Run: run.RunID, Attempt: run.Attempt, Stage: stage, Epoch: run.BaseContextEpoch, SceneVersion: snapshot.SceneVersion, Purpose: purpose, Recipient: recipient, Template: template, PolicyRevision: material.PolicyRevision}, Window: window, ReasoningReserve: reasoning}}
}

// Field and Expected are taken from the local schema, never response values.
func generatedDecodeError(err error, target any) error {
	result := &GenerationError{Code: "json_schema_invalid", Cause: err}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		result.Code = "json_field_type"
		result.Field = schemaField(reflect.TypeOf(target), strings.Split(mismatch.Field, "."))
		result.Expected = mismatch.Type.Kind().String()
		switch mismatch.Type.Kind() {
		case reflect.Slice, reflect.Array:
			result.Expected = "array"
		case reflect.Bool:
			result.Expected = "boolean"
		case reflect.Int, reflect.Int64:
			result.Expected = "integer"
		}
	} else if strings.HasPrefix(err.Error(), "json: unknown field ") {
		result.Code = "json_unknown_field"
	}
	return result
}

func schemaField(t reflect.Type, path []string) string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || len(path) == 0 {
		return ""
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || path[0] != name {
			continue
		}
		if len(path) == 1 {
			return name
		}
		if nested := schemaField(field.Type, path[1:]); nested != "" {
			return name + "." + nested
		}
	}
	return ""
}

func (g *ContextGenerator) recordJSONValidation(err error) {
	if g.logger == nil {
		return
	}
	s := g.composer.Scope
	var detail *GenerationError
	field, expected := "", ""
	if errors.As(err, &detail) {
		field, expected = detail.Field, detail.Expected
	}
	g.logger.Printf("story JSON validation: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d success=%t error_code=%q field=%q expected=%q", s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, g.calls, err == nil, ErrorCode(err), field, expected)
}

func GenerateJSON(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, requiredFields ...string) error {
	_, err := GenerateJSONMetrics(ctx, generator, system, input, target, maxOutput, requiredFields...)
	return err
}

func GenerateJSONWithNullableFields(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields []string, requiredFields ...string) error {
	_, err := GenerateJSONWithNullableFieldsMetrics(ctx, generator, system, input, target, maxOutput, nullableFields, requiredFields...)
	return err
}

func GenerateJSONMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, requiredFields ...string) (int, error) {
	return GenerateJSONWithNullableFieldsMetrics(ctx, generator, system, input, target, maxOutput, nil, requiredFields...)
}

func GenerateJSONWithNullableFieldsMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields []string, requiredFields ...string) (int, error) {
	return GenerateJSONCheckedMetrics(ctx, generator, system, input, target, maxOutput, nullableFields, requiredFields, nil)
}

func GenerateJSONCheckedMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields, requiredFields []string, check func() error) (int, error) {
	if t := reflect.TypeOf(target); t != nil && t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		system += "\n机器可读字段合同（对象只使用以下字段；string表示字符串，[]表示数组，boolean表示布尔值，integer表示整数）：" + generatedFieldContract(t)
	}
	var lastValidation error
	for attempt := 0; attempt < 2; attempt++ {
		requestSystem := system
		if attempt > 0 {
			requestSystem += "\n上一次响应不是可接受的完整 JSON。请重新生成，只输出满足字段要求的单个 JSON 对象。"
			var detail *GenerationError
			if errors.As(lastValidation, &detail) {
				requestSystem += "\n本地字段校验：" + detail.Error() + "。按本地字段类型生成；只使用输出合同列出的字段。"
			}
		}
		response, err := generator.GenerateText(ctx, model.TextRequest{System: requestSystem, Input: input, MaxInputTokens: 12000, MaxOutputTokens: maxOutput, MaxResponseBytes: 1 << 20})
		if err != nil {
			if attempt == 0 && errors.Is(err, model.ErrInvalidTextResponse) {
				continue
			}
			return attempt, err
		}
		lastValidation = DecodeGeneratedJSON(response.Text, target, nullableFields, requiredFields)
		if lastValidation == nil && check != nil {
			lastValidation = check()
		}
		if recorder, ok := generator.(interface{ recordJSONValidation(error) }); ok {
			recorder.recordJSONValidation(lastValidation)
		}
		if err := lastValidation; err != nil {
			if attempt == 0 {
				continue
			}
			return attempt, err
		}
		return attempt, nil
	}
	return 1, ErrGenerationFailed
}

func generatedFieldContract(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return generatedFieldContract(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fields = append(fields, fmt.Sprintf("%q:%s", name, generatedFieldContract(f.Type)))
		}
		return "{" + strings.Join(fields, ",") + "}"
	case reflect.Slice:
		return "[" + generatedFieldContract(t.Elem()) + "]"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	default:
		return "integer"
	}
}

// DecodeGeneratedJSON applies the local schema to one model answer: strict JSON first,
// then the required and nullable fields, then the typed decode with unknown fields
// refused. It is exported because a caller that already has a response — a fixture, or a
// pipeline that recorded one — needs the same rules without another model call.
func DecodeGeneratedJSON(text string, target any, nullableFields, requiredFields []string) error {
	if err := ValidateStrictJSON([]byte(text)); err != nil {
		return &GenerationError{Code: "json_syntax_invalid", Cause: err}
	}
	if len(requiredFields) > 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
			return &GenerationError{Code: "json_object_required", Cause: ErrGenerationFailed}
		}
		nullable := make(map[string]bool, len(nullableFields))
		for _, field := range nullableFields {
			nullable[field] = true
		}
		for _, field := range requiredFields {
			raw, ok := object[field]
			if !ok {
				return &GenerationError{Code: "json_required_field_missing", Field: field, Cause: ErrGenerationFailed}
			}
			if strings.EqualFold(strings.TrimSpace(string(raw)), "null") && !nullable[field] {
				return &GenerationError{Code: "json_required_field_null", Field: field, Cause: ErrGenerationFailed}
			}
		}
	}
	value := reflect.ValueOf(target)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value.Elem().Set(reflect.Zero(value.Elem().Type()))
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return generatedDecodeError(err, target)
	}
	if intent, ok := target.(*TurnIntent); ok {
		return validateTurnIntent(intent)
	}
	if validator, ok := target.(interface{ validateGeneratedFields() error }); ok {
		return validator.validateGeneratedFields()
	}
	return nil
}

// ValidateStrictJSON rejects duplicate object keys and trailing values before
// the typed decoder applies the business schema. A model response that merely
// parses is not automatically a valid story fact.
func ValidateStrictJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("json contains trailing data")
		}
		return err
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
				if err := walkJSON(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walkJSON(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
	}
	return nil
}

// ErrGenerationFailed marks a model answer this turn refused: the JSON was malformed,
// a required field was missing or wrong, or a local check rejected the result after the
// one repair attempt.
var ErrGenerationFailed = errors.New("generation failed")

// ErrContextSourceMissing marks stored provenance that cannot be resolved to its
// committed source. Scene and progression validation share this boundary.
var ErrContextSourceMissing = errors.New("context source is missing")

// GenerationError says which part of the generated JSON was rejected. A failure code and
// field are taken from the local schema, never from the response text.
type GenerationError struct {
	Code, Field, Expected string
	Cause                 error
}

func (e *GenerationError) Error() string {
	return fmt.Sprintf("generated JSON: code=%s field=%s expected=%s", e.Code, e.Field, e.Expected)
}
func (e *GenerationError) Unwrap() error        { return e.Cause }
func (e *GenerationError) Is(target error) bool { return target == ErrGenerationFailed }

// ErrorCode is the diagnostic code for a failure this turn can name. It is the turn's own
// vocabulary plus whatever the model layer reported: the application adds its own
// sentinels around it, because those belong to the application's error space.
func ErrorCode(err error) string {
	var validation *GenerationError
	if errors.As(err, &validation) {
		return validation.Code
	}
	code := model.TextErrorCode(err)
	if code != "unknown" {
		return code
	}
	switch {
	case errors.Is(err, ErrGenerationFailed):
		return "generated_content_invalid"
	case errors.Is(err, ErrContextCapacity):
		return "context_capacity"
	case errors.Is(err, ErrContextSourceMissing):
		return "context_source_missing"
	default:
		return "internal_error"
	}
}

// Logger is what a turn writes diagnostics through. It is a single method because that
// is all a turn does with it.
type Logger interface {
	Printf(string, ...any)
}

// MeteredText performs one composed model call with usage metering. It is a function
// rather than an interface because a turn needs exactly one capability here, and an
// interface for a single method would describe the application's shape instead of what a
// composed request requires.
type MeteredText func(ctx context.Context, generator model.TextGenerator, request model.TextRequest, scope ContextScope, report ContextBuildReport) (model.TextResponse, error)

// Deps is what a turn still needs from the application that hosts it, beyond the Host
// operations it asks for by name. Logger and Meter are optional: without a meter the call
// goes straight to the wrapped generator, and without a logger nothing is written. Owner
// is the account the world belongs to and is recorded in every composed scope.
type Deps struct {
	Logger Logger
	Meter  MeteredText
	Owner  string
}
