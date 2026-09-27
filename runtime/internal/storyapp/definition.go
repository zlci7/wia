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
	Clock            string
	Characters       []Character
	Bystanders       []string
	Secret           string
	Plot             *PlotDefinition
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

func cleanText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}
