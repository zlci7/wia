package memory

// This file holds the rules a memory record set obeys: how committed sources are
// grouped, searched, rendered and bounded. They are pure functions over the types in
// types.go and correction.go — no storage, no model, no application — which is why they
// can be tested without a world.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// DigestCoverageMatches verifies that a digest names exactly the continuous source
// prefix it claims to cover. Source identifiers are canonicalized by stream sequence;
// gaps, duplicate identifiers and records from another scope invalidate the claim.
func DigestCoverageMatches(d MemoryDigest, items []MemorySource) bool {
	if d.Through < 0 || d.Head < d.Through {
		return false
	}
	covered := make([]string, 0, len(items))
	seen := map[string]bool{}
	wantSeq := int64(1)
	for _, item := range items {
		if item.Seq > d.Through {
			break
		}
		if item.Scope != d.Scope || item.Seq != wantSeq || item.ID == "" || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
		covered = append(covered, item.ID)
		wantSeq++
	}
	if wantSeq != d.Through+1 {
		return false
	}
	return slices.Equal(covered, d.Sources)
}

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

const (
	memorySearchQueryChars     = 256
	memorySearchQueryTerms     = 32
	memorySearchScanCandidates = 512
	memorySearchBytes          = 32 << 20
	memorySearchTimeout        = 500 * time.Millisecond
)

type memorySearchCandidate struct {
	index int
	score int
}

type memorySearchBudget struct {
	deadline time.Time
	counted  map[int]bool
	bytes    int
	limited  bool
}

// MemoryGroupSearchResult contains complete committed groups only. Limited means the
// candidate window, byte budget, query budget or deadline ended before the archive did;
// an empty result under that condition is not proof that older memory has no match.
type MemoryGroupSearchResult struct {
	Groups  [][]MemorySource
	Limited bool
}

// SearchMemory receives one authorized scope, not the world's unprojected events.
// Work is bounded before matching: the query, records inspected, source bytes and
// elapsed time all have fixed ceilings. Archive streams are ordered by sequence, so
// scanning from the end keeps the newest eligible records when the archive grows past
// the scan budget.
func SearchMemory(items []MemorySource, query string, limit int) []MemorySource {
	if limit <= 0 {
		return nil
	}
	candidates, _ := searchMemoryCandidates(items, query, nil)
	result := []MemorySource{}
	for i := 0; i < min(limit, len(candidates)); i++ {
		result = append(result, items[candidates[i].index])
	}
	return result
}

// SearchMemoryGroups prioritizes exact personal references before keyword matches
// in the same bounded candidate scan. It filters groups that
// overlap records already supplied to the request, and expands only complete groups
// that still fit the same byte and time budget. It never builds text for a rejected
// group.
func SearchMemoryGroups(items []MemorySource, query string, references []string, excluded map[string]bool, limit int) MemoryGroupSearchResult {
	if limit <= 0 {
		return MemoryGroupSearchResult{}
	}
	candidates, budget := searchMemoryCandidates(items, query, references)
	result := MemoryGroupSearchResult{}
	visited := map[string]bool{}
	for _, candidate := range candidates {
		if len(result.Groups) >= limit {
			break
		}
		hit := items[candidate.index]
		if excluded[hit.ID] || visited[hit.ID] {
			continue
		}
		group, complete := memoryGroupWithinBudget(items, candidate.index, budget)
		if !complete {
			continue
		}
		overlap := false
		for _, record := range group {
			visited[record.ID] = true
			if excluded[record.ID] {
				overlap = true
			}
		}
		if overlap {
			continue
		}
		result.Groups = append(result.Groups, group)
	}
	result.Limited = budget.limited
	return result
}

func searchMemoryCandidates(items []MemorySource, query string, references []string) ([]memorySearchCandidate, *memorySearchBudget) {
	tokens, queryLimited := memorySearchTerms(query)
	budget := &memorySearchBudget{deadline: time.Now().Add(memorySearchTimeout), counted: map[int]bool{}, limited: queryLimited}
	exact := map[string]bool{}
	for index, id := range references {
		if index >= memorySearchQueryTerms {
			budget.limited = true
			break
		}
		if id == "" || exact[id] {
			continue
		}
		if len(id) > memorySearchQueryChars*4 {
			budget.limited = true
			continue
		}
		exact[id] = true
	}
	if len(tokens) == 0 && len(exact) == 0 {
		return nil, budget
	}
	var candidates []memorySearchCandidate
	scanned := 0
	for i := len(items) - 1; i >= 0 && scanned < memorySearchScanCandidates; i-- {
		if !budget.include(items, i) {
			break
		}
		scanned++
		score := 0
		text := strings.ToLower(items[i].Content)
		for _, token := range tokens {
			if strings.Contains(text, token) {
				score++
			}
		}
		if exact[items[i].ID] || items[i].EventID != "" && exact[items[i].EventID] {
			score = len(tokens) + 1
		}
		if score > 0 {
			candidates = append(candidates, memorySearchCandidate{index: i, score: score})
		}
	}
	if scanned < len(items) {
		budget.limited = true
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := items[candidates[i].index], items[candidates[j].index]
		if candidates[i].score == candidates[j].score {
			return left.Seq > right.Seq
		}
		return candidates[i].score > candidates[j].score
	})
	return candidates, budget
}

func memoryGroupWithinBudget(items []MemorySource, index int, budget *memorySearchBudget) ([]MemorySource, bool) {
	runID := items[index].RunID
	start := index
	for start > 0 && items[start-1].RunID == runID {
		if !budget.include(items, start-1) {
			return nil, false
		}
		start--
	}
	end := index + 1
	for end < len(items) && items[end].RunID == runID {
		if !budget.include(items, end) {
			return nil, false
		}
		end++
	}
	return items[start:end], true
}

func (budget *memorySearchBudget) include(items []MemorySource, index int) bool {
	if time.Now().After(budget.deadline) {
		budget.limited = true
		return false
	}
	if budget.counted[index] {
		return true
	}
	n := memorySourceBytes(items[index])
	if n > memorySearchBytes-budget.bytes {
		budget.limited = true
		return false
	}
	budget.bytes += n
	budget.counted[index] = true
	return true
}

// memorySearchTerms mirrors the bounded literal query vocabulary previously used by
// the history store: Latin/digit runs stay whole and adjacent Han characters form
// bigrams. The rune and distinct-term ceilings apply during tokenization.
func memorySearchTerms(text string) ([]string, bool) {
	var terms []string
	seen := map[string]bool{}
	limited := false
	add := func(term string) {
		if term != "" && !seen[term] && len(terms) < memorySearchQueryTerms {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	var latin strings.Builder
	var previous rune
	count := 0
	flush := func() {
		add(latin.String())
		latin.Reset()
	}
	for _, r := range text {
		if count >= memorySearchQueryChars || len(terms) >= memorySearchQueryTerms {
			limited = true
			break
		}
		count++
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			if previous != 0 {
				add(string([]rune{previous, r}))
			}
			previous = r
		case unicode.Is(unicode.Latin, r) || unicode.IsDigit(r):
			previous = 0
			latin.WriteRune(unicode.ToLower(r))
		default:
			previous = 0
			flush()
		}
	}
	flush()
	return terms, limited
}

func memorySourceBytes(source MemorySource) int {
	// The fixed reserve covers the labels, sequence digits and separators added by
	// MemoryRecordsText, so accepted groups cannot exceed the byte budget when rendered.
	return len(source.Scope) + len(source.ID) + len(source.EventID) + len(source.RunID) + len(source.Actor) + len(source.Kind) + len(source.Content) + len(source.CreatedAt) + 128
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
