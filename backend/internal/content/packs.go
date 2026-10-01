package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

//go:embed packs
var packagedStories embed.FS

type StoryPack struct {
	SchemaVersion       int                         `json:"schema_version"`
	Requires            map[string]int              `json:"requires,omitempty"`
	GameID              string                      `json:"game_id"`
	Revision            string                      `json:"revision"`
	Mode                string                      `json:"mode"`
	Title               string                      `json:"title"`
	Description         string                      `json:"description"`
	Gameplay            string                      `json:"gameplay"`
	Background          string                      `json:"background"`
	Rules               string                      `json:"rules"`
	AuthorFacts         string                      `json:"author_facts"`
	Cover               string                      `json:"cover,omitempty"`
	CoverAlt            string                      `json:"cover_alt,omitempty"`
	Player              PlayerDefaults              `json:"player"`
	Opening             string                      `json:"opening"`
	InitialLocation     string                      `json:"initial_location"`
	Clock               string                      `json:"clock"`
	Locations           []PackLocation              `json:"locations"`
	NPCs                []string                    `json:"npcs"`
	Bystanders          []PackBystander             `json:"bystanders"`
	Plot                *plot.Definition            `json:"plot,omitempty"`
	EventGeneration     *plot.EventGenerationPolicy `json:"event_generation,omitempty"`
	Defaults            *wiaworld.NarrativeSettings `json:"defaults,omitempty"`
	StateDefinitions    []PackStateDefinition       `json:"state_definitions,omitempty"`
	RelationDefinitions []PackRelationDefinition    `json:"relation_definitions,omitempty"`
	InitialRelations    []PackInitialRelation       `json:"initial_relations,omitempty"`
	ItemDefinitions     []PackItemDefinition        `json:"item_definitions,omitempty"`
	ItemInstances       []PackItemInstance          `json:"item_instances,omitempty"`
	ActionRules         []story.ActionRule          `json:"action_rules,omitempty"`
	MaterialsFile       string                      `json:"materials_file,omitempty"`
	DataFiles           map[string]string           `json:"data_files,omitempty"`
}

type PackNPC struct {
	DefinitionID     string                     `json:"definition_id"`
	Revision         string                     `json:"revision"`
	EntityID         string                     `json:"entity_id"`
	Name             string                     `json:"name"`
	Role             string                     `json:"role"`
	Appearance       string                     `json:"appearance"`
	Profile          string                     `json:"profile"`
	Knowledge        string                     `json:"knowledge"`
	InitialConcerns  string                     `json:"initial_concerns"`
	InitialLocation  string                     `json:"initial_location"`
	Avatar           string                     `json:"avatar,omitempty"`
	SpeakingExamples []string                   `json:"speaking_examples,omitempty"`
	InitialState     map[string]json.RawMessage `json:"initial_state,omitempty"`
}

type PackStateDefinition struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Type         string                  `json:"type"`
	Minimum      *int                    `json:"minimum,omitempty"`
	Maximum      *int                    `json:"maximum,omitempty"`
	EnumValues   []string                `json:"enum_values,omitempty"`
	Default      json.RawMessage         `json:"default"`
	Scope        string                  `json:"scope"`
	Projection   string                  `json:"projection"`
	Knowledge    string                  `json:"knowledge"`
	UpdatePolicy story.StateUpdatePolicy `json:"update_policy"`
	Description  string                  `json:"description,omitempty"`
	Unit         string                  `json:"unit,omitempty"`
}

type PackRelationDefinition struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Minimum          int    `json:"minimum"`
	Maximum          int    `json:"maximum"`
	Default          int    `json:"default"`
	MaxChangePerTurn int    `json:"max_change_per_turn"`
	Projection       string `json:"projection"`
	Description      string `json:"description,omitempty"`
}

type PackInitialRelation struct {
	SubjectID    string `json:"subject_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Value        int    `json:"value"`
}

type PackItemDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Projection  string `json:"projection"`
}

type PackItemInstance struct {
	InstanceID   string `json:"instance_id"`
	DefinitionID string `json:"definition_id"`
	HolderID     string `json:"holder_id,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
}

