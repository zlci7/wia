package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// LoadSnapshot reads the persisted world state that one turn freezes before any model
// call. It is also used by application reads that expose the same committed snapshot.
func LoadSnapshot(ctx context.Context, store *storage.WorldStore, limit int) (Snapshot, error) {
	var out Snapshot
	get := func(key string) (string, error) { return store.MetaGet(ctx, key) }
	var err error
	out.Summary.GameID, err = get("game_id")
	if err != nil {
		return out, err
	}
	out.Summary.WorldID, err = get("world_id")
	if err != nil {
		return out, err
	}
	out.Summary.Mode, err = get("mode")
	if err != nil {
		return out, err
	}
	out.Summary.Scene, err = get("scene")
	if err != nil {
		return out, err
	}
	out.SceneLocation, err = get("scene_location")
	if errors.Is(err, sql.ErrNoRows) {
		// Worlds written before locations were recorded derive it from the definition.
		out.SceneLocation, err = "", nil
	} else if err != nil {
		return out, err
	}
	out.Summary.Clock, err = get("clock")
	if err != nil {
		return out, err
	}
	out.Summary.Status, err = get("status")
	if err != nil {
		return out, err
	}
	out.Summary.Name, err = get("name")
	if errors.Is(err, sql.ErrNoRows) {
		out.Summary.Name = "暮灯镇 · 新存档"
	} else if err != nil {
		return out, err
	}
	out.PlayerName, err = get("player_name")
	if err != nil {
		return out, err
	}
	out.PlayerProfile, err = get("player_profile")
	if err != nil {
		return out, err
	}
	out.Narrative, err = LoadNarrativeSettings(ctx, store)
	if err != nil {
		return out, err
	}
	out.Plot, out.PlotProgress, err = readPlot(ctx, store)
	if err != nil {
		return out, err
	}
	out.Summary.StoryEnded = out.Summary.Mode == "guided" && out.PlotProgress.Ending != ""
	for key, target := range map[string]*int64{"turn_seq": &out.Summary.TurnSeq, "message_head": &out.Summary.MessageHead, "event_head": &out.Summary.EventHead, "context_epoch": &out.Summary.ContextEpoch, "scene_version": &out.SceneVersion} {
		*target, err = store.MetaInt(ctx, key)
		if err != nil {
			return out, err
		}
	}
	if value, e := get("updated_at"); e == nil {
		out.Summary.UpdatedAt, _ = time.Parse(time.RFC3339Nano, value)
	}
	if value, e := get("bystanders"); e == nil {
		_ = json.Unmarshal([]byte(value), &out.Bystanders)
	}
	out.Characters, err = store.LoadCharacters(ctx)
	if err != nil {
		return out, err
	}
	out.Definition, err = snapshotDefinition(ctx, store, out)
	if err != nil {
		return out, err
	}
	out.Summary.GameTitle = out.Definition.Summary.Title
	out.OpenProgress, err = store.LoadOpenProgress(ctx)
	if err != nil {
		return out, err
	}
	if out.Definition.Progression != nil && out.OpenProgress == nil {
		return out, fmt.Errorf("open progression state is missing")
	}
	if err := validateOpenProgress(out.OpenProgress, out.Definition, out.Summary.ContextEpoch); err != nil {
		return out, err
	}
	out.Summary.Revision = out.Definition.Revision
	out.BystanderRefs = append([]story.Bystander(nil), out.Definition.BystanderRefs...)
	if err = validateCapabilityManifest(ctx, store, out.Definition); err != nil {
		return out, err
	}
	out.States, err = store.LoadEntityStates(ctx)
	if err != nil {
		return out, err
	}
	out.Relationships, err = store.LoadRelationships(ctx)
	if err != nil {
		return out, err
	}
	out.AppliedRelationshipSources = map[string]bool{}
	out.Items, err = store.LoadItems(ctx)
	if err != nil {
		return out, err
	}
	settledRules, err := store.LoadSettledActionResults(ctx)
	if err != nil {
		return out, err
	}
	out.RuleResults = map[string]map[string]string{}
	for _, result := range settledRules {
		if out.RuleResults[result.RuleID] == nil {
			out.RuleResults[result.RuleID] = map[string]string{}
		}
		out.RuleResults[result.RuleID][result.Status] = result.EventID
	}
	currentSources := map[string]bool{}
	for _, values := range out.States {
		for _, state := range values {
			if state.SourceEvent != "" {
				currentSources[state.SourceEvent] = true
			}
		}
	}
	for _, relation := range out.Relationships {
		if relation.SourceEvent != "" {
			currentSources[relation.SourceEvent] = true
		}
	}
	for _, item := range out.Items {
		if item.SourceEvent != "" {
			currentSources[item.SourceEvent] = true
		}
	}
	sourceIDs := make([]string, 0, len(currentSources))
	for id := range currentSources {
		sourceIDs = append(sourceIDs, id)
	}
	out.PerceivedSources, err = store.LoadPerceivedSources(ctx, sourceIDs)
	if err != nil {
		return out, err
	}
	if err = validateMechanicsSnapshot(out); err != nil {
		return out, err
	}
	out.Positions, out.PositionSources, err = store.LoadEntityPositions(ctx)
	if err != nil {
		return out, err
	}
	if out.Definition.Capabilities["spatial"] == 1 {
		if err = applySpatialProjection(&out); err != nil {
			return out, err
		}
	} else {
		out.Summary.SceneLocation = out.SceneLocation
	}
	out.Messages, err = store.LoadMessages(ctx, limit)
	if err != nil {
		return out, err
	}
	out.Events, err = store.LoadEvents(ctx, limit)
	if err != nil {
		return out, err
	}
	out.Dialogue, err = store.LoadDialogue(ctx)
	if err != nil {
		return out, err
	}
	out.Perceptions = make(map[string][]wiaworld.Perception)
	out.Memories = make(map[string][]wiaworld.Memory)
	out.Perceptions["player"], err = store.LoadPerceptions(ctx, "player", 20)
	if err != nil {
		return out, err
	}
	for _, c := range out.Characters {
		out.Perceptions[c.EntityID], err = store.LoadPerceptions(ctx, c.EntityID, 20)
		if err != nil {
			return out, err
		}
		out.Memories[c.EntityID], err = store.LoadMemories(ctx, c.EntityID, 20)
		if err != nil {
			return out, err
		}
	}
	raw, sceneErr := get("scene_views")
	if errors.Is(sceneErr, sql.ErrNoRows) {
		out.SceneViews = initialSceneViews(out)
	} else {
		if sceneErr != nil {
			return out, sceneErr
		}
		if err = json.Unmarshal([]byte(raw), &out.SceneViews); err != nil {
			return out, err
		}
		if err = validateSceneViews(out); err != nil {
			return out, err
		}
	}
	out.GeneratedEvents, err = readGeneratedEvents(ctx, store, out.Definition)
	if err != nil {
		return out, err
	}
	if err = applySnapshotCorrections(ctx, store, &out); err != nil {
		return out, err
	}
	return out, nil
}

