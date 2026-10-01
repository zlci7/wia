package plot

import (
	"fmt"
	wiaworld "gameagent/backend/internal/world"
	"slices"
)

// DuePlanOwners selects one review per owner, ordered by game time and stable ID.
func DuePlanOwners(plans []wiaworld.PersonalPlan, minute, limit int) []string {
	ordered := slices.Clone(plans)
	slices.SortFunc(ordered, func(a, b wiaworld.PersonalPlan) int {
		if a.NextCheck < b.NextCheck {
			return -1
		}
		if a.NextCheck > b.NextCheck {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	var owners []string
	for _, plan := range ordered {
		if plan.Status != "active" || plan.NextCheck > minute || plan.LastCheck == minute || slices.Contains(owners, plan.OwnerID) {
			continue
		}
		owners = append(owners, plan.OwnerID)
		if len(owners) >= limit {
			break
		}
	}
	return owners
}

// OpenDefinition describes situations and intentions, rather than successful
// routes. The turn obtains each character's actual choice from that character.
type OpenDefinition struct {
	Developments      []Development      `json:"developments"`
	InitialPlans      []InitialPlan      `json:"initial_plans"`
	ExternalSchedules []ExternalSchedule `json:"external_schedules"`
}

type Development struct {
	ID          string   `json:"id"`
	MaterialIDs []string `json:"material_ids"`
	LocationIDs []string `json:"location_ids,omitempty"`
	EntityIDs   []string `json:"entity_ids,omitempty"`
}

type InitialPlan struct {
	ID                 string `json:"id"`
	OwnerID            string `json:"owner_id"`
	MaterialID         string `json:"material_id"`
	ReviewAfterMinutes int    `json:"review_after_minutes"`
}

type ExternalSchedule struct {
	ID         string `json:"id"`
	AtMinute   int    `json:"at_minute"`
	MaterialID string `json:"material_id"`
}

func ValidateOpenDefinition(def OpenDefinition) error {
	if len(def.Developments) > 32 || len(def.InitialPlans) > 32 || len(def.ExternalSchedules) > 32 {
		return fmt.Errorf("progression lists exceed 32 entries")
	}
	seen := map[string]bool{}
	check := func(id string) bool {
		if id == "" || len(id) > 80 || seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
	for _, development := range def.Developments {
		if !check(development.ID) || len(development.MaterialIDs) == 0 {
			return fmt.Errorf("invalid development identity or materials")
		}
	}
	for _, plan := range def.InitialPlans {
		if !check(plan.ID) || plan.OwnerID == "" || plan.MaterialID == "" || plan.ReviewAfterMinutes < 1 || plan.ReviewAfterMinutes > 1440*30 {
			return fmt.Errorf("invalid initial plan")
		}
	}
	for _, schedule := range def.ExternalSchedules {
		if !check(schedule.ID) || schedule.MaterialID == "" || schedule.AtMinute < 0 {
			return fmt.Errorf("invalid external schedule")
		}
	}
	return nil
}
