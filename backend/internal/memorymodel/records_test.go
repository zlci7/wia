package memorymodel

import "testing"

func TestDigestCoverageMatchesTheContinuousScopePrefix(t *testing.T) {
	items := []MemorySource{
		{Scope: "npc:a", Seq: 1, ID: "source:1"},
		{Scope: "npc:a", Seq: 2, ID: "source:2"},
		{Scope: "npc:a", Seq: 3, ID: "source:3"},
	}
	valid := MemoryDigest{Scope: "npc:a", Through: 2, Head: 3, Sources: []string{"source:1", "source:2"}}
	if !DigestCoverageMatches(valid, items) {
		t.Fatal("valid continuous coverage was rejected")
	}
	for name, change := range map[string]func(MemoryDigest, []MemorySource) (MemoryDigest, []MemorySource){
		"missing id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources = d.Sources[:1]
			return d, items
		},
		"extra id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources = append(d.Sources, "source:3")
			return d, items
		},
		"wrong order": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources[0], d.Sources[1] = d.Sources[1], d.Sources[0]
			return d, items
		},
		"sequence gap": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].Seq = 3
			return d, items
		},
		"foreign scope": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].Scope = "npc:b"
			return d, items
		},
		"duplicate id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].ID = items[0].ID
			d.Sources[1] = items[0].ID
			return d, items
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			d.Sources = append([]string{}, valid.Sources...)
			copyItems := append([]MemorySource{}, items...)
			d, copyItems = change(d, copyItems)
			if DigestCoverageMatches(d, copyItems) {
				t.Fatal("invalid coverage was accepted")
			}
		})
	}
}