func validateCapabilityManifest(ctx context.Context, store *storage.WorldStore, definition story.Definition) error {
	if len(definition.Capabilities) == 0 {
		return nil
	}
	if definition.Capabilities["spatial"] != 1 {
		return fmt.Errorf("%w: unsupported frozen capability manifest", memory.ErrStorageUnavailable)
	}
	for name, version := range definition.Capabilities {
		if version != 1 || (name != "spatial" && name != "state" && name != "relations" && name != "items" && name != "rules" && !(definition.SchemaVersion == 4 && name == "progression")) {
			return fmt.Errorf("%w: unsupported frozen capability manifest", memory.ErrStorageUnavailable)
		}
	}
	raw, err := store.MetaGet(ctx, "capability_manifest")
	if err != nil {
		return fmt.Errorf("%w: capability manifest is missing", memory.ErrStorageUnavailable)
	}
	var stored map[string]int
	if json.Unmarshal([]byte(raw), &stored) != nil || len(stored) != len(definition.Capabilities) {
		return fmt.Errorf("%w: capability manifest is invalid", memory.ErrStorageUnavailable)
	}
	for name, version := range definition.Capabilities {
		if stored[name] != version {
			return fmt.Errorf("%w: capability manifest does not match frozen definition", memory.ErrStorageUnavailable)
		}
	}
	return nil
}

