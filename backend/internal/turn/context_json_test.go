package turn

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestContextJSONPreservesNormalBytesAndBoundsShape(t *testing.T) {
	normal := struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}{Name: "旅店", Items: []string{"灯", "门"}}
	want, _ := json.Marshal(normal)
	if got := contextJSON(normal); got != string(want) {
		t.Fatalf("normal JSON changed\n got: %s\nwant: %s", got, want)
	}

	t.Run("array items", func(t *testing.T) {
		values := make([]int, contextJSONMaxArrayItems+1)
		var got []any
		if err := json.Unmarshal([]byte(contextJSON(values)), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != contextJSONMaxArrayItems+1 || got[len(got)-1] != "_truncated_items" {
			t.Fatalf("bounded array = %#v", got)
		}
	})

	t.Run("object fields", func(t *testing.T) {
		values := map[string]any{}
		for i := 0; i <= contextJSONMaxFields; i++ {
			values[fmt.Sprintf("field_%02d", i)] = i
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(contextJSON(values)), &got); err != nil {
			t.Fatal(err)
		}
		if got["_truncated_fields"] != float64(1) {
			t.Fatalf("field projection = %#v", got)
		}
	})

	t.Run("depth", func(t *testing.T) {
		value := map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": map[string]any{"e": true}}}}}
		if got := contextJSON(value); !strings.Contains(got, "_truncated: max depth exceeded") {
			t.Fatalf("deep JSON was not projected: %s", got)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		value := map[string]any{"text": strings.Repeat("x", contextJSONMaxBytes)}
		if got := contextJSON(value); got != `{"_truncated":"max bytes exceeded"}` {
			t.Fatalf("byte fallback = %s", got)
		}
	})
}
