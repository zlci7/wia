package turn

import (
	"encoding/json"
	"slices"
	"strings"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
)

// Tables retain every value under one shared field list instead of repeating
// keys in required host material. They do not change stored or response JSON.
type contextTable struct {
	Columns []string `json:"columns"`
	Records [][]any  `json:"records"`
}

func newContextTable(columns ...string) contextTable {
	return contextTable{Columns: columns, Records: [][]any{}}
}

func (table *contextTable) add(values ...any) { table.Records = append(table.Records, values) }

func (table *contextTable) sortRows() {
	slices.SortFunc(table.Records, func(a, b []any) int {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return strings.Compare(string(left), string(right))
	})
}

func locationContext(locations []story.Location) string {
	table := newContextTable("id", "kind", "parent", "name", "description", "connections", "public")
	for _, location := range locations {
		table.add(location.ID, location.Kind, location.Parent, location.Name, location.Description, location.Connections, location.Public)
	}
	return wire.MarshalJSON(table)
}