func applySpatialProjection(snapshot *Snapshot) error {
	places := story.PlaceGraph(snapshot.Definition)
	expected := map[string]bool{"player": true}
	for _, character := range snapshot.Characters {
		expected[character.EntityID] = true
	}
	for _, bystander := range snapshot.Definition.BystanderRefs {
		if bystander.BystanderID != "" {
			expected[bystander.BystanderID] = true
		}
	}
	for entityID := range expected {
		locationID, ok := snapshot.Positions[entityID]
		if !ok {
			return fmt.Errorf("%w: spatial entity %s has no position", memory.ErrStorageUnavailable, entityID)
		}
		if _, ok := places[locationID]; !ok {
			return fmt.Errorf("%w: spatial entity %s has invalid position", memory.ErrStorageUnavailable, entityID)
		}
		if snapshot.PositionSources[entityID] == "" {
			return fmt.Errorf("%w: spatial entity %s has no position source", memory.ErrStorageUnavailable, entityID)
		}
	}
	for entityID := range snapshot.Positions {
		if !expected[entityID] || !wiaworld.ValidEntityRef(entityID) {
			return fmt.Errorf("%w: unknown spatial entity %s", memory.ErrStorageUnavailable, entityID)
		}
	}
	return refreshSpatialProjection(snapshot)
}

func refreshSpatialProjection(snapshot *Snapshot) error {
	places := story.PlaceGraph(snapshot.Definition)
	playerLocation := snapshot.Positions["player"]
	snapshot.SceneLocation = playerLocation
	snapshot.Summary.SceneLocation = playerLocation
	location, ok := story.LocationByID(snapshot.Definition, playerLocation)
	if !ok {
		return memory.ErrStorageUnavailable
	}
	snapshot.Summary.Location = &wiaworld.LocationView{ID: location.ID, Name: location.Name, Description: location.Description}
	snapshot.Summary.AdjacentLocations = nil
	for _, id := range places[playerLocation] {
		candidate, found := story.LocationByID(snapshot.Definition, id)
		if found && candidate.Public {
			snapshot.Summary.AdjacentLocations = append(snapshot.Summary.AdjacentLocations, wiaworld.LocationView{ID: candidate.ID, Name: candidate.Name, Description: candidate.Description})
		}
	}
	for i := range snapshot.Characters {
		snapshot.Characters[i].InScene = snapshot.Positions[snapshot.Characters[i].EntityID] == playerLocation
	}
	snapshot.Bystanders = snapshot.Bystanders[:0]
	snapshot.BystanderRefs = snapshot.BystanderRefs[:0]
	for _, bystander := range snapshot.Definition.BystanderRefs {
		if snapshot.Positions[bystander.BystanderID] == playerLocation {
			snapshot.Bystanders = append(snapshot.Bystanders, bystander.Name)
			snapshot.BystanderRefs = append(snapshot.BystanderRefs, bystander)
		}
	}
	return nil
}

// LoadNarrativeSettings reads and validates the settings stored with a world.
func LoadNarrativeSettings(ctx context.Context, store *storage.WorldStore) (wiaworld.NarrativeSettings, error) {
	settings := wiaworld.DefaultNarrativeSettings()
	for key, target := range map[string]*string{
		"narrative_perspective":        &settings.Perspective,
		"narrative_length":             &settings.Length,
		"narrative_detail":             &settings.Detail,
		"player_elaboration":           &settings.PlayerElaboration,
		"npc_initiative":               &settings.NPCInitiative,
		"narrative_custom_instruction": &settings.CustomInstruction,
	} {
		if value, err := store.MetaGet(ctx, key); err == nil {
			*target = value
		} else if !errors.Is(err, sql.ErrNoRows) {
			return wiaworld.NarrativeSettings{}, err
		}
	}
	if value, err := store.MetaGet(ctx, "behavior_policies"); err == nil {
		if err := json.Unmarshal([]byte(value), &settings.Policies); err != nil {
			return wiaworld.NarrativeSettings{}, fmt.Errorf("invalid stored behavior policies: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return wiaworld.NarrativeSettings{}, err
	}
	validated, err := wiaworld.ValidateNarrativeSettings(MigrateWritingPreference(settings))
	if err != nil {
		return wiaworld.NarrativeSettings{}, fmt.Errorf("invalid stored story settings: %w", err)
	}
	return validated, nil
}

// MigrateWritingPreference reads a settings record written before writing preferences
// became a behavior policy and folds the old free-text preference into narration.
func MigrateWritingPreference(settings wiaworld.NarrativeSettings) wiaworld.NarrativeSettings {
	if settings.CustomInstruction != "" && settings.Policies.Narration == "" {
		settings.Policies.Narration = PacingInstruction() + "\n写作偏好：" + settings.CustomInstruction
	}
	settings.CustomInstruction = ""
	return settings
}

func readPlot(ctx context.Context, store *storage.WorldStore) (*plot.Definition, plot.Progress, error) {
	var def plot.Definition
	var state plot.Progress
	raw, err := store.MetaGet(ctx, "plot_definition")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, state, nil
	}
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &def); err != nil {
		return nil, state, err
	}
	raw, err = store.MetaGet(ctx, "plot_progress")
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, state, err
	}
	if err = plot.Validate(def, state); err != nil {
		return nil, state, err
	}
	return &def, state, nil
}

