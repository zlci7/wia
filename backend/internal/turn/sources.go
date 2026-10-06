package turn

import (
	"fmt"
	"slices"
	"strings"
)

// Personal record identities belong to one recipient. Dependencies use the
// events behind those records; frozen material identities remain unchanged.
func canonicalContextSources(snapshot Snapshot, recipient string, ids []string) []string {
	context := snapshot.LongMemory[recipient]
	aliases := map[string]string{}
	for _, record := range append(slices.Clone(context.Archive), context.Tail...) {
		if record.Scope == recipient {
			aliases[record.ID] = record.EventID
		}
	}
	var expanded []string
	for _, id := range ids {
		if context.Digest.Revision > 0 && id == fmt.Sprintf("digest:%s:%d", recipient, context.Digest.Revision) {
			expanded = append(expanded, context.Digest.Sources...)
		} else {
			expanded = append(expanded, id)
		}
	}
	var canonical []string
	for _, id := range expanded {
		if eventID, exists := aliases[id]; exists {
			id = eventID
		}
		if !slices.Contains(canonical, id) {
			canonical = append(canonical, id)
		}
	}
	return canonical
}

// Frozen definitions and derived fact labels are context references, not event
// rows. Their actual event evidence is supplied separately when available.
func eventBasisSources(ids []string) []string {
	var events []string
	for _, id := range ids {
		if id != "" && !strings.HasPrefix(id, "material:") && !strings.HasPrefix(id, "definition:") && !strings.HasPrefix(id, "fact:") && !slices.Contains(events, id) {
			events = append(events, id)
		}
	}
	return events
}

func decisionEventSources(snapshot Snapshot, recipient string, inputIDs, provided []string) []string {
	known := map[string]bool{}
	for id := range snapshot.Sources {
		known[id] = true
	}
	for _, id := range append(slices.Clone(inputIDs), SceneViewSources(snapshot, recipient)...) {
		known[id] = true
	}
	for _, p := range snapshot.Perceptions[recipient] {
		known[p.SourceEventID] = true
	}
	for _, record := range snapshot.LongMemory[recipient].Archive {
		if record.Scope == recipient && record.EventID != "" {
			known[record.EventID] = true
		}
	}
	var events []string
	for _, id := range eventBasisSources(canonicalContextSources(snapshot, recipient, provided)) {
		if known[id] {
			events = append(events, id)
		}
	}
	return events
}
