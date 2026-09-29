package storyapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// M3 pack schema v1 described bystanders as bare display names. v2 gives them a
// stable identity plus optional public description, home location and avatar, and
// adds optional avatars and speaking examples to important characters. Pack
// loading normalizes both inputs into the v2 shape, and everything downstream
// reads only that shape.
const (
	packSchemaV1 = 1
	packSchemaV2 = 2
)

// PackBystander is the normalized bystander definition.
type PackBystander struct {
	BystanderID     string `json:"bystander_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InitialLocation string `json:"initial_location,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
}

// UnmarshalJSON accepts both the v1 display string and the v2 object.
func (b *PackBystander) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty bystander")
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return err
		}
		*b = PackBystander{Name: name}
		return nil
	}
	type plain PackBystander
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*b = PackBystander(value)
	return nil
}

// MarshalJSON always writes the v2 object so exported and republished content has
// one representation.
func (b PackBystander) MarshalJSON() ([]byte, error) {
	type plain PackBystander
	return json.Marshal(plain(b))
}

var bystanderIDPattern = regexp.MustCompile(`^bystander:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

// normalizePackBystanders is the single compatibility entry: it validates the
// v1/v2 input, fills stable ids for legacy entries and rejects duplicates.
func normalizePackBystanders(items []PackBystander, revision string, locations map[string]PackLocation) ([]PackBystander, error) {
	if len(items) > 40 {
		return nil, errors.New("bystanders exceed 40")
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	out := make([]PackBystander, 0, len(items))
	for index, item := range items {
		entry := PackBystander{
			BystanderID:     strings.TrimSpace(item.BystanderID),
			Name:            strings.TrimSpace(item.Name),
			Description:     strings.TrimSpace(item.Description),
			InitialLocation: strings.TrimSpace(item.InitialLocation),
			Avatar:          strings.TrimSpace(item.Avatar),
		}
		if entry.Name == "" || len([]rune(entry.Name)) > 200 {
			return nil, fmt.Errorf("bystander %d name is invalid", index)
		}
		if entry.BystanderID == "" {
			entry.BystanderID = legacyBystanderID(revision, index, entry.Name)
		}
		if !bystanderIDPattern.MatchString(entry.BystanderID) {
			return nil, fmt.Errorf("bystander %d id is invalid", index)
		}
		if entry.InitialLocation != "" && locations[entry.InitialLocation].ID == "" {
			return nil, fmt.Errorf("bystander %d location is unknown", index)
		}
		if entry.Avatar != "" && !strings.HasPrefix(entry.Avatar, "assets/") {
			return nil, fmt.Errorf("bystander %d avatar must be a package asset", index)
		}
		if seenID[entry.BystanderID] || seenName[entry.Name] {
			return nil, fmt.Errorf("bystander %d is duplicated", index)
		}
		seenID[entry.BystanderID], seenName[entry.Name] = true, true
		out = append(out, entry)
	}
	return out, nil
}

// legacyBystanderID derives a stable id for a v1 display string from the
// definition revision, the array position and the normalized text.
func legacyBystanderID(revision string, index int, name string) string {
	clean := strings.ToLower(strings.Join(strings.Fields(name), "-"))
	var builder strings.Builder
	for _, r := range clean {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		case r > 127:
			builder.WriteString(fmt.Sprintf("%x", r))
		default:
			builder.WriteRune('-')
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "entry"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return fmt.Sprintf("bystander:%s-%d-%s", revision, index, slug)
}