func snapshotDefinition(ctx context.Context, store *storage.WorldStore, snapshot Snapshot) (story.Definition, error) {
	raw, err := store.MetaGet(ctx, "definition_snapshot")
	if err == nil {
		var definition story.Definition
		if json.Unmarshal([]byte(raw), &definition) != nil || definition.Summary.ID != snapshot.Summary.GameID || definition.Revision == "" {
			return definition, memory.ErrStorageUnavailable
		}
		if stored, readErr := store.MetaGet(ctx, "bystander_refs"); readErr == nil {
			var refs []story.Bystander
			if json.Unmarshal([]byte(stored), &refs) == nil {
				definition.BystanderRefs = refs
			}
		} else if !errors.Is(readErr, sql.ErrNoRows) {
			return definition, readErr
		}
		return definition, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return story.Definition{}, err
	}
	definition := story.Definition{Summary: story.Summary{ID: snapshot.Summary.GameID, Mode: snapshot.Summary.Mode}, Characters: snapshot.Characters, Bystanders: snapshot.Bystanders, Clock: snapshot.Summary.Clock, Plot: snapshot.Plot, Settings: snapshot.Narrative}
	definition.Revision, _ = store.MetaGet(ctx, "game_revision")
	definition.Summary.Revision = definition.Revision
	if definition.Summary.ID == "lantern-dusk" {
		definition.Summary.Title = "暮灯镇的失踪信使"
	} else {
		definition.Summary.Title = definition.Summary.ID
	}
	return definition, nil
}

func readGeneratedEvents(ctx context.Context, store *storage.WorldStore, def story.Definition) (GeneratedEventState, error) {
	s := GeneratedEventState{Active: []GeneratedEvent{}}
	raw, err := store.MetaGet(ctx, "generated_events")
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal([]byte(raw), &s) != nil || def.EventGeneration == nil || s.LastOfferTurn < 0 || s.Completed < 0 || len(s.Active) > def.EventGeneration.MaxActive {
		return s, memory.ErrStorageUnavailable
	}
	seen := map[string]bool{}
	for _, e := range s.Active {
		if seen[e.Node.ID] || e.StartID == "" || e.TriggerID == "" || e.Premise == "" || !slices.Contains(def.EventGeneration.Locations, e.Location) || e.Node.Terminal || len(e.Node.After) != 0 {
			return s, memory.ErrStorageUnavailable
		}
		seen[e.Node.ID] = true
		if err := plot.ValidateDefinition(plot.Definition{Revision: "generated.v1", Nodes: []plot.Node{e.Node}}); err != nil {
			return s, err
		}
		if e.State.Status != "" && e.State.Status != "deferred" {
			return s, memory.ErrStorageUnavailable
		}
		for _, id := range e.Node.Audience {
			if id != "player" && !slices.Contains(def.EventGeneration.Participants, id) {
				return s, memory.ErrStorageUnavailable
			}
		}
		for _, id := range []string{e.StartID, e.TriggerID} {
			exists, err := store.EventExists(ctx, id)
			if err != nil {
				return s, err
			}
			if !exists {
				return s, ErrContextSourceMissing
			}
		}
	}
	return s, nil
}

