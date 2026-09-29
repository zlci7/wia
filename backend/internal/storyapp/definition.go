package storyapp

import (
	"strings"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
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

func characterByID(def story.Definition, id string) (wiaworld.Character, bool) {
	for _, c := range def.Characters {
		if c.EntityID == strings.TrimSpace(id) {
			return c, true
		}
	}
	return wiaworld.Character{}, false
}

func sceneCharacters(characters []wiaworld.Character) []wiaworld.Character {
	result := make([]wiaworld.Character, 0, len(characters))
	for _, character := range characters {
		if character.InScene {
			result = append(result, character)
		}
	}
	return result
}

func findSceneCharacter(characters []wiaworld.Character, id string) (wiaworld.Character, bool) {
	for _, character := range characters {
		if character.InScene && character.EntityID == strings.TrimSpace(id) {
			return character, true
		}
	}
	return wiaworld.Character{}, false
}
