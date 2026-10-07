package turn

import (
	"encoding/json"
	"slices"
	"strings"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Cancel shares the acceptance gate: a cancellation acknowledged before acceptance
// preserves the previous state; an already accepted scene remains accepted.
func (s *CreationSession) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// PlayerSnapshot supplies accepted player prose and the current spatial projection.
// The application publishes its public fields, never the embedded author definition.
func (s *CreationSession) PlayerSnapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playerSnapshotLocked()
}

func (s *CreationSession) playerSnapshotLocked() Snapshot {
	snapshot := s.snapshot
	snapshot.Summary.TurnSeq = s.turn
	snapshot.Positions = clonePositions(snapshot.Positions)
	snapshot.States = cloneStates(snapshot.States)
	snapshot.Items = cloneItems(snapshot.Items)
	snapshot.Characters = slices.Clone(snapshot.Characters)
	snapshot.Messages = []wiaworld.Message{{MessageID: "opening", Kind: "narrative", Content: snapshot.Definition.Opening, Seq: 1}}
	for _, exchange := range s.exchanges {
		snapshot.Messages = append(snapshot.Messages,
			wiaworld.Message{MessageID: exchange.RunID + ":input", Kind: "player", Content: exchange.Input, Seq: int64(len(snapshot.Messages) + 1)},
			wiaworld.Message{MessageID: exchange.RunID + ":narrative", Kind: "narrative", Content: exchange.Narrative, Seq: int64(len(snapshot.Messages) + 2)},
		)
	}
	return snapshot
}

func (s *CreationSession) SuggestionContext() (Snapshot, Material) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.playerSnapshotLocked()
	if len(snapshot.Messages) > 9 {
		snapshot.Messages = slices.Clone(snapshot.Messages[len(snapshot.Messages)-8:])
	}
	material := ComposeSuggestions(snapshot)
	if snapshot.Summary.Location != nil {
		material.Required += "\n当前权威地点：" + snapshot.Summary.Location.Name
	}
	sources := s.sources["player"]
	var notes []string
	for i := len(sources) - 1; i >= 0 && len(notes) < 12; i-- {
		source := sources[i]
		if source.Kind != "input" && source.Kind != "narrative" {
			notes = append(notes, source.Kind+": "+source.Content)
		}
	}
	if len(notes) > 0 {
		material.Required += "\n玩家已知的重要记录：" + strings.Join(notes, "\n")
	}
	return snapshot, material
}

// ValidateSuggestions is shared by formal worlds and transient play sessions.
func ValidateSuggestions(items []string) error {
	if len(items) != 3 {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, item := range items {
		clean := wire.Clean(item)
		if clean == "" || len([]rune(clean)) > 120 || seen[clean] {
			return ErrInvalidRequest
		}
		seen[clean] = true
	}
	return nil
}

// NarrativePrefix extracts only the top-level narrative string from incomplete JSON.
// It never projects notes, author data or the raw response onto the reading surface.
func NarrativePrefix(text string) string {
	// Decode complete JSON members as they arrive; skip nested objects/arrays and
	// escaped quotes. The final field contract is still checked before acceptance.
	depth, quoted, escaped, start := 0, false, false, -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c != '"' {
				continue
			}
			quoted = false
			if depth == 1 && start >= 0 && text[start:i] == "narrative" {
				j := i + 1
				for j < len(text) && strings.ContainsRune(" \r\n\t", rune(text[j])) {
					j++
				}
				if j >= len(text) || text[j] != ':' {
					continue
				}
				j++
				for j < len(text) && strings.ContainsRune(" \r\n\t", rune(text[j])) {
					j++
				}
				if j < len(text) && text[j] == '"' {
					return decodeNarrativePrefix(text[j+1:])
				}
			}
			continue
		}
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case '"':
			quoted, start = true, i+1
		}
	}
	return ""
}

func decodeNarrativePrefix(value string) string {
	var result strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '"' {
			break
		}
		if value[i] == '\\' {
			// JSON escapes may cross transport chunks, including surrogate pairs.
			end := i + 2
			if end > len(value) {
				break
			}
			if value[i+1] == 'u' {
				end = i + 6
				if end > len(value) {
					break
				}
				if strings.HasPrefix(strings.ToLower(value[i+2:end]), "d8") || strings.HasPrefix(strings.ToLower(value[i+2:end]), "d9") || strings.HasPrefix(strings.ToLower(value[i+2:end]), "da") || strings.HasPrefix(strings.ToLower(value[i+2:end]), "db") {
					end += 6
					if end > len(value) {
						break
					}
				}
			}
			var decoded string
			if err := json.Unmarshal([]byte("\""+value[i:end]+"\""), &decoded); err != nil {
				break
			}
			result.WriteString(decoded)
			i = end
			continue
		}
		result.WriteByte(value[i])
		i++
	}
	return strings.ToValidUTF8(result.String(), "")
}