func loadSourceMetadata(ctx context.Context, db *sql.DB, snapshot Snapshot) (map[string]SourceMetadata, error) {
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, items := range snapshot.Perceptions {
		for _, p := range items {
			add(p.SourceEventID)
		}
	}
	for _, items := range snapshot.Memories {
		for _, m := range items {
			add(m.SourceEventID)
		}
	}
	for _, view := range snapshot.SceneViews {
		for _, id := range view.SourceIDs {
			add(id)
		}
	}
	for _, node := range snapshot.PlotProgress.Nodes {
		add(node.EventID)
		for _, id := range node.Evidence {
			if !strings.HasPrefix(id, "definition:") && !strings.HasPrefix(id, "fact:") {
				add(id)
			}
		}
	}
	result := map[string]SourceMetadata{}
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		rows, err := db.QueryContext(ctx, `SELECT event_id,actor_id,event_type,seq,run_id,stage,scene_version FROM events WHERE event_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m SourceMetadata
			if err := rows.Scan(&m.ID, &m.Actor, &m.Kind, &m.Seq, &m.RunID, &m.Stage, &m.SceneVersion); err != nil {
				rows.Close()
				return nil, err
			}
			result[m.ID] = m
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(result) != len(ids) {
		return nil, fmt.Errorf("%w: %d references unresolved", ErrContextSourceMissing, len(ids)-len(result))
	}
	return result, nil
}

// LoadInputSnapshot adds source provenance to the committed snapshot used by a turn.
func LoadInputSnapshot(ctx context.Context, store *storage.WorldStore, limit int) (Snapshot, error) {
	snapshot, err := LoadSnapshot(ctx, store, limit)
	if err != nil {
		return snapshot, err
	}
	snapshot.Sources, err = loadSourceMetadata(ctx, store.Database(), snapshot)
	if err != nil {
		return snapshot, err
	}
	sourceIDs := make([]string, 0, len(snapshot.Sources))
	for id := range snapshot.Sources {
		sourceIDs = append(sourceIDs, id)
	}
	snapshot.AppliedRelationshipSources, err = store.LoadAppliedRelationshipSources(ctx, sourceIDs)
	return snapshot, err
}

func applySnapshotCorrections(ctx context.Context, store *storage.WorldStore, s *Snapshot) error {
	list, err := memory.ReadCorrections(ctx, store)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Kind == "character" {
			for i := range s.Characters {
				ch := &s.Characters[i]
				if ch.EntityID != c.Scope {
					continue
				}
				switch c.TargetID {
				case "profile":
					ch.Profile = c.Replacement
				case "knowledge":
					ch.Knowledge = c.Replacement
				case "initial_concerns":
					ch.InitialConcerns = c.Replacement
				}
			}
		}
		if c.Kind == "event" {
			active := make([]GeneratedEvent, 0, len(s.GeneratedEvents.Active))
			for _, e := range s.GeneratedEvents.Active {
				if !c.Affects(e.StartID) && !c.Affects(e.TriggerID) {
					active = append(active, e)
				}
			}
			s.GeneratedEvents.Active = active
			for i := range s.Events {
				if s.Events[i].EventID == c.TargetID {
					s.Events[i].Content = c.Replacement
				} else if c.Affects(s.Events[i].EventID) {
					s.Events[i].Content = memory.ReplaceSource(memory.MemorySource{EventID: s.Events[i].EventID, Content: s.Events[i].Content}, []memory.Correction{c}, nil).Content
				}
			}
			for i := range s.Dialogue {
				if s.Dialogue[i].EventID == c.TargetID {
					s.Dialogue[i].Content = c.Replacement
				}
			}
			for i := range s.SceneViews {
				view := &s.SceneViews[i]
				affected := false
				for _, id := range view.SourceIDs {
					affected = affected || c.Affects(id)
				}
				if view.Version <= c.SceneVersion && affected {
					view.Content = "当前位置沿用已提交经历；关联情境已纠正，应以本人有效经历重新确认细节。"
				}
			}
			for id, n := range s.PlotProgress.Nodes {
				if n.EventID == c.TargetID {
					n.Content = c.Replacement
					s.PlotProgress.Nodes[id] = n
				}
			}
		}
	}
	for scope, items := range s.Perceptions {
		for i := range items {
			p := &items[i]
			id := "perception:" + strconv.FormatInt(p.Seq, 10)
			p.Content = memory.ReplaceSource(memory.MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil).Content
		}
		s.Perceptions[scope] = items
	}
	for scope, items := range s.Memories {
		for i := range items {
			p := &items[i]
			id := "memory:" + strconv.FormatInt(p.Seq, 10)
			p.Content = memory.ReplaceSource(memory.MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil).Content
		}
		s.Memories[scope] = items
	}
	return nil
}