// Catalog is the entry the content routes serve for this pack. It is wider than the
// running definition on purpose: modes, cover route and alternative text are things
// the catalog and the editor display, built from the pack itself so the definition a
// world runs from carries none of them.
type LoadedPack struct {
	Definition story.Definition
	// Catalog is the entry the content routes serve for this pack. It is wider than
	// the running definition on purpose: modes, the cover route and alternative text
	// are things the catalog and the editor display, and they are built from the pack
	// itself so the definition a world runs from carries none of them.
	Catalog   GameSummary
	Cover     []byte
	CoverType string
	Digest    string
	// LegacyDigest is the digest the previous algorithm produces for this same package.
	// It exists only so an older database's record is recognised during migration.
	LegacyDigest       string
	legacyStringDigest string
	// CoverRelative is the package-relative cover reference, kept so a world can
	// copy the image it started with.
	CoverRelative string
	// Root is the directory the package was read from; empty for in-memory packs.
	Root string
	// Story and NPCFiles keep the package's own JSON so an export can rebuild the
	// same content without re-serialising a reduced view.
	Story    StoryPack
	NPCFiles map[string][]byte
	// Assets maps package-relative asset paths to their bytes.
	Assets map[string][]byte
	// PackageFiles contains all explicitly referenced v4 data and text files.
	// Export and publication preserve these files rather than flattening them.
	PackageFiles map[string][]byte
}

type loadedPack = LoadedPack

const worldAssetLimit = 4 * 1024 * 1024

// Load reads and validates one story package directory.
func Load(root string) (LoadedPack, error) { return loadPack(root) }

// ReadAsset reads one validated package-relative asset from the package directory.
func (p LoadedPack) ReadAsset(relative string, maxSize int64) ([]byte, error) {
	return packFile(p.Root, relative, maxSize)
}

func storyLocations(items []PackLocation, schemaVersion int) []story.Location {
	out := make([]story.Location, 0, len(items))
	for _, item := range items {
		kind, public := item.Kind, false
		if schemaVersion >= SchemaV3 {
			public = true
			if item.Public != nil {
				public = *item.Public
			}
		}
		out = append(out, story.Location{ID: item.ID, Kind: kind, Parent: item.Parent, Name: item.Name, Description: item.Description, Connections: item.Connections, Public: public})
	}
	return out
}

func storyBystanders(items []PackBystander) []story.Bystander {
	out := make([]story.Bystander, 0, len(items))
	for _, item := range items {
		out = append(out, story.Bystander{BystanderID: item.BystanderID, Name: item.Name, Description: item.Description, InitialLocation: item.InitialLocation, Avatar: item.Avatar})
	}
	return out
}

func ValidateEventPolicy(p *plot.EventGenerationPolicy, def story.Definition) error {
	if p == nil {
		return nil
	}
	if def.Summary.Mode != "open" || wire.Clean(p.Scope) == "" || p.MaxActive < 1 || p.MaxActive > 3 || p.CooldownTurns < 2 || p.CooldownTurns > 20 || len(p.Locations) == 0 {
		return fmt.Errorf("story.json: invalid event_generation policy")
	}
	seen := map[string]bool{}
	for _, id := range p.Locations {
		if seen[id] || !slices.ContainsFunc(def.Locations, func(l story.Location) bool { return l.ID == id }) {
			return fmt.Errorf("story.json: invalid event_generation location")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, id := range p.Participants {
		if _, ok := story.CharacterByID(def, id); !ok || seen[id] {
			return fmt.Errorf("story.json: invalid event_generation participant")
		}
		seen[id] = true
	}
	return nil
}

type PackIssue struct {
	File    string `json:"file"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type packValidationError struct {
	File     string
	Field    string
	Code     string
	Expected string
}

func (e *packValidationError) Error() string {
	location := e.File
	if e.Field != "" {
		location += ": " + e.Field
	}
	return fmt.Sprintf("%s: %s: expected %s", location, e.Code, e.Expected)
}

func invalidPack(file, field, code, expected string) error {
	return &packValidationError{File: file, Field: field, Code: code, Expected: expected}
}

func invalidPackSchema(file string, err error) error {
	message, field, code := err.Error(), "", "schema_invalid"
	switch {
	case strings.Contains(message, "/requires"):
		field, code = "requires", "capability_manifest_unsupported"
	case strings.Contains(message, "/schema_version"):
		field, code = "schema_version", "schema_version_unsupported"
	}
	return invalidPack(file, field, code, message)
}

func packIssue(directory string, err error) PackIssue {
	issue := PackIssue{File: directory, Code: "pack_invalid", Message: err.Error()}
	var validation *packValidationError
	if errors.As(err, &validation) {
		issue.File = filepath.ToSlash(filepath.Join(directory, validation.File))
		issue.Field = validation.Field
		issue.Code = validation.Code
		issue.Message = "expected " + validation.Expected
	}
	return issue
}

var packID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var entityID = regexp.MustCompile(`^npc:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

func strictPackJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON text must be UTF-8")
	}
	// Reject duplicate keys as well as unknown execution options.
	if err := validatePackJSONKeys(data); err != nil {
		return err
	}
	_, npc := target.(*PackNPC)
	if err := validatePackSchema(data, npc); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("invalid field or type: %w", err)
	}
	return nil
}

