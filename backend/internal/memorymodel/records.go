package memorymodel

// This file holds the rules a memory record set obeys: how committed sources are
// grouped, searched, rendered and bounded. They are pure functions over the types in
// types.go and correction.go — no storage, no model, no application — which is why they
// can be tested without a world.

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// ProjectRecentExperience splits the unsummarized tail into the groups supplied to
// this request and the older backlog that stays recall-only. The digest watermark
// is never advanced here: supplying fewer groups must not claim they were summarized.
func ProjectRecentExperience(items []MemorySource) (block []MemorySource, backlog [][]MemorySource, supplied map[string]bool) {
	supplied = map[string]bool{}
	groups := MemoryGroups(items)
	if len(groups) == 0 {
		return nil, nil, supplied
	}
	start := len(groups) - TargetRecentGroups
	if start < 0 {
		start = 0
	}
	if len(groups)-start > 1 && len(MemoryRecordsText(FlattenGroups(groups[start:]))) > RecentWindowChars {
		for start < len(groups)-1 && len(MemoryRecordsText(FlattenGroups(groups[start:]))) > RecentWindowChars {
			start++
		}
	}
	block = FlattenGroups(groups[start:])
	for _, record := range block {
		supplied[record.ID] = true
	}
	backlog = groups[:start]
	return block, backlog, supplied
}

// FlattenGroups concatenates groups back into one ordered stream.
func FlattenGroups(groups [][]MemorySource) []MemorySource {
	out := []MemorySource{}
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// DigestContext renders a digest as the JSON a model reads: scope, the sequence it
// covers, its content and the states it still stands on.
func DigestContext(d MemoryDigest) string {
	return wire.MarshalJSON(struct {
		Scope   string            `json:"scope"`
		Through int64             `json:"through_seq"`
		Content string            `json:"content"`
		States  []SubjectiveState `json:"states"`
	}{d.Scope, d.Through, d.Content, d.States})
}

// RetainedStateSources lists the sources a digest's subjective states still stand on,
// without duplicates.
func RetainedStateSources(d MemoryDigest) []string {
	var ids []string
	for _, state := range d.States {
		for _, id := range state.Sources {
			if !wiaworld.ContainsID(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// Search receives one authorized scope, not the world's unprojected events.
func SearchMemory(items []MemorySource, query string, limit int) []MemorySource {
	tokens := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		rs := []rune(word)
		if len(rs) < 2 {
			continue
		}
		tokens[word] = true
		for i := 0; i+1 < len(rs); i++ {
			tokens[string(rs[i:i+2])] = true
		}
	}
	type ranked struct {
		s     MemorySource
		score int
	}
	var candidates []ranked
	for _, s := range items {
		score := 0
		text := strings.ToLower(s.Content)
		for token := range tokens {
			if strings.Contains(text, token) {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, ranked{s, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].s.Seq > candidates[j].s.Seq
		}
		return candidates[i].score > candidates[j].score
	})
	result := []MemorySource{}
	for i := 0; i < min(limit, len(candidates)); i++ {
		result = append(result, candidates[i].s)
	}
	return result
}

// MemoryGroups splits a committed record stream into the runs it came from: one group
// per run, in order, so a group is never split across the window boundary.
func MemoryGroups(items []MemorySource) [][]MemorySource {
	var groups [][]MemorySource
	for _, item := range items {
		if len(groups) == 0 || groups[len(groups)-1][0].RunID != item.RunID {
			groups = append(groups, []MemorySource{})
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], item)
	}
	return groups
}

// MemoryRecordsText renders records the way a model reads them: identifier, personal
// sequence, speaker, kind, source and time, then the content.
func MemoryRecordsText(items []MemorySource) string {
	var b strings.Builder
	for _, s := range items {
		fmt.Fprintf(&b, "[%s；个人序号=%d；说话者=%s；类型=%s；来源=%s；记录于=%s] %s\n", s.ID, s.Seq, s.Actor, s.Kind, s.EventID, s.CreatedAt, s.Content)
	}
	return b.String()
}

// Recent requests carry a bounded window of committed groups. The newest group
// stays required; older groups inside the window follow as optional material so the
// composer can drop them whole when the complete request does not fit.
const (
	TargetRecentGroups = 4
	RecentWindowChars  = 8000
)
