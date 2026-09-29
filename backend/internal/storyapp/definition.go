package storyapp

import "strings"

type gameDefinition struct {
	Revision         string
	Background       string
	Rules            string
	Locations        []PackLocation
	InitialLocations map[string]string
	Settings         NarrativeSettings
	SettingsSource   string
	Summary          GameSummary
	Opening          string
	Scene            string
	// InitialLocation is the identifier of the scene the story starts in.
	InitialLocation string
	Clock           string
	Characters      []Character
	Bystanders      []string
	// BystanderRefs carries the normalized v2 bystander definitions next to their
	// display names so identity survives the pack boundary.
	BystanderRefs   []PackBystander
	Secret          string
	Plot            *PlotDefinition
	EventGeneration *EventGenerationPolicy
}

func characterByID(def gameDefinition, id string) (Character, bool) {
	for _, c := range def.Characters {
		if c.EntityID == strings.TrimSpace(id) {
			return c, true
		}
	}
	return Character{}, false
}

func sceneCharacters(characters []Character) []Character {
	result := make([]Character, 0, len(characters))
	for _, character := range characters {
		if character.InScene {
			result = append(result, character)
		}
	}
	return result
}

func findSceneCharacter(characters []Character, id string) (Character, bool) {
	for _, character := range characters {
		if character.InScene && character.EntityID == strings.TrimSpace(id) {
			return character, true
		}
	}
	return Character{}, false
}