func validatePackJSONKeys(data []byte) error {
	if !json.Valid(data) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return errors.New("JSON nesting exceeds 32")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t {
		case nil:
			return errors.New("null is unsupported; omit optional fields")
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				k := key.(string)
				if seen[k] {
					return fmt.Errorf("duplicate field %s", k)
				}
				seen[k] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
		case json.Delim('['):
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	return visit(0)
}

// Pack references are relative files, with links resolved inside the pack root.
func packFile(root, relative string, maxSize int64) ([]byte, error) {
	if relative == "" || strings.Contains(relative, "\\") || strings.Contains(relative, ":") || !fs.ValidPath(relative) {
		return nil, errors.New("invalid relative path")
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("pack directory unavailable")
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(relative)))
	if err != nil {
		return nil, errors.New("referenced file unavailable")
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, errors.New("reference leaves pack directory")
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil, errors.New("file is not regular or exceeds size limit")
	}
	return os.ReadFile(target)
}

// The default lead used by packages and imports that do not name one.
const (
	defaultPlayerName    = "旅人"
	defaultPlayerProfile = "一个正在寻找答案的旅人。"
)

func loadPack(root string) (loadedPack, error) {
	var result loadedPack
	result.Root = root
	data, err := packFile(root, "story.json", 256*1024)
	if err != nil {
		return result, invalidPack("story.json", "", "file_unavailable", err.Error())
	}
	p := StoryPack{Player: PlayerDefaults{Name: defaultPlayerName, Profile: defaultPlayerProfile, Editable: true}}
	if err := strictPackJSON(data, &p); err != nil {
		return result, invalidPackSchema("story.json", err)
	}
	var materials []story.Material
	var progression *plot.OpenDefinition
	if p.SchemaVersion == SchemaV4 {
		result.PackageFiles = map[string][]byte{"story.json": data}
		materials, progression, err = loadV4Data(root, &p, result.PackageFiles)
		if err != nil {
			return result, err
		}
	}
	bad := func(field string) (loadedPack, error) {
		code, expected := "field_invalid", "valid value"
		switch {
		case field == "schema_version":
			code, expected = "schema_version_unsupported", "1, 2, 3, or 4"
		case strings.HasPrefix(field, "requires"):
			code, expected = "capability_manifest_unsupported", "supported schema v3 capability versions"
		case strings.Contains(field, "initial_location"), strings.Contains(field, "connections"), strings.Contains(field, "parent"), strings.Contains(field, "audience"):
			code, expected = "reference_invalid", "existing compatible identifier"
		}
		return result, invalidPack("story.json", field, code, expected)
	}
	if p.SchemaVersion != SchemaV1 && p.SchemaVersion != SchemaV2 && p.SchemaVersion != SchemaV3 && p.SchemaVersion != SchemaV4 {
		return bad("schema_version")
	}
	if p.SchemaVersion >= SchemaV3 {
		if p.Requires["spatial"] != 1 {
			return bad("requires.spatial")
		}
		for name, version := range p.Requires {
			if version != 1 || (name != "spatial" && name != "state" && name != "relations" && name != "items" && name != "rules" && !(p.SchemaVersion == SchemaV4 && name == "progression")) {
				return bad("requires." + name)
			}
		}
	} else if len(p.Requires) != 0 {
		return bad("requires (only schema v3 may declare capabilities)")
	}
	if !packID.MatchString(p.GameID) {
		return bad("game_id")
	}
	if !packID.MatchString(p.Revision) {
		return bad("revision")
	}
	if p.Mode != "open" && p.Mode != "guided" {
		return bad("mode")
	}
	for field, value := range map[string]string{"title": p.Title, "description": p.Description, "gameplay": p.Gameplay, "opening": p.Opening, "player.name": p.Player.Name} {
		if strings.TrimSpace(value) == "" || len([]rune(value)) > 8000 {
			return bad(field)
		}
	}
	if len([]rune(p.Title)) > 120 || len([]rune(p.Player.Name)) > 80 || len([]rune(p.Player.Profile)) > 2000 {
		return bad("title/player length")
	}
	if _, err := plot.ClockMinute(p.Clock); err != nil {
		return bad("clock")
	}
	if len(p.Locations) == 0 || len(p.Locations) > 32 {
		return bad("locations")
	}
	locations := map[string]PackLocation{}
	for _, loc := range p.Locations {
		if !packID.MatchString(loc.ID) || loc.Name == "" || locations[loc.ID].ID != "" {
			return bad("locations.id/name")
		}
		if p.SchemaVersion >= SchemaV3 && loc.Kind != "place" && loc.Kind != "region" {
			return bad("locations.kind")
		}
		if p.SchemaVersion < SchemaV3 && (loc.Kind != "" || loc.Parent != "" || loc.Public != nil) {
			return bad("locations v3 fields")
		}
		locations[loc.ID] = loc
	}
	if locations[p.InitialLocation].ID == "" || (p.SchemaVersion >= SchemaV3 && locations[p.InitialLocation].Kind != "place") {
		return bad("initial_location")
	}
	for _, loc := range p.Locations {
		if p.SchemaVersion >= SchemaV3 {
			if loc.Kind == "region" && (loc.Parent != "" || len(loc.Connections) != 0) {
				return bad("locations region")
			}
			if loc.Parent != "" {
				parent, ok := locations[loc.Parent]
				if !ok || parent.Kind != "region" || loc.Kind != "place" {
					return bad("locations.parent")
				}
			}
		}
		for _, id := range loc.Connections {
			if locations[id].ID == "" || (p.SchemaVersion >= SchemaV3 && (loc.Kind != "place" || locations[id].Kind != "place")) {
				return bad("locations.connections")
			}
		}
	}
	if len(p.NPCs) == 0 || len(p.NPCs) > 16 || len(p.Bystanders) > 40 {
		return bad("npcs/bystanders")
	}
	bystanders, err := NormalizePackBystanders(p.Bystanders, p.Revision, locations)
	if err != nil {
		return bad("bystanders")
	}
	if p.SchemaVersion >= SchemaV3 {
		for _, bystander := range bystanders {
			if bystander.InitialLocation == "" || locations[bystander.InitialLocation].Kind != "place" {
				return bad("bystanders.initial_location")
			}
		}
	}
	bystanderNames := make([]string, 0, len(bystanders))
	for _, bystander := range bystanders {
		bystanderNames = append(bystanderNames, bystander.Name)
	}
	settings := wiaworld.DefaultNarrativeSettings()
	if p.Defaults != nil {
		// Decode again onto defaults so omitted values inherit the application defaults.
		var raw struct {
			Defaults json.RawMessage `json:"defaults"`
		}
		_ = json.Unmarshal(data, &raw)
		if err := json.Unmarshal(raw.Defaults, &settings); err != nil {
			return bad("defaults")
		}
	}
	settings, err = wiaworld.ValidateNarrativeSettings(settings)
	if err != nil {
		return bad("defaults")
	}
	initialLocations := map[string]string{}
	if p.SchemaVersion >= SchemaV3 {
		initialLocations["player"] = p.InitialLocation
		for _, bystander := range bystanders {
			initialLocations[bystander.BystanderID] = bystander.InitialLocation
		}
	}
	def := story.Definition{SchemaVersion: p.SchemaVersion, Capabilities: p.Requires, Revision: p.Revision, Background: p.Background, Rules: p.Rules, Locations: storyLocations(p.Locations, p.SchemaVersion), InitialLocations: initialLocations, Settings: settings, SettingsSource: "application", Opening: p.Opening, Scene: locations[p.InitialLocation].Name, InitialLocation: p.InitialLocation, Clock: p.Clock, Secret: p.AuthorFacts, Plot: p.Plot, Bystanders: bystanderNames, BystanderRefs: storyBystanders(bystanders), Materials: materials, Progression: progression}
	result.CoverRelative = p.Cover
	if p.Defaults != nil {
		def.SettingsSource = "pack:" + p.Revision
	}
	// The running definition takes only what a turn reads. The catalog the API serves
	// is wider — cover URL, description, modes — and is built at the route from the
	// pack, so a route path never becomes part of a world's definition.
	def.Summary = story.Summary{
		ID: p.GameID, Revision: p.Revision, Title: p.Title,
		Mode: p.Mode, Gameplay: p.Gameplay, Description: p.Description,
		Player: story.Player{Name: p.Player.Name, Profile: p.Player.Profile, Editable: p.Player.Editable},
	}
	seen, definitions := map[string]bool{}, map[string]bool{}
	loadedNPCs := map[string]PackNPC{}
	npcBodies := make([]json.RawMessage, 0, len(p.NPCs))
	for _, file := range p.NPCs {
		if !strings.HasPrefix(file, "npcs/") || !strings.HasSuffix(file, ".json") {
			return bad("npcs path")
		}
		body, err := packFile(root, file, 64*1024)
		if err != nil {
			return result, invalidPack(file, "", "file_unavailable", err.Error())
		}
		var npc PackNPC
		if err := strictPackJSON(body, &npc); err != nil {
			return result, invalidPackSchema(file, err)
		}
		if p.SchemaVersion == SchemaV4 {
			if npc.Knowledge != "" || npc.InitialConcerns != "" {
				return result, invalidPack(file, "knowledge/initial_concerns", "field_invalid", "v4 indexed materials")
			}
			result.PackageFiles[file] = body
		}
		var normalized any
		_ = json.Unmarshal(body, &normalized)
		canonicalNPC, _ := json.Marshal(normalized)
		npcBodies = append(npcBodies, canonicalNPC)
		if !entityID.MatchString(npc.EntityID) || !packID.MatchString(npc.DefinitionID) || !packID.MatchString(npc.Revision) || seen[npc.EntityID] || definitions[npc.DefinitionID] || strings.TrimSpace(npc.Name) == "" || strings.TrimSpace(npc.Role) == "" || strings.TrimSpace(npc.Profile) == "" || locations[npc.InitialLocation].ID == "" || (p.SchemaVersion >= SchemaV3 && locations[npc.InitialLocation].Kind != "place") {
			return result, invalidPack(file, "identity/profile/initial_location", "field_invalid", "unique valid identity, profile, and existing place")
		}
		seen[npc.EntityID], definitions[npc.DefinitionID] = true, true
		loadedNPCs[npc.EntityID] = npc
		def.InitialLocations[npc.EntityID] = npc.InitialLocation
		// The character's authored dialogue samples belong to its definition, so anything
		// reading the loaded package sees them without a second lookup.
		def.Characters = append(def.Characters, wiaworld.Character{EntityID: npc.EntityID, DefinitionID: npc.DefinitionID, DefinitionRevision: npc.Revision, Name: npc.Name, Role: npc.Role, Appearance: npc.Appearance, Avatar: npc.Avatar, Profile: npc.Profile, Knowledge: npc.Knowledge, InitialConcerns: npc.InitialConcerns, SpeakingExamples: npc.SpeakingExamples, InScene: npc.InitialLocation == p.InitialLocation})
	}
	hasStateData := len(p.StateDefinitions) > 0 || len(p.Player.InitialState) > 0
	hasRulesData := len(p.ActionRules) > 0
	for _, definition := range p.StateDefinitions {
		hasRulesData = hasRulesData || definition.UpdatePolicy.Kind == "rule_only"
	}
	if p.Plot != nil {
		for _, node := range p.Plot.Nodes {
			hasRulesData = hasRulesData || len(node.Requirements) > 0
		}
	}
	for _, npc := range loadedNPCs {
		hasStateData = hasStateData || len(npc.InitialState) > 0
	}
	if (p.Requires["state"] == 1) != hasStateData || (p.Requires["relations"] == 1) != (len(p.RelationDefinitions) > 0) || (p.Requires["items"] == 1) != (len(p.ItemDefinitions) > 0 || len(p.ItemInstances) > 0) || (p.Requires["rules"] == 1) != hasRulesData {
		return bad("requires capability/data mismatch")
	}
	def.StateDefinitions, def.InitialStates, def.RelationDefinitions, def.InitialRelations, def.ItemDefinitions, def.InitialItems, err = compileMechanics(p, loadedNPCs, locations)
	if err != nil {
		return bad("state/relations/items: " + err.Error())
	}
	def.ActionRules, err = compileActionRules(p, def, seen)
	if err != nil {
		return bad("action_rules: " + err.Error())
	}
	if p.SchemaVersion == SchemaV4 {
		if err := validateV4Definition(def); err != nil {
			return result, invalidPack(p.MaterialsFile, "materials/progression", "reference_invalid", err.Error())
		}
	}
	if p.Plot != nil {
		if err := plot.ValidateDefinition(*p.Plot); err != nil {
			return bad("plot nodes/dependencies")
		}
		for _, node := range p.Plot.Nodes {
			for _, id := range node.Audience {
				if id != "player" && !seen[id] {
					return bad("plot audience")
				}
			}
			for _, condition := range node.Requirements {
				if err := validateFactReferences(condition, def, seen, false); err != nil {
					return bad("plot requirements: " + err.Error())
				}
			}
		}
	}
	if p.Cover != "" {
		if !strings.HasPrefix(p.Cover, "assets/") {
			return bad("cover")
		}
		result.Cover, err = packFile(root, p.Cover, 4*1024*1024)
		if err != nil {
			return result, fmt.Errorf("cover: %w", err)
		}
		config, format, e := image.DecodeConfig(bytes.NewReader(result.Cover))
		if e != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
			return bad("cover (PNG/JPEG, maximum 4096 pixels)")
		}
		result.CoverType = "image/" + format
	}
	if err := ValidateEventPolicy(p.EventGeneration, def); err != nil {
		return result, invalidPack("story.json", "event_generation", "field_invalid", err.Error())
	}
	def.EventGeneration = p.EventGeneration
	result.Definition = def
	catalogPlayer := p.Player
	catalogPlayer.InitialState = nil
	result.Catalog = GameSummary{
		ID: p.GameID, Title: p.Title, Description: p.Description, Revision: p.Revision,
		Mode: p.Mode, Modes: []string{p.Mode}, DefaultMode: p.Mode, Gameplay: p.Gameplay,
		Background: p.Background, Player: catalogPlayer, CoverAlt: p.CoverAlt,
	}
	if p.SchemaVersion == SchemaV4 {
		for _, material := range materials {
			if material.Purpose == "background" && material.Visibility == "public" && material.Delivery == "core" {
				result.Catalog.Background += material.Body + "\n"
			}
		}
	}
	if p.Cover != "" {
		result.Catalog.CoverURL = "/api/v1/games/" + p.GameID + "/cover?revision=" + p.Revision
	}
	result.Story = p
	result.NPCFiles = map[string][]byte{}
	for index, file := range p.NPCs {
		result.NPCFiles[file] = npcBodies[index]
	}
	result.Assets = map[string][]byte{}
	for _, asset := range referencedAssets(ContentDraftPayload{
		Cover:      p.Cover,
		NPCs:       draftNPCRefs(def.Characters),
		Bystanders: bystanders,
	}) {
		if body, err := packFile(root, asset, worldAssetLimit); err == nil {
			result.Assets[asset] = body
		}
	}
	// The identity covers the complete effective package: the story, every character
	// file and every referenced image. Two publications must never share a revision
	// identifier while differing in any byte a world can end up showing.
	assetNames := make([]string, 0, len(result.Assets))
	for name := range result.Assets {
		assetNames = append(assetNames, name)
	}
	sort.Strings(assetNames)
	assetList := make([]assetDigestEntry, 0, len(assetNames))
	for _, name := range assetNames {
		assetList = append(assetList, assetDigestEntry{Name: name, Body: result.Assets[name]})
	}
	result.Digest = packDigest(packDigestCurrent, p, npcBodies, result.Cover, assetList)
	if p.SchemaVersion == SchemaV4 {
		result.Digest = v4PackageDigest(result.PackageFiles, result.Assets)
		return result, nil
	}
	// Historical encodings identify unchanged content registered by older releases.
	result.LegacyDigest = packDigest(packDigestAssetsExcluded, p, npcBodies, result.Cover, assetList)
	result.legacyStringDigest = legacyStringPackDigest(data, npcBodies, result.Cover)
	return result, nil
}

