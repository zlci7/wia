package turn

// Dynamic JSON included in a model request is bounded before it becomes prompt text.
// Normal payloads retain json.Marshal's exact bytes; only values outside the shape or
// byte envelope are projected to a smaller representation.

import (
	"bytes"
	"encoding/json"
	"sort"
)

const (
	contextJSONMaxDepth      = 4
	contextJSONMaxFields     = 64
	contextJSONMaxArrayItems = 32
	contextJSONMaxBytes      = 1 << 20
)

func contextJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	if len(data) <= contextJSONMaxBytes && contextJSONShapeWithin(data) {
		return string(data)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "{}"
	}
	projected := projectContextJSON(decoded, 1)
	data, err = json.Marshal(projected)
	if err != nil {
		return "{}"
	}
	if len(data) > contextJSONMaxBytes {
		data, _ = json.Marshal(contextJSONByteFallback(decoded))
	}
	return string(data)
}

func contextJSONShapeWithin(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return contextJSONValueWithin(decoder, 1) == nil
}

func contextJSONValueWithin(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth > contextJSONMaxDepth {
		return errContextJSONShape
	}
	switch delim {
	case '{':
		fields := 0
		for decoder.More() {
			fields++
			if fields > contextJSONMaxFields {
				return errContextJSONShape
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := contextJSONValueWithin(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		items := 0
		for decoder.More() {
			items++
			if items > contextJSONMaxArrayItems {
				return errContextJSONShape
			}
			if err := contextJSONValueWithin(decoder, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}

var errContextJSONShape = &contextJSONShapeError{}

type contextJSONShapeError struct{}

func (*contextJSONShapeError) Error() string { return "context JSON shape exceeds limits" }

func projectContextJSON(value any, depth int) any {
	switch typed := value.(type) {
	case map[string]any:
		if depth > contextJSONMaxDepth {
			return "_truncated: max depth exceeded"
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		limit := min(len(keys), contextJSONMaxFields)
		out := make(map[string]any, limit+1)
		for _, key := range keys[:limit] {
			out[key] = projectContextJSON(typed[key], depth+1)
		}
		if limit < len(keys) {
			out["_truncated_fields"] = len(keys) - limit
		}
		return out
	case []any:
		if depth > contextJSONMaxDepth {
			return "_truncated: max depth exceeded"
		}
		limit := min(len(typed), contextJSONMaxArrayItems)
		out := make([]any, 0, limit+1)
		for _, item := range typed[:limit] {
			out = append(out, projectContextJSON(item, depth+1))
		}
		if limit < len(typed) {
			out = append(out, "_truncated_items")
		}
		return out
	default:
		return typed
	}
}

func contextJSONByteFallback(value any) any {
	switch value.(type) {
	case map[string]any:
		return map[string]any{"_truncated": "max bytes exceeded"}
	case []any:
		return []any{}
	default:
		return "_truncated: max bytes exceeded"
	}
}
