package context

import (
	"encoding/json"
	"fmt"
	"sort"

	"gameagent/backend/internal/tokenestimate"
)

type projectionBounds struct {
	maxTokens     int
	maxDepth      int
	maxFields     int
	maxArrayItems int
}

func projectionBoundsFromEngineConfig(config EngineConfig) projectionBounds {
	return projectionBounds{
		maxTokens:     positiveOrDefault(config.MaxToolResultOutputTokens, 8192),
		maxDepth:      positiveOrDefault(config.MaxToolResultOutputDepth, 4),
		maxFields:     positiveOrDefault(config.MaxToolResultOutputFields, 64),
		maxArrayItems: positiveOrDefault(config.MaxToolResultOutputArrayItems, 32),
	}
}

func positiveOrDefault(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func projectToolArguments(arguments map[string]any, bounds projectionBounds) map[string]any {
	return projectBoundedMap(arguments, bounds)
}

func projectToolResultOutput(output map[string]any, bounds projectionBounds) map[string]any {
	return projectBoundedMap(output, bounds)
}

func projectBoundedMap(values map[string]any, bounds projectionBounds) map[string]any {
	if len(values) == 0 {
		return nil
	}

	projected, ok := projectOutputValue(values, 1, bounds).(map[string]any)
	if !ok || len(projected) == 0 {
		return nil
	}

	estimatedTokens, err := tokenestimate.EstimateStableJSON(orderedMap(projected))
	if err == nil && bounds.maxTokens > 0 && estimatedTokens > bounds.maxTokens {
		return boundedTruncationMarker(bounds)
	}
	return projected
}

func boundedTruncationMarker(bounds projectionBounds) map[string]any {
	marker := map[string]any{"_truncated": true}
	estimatedTokens, err := tokenestimate.EstimateStableJSON(marker)
	if err == nil && bounds.maxTokens > 0 && estimatedTokens > bounds.maxTokens {
		return nil
	}
	return marker
}

func projectOutputValue(value any, depth int, bounds projectionBounds) any {
	switch typed := value.(type) {
	case map[string]any:
		if bounds.maxDepth > 0 && depth > bounds.maxDepth {
			return "_truncated: max depth exceeded"
		}
		return projectOutputMap(typed, depth, bounds)
	case []any:
		if bounds.maxDepth > 0 && depth > bounds.maxDepth {
			return "_truncated: max depth exceeded"
		}
		return projectOutputArray(typed, depth, bounds)
	case string, bool, nil:
		return typed
	case int:
		return typed
	case int32:
		return typed
	case int64:
		return typed
	case float32:
		return typed
	case float64:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func projectOutputMap(values map[string]any, depth int, bounds projectionBounds) map[string]any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	limit := len(keys)
	if bounds.maxFields > 0 && limit > bounds.maxFields {
		limit = bounds.maxFields
	}

	out := make(map[string]any, limit+1)
	for _, key := range keys[:limit] {
		out[key] = projectOutputValue(values[key], depth+1, bounds)
	}
	if limit < len(keys) {
		out["_truncated_fields"] = len(keys) - limit
	}
	return out
}

func projectOutputArray(values []any, depth int, bounds projectionBounds) []any {
	limit := len(values)
	if bounds.maxArrayItems > 0 && limit > bounds.maxArrayItems {
		limit = bounds.maxArrayItems
	}

	out := make([]any, 0, limit+1)
	for _, value := range values[:limit] {
		out = append(out, projectOutputValue(value, depth+1, bounds))
	}
	if limit < len(values) {
		out = append(out, fmt.Sprintf("_truncated_items:%d", len(values)-limit))
	}
	return out
}

func orderedMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[key] = values[key]
	}
	return out
}

func stableCompactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func mustMarshalJSONString(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}
