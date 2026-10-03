package turn

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func decodeContextRecords(t *testing.T, raw json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var table struct {
		Columns []string            `json:"columns"`
		Records [][]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]json.RawMessage{}
	for _, record := range table.Records {
		if len(record) != len(table.Columns) {
			t.Fatal("context row width mismatch")
		}
		row := map[string]json.RawMessage{}
		for i, key := range table.Columns {
			row[key] = record[i]
		}
		rows = append(rows, row)
	}
	return rows
}

func decodeHostContext(t *testing.T, input string) map[string]json.RawMessage {
	t.Helper()
	index := strings.Index(input, `{"state_definitions":`)
	if index < 0 {
		t.Fatal("required host facts missing")
	}
	var value map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(input[index:])).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertHostItemContext(t *testing.T, input, id, holder, source string) {
	t.Helper()
	for _, row := range decodeContextRecords(t, decodeHostContext(t, input)["items"]) {
		if string(row["instance_id"]) == wire.MarshalJSON(id) {
			if string(row["holder_id"]) != wire.MarshalJSON(holder) || string(row["source_event_id"]) != wire.MarshalJSON(source) {
				t.Fatal("current item ownership or provenance changed", row)
			}
			return
		}
	}
	t.Fatal("current item missing", id)
}

func TestHostContextTablesPreserveRuntimeFactsAndPermissions(t *testing.T) {
	min, max := 0, 100
	currency := &wiaworld.Currency{Name: "积分", Denominations: []wiaworld.Denomination{{Name: "积分", Units: 100}, {Name: "分", Units: 1}}}
	snapshot := Snapshot{
		Definition: story.Definition{
			StateDefinitions: []story.StateDefinition{
				{ID: "wallet", Name: "现金", Type: "integer", Minimum: &min, Maximum: &max, Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "model", MaxChangePerTurn: 10}, Description: "实际余额", Currency: currency},
				{ID: "skill", Name: "维修", Type: "boolean", Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "readonly"}, Description: "职业能力"},
				{ID: "secret", Name: "私密状态", Type: "enum", EnumValues: []string{"calm", "alert"}, Projection: "hidden", Knowledge: "host_only", UpdatePolicy: story.StateUpdatePolicy{Kind: "rule_only"}},
			},
			RelationDefinitions: []story.RelationDefinition{{ID: "trust", Name: "信任", Minimum: -50, Maximum: 50, Default: 5, MaxChangePerTurn: 3, Projection: "hidden", Description: "有向主观判断"}},
			ItemDefinitions:     []story.ItemDefinition{{ID: "tool", Name: "扳手", Description: "随身工具", Projection: "holder"}},
		},
		States:        map[string]map[string]wiaworld.EntityState{"player": {"wallet": {Value: wiaworld.StateValue{Type: "integer", Integer: 0}}, "skill": {Value: wiaworld.StateValue{Type: "boolean", Boolean: false}}, "secret": {Value: wiaworld.StateValue{Type: "enum", Enum: "alert"}}}},
		Relationships: []wiaworld.Relationship{{SubjectID: "npc:a", TargetID: "player", RelationType: "trust", Value: 5}, {SubjectID: "npc:b", TargetID: "player", RelationType: "trust", Value: 5, UpdatedTurn: 2, SourceEvent: "witness"}},
		Items:         map[string]wiaworld.ItemInstance{"wrench": {InstanceID: "wrench", DefinitionID: "tool", HolderID: "player", SourceEvent: "transfer"}, "floor": {InstanceID: "floor", DefinitionID: "tool", LocationID: "bay", SourceEvent: "opening"}},
	}
	text := HostMechanicsContext(snapshot)
	decoded := decodeHostContext(t, text)
	for i, row := range decodeContextRecords(t, decoded["state_definitions"]) {
		encoded, _ := json.Marshal(row)
		var actual story.StateDefinition
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		expected := snapshot.Definition.StateDefinitions[i]
		expected.Default, expected.Scope, expected.Category = wiaworld.StateValue{}, "", ""
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("state meaning or authorization changed", actual, expected)
		}
	}
	var values map[string]map[string]any
	if err := json.Unmarshal(decoded["states"], &values); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, map[string]map[string]any{"player": {"wallet": float64(0), "skill": false, "secret": "alert"}}) {
		t.Fatal("typed zero, false or enum lost", values)
	}
	for i, row := range decodeContextRecords(t, decoded["relation_definitions"]) {
		encoded, _ := json.Marshal(row)
		var actual story.RelationDefinition
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		if actual != snapshot.Definition.RelationDefinitions[i] {
			t.Fatal("relationship default or limits changed")
		}
	}
	relations := decodeContextRecords(t, decoded["relationships"])
	if len(relations) != 1 || string(relations[0]["source_event_id"]) != `"witness"` || string(relations[0]["value"]) != "5" {
		t.Fatal("changed default relation was omitted", relations)
	}
	for i, row := range decodeContextRecords(t, decoded["item_definitions"]) {
		encoded, _ := json.Marshal(row)
		var actual story.ItemDefinition
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		if actual != snapshot.Definition.ItemDefinitions[i] {
			t.Fatal("item definition or visibility changed")
		}
	}
	assertHostItemContext(t, text, "wrench", "player", "transfer")
	items := decodeContextRecords(t, decoded["items"])
	if len(items) != 2 || string(items[0]["location_id"]) != `"bay"` {
		t.Fatal("scene item placement lost", items)
	}
	if text != HostMechanicsContext(snapshot) {
		t.Fatal("context order is unstable")
	}
}

func TestLocationContextTablePreservesDirectedGraphAndPrivatePlaces(t *testing.T) {
	locations := []story.Location{{ID: "region", Kind: "region", Name: "地区", Description: "公开区域", Public: true}, {ID: "bay", Kind: "place", Parent: "region", Name: "维修舱", Description: "当前位置", Connections: []string{"hidden"}, Public: true}, {ID: "hidden", Kind: "place", Name: "隐藏舱室", Description: "作者秘密", Connections: []string{}, Public: false}}
	actual := []story.Location{}
	for _, row := range decodeContextRecords(t, json.RawMessage(locationContext(locations))) {
		encoded, _ := json.Marshal(row)
		var location story.Location
		if err := json.Unmarshal(encoded, &location); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, location)
	}
	if !reflect.DeepEqual(actual, locations) {
		t.Fatal("host location facts or directed connections changed", actual)
	}
}
