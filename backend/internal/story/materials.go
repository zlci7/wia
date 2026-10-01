package story

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Material is frozen author data. Availability and relevance are separate: a
// public document can still be outside a character's initial knowledge.
type Material struct {
	ID          string   `json:"id"`
	Purpose     string   `json:"purpose"`
	Visibility  string   `json:"visibility"`
	OwnerID     string   `json:"owner_id,omitempty"`
	Delivery    string   `json:"delivery"`
	Summary     string   `json:"summary"`
	LocationIDs []string `json:"location_ids,omitempty"`
	EntityIDs   []string `json:"entity_ids,omitempty"`
	ItemIDs     []string `json:"item_ids,omitempty"`
	KnownTo     []string `json:"known_to,omitempty"`
	Body        string   `json:"body"`
}

var materialID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

func MaterialSourceID(revision, id string) string { return "material:" + revision + ":" + id }

func (m Material) SourceID(revision string) string { return MaterialSourceID(revision, m.ID) }

func MaterialByID(def Definition, id string) (Material, bool) {
	for _, material := range def.Materials {
		if material.ID == id {
			return material, true
		}
	}
	return Material{}, false
}

func ValidateMaterials(def Definition) error {
	if len(def.Materials) > 128 {
		return fmt.Errorf("materials exceed 128 entries")
	}
	actors := map[string]bool{"player": true}
	for _, character := range def.Characters {
		actors[character.EntityID] = true
	}
	locations, items := map[string]bool{}, map[string]bool{}
	for _, location := range def.Locations {
		locations[location.ID] = true
	}
	for _, item := range def.InitialItems {
		items[item.InstanceID] = true
	}
	seen, total := map[string]bool{}, 0
	for _, material := range def.Materials {
		if !materialID.MatchString(material.ID) || seen[material.ID] {
			return fmt.Errorf("material identity is invalid or repeated")
		}
		seen[material.ID] = true
		if !slices.Contains([]string{"background", "world_rules", "author_facts", "development", "npc_profile", "npc_knowledge", "npc_plan", "location_lore", "item_lore"}, material.Purpose) {
			return fmt.Errorf("material %s has unsupported purpose", material.ID)
		}
		if !slices.Contains([]string{"public", "author", "owner"}, material.Visibility) || !slices.Contains([]string{"core", "on_demand"}, material.Delivery) {
			return fmt.Errorf("material %s has invalid visibility or delivery", material.ID)
		}
		if material.Visibility == "owner" {
			if !actors[material.OwnerID] {
				return fmt.Errorf("material %s has unknown owner", material.ID)
			}
		} else if material.OwnerID != "" {
			return fmt.Errorf("material %s declares owner outside owner visibility", material.ID)
		}
		if strings.HasPrefix(material.Purpose, "npc_") && (material.Visibility != "owner" || material.OwnerID == "player") {
			return fmt.Errorf("material %s requires NPC owner visibility", material.ID)
		}
		if (material.Purpose == "author_facts" || material.Purpose == "development") && material.Visibility != "author" {
			return fmt.Errorf("material %s requires author visibility", material.ID)
		}
		if !utf8.ValidString(material.Body) || strings.TrimSpace(material.Body) == "" || len(material.Body) > 64*1024 || !utf8.ValidString(material.Summary) || strings.TrimSpace(material.Summary) == "" || utf8.RuneCountInString(material.Summary) > 256 {
			return fmt.Errorf("material %s has invalid text or size", material.ID)
		}
		if material.Delivery == "core" && utf8.RuneCountInString(material.Body) > 800 {
			return fmt.Errorf("material %s core body exceeds 800 characters", material.ID)
		}
		total += len(material.Body)
		for field, refs := range map[string]struct {
			IDs   []string
			Known map[string]bool
		}{"location_ids": {material.LocationIDs, locations}, "entity_ids": {material.EntityIDs, actors}, "item_ids": {material.ItemIDs, items}, "known_to": {material.KnownTo, actors}} {
			unique := map[string]bool{}
			for _, id := range refs.IDs {
				if !refs.Known[id] || unique[id] {
					return fmt.Errorf("material %s has invalid %s", material.ID, field)
				}
				unique[id] = true
			}
		}
		if len(material.KnownTo) > 0 && material.Visibility != "public" {
			return fmt.Errorf("material %s known_to requires public visibility", material.ID)
		}
	}
	if total > 1024*1024 {
		return fmt.Errorf("material bodies exceed 1 MiB")
	}
	return nil
}
