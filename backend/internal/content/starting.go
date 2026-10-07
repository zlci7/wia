package content

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/story"
)

func compileStartingOptions(pack StoryPack, npcs map[string]PackNPC, locations map[string]PackLocation) ([]story.StartingOption, error) {
	if len(pack.StartingOptions) == 0 {
		return nil, nil
	}
	if pack.SchemaVersion != SchemaV4 || len(pack.StartingOptions) > 8 {
		return nil, fmt.Errorf("v4 allows at most eight starting options")
	}
	seen := map[string]bool{}
	var result []story.StartingOption
	for _, option := range pack.StartingOptions {
		if !packID.MatchString(option.ID) || seen[option.ID] || strings.TrimSpace(option.Title) == "" || utf8.RuneCountInString(option.Title) > 80 || strings.TrimSpace(option.Description) == "" || utf8.RuneCountInString(option.Description) > 500 {
			return nil, fmt.Errorf("unique ID and bounded title/description required")
		}
		seen[option.ID] = true
		selected := pack
		if option.Player != nil {
			selected.Player = *option.Player
		}
		if option.Opening != "" {
			selected.Opening = option.Opening
		}
		if option.InitialLocation != "" {
			selected.InitialLocation = option.InitialLocation
		}
		if strings.TrimSpace(selected.Player.Name) == "" || utf8.RuneCountInString(selected.Player.Name) > 80 || strings.TrimSpace(selected.Player.Profile) == "" || utf8.RuneCountInString(selected.Player.Profile) > 2000 || strings.TrimSpace(selected.Opening) == "" || utf8.RuneCountInString(selected.Opening) > 8000 || locations[selected.InitialLocation].Kind != "place" {
			return nil, fmt.Errorf("%s: valid player, opening and place required", option.ID)
		}
		if option.Items != nil {
			selected.ItemInstances = nil
			for _, item := range pack.ItemInstances {
				if item.HolderID != "player" {
					selected.ItemInstances = append(selected.ItemInstances, item)
				}
			}
			for _, item := range *option.Items {
				if item.HolderID != "player" || item.LocationID != "" {
					return nil, fmt.Errorf("%s: starting inventory belongs to player", option.ID)
				}
				selected.ItemInstances = append(selected.ItemInstances, item)
			}
		}
		_, states, _, _, _, items, err := compileMechanics(selected, npcs, locations)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", option.ID, err)
		}
		var info story.Definition
		if err := compileWorldInformation(selected, &info, locations); err != nil {
			return nil, fmt.Errorf("%s: %w", option.ID, err)
		}
		result = append(result, story.StartingOption{ID: option.ID, Title: option.Title, Description: option.Description, Player: story.Player{Name: selected.Player.Name, Profile: selected.Player.Profile, Editable: selected.Player.Editable}, Opening: selected.Opening, InitialLocation: selected.InitialLocation, KnownLocations: info.KnownLocations, States: states["player"], Items: items})
	}
	return result, nil
}
