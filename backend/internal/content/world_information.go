package content

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
)

func compileWorldInformation(pack StoryPack, definition *story.Definition, locations map[string]PackLocation) error {
	hasInformation := pack.Calendar != nil || len(pack.Player.KnownLocations) > 0
	for _, state := range pack.StateDefinitions {
		hasInformation = hasInformation || state.Category != "" || state.Currency != nil
	}
	if (pack.Requires["world_info"] == 1) != hasInformation {
		return fmt.Errorf("world_info capability must match authored information")
	}
	if pack.Calendar == nil && plot.IsDateClock(pack.Clock) {
		return fmt.Errorf("absolute clock needs an authored calendar")
	}
	if pack.Calendar != nil {
		if pack.SchemaVersion != SchemaV4 || pack.Calendar.Kind != "gregorian" || !plot.IsDateClock(pack.Clock) || strings.TrimSpace(pack.Calendar.Era) != pack.Calendar.Era || utf8.RuneCountInString(pack.Calendar.Era) > 80 {
			return fmt.Errorf("unsupported calendar or invalid dated clock")
		}
		calendar := *pack.Calendar
		definition.Calendar = &calendar
	}
	known := map[string]bool{}
	for _, id := range pack.Player.KnownLocations {
		location, found := locations[id]
		if !found || known[id] || location.Public != nil && !*location.Public {
			return fmt.Errorf("initial known locations must be unique public locations")
		}
		known[id] = true
	}
	definition.KnownLocations = append([]string(nil), pack.Player.KnownLocations...)
	epoch, err := plot.ClockDayStart(pack.Clock)
	if err != nil || epoch == 0 {
		return err
	}
	shift := func(minute int) (int, error) {
		if minute < 0 || minute > math.MaxInt-epoch {
			return 0, fmt.Errorf("scheduled minute is outside supported range")
		}
		return minute + epoch, nil
	}
	if definition.Plot != nil {
		compiled := *definition.Plot
		compiled.Nodes = append([]plot.Node(nil), compiled.Nodes...)
		for i := range compiled.Nodes {
			compiled.Nodes[i].AtMinute, err = shift(compiled.Nodes[i].AtMinute)
			if err != nil {
				return err
			}
		}
		definition.Plot = &compiled
	}
	if definition.Progression != nil {
		compiled := *definition.Progression
		compiled.ExternalSchedules = append([]plot.ExternalSchedule(nil), compiled.ExternalSchedules...)
		for i := range compiled.ExternalSchedules {
			compiled.ExternalSchedules[i].AtMinute, err = shift(compiled.ExternalSchedules[i].AtMinute)
			if err != nil {
				return err
			}
		}
		definition.Progression = &compiled
	}
	return nil
}
