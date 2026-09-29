package storyapp

import (
	"gameagent/backend/internal/content"
	"gameagent/backend/internal/story"
)

// storyLocations normalizes the pack's place entries into the running definition's
// shape. The two look alike today; they stay separate because a pack is a file format
// an author writes and this is a definition the engine reasons about, and because the
// format is expected to grow a hierarchy the runtime need not model the same way.
func storyLocations(items []content.PackLocation) []story.Location {
	out := make([]story.Location, 0, len(items))
	for _, item := range items {
		out = append(out, story.Location{ID: item.ID, Name: item.Name, Description: item.Description, Connections: item.Connections})
	}
	return out
}

// storyBystanders normalizes the pack's bystander entries. The pack type also knows
// the v1 display-string form and how to marshal the v2 object; that compatibility
// belongs to reading packs and stops at this boundary.
func storyBystanders(items []content.PackBystander) []story.Bystander {
	out := make([]story.Bystander, 0, len(items))
	for _, item := range items {
		out = append(out, story.Bystander{BystanderID: item.BystanderID, Name: item.Name, Description: item.Description, InitialLocation: item.InitialLocation, Avatar: item.Avatar})
	}
	return out
}
