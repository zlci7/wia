package storyapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Field and Expected are taken from the local schema, never response values.
type generationJSONError struct {
	Code, Field, Expected string
	Cause                 error
}

func (e *generationJSONError) Error() string {
	return fmt.Sprintf("generated JSON: code=%s field=%s expected=%s", e.Code, e.Field, e.Expected)
}
func (e *generationJSONError) Unwrap() error        { return e.Cause }
func (e *generationJSONError) Is(target error) bool { return target == ErrGenerationFailed }

func generatedDecodeError(err error, target any) error {
	result := &generationJSONError{Code: "json_schema_invalid", Cause: err}
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

func (g *contextGenerator) recordJSONValidation(err error) {
	if g.logger == nil {
		return
	}
	s := g.composer.Scope
	var detail *generationJSONError
	field, expected := "", ""
	if errors.As(err, &detail) {
		field, expected = detail.Field, detail.Expected
	}
	g.logger.Printf("story JSON validation: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d success=%t error_code=%q field=%q expected=%q", s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, g.calls, err == nil, safeTurnErrorCode(err), field, expected)
}
