package storyapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
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
	"sort"
	"strings"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

//go:embed packs
var packagedStories embed.FS

type StoryPack struct {
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
	Player          content.PlayerDefaults      `json:"player"`
	Opening         string                      `json:"opening"`
	InitialLocation string                      `json:"initial_location"`
	Clock           string                      `json:"clock"`
	Locations       []content.PackLocation      `json:"locations"`
	NPCs            []string                    `json:"npcs"`
	Bystanders      []content.PackBystander     `json:"bystanders"`
	Plot            *plot.Definition            `json:"plot,omitempty"`
	EventGeneration *plot.EventGenerationPolicy `json:"event_generation,omitempty"`
	Defaults        *wiaworld.NarrativeSettings `json:"defaults,omitempty"`
}

type PackNPC struct {
	DefinitionID     string   `json:"definition_id"`
	Revision         string   `json:"revision"`
	EntityID         string   `json:"entity_id"`
	Name             string   `json:"name"`
	Role             string   `json:"role"`
	Appearance       string   `json:"appearance"`
	Profile          string   `json:"profile"`
	Knowledge        string   `json:"knowledge"`
	InitialConcerns  string   `json:"initial_concerns"`
	InitialLocation  string   `json:"initial_location"`
	Avatar           string   `json:"avatar,omitempty"`
	SpeakingExamples []string `json:"speaking_examples,omitempty"`
}

// Catalog is the entry the content routes serve for this pack. It is wider than the
// running definition on purpose: modes, cover route and alternative text are things
// the catalog and the editor display, built from the pack itself so the definition a
// world runs from carries none of them.
type loadedPack struct {
	Definition story.Definition
	// Catalog is the entry the content routes serve for this pack. It is wider than
	// the running definition on purpose: modes, the cover route and alternative text
	// are things the catalog and the editor display, and they are built from the pack
	// itself so the definition a world runs from carries none of them.
	Catalog   content.GameSummary
	Cover     []byte
	CoverType string
	Digest    string
	// LegacyDigest is the digest the previous algorithm produces for this same package.
	// It exists only so an older database's record is recognised during migration.
	LegacyDigest string
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
}

type PackIssue struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

