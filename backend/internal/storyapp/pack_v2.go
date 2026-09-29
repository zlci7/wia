package storyapp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gameagent/backend/internal/content"
)

// Pack loading normalizes both schema generations into the v2 shape, and everything
// downstream reads only that shape. The generations and the JSON representation
// belong to the content package.

var bystanderIDPattern = regexp.MustCompile(`^bystander:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

// normalizePackBystanders is the single compatibility entry: it validates the
// v1/v2 input, fills stable ids for legacy entries and rejects duplicates.
func normalizePackBystanders(items []content.PackBystander, revision string, locations map[string]content.PackLocation) ([]content.PackBystander, error) {
	if len(items) > 40 {
		return nil, errors.New("bystanders exceed 40")
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	out := make([]content.PackBystander, 0, len(items))
	for index, item := range items {
		entry := content.PackBystander{
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
