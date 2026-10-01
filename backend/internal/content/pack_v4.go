package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
)

type packMaterial struct {
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
	File        string   `json:"file,omitempty"`
	Text        *string  `json:"text,omitempty"`
}

func decodePackData(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("text must be UTF-8")
	}
	if err := validatePackJSONKeys(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func loadV4Data(root string, p *StoryPack, files map[string][]byte) ([]story.Material, *plot.OpenDefinition, error) {
	readJSON := func(name string, target any) error {
		if !strings.HasSuffix(name, ".json") {
			return invalidPack(name, "", "file_invalid", "package-relative JSON file")
		}
		body, err := packFile(root, name, 256*1024)
		if err != nil {
			return invalidPack(name, "", "file_unavailable", err.Error())
		}
		if err := decodePackData(body, target); err != nil {
			return invalidPack(name, "", "field_invalid", err.Error())
		}
		files[name] = body
		return nil
	}
	var progression *plot.OpenDefinition
	for name, path := range p.DataFiles {
		var target any
		switch name {
		case "locations":
			target = &struct {
				Locations *[]PackLocation `json:"locations"`
			}{&p.Locations}
		case "state":
			target = &struct {
				States *[]PackStateDefinition `json:"state_definitions"`
			}{&p.StateDefinitions}
		case "relations":
			target = &struct {
				Definitions *[]PackRelationDefinition `json:"relation_definitions"`
				Initial     *[]PackInitialRelation    `json:"initial_relations"`
			}{&p.RelationDefinitions, &p.InitialRelations}
		case "items":
			target = &struct {
				Definitions *[]PackItemDefinition `json:"item_definitions"`
				Instances   *[]PackItemInstance   `json:"item_instances"`
			}{&p.ItemDefinitions, &p.ItemInstances}
		case "rules":
			target = &struct {
				Rules *[]story.ActionRule `json:"action_rules"`
			}{&p.ActionRules}
		case "bystanders":
			target = &struct {
				Bystanders *[]PackBystander `json:"bystanders"`
			}{&p.Bystanders}
		case "progression":
			progression = &plot.OpenDefinition{}
			target = progression
		default:
			return nil, nil, invalidPack("story.json", "data_files", "field_invalid", "known data module")
		}
		if err := readJSON(path, target); err != nil {
			return nil, nil, err
		}
		if name != "locations" && name != "bystanders" && p.Requires[name] != 1 {
			return nil, nil, invalidPack("story.json", "requires."+name, "capability_manifest_unsupported", "matching data capability")
		}
	}
	if p.DataFiles["locations"] == "" || (p.Requires["progression"] == 1) != (progression != nil) {
		return nil, nil, invalidPack("story.json", "data_files", "reference_invalid", "matching location and progression data")
	}
	for _, capability := range []string{"state", "relations", "items", "rules"} {
		if p.Requires[capability] == 1 && p.DataFiles[capability] == "" {
			return nil, nil, invalidPack("story.json", "data_files."+capability, "reference_invalid", "matching enabled data module")
		}
	}
	var index struct {
		Materials []packMaterial `json:"materials"`
	}
	if err := readJSON(p.MaterialsFile, &index); err != nil {
		return nil, nil, err
	}
	if len(index.Materials) == 0 || len(index.Materials) > 128 {
		return nil, nil, invalidPack(p.MaterialsFile, "materials", "field_invalid", "1 to 128 materials")
	}
	materials := make([]story.Material, 0, len(index.Materials))
	for _, entry := range index.Materials {
		if (entry.File != "") == (entry.Text != nil) {
			return nil, nil, invalidPack(p.MaterialsFile, entry.ID, "field_invalid", "one file or text source")
		}
		body := ""
		if entry.Text != nil {
			body = *entry.Text
		} else {
			if !strings.HasSuffix(entry.File, ".md") {
				return nil, nil, invalidPack(entry.File, "", "file_invalid", "UTF-8 Markdown file")
			}
			text, err := packFile(root, entry.File, 64*1024)
			if err != nil || !utf8.Valid(text) {
				return nil, nil, invalidPack(entry.File, "", "file_unavailable", "readable UTF-8 text within size limit")
			}
			files[entry.File] = text
			body = string(text)
		}
		materials = append(materials, story.Material{ID: entry.ID, Purpose: entry.Purpose, Visibility: entry.Visibility, OwnerID: entry.OwnerID, Delivery: entry.Delivery, Summary: entry.Summary, LocationIDs: entry.LocationIDs, EntityIDs: entry.EntityIDs, ItemIDs: entry.ItemIDs, KnownTo: entry.KnownTo, Body: body})
	}
	return materials, progression, nil
}

func validateV4Definition(def story.Definition) error {
	if err := story.ValidateMaterials(def); err != nil {
		return err
	}
	if def.Progression == nil {
		return nil
	}
	if err := plot.ValidateOpenDefinition(*def.Progression); err != nil {
		return err
	}
	for _, development := range def.Progression.Developments {
		if !packID.MatchString(development.ID) {
			return fmt.Errorf("invalid development id")
		}
		for _, id := range development.MaterialIDs {
			material, ok := story.MaterialByID(def, id)
			if !ok || material.Purpose != "development" {
				return fmt.Errorf("development %s has invalid material", development.ID)
			}
		}
		for _, id := range development.LocationIDs {
			if _, ok := story.LocationByID(def, id); !ok {
				return fmt.Errorf("development %s has unknown location", development.ID)
			}
		}
		for _, id := range development.EntityIDs {
			if id != "player" {
				if _, ok := story.CharacterByID(def, id); !ok {
					return fmt.Errorf("development %s has unknown entity", development.ID)
				}
			}
		}
	}
	for _, plan := range def.Progression.InitialPlans {
		material, ok := story.MaterialByID(def, plan.MaterialID)
		if !packID.MatchString(plan.ID) || !ok || material.Purpose != "npc_plan" || material.OwnerID != plan.OwnerID || len([]rune(material.Body)) > 1200 {
			return fmt.Errorf("plan %s has invalid owner or material", plan.ID)
		}
	}
	for _, schedule := range def.Progression.ExternalSchedules {
		material, ok := story.MaterialByID(def, schedule.MaterialID)
		if !packID.MatchString(schedule.ID) || !ok || material.Visibility != "author" {
			return fmt.Errorf("schedule %s has invalid material", schedule.ID)
		}
	}
	return nil
}

func v4PackageDigest(files, assets map[string][]byte) string {
	names := make([]string, 0, len(files)+len(assets))
	for name := range files {
		names = append(names, name)
	}
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]assetDigestEntry, 0, len(names))
	for _, name := range names {
		body, ok := files[name]
		if !ok {
			body = assets[name]
		}
		entries = append(entries, assetDigestEntry{Name: name, Body: body})
	}
	canonical, _ := json.Marshal(entries)
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func clonePackageFiles(files map[string][]byte) map[string][]byte {
	if len(files) == 0 {
		return nil
	}
	copy := make(map[string][]byte, len(files))
	for name, body := range files {
		copy[name] = append([]byte(nil), body...)
	}
	return copy
}