var packID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var entityID = regexp.MustCompile(`^npc:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

func strictPackJSON(data []byte, target any) error {
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
		return result, fmt.Errorf("story.json: %w", err)
	}
	p := StoryPack{Player: content.PlayerDefaults{Name: defaultPlayerName, Profile: defaultPlayerProfile, Editable: true}}
	if err := strictPackJSON(data, &p); err != nil {
		return result, fmt.Errorf("story.json: %w", err)
	}
	bad := func(field string) (loadedPack, error) {
		return result, fmt.Errorf("story.json: invalid or missing %s", field)
	}
	if p.SchemaVersion != content.SchemaV1 && p.SchemaVersion != content.SchemaV2 {
		return bad("schema_version")
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
	locations := map[string]content.PackLocation{}
	for _, loc := range p.Locations {
		if !packID.MatchString(loc.ID) || loc.Name == "" || locations[loc.ID].ID != "" {
			return bad("locations.id/name")
		}
		locations[loc.ID] = loc
	}
	if locations[p.InitialLocation].ID == "" {
		return bad("initial_location")
	}
	for _, loc := range p.Locations {
		for _, id := range loc.Connections {
			if locations[id].ID == "" {
				return bad("locations.connections")
			}
		}
	}
	if len(p.NPCs) == 0 || len(p.NPCs) > 16 || len(p.Bystanders) > 40 {
		return bad("npcs/bystanders")
	}
	bystanders, err := normalizePackBystanders(p.Bystanders, p.Revision, locations)
	if err != nil {
		return bad("bystanders")
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
	def := story.Definition{Revision: p.Revision, Background: p.Background, Rules: p.Rules, Locations: storyLocations(p.Locations), InitialLocations: map[string]string{}, Settings: settings, SettingsSource: "application", Opening: p.Opening, Scene: locations[p.InitialLocation].Name, InitialLocation: p.InitialLocation, Clock: p.Clock, Secret: p.AuthorFacts, Plot: p.Plot, Bystanders: bystanderNames, BystanderRefs: storyBystanders(bystanders)}
	result.CoverRelative = p.Cover
	if p.Defaults != nil {
		def.SettingsSource = "pack:" + p.Revision
	}
	// The running definition takes only what a turn reads. The catalog the API serves
	// is wider — cover URL, description, modes — and is built at the route from the
	// pack, so a route path never becomes part of a world's definition.
	def.Summary = story.Summary{
		ID: p.GameID, GameID: p.GameID, Revision: p.Revision, Title: p.Title,
		Mode: p.Mode, Gameplay: p.Gameplay, Description: p.Description,
		Player: story.Player{Name: p.Player.Name, Profile: p.Player.Profile, Editable: p.Player.Editable},
	}
	seen, definitions := map[string]bool{}, map[string]bool{}
	npcBodies := make([]json.RawMessage, 0, len(p.NPCs))
	for _, file := range p.NPCs {
		if !strings.HasPrefix(file, "npcs/") || !strings.HasSuffix(file, ".json") {
			return bad("npcs path")
		}
		body, err := packFile(root, file, 64*1024)
		if err != nil {
			return result, fmt.Errorf("%s: %w", file, err)
		}
		var npc PackNPC
		if err := strictPackJSON(body, &npc); err != nil {
			return result, fmt.Errorf("%s: %w", file, err)
		}
		var normalized any
		_ = json.Unmarshal(body, &normalized)
		canonicalNPC, _ := json.Marshal(normalized)
		npcBodies = append(npcBodies, canonicalNPC)
		if !entityID.MatchString(npc.EntityID) || !packID.MatchString(npc.DefinitionID) || !packID.MatchString(npc.Revision) || seen[npc.EntityID] || definitions[npc.DefinitionID] || strings.TrimSpace(npc.Name) == "" || strings.TrimSpace(npc.Role) == "" || strings.TrimSpace(npc.Profile) == "" || locations[npc.InitialLocation].ID == "" {
			return result, fmt.Errorf("%s: invalid identity, profile or initial_location", file)
		}
		seen[npc.EntityID], definitions[npc.DefinitionID] = true, true
		def.InitialLocations[npc.EntityID] = npc.InitialLocation
		// The character's authored dialogue samples belong to its definition, so anything
		// reading the loaded package sees them without a second lookup.
		def.Characters = append(def.Characters, wiaworld.Character{EntityID: npc.EntityID, DefinitionID: npc.DefinitionID, DefinitionRevision: npc.Revision, Name: npc.Name, Role: npc.Role, Appearance: npc.Appearance, Avatar: npc.Avatar, Profile: npc.Profile, Knowledge: npc.Knowledge, InitialConcerns: npc.InitialConcerns, SpeakingExamples: npc.SpeakingExamples, InScene: npc.InitialLocation == p.InitialLocation})
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
	if err := validateEventPolicy(p.EventGeneration, def); err != nil {
		return result, err
	}
	def.EventGeneration = p.EventGeneration
	result.Definition = def
	result.Catalog = content.GameSummary{
		ID: p.GameID, Title: p.Title, Description: p.Description, Revision: p.Revision,
		Mode: p.Mode, Modes: []string{p.Mode}, DefaultMode: p.Mode, Gameplay: p.Gameplay,
		Background: p.Background, Player: p.Player, CoverAlt: p.CoverAlt,
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
	// The previous algorithm is kept so a database written by an earlier release is
	// still recognised instead of being reported as changed content.
	result.LegacyDigest = packDigest(packDigestAssetsExcluded, p, npcBodies, result.Cover, assetList)
	return result, nil
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
	return stored != "" && p.LegacyDigest != "" && stored == p.LegacyDigest
}

func (a *App) loadPacks(ctx context.Context, path string) error {
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
		a.packErrors = append(a.packErrors, PackIssue{"story-packs", "剧本目录无法读取，已有存档仍可继续。"})
		return nil
	}
	candidates := map[string][]loadedPack{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pack, e := loadPack(filepath.Join(path, entry.Name()))
		if e != nil {
			a.packErrors = append(a.packErrors, PackIssue{entry.Name(), e.Error()})
			continue
		}
		candidates[pack.Definition.Summary.ID] = append(candidates[pack.Definition.Summary.ID], pack)
	}
	for id, versions := range candidates {
		if len(versions) != 1 {
			a.packErrors = append(a.packErrors, PackIssue{id, "duplicate game_id"})
			continue
		}
		pack := versions[0]
		_, err = a.appDB.ExecContext(ctx, `INSERT OR IGNORE INTO pack_revisions(game_id,revision,digest) VALUES(?,?,?)`, id, pack.Definition.Revision, pack.Digest)
		if err != nil {
			return err
		}
		var digest string
		if err = a.appDB.QueryRowContext(ctx, `SELECT digest FROM pack_revisions WHERE game_id=? AND revision=?`, id, pack.Definition.Revision).Scan(&digest); err != nil {
			return err
		}
		if digest != pack.Digest {
			// A registered digest may come from an older digest algorithm. That is not a
			// content change, so it is accepted under a controlled migration instead of
			// making an unchanged story unplayable after an upgrade.
			if !pack.matchesLegacyDigest(digest) {
				a.packsMu.Lock()
				a.packErrors = append(a.packErrors, PackIssue{id, "revision 内容已变化，请使用新的 revision"})
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
func (a *App) setPack(id string, pack loadedPack) {
	a.packsMu.Lock()
	defer a.packsMu.Unlock()
	if a.packs == nil {
		a.packs = map[string]loadedPack{}
	}
	a.packs[id] = pack
}

// characterSpeakingExamples reads the samples a frozen definition recorded for one
// character, so the running turn uses the world's own version.
func characterSpeakingExamples(characters []wiaworld.Character, entityID string) ([]string, bool) {
	for _, character := range characters {
		if character.EntityID == entityID {
			if len(character.SpeakingExamples) == 0 {
				return nil, false
			}
			return character.SpeakingExamples, true
		}
	}
	return nil, false
}

func (a *App) pack(id string) (loadedPack, bool) {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	pack, ok := a.packs[id]
	return pack, ok
}

func (a *App) packList() []loadedPack {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	out := make([]loadedPack, 0, len(a.packs))
	for _, pack := range a.packs {
		out = append(out, pack)
	}
	return out
}

func (a *App) Games() []content.GameSummary {
	packs := a.packList()
	result := make([]content.GameSummary, 0, len(packs))
	for _, p := range packs {
		result = append(result, p.Catalog)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (a *App) PackIssues() []PackIssue {
	a.packsMu.RLock()
	defer a.packsMu.RUnlock()
	return append([]PackIssue{}, a.packErrors...)
}
func (a *App) Game(id string) (content.GameSummary, error) {
	p, ok := a.pack(id)
	if !ok {
		return content.GameSummary{}, ErrWorldNotFound
	}
	return p.Catalog, nil
}
func (a *App) GameCover(id, revision string) ([]byte, string, error) {
	p, ok := a.pack(id)
	if !ok || p.Definition.Revision != revision || len(p.Cover) == 0 {
		return nil, "", ErrWorldNotFound
	}
	return p.Cover, p.CoverType, nil
}

func snapshotDefinition(ctx context.Context, store *storage.WorldStore, s worldSnapshot) (story.Definition, error) {
	raw, err := store.MetaGet(ctx, "definition_snapshot")
	if err == nil {
		var d story.Definition
		if json.Unmarshal([]byte(raw), &d) != nil || d.Summary.ID != s.Summary.GameID || d.Revision == "" {
			return d, ErrStorageUnavailable
		}
		// Promotion removes a passer-by from the world, so the stored list is the
		// authority once it exists. Legacy worlds keep display names only until the
		// next world starts from the same content.
		if stored, e := store.MetaGet(ctx, "bystander_refs"); e == nil {
			var refs []story.Bystander
			if json.Unmarshal([]byte(stored), &refs) == nil {
				d.BystanderRefs = refs
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return d, e
		}
		return d, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return story.Definition{}, err
	}
	// Legacy worlds use only their persisted facts, never a newer installed pack.
	d := story.Definition{Summary: story.Summary{ID: s.Summary.GameID, GameID: s.Summary.GameID, Mode: s.Summary.Mode}, Characters: s.Characters, Bystanders: s.Bystanders, Clock: s.Summary.Clock, Plot: s.Plot, Settings: s.Narrative}
	d.Revision, _ = store.MetaGet(ctx, "game_revision")
	d.Summary.Revision = d.Revision
	if d.Summary.ID == GameID {
		d.Summary.Title = "暮灯镇的失踪信使"
	} else {
		d.Summary.Title = d.Summary.ID
	}
	return d, nil
}
