package story_test

import (
	"encoding/json"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
)

// TestBystanderKeepsItsWireNames pins the JSON names of the bystander shape.
//
// A world's frozen definition is stored as JSON and read back by later releases, and the
// API hands the same list to the client. The Go field names are not part of that
// contract and must not leak into it: a release that serialised BystanderID instead of
// bystander_id would write saves that an older release can no longer read, and would
// change what a client sees, without any test failing.
func TestBystanderKeepsItsWireNames(t *testing.T) {
	encoded, err := json.Marshal(story.Bystander{
		BystanderID:     "bystander:boatman",
		Name:            "船夫",
		Description:     "在栈桥等活",
		InitialLocation: "pier",
		Avatar:          "assets/boatman.png",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"bystander_id"`, `"name"`, `"description"`, `"initial_location"`, `"avatar"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("encoded bystander is missing %s: %s", want, encoded)
		}
	}
	for _, unwanted := range []string{"BystanderID", "InitialLocation", "Description"} {
		if strings.Contains(string(encoded), unwanted) {
			t.Errorf("encoded bystander leaks the Go field name %s: %s", unwanted, encoded)
		}
	}

	var decoded story.Bystander
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.BystanderID != "bystander:boatman" || decoded.InitialLocation != "pier" || decoded.Avatar != "assets/boatman.png" {
		t.Errorf("round trip lost identity: %+v", decoded)
	}
}

// TestFrozenDefinitionStillReadsOlderSaves is the compatibility check for a world that
// was created by an earlier release.
//
// Two things changed under a stored definition: the story identifier now lives in one
// field rather than two, and the definition is the runtime shape rather than the pack
// shape. Neither may make an existing save unreadable, so this decodes a snapshot
// written the old way — including a GameID that no longer exists and locations under
// the pack's field names — and requires that the world still knows which story it is.
func TestFrozenDefinitionStillReadsOlderSaves(t *testing.T) {
	// Written by an earlier release: Summary carried both ID and GameID, and the
	// location entries used the pack's own field names.
	const older = `{
		"Revision": "lantern-dusk.pack.v2",
		"Background": "旧渡口客栈的雨夜",
		"Locations": [{"ID": "inn", "Name": "客栈", "Description": "风雨中的落脚处", "Connections": ["pier"]}],
		"Summary": {
			"ID": "lantern-dusk",
			"GameID": "lantern-dusk",
			"Revision": "lantern-dusk.pack.v2",
			"Title": "暮灯镇的失踪信使",
			"Mode": "guided",
			"Player": {"Name": "旅人", "Profile": "外来者", "Editable": true}
		},
		"InitialLocation": "inn",
		"Clock": "第 1 日 19:00",
		"BystanderRefs": [{"bystander_id": "bystander:boatman", "name": "船夫", "initial_location": "pier"}],
		"Plot": {"revision": "lantern-dusk.plot.v2", "facts": "", "nodes": [{"id": "open", "after": [], "at_minute": 0, "condition": "c", "development": "d", "audience": ["player"]}]}
	}`

	var definition story.Definition
	if err := json.Unmarshal([]byte(older), &definition); err != nil {
		t.Fatalf("an older save no longer decodes: %v", err)
	}
	if definition.Summary.ID != "lantern-dusk" {
		t.Errorf("story identity was lost: %q", definition.Summary.ID)
	}
	if definition.Summary.Mode != "guided" || definition.Summary.Player.Name != "旅人" {
		t.Errorf("summary did not survive: %+v", definition.Summary)
	}
	if definition.Revision != "lantern-dusk.pack.v2" {
		t.Errorf("revision was lost: %q", definition.Revision)
	}
	if definition.Plot == nil || len(definition.Plot.Nodes) != 1 {
		t.Errorf("plot did not survive: %+v", definition.Plot)
	}
	for _, ref := range definition.BystanderRefs {
		if ref.InitialLocation != "pier" {
			t.Errorf("bystander location did not survive: %+v", ref)
		}
	}
}

// TestSummaryCarriesOneIdentifier states the rule the older save above depends on: a
// story is named once. A second field holding the same value is one more thing that can
// disagree with itself after a round trip.
func TestSummaryCarriesOneIdentifier(t *testing.T) {
	encoded, err := json.Marshal(story.Summary{ID: "harbor", Mode: "open"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "GameID") {
		t.Errorf("summary serialises a second identifier: %s", encoded)
	}
}

// TestDefinitionSurvivesARoundTrip checks that what a world writes is what it reads.
func TestDefinitionSurvivesARoundTrip(t *testing.T) {
	original := story.Definition{
		Revision: "harbor.v1",
		Summary:  story.Summary{ID: "harbor", Mode: "open", Title: "港口", Player: story.Player{Name: "旅人", Editable: true}},
		Locations: []story.Location{
			{ID: "pier", Name: "栈桥", Description: "潮湿的木板", Connections: []string{"inn"}},
		},
		BystanderRefs:   []story.Bystander{{BystanderID: "bystander:boatman", Name: "船夫", InitialLocation: "pier"}},
		InitialLocation: "pier",
		Clock:           "第 1 日 08:00",
		Plot:            &plot.Definition{Revision: "harbor.plot.v1", Nodes: []plot.Node{{ID: "open", Condition: "c", Development: "d", Audience: []string{"player"}}}},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded story.Definition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Summary.ID != original.Summary.ID || decoded.InitialLocation != original.InitialLocation || decoded.Clock != original.Clock {
		t.Errorf("round trip changed the definition: %+v", decoded)
	}
	if len(decoded.Locations) != 1 || len(decoded.Locations[0].Connections) != 1 || decoded.Locations[0].Connections[0] != "inn" {
		t.Errorf("locations did not round trip: %+v", decoded.Locations)
	}
	if len(decoded.BystanderRefs) != 1 || decoded.BystanderRefs[0].BystanderID != "bystander:boatman" {
		t.Errorf("bystanders did not round trip: %+v", decoded.BystanderRefs)
	}
}