// legacyStringPackDigest reproduces the persisted pre-v2 representation. Its
// field order and string-array bystanders are part of the historical hash format.
// Decode the source directly: normalized objects must not gain string-era identity.
func legacyStringPackDigest(data []byte, npcs []json.RawMessage, cover []byte) string {
	var historical struct {
		SchemaVersion   int                         `json:"schema_version"`
		GameID          string                      `json:"game_id"`
		Revision        string                      `json:"revision"`
		Mode            string                      `json:"mode"`
		Title           string                      `json:"title"`
		Description     string                      `json:"description"`
		Gameplay        string                      `json:"gameplay"`
		Background      string                      `json:"background"`
		Rules           string                      `json:"rules"`
		AuthorFacts     string                      `json:"author_facts"`
		Cover           string                      `json:"cover,omitempty"`
		CoverAlt        string                      `json:"cover_alt,omitempty"`
		Player          PlayerDefaults              `json:"player"`
		Opening         string                      `json:"opening"`
		InitialLocation string                      `json:"initial_location"`
		Clock           string                      `json:"clock"`
		Locations       []PackLocation              `json:"locations"`
		NPCs            []string                    `json:"npcs"`
		Bystanders      []string                    `json:"bystanders"`
		Plot            *plot.Definition            `json:"plot,omitempty"`
		EventGeneration *plot.EventGenerationPolicy `json:"event_generation,omitempty"`
		Defaults        *wiaworld.NarrativeSettings `json:"defaults,omitempty"`
	}
	historical.Player = PlayerDefaults{Name: defaultPlayerName, Profile: defaultPlayerProfile, Editable: true}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&historical); err != nil || historical.SchemaVersion != SchemaV1 {
		return ""
	}
	story, err := json.Marshal(historical)
	if err != nil {
		return ""
	}
	canonical, err := json.Marshal(struct {
		Story json.RawMessage
		NPCs  []json.RawMessage
		Cover []byte
	}{story, npcs, cover})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// The package digest has a version because its input changed once: the first algorithm