func exportPackageFiles(pack LoadedPack) map[string][]byte {
	files := clonePackageFiles(pack.PackageFiles)
	if files == nil {
		files = map[string][]byte{"story.json": mustJSON(pack.Story)}
		for name, body := range pack.NPCFiles {
			files[name] = body
		}
	}
	for name, body := range pack.Assets {
		files[name] = body
	}
	return files
}

func validateV4DraftFiles(files map[string][]byte) error {
	if len(files) == 0 || len(files) > importZipEntries || len(files["story.json"]) == 0 {
		return fmt.Errorf("%w: complete v4 file package required", ErrContentInvalid)
	}
	total := 0
	for name, body := range files {
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") || (!strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".md")) || !utf8.Valid(body) {
			return fmt.Errorf("%w: invalid v4 package file", ErrContentInvalid)
		}
		total += len(body)
	}
	if total > 6*1024*1024 {
		return fmt.Errorf("%w: v4 package files exceed size limit", ErrContentInvalid)
	}
	return nil
}

func (a *Service) buildV4Package(ctx context.Context, draft ContentDraft, project ContentProject) (string, map[string][]byte, string, error) {
	files := clonePackageFiles(draft.Payload.PackageFiles)
	var entry map[string]any
	if err := decodePackData(files["story.json"], &entry); err != nil {
		return "", nil, "", fmt.Errorf("%w: invalid v4 entry", ErrContentInvalid)
	}
	if version, ok := entry["schema_version"].(float64); !ok || version != SchemaV4 {
		return "", nil, "", fmt.Errorf("%w: expected v4 file entry", ErrContentInvalid)
	}
	entry["game_id"], entry["revision"] = project.GameID, ""
	body, err := json.Marshal(entry)
	if err != nil {
		return "", nil, "", err
	}
	files["story.json"] = body
	assets := map[string][]byte{}
	for _, name := range referencedAssets(draft.Payload) {
		body, err := a.draftAsset(ctx, draft.DraftID, name)
		if err != nil {
			return "", nil, "", err
		}
		assets[name] = body
	}
	revision := "r-" + wire.NowText()[:10] + "-" + v4PackageDigest(files, assets)[:8]
	entry["revision"] = revision
	files["story.json"], err = json.Marshal(entry)
	if err != nil {
		return "", nil, "", err
	}
	for name, body := range assets {
		files[name] = body
	}
	return revision, files, "", nil
}