// covered the story, the character files and the cover, and the current one also covers
// every referenced image. A database written by the earlier release holds first-version
// digests, so both are computed and an old record is migrated rather than reported as a
// content change.
const (
	packDigestAssetsExcluded = 1
	packDigestCurrent        = 2
	// packDigestVersion marks the algorithm a freshly written digest belongs to.
	packDigestVersion = packDigestCurrent
)

type assetDigestEntry struct {
	Name string
	Body []byte
}

func packDigest(version int, story StoryPack, npcs []json.RawMessage, cover []byte, assets []assetDigestEntry) string {
	var canonical []byte
	if version < packDigestCurrent {
		canonical, _ = json.Marshal(struct {
			Story StoryPack
			NPCs  []json.RawMessage
			Cover []byte
		}{story, npcs, cover})
	} else {
		canonical, _ = json.Marshal(struct {
			Story  StoryPack
			NPCs   []json.RawMessage
			Cover  []byte
			Assets []assetDigestEntry
		}{story, npcs, cover, assets})
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// matchesLegacyDigest reports whether a stored digest was produced by the earlier
// algorithm for exactly this package.
func (p loadedPack) matchesLegacyDigest(stored string) bool {
	return stored != "" && (stored == p.LegacyDigest || stored == p.legacyStringDigest)
}

func (a *Service) loadPacks(ctx context.Context, path string) error {
	a.packsMu.Lock()
	a.packs = map[string]loadedPack{}
	a.packErrors = []PackIssue{}
	a.packsMu.Unlock()
	if path == "" {
		path = strings.TrimSpace(os.Getenv("WIA_STORY_PACKS"))
	}
	if path == "" {
		path = filepath.Join(a.root, "story-packs")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
			err = fs.WalkDir(packagedStories, "packs", func(name string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				rel := strings.TrimPrefix(name, "packs/")
				if name == "packs" {
					return nil
				}
				target := filepath.Join(path, filepath.FromSlash(rel))
				if entry.IsDir() {
					return os.MkdirAll(target, 0755)
				}
				data, e := packagedStories.ReadFile(name)
				if e != nil {
					return e
				}
				return os.WriteFile(target, data, 0644)
			})
			if err != nil {
				return err
			}
		}
	}
	a.packRoot = path
	entries, err := os.ReadDir(path)
	if err != nil {
		a.packErrors = append(a.packErrors, PackIssue{File: "story-packs", Code: "catalog_unavailable", Message: "剧本目录无法读取，已有存档仍可继续。"})
		return nil
	}
	candidates := map[string][]loadedPack{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pack, e := loadPack(filepath.Join(path, entry.Name()))
		if e != nil {
			a.packErrors = append(a.packErrors, packIssue(entry.Name(), e))
			continue
		}
		candidates[pack.Definition.Summary.ID] = append(candidates[pack.Definition.Summary.ID], pack)
	}
	for id, versions := range candidates {
		if len(versions) != 1 {
			a.packErrors = append(a.packErrors, PackIssue{File: id, Field: "game_id", Code: "game_id_duplicate", Message: "duplicate game_id"})
			continue
		}
		pack := versions[0]
		_, err = a.appDB.ExecContext(ctx, `INSERT OR IGNORE INTO pack_revisions(game_id,revision,digest,digest_version) VALUES(?,?,?,?)`, id, pack.Definition.Revision, pack.Digest, packDigestVersion)
		if err != nil {
			return err
		}
		var digest string
		var digestVersion int
		if err = a.appDB.QueryRowContext(ctx, `SELECT digest,digest_version FROM pack_revisions WHERE game_id=? AND revision=?`, id, pack.Definition.Revision).Scan(&digest, &digestVersion); err != nil {
			return err
		}
		if digest != pack.Digest {
			// A registered digest may come from an older digest algorithm. That is not a
			// content change, so it is accepted under a controlled migration instead of
			// making an unchanged story unplayable after an upgrade.
			if digestVersion != packDigestAssetsExcluded || !pack.matchesLegacyDigest(digest) {
				a.packsMu.Lock()
				a.packErrors = append(a.packErrors, PackIssue{File: id, Field: "revision", Code: "revision_content_changed", Message: "revision 内容已变化，请使用新的 revision"})
				a.packsMu.Unlock()
				continue
			}
			if _, err = a.appDB.ExecContext(ctx, `UPDATE pack_revisions SET digest=?,digest_version=? WHERE game_id=? AND revision=?`, pack.Digest, packDigestVersion, id, pack.Definition.Revision); err != nil {
				return err
			}
		}
		a.setPack(id, pack)
	}
	a.packsMu.Lock()
	sort.Slice(a.packErrors, func(i, j int) bool { return a.packErrors[i].File < a.packErrors[j].File })
	a.packsMu.Unlock()
	return nil
}

// setPack swaps one directory entry inside a short critical section; loading and
// validating a package always happens outside it.
func (a *Service) setPack(id string, pack loadedPack) {
	a.packsMu.Lock()
	defer a.packsMu.Unlock()
	if a.packs == nil {
		a.packs = map[string]loadedPack{}
	}
	a.packs[id] = pack
}

func (a *Service) pack(id string) (loadedPack, bool) {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	pack, ok := a.packs[id]
	return pack, ok
}

func (a *Service) packList() []loadedPack {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	out := make([]loadedPack, 0, len(a.packs))
	for _, pack := range a.packs {
		out = append(out, pack)
	}
	return out
}

func (a *Service) Games() []GameSummary {
	packs := a.packList()
	result := make([]GameSummary, 0, len(packs))
	for _, p := range packs {
		result = append(result, p.Catalog)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (a *Service) PackIssues() []PackIssue {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	return append([]PackIssue{}, a.packErrors...)
}
func (a *Service) Game(id string) (GameSummary, error) {
	p, ok := a.pack(id)
	if !ok {
		return GameSummary{}, ErrContentNotFound
	}
	return p.Catalog, nil
}
func (a *Service) GameCover(id, revision string) ([]byte, string, error) {
	p, ok := a.pack(id)
	if !ok || p.Definition.Revision != revision || len(p.Cover) == 0 {
		return nil, "", ErrContentNotFound
	}
	return p.Cover, p.CoverType, nil
}
