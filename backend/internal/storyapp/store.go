package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
	_ "modernc.org/sqlite"
)

// InputBudgetTokens is how many input tokens this world's requests may use; the long
// memory projection reads it so a small model window shrinks the recent window
// instead of failing the turn.

type worldStore struct {
	path string
	db   *sql.DB
}

// Columns added after a release are applied to existing databases here, so an
// author's workspace keeps working without a migration step.

// ensureColumn adds a column when an older database does not have it yet.

const appSchema = `
CREATE TABLE IF NOT EXISTS pack_revisions (game_id TEXT NOT NULL, revision TEXT NOT NULL, digest TEXT NOT NULL, digest_version INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(game_id,revision));
CREATE TABLE IF NOT EXISTS creation_operations (user_id TEXT NOT NULL, request_key TEXT NOT NULL, request_hash TEXT NOT NULL, world_id TEXT NOT NULL, PRIMARY KEY(user_id,request_key));
CREATE TABLE IF NOT EXISTS app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS worlds (
  user_id TEXT NOT NULL, game_id TEXT NOT NULL, world_id TEXT NOT NULL,
  name TEXT NOT NULL, path TEXT NOT NULL, status TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id, world_id)
);
CREATE TABLE IF NOT EXISTS user_play_state (
  user_id TEXT PRIMARY KEY, active_world_id TEXT NOT NULL, active_revision INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS activation_operations (
  operation_id TEXT PRIMARY KEY, request_key TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL, game_id TEXT NOT NULL, target_world_id TEXT NOT NULL,
  expected_revision INTEGER NOT NULL, request_hash TEXT NOT NULL,
  status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS copy_operations (
  operation_id TEXT PRIMARY KEY, request_key TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL, game_id TEXT NOT NULL, source_world_id TEXT NOT NULL,
  target_world_id TEXT NOT NULL, target_name TEXT NOT NULL, status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_worlds_owner_updated ON worlds(user_id, updated_at DESC);
`

const worldSchema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS characters (
  entity_id TEXT PRIMARY KEY, definition_id TEXT NOT NULL, name TEXT NOT NULL,
  role TEXT NOT NULL, profile TEXT NOT NULL, knowledge TEXT NOT NULL, in_scene INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  seq INTEGER PRIMARY KEY, message_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
  content TEXT NOT NULL, run_id TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY, event_id TEXT NOT NULL UNIQUE, event_type TEXT NOT NULL,
  actor_id TEXT NOT NULL, target_id TEXT NOT NULL, content TEXT NOT NULL,
  run_id TEXT NOT NULL, stage INTEGER NOT NULL, scene_version INTEGER NOT NULL,
  source_type TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS perceptions (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, recipient_id TEXT NOT NULL,
  source_event_id TEXT NOT NULL, source_type TEXT NOT NULL, content TEXT NOT NULL,
  stage INTEGER NOT NULL, scene_version INTEGER NOT NULL, created_at TEXT NOT NULL,
  UNIQUE(recipient_id, source_event_id, content)
);
CREATE TABLE IF NOT EXISTS memories (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, recipient_id TEXT NOT NULL,
  kind TEXT NOT NULL, content TEXT NOT NULL, source_event_id TEXT NOT NULL,
  created_at TEXT NOT NULL, UNIQUE(recipient_id, kind, content, source_event_id)
);
-- Where a character in this world came from, and which of that person's own
-- experiences were carried over when they were promoted.
CREATE TABLE IF NOT EXISTS character_origins (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, entity_id TEXT NOT NULL,
  source_kind TEXT NOT NULL, source_id TEXT NOT NULL, created_at TEXT NOT NULL,
  UNIQUE(entity_id, source_kind, source_id)
);
CREATE TABLE IF NOT EXISTS runs (
  run_id TEXT PRIMARY KEY, request_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
  input TEXT NOT NULL, addressee_id TEXT NOT NULL, attempt INTEGER NOT NULL,
  status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
	  message_seq INTEGER NOT NULL DEFAULT 0, cancel_requested INTEGER NOT NULL DEFAULT 0,
	  input_id TEXT NOT NULL DEFAULT '', input_seq INTEGER NOT NULL DEFAULT 0,
	  base_turn_seq INTEGER NOT NULL DEFAULT 0, base_message_head INTEGER NOT NULL DEFAULT 0,
  base_event_head INTEGER NOT NULL DEFAULT 0, base_context_epoch INTEGER NOT NULL DEFAULT 0,
  base_scene_version INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(seq);
CREATE INDEX IF NOT EXISTS idx_events_seq ON events(seq);
CREATE INDEX IF NOT EXISTS idx_perceptions_recipient_seq ON perceptions(recipient_id, seq);
CREATE INDEX IF NOT EXISTS idx_memories_recipient_seq ON memories(recipient_id, seq);
`

func initializeWorld(ctx context.Context, store *storage.WorldStore, userID, worldID string, def story.Definition, mode, playerName, playerProfile string) error {
	if mode != "open" && mode != "guided" {
		return ErrInvalidRequest
	}
	now := wire.NowText()
	values := map[string]string{
		"schema_version": strconv.Itoa(SchemaVersion), "user_id": userID, "game_id": def.Summary.ID,
		"world_id": worldID, "game_revision": def.Revision, "mode": mode,
		"turn_seq": "0", "message_head": "1", "event_head": "0", "context_epoch": "1",
		"scene_version": "1", "scene": def.Scene, "scene_location": def.InitialLocation, "clock": def.Clock,
		"player_name": playerName, "player_profile": playerProfile, "status": "ready",
		"generation": "1", "plot_status": "active", "bystanders": wire.MarshalJSON(def.Bystanders),
		"narrative_perspective": wiaworld.PerspectiveSecondPerson, "narrative_length": wiaworld.NarrativeLengthStandard,
		"narrative_detail": wiaworld.NarrativeDetailBalanced, "narrative_custom_instruction": "",
		"player_elaboration": wiaworld.PlayerElaborationNatural, "npc_initiative": wiaworld.NPCInitiativeContextual,
	}
	settings := def.Settings
	if settings.Perspective == "" {
		settings = wiaworld.DefaultNarrativeSettings()
	}
	values["narrative_perspective"], values["narrative_length"], values["narrative_detail"] = settings.Perspective, settings.Length, settings.Detail
	values["player_elaboration"], values["npc_initiative"] = settings.PlayerElaboration, settings.NPCInitiative
	values["behavior_policies"] = wire.MarshalJSON(settings.Policies)
	values["narrative_custom_instruction"] = settings.CustomInstruction
	values["settings_origin"] = def.SettingsSource
	values["definition_snapshot"] = wire.MarshalJSON(def)
	// A world is either created whole or not at all: the headers, the characters, the
	// opening message and the opening event together are what "this world exists" means.
	return store.InTx(ctx, func(tx *storage.WorldTx) error {
		for key, value := range values {
			if err := tx.SetMeta(ctx, key, value); err != nil {
				return err
			}
		}
		if def.Plot != nil {
			state := plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}
			if err := plot.Validate(*def.Plot, state); err != nil {
				return err
			}
			if err := tx.SetMeta(ctx, "plot_definition", wire.MarshalJSON(def.Plot)); err != nil {
				return err
			}
			if err := tx.SetMeta(ctx, "plot_progress", wire.MarshalJSON(state)); err != nil {
				return err
			}
		}
		for _, c := range def.Characters {
			if err := tx.SetMeta(ctx, "appearance:"+c.EntityID, c.Appearance); err != nil {
				return err
			}
			if err := tx.SetMeta(ctx, "definition_revision:"+c.EntityID, c.DefinitionRevision); err != nil {
				return err
			}
			if err := tx.SetMeta(ctx, "initial_concerns:"+c.EntityID, c.InitialConcerns); err != nil {
				return err
			}
			if err := tx.InsertCharacter(ctx, storage.CharacterWrite{
				EntityID: c.EntityID, DefinitionID: c.DefinitionID, Name: c.Name, Role: c.Role,
				Profile: c.Profile, Knowledge: c.Knowledge, InScene: c.InScene,
			}); err != nil {
				return err
			}
		}
		if err := tx.InsertMessage(ctx, 1, "opening", "narrative", def.Opening, "system", now); err != nil {
			return err
		}
		if err := tx.InsertEvent(ctx, storage.EventWrite{
			Seq: 1, EventID: "opening", EventType: "scene_opened", ActorID: "system",
			Content: def.Opening, RunID: "system", SceneVersion: 1, SourceType: "definition", CreatedAt: now,
		}); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "event_head", "1")
	})
}

func loadTurnSnapshot(ctx context.Context, store *storage.WorldStore, limit int) (turn.Snapshot, error) {
	var out turn.Snapshot
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
	out.Narrative, err = loadNarrativeSettings(ctx, store)
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
	out.Summary.Revision = out.Definition.Revision
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

// A promoted or authored character can carry its own avatar; the world copy is
// recorded separately under the world's assets.

func commitTurn(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, narrative string, events []wiaworld.Event, perceptions []wiaworld.Perception, memories []wiaworld.Memory, clock, scene, sceneLocation string, sceneVersion int64, sceneCharacters []string, sceneViews []turn.SceneView, plotState *plot.Progress, generated ...*turn.GeneratedEventState) (int64, error) {
	var messageHead int64
	var eventHead int64
	var turnSeq int64
	var currentSceneVersion int64
	var contextEpoch int64
	if err := store.InTx(ctx, func(tx *storage.WorldTx) error {
		status, cancelled, err := tx.RunStatus(ctx, run.RunID)
		if err != nil {
			return err
		}
		if status != "running" {
			return ErrWorldBusy
		}
		if cancelled {
			return context.Canceled
		}
		for key, target := range map[string]*int64{"message_head": &messageHead, "event_head": &eventHead, "turn_seq": &turnSeq, "scene_version": &currentSceneVersion, "context_epoch": &contextEpoch} {
			value, err := tx.GetMeta(ctx, key)
			if err != nil {
				return err
			}
			*target, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
		}
		if run.InputSeq > 0 && (run.BaseTurnSeq != turnSeq || run.BaseMessageHead != messageHead || run.BaseEventHead != eventHead || run.BaseContextEpoch != contextEpoch || run.BaseSceneVersion != currentSceneVersion) {
			return ErrVersionConflict
		}
		if sceneVersion < currentSceneVersion {
			return ErrVersionConflict
		}
		for _, e := range events {
			eventHead++
			e.Seq = eventHead
			if err := tx.InsertEvent(ctx, storage.EventWrite{
				Seq: eventHead, EventID: e.EventID, EventType: e.EventType, ActorID: e.ActorID,
				TargetID: e.TargetID, Content: e.Content, RunID: e.RunID, Stage: e.Stage,
				SceneVersion: e.SceneVersion, SourceType: e.SourceType, CreatedAt: e.CreatedAt.Format(time.RFC3339Nano),
			}); err != nil {
				return err
			}
			if e.ProjectionParentID != "" {
				if err := tx.InsertEventDependency(ctx, e.EventID, e.ProjectionParentID); err != nil {
					return err
				}
			}
		}
		for _, p := range perceptions {
			if err := tx.InsertPerceptionIfAbsent(ctx, storage.PerceptionWrite{
				RecipientID: p.RecipientID, SourceEventID: p.SourceEventID, SourceType: p.SourceType,
				Content: p.Content, Stage: p.Stage, SceneVersion: p.SceneVersion, CreatedAt: p.CreatedAt.Format(time.RFC3339Nano),
			}); err != nil {
				return err
			}
		}
		for _, m := range memories {
			if err := tx.InsertMemoryIfAbsent(ctx, storage.MemoryWrite{
				RecipientID: m.RecipientID, Kind: m.Kind, Content: m.Content,
				SourceEventID: m.SourceEventID, CreatedAt: m.CreatedAt.Format(time.RFC3339Nano),
			}); err != nil {
				return err
			}
		}
		messageHead++
		if err := tx.InsertMessage(ctx, messageHead, run.RunID+":input", "player", run.Input, run.RunID, wire.NowText()); err != nil {
			return err
		}
		messageHead++
		if err := tx.InsertMessage(ctx, messageHead, run.RunID+":narrative", "narrative", narrative, run.RunID, wire.NowText()); err != nil {
			return err
		}
		turnSeq++
		if err := tx.SetMeta(ctx, "message_head", strconv.FormatInt(messageHead, 10)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "event_head", strconv.FormatInt(eventHead, 10)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "turn_seq", strconv.FormatInt(turnSeq, 10)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "clock", clock); err != nil {
			return err
		}
		if plotState != nil {
			if err := tx.SetMeta(ctx, "plot_progress", wire.MarshalJSON(plotState)); err != nil {
				return err
			}
		}
		if len(generated) > 0 && generated[0] != nil {
			if err := tx.SetMeta(ctx, "generated_events", wire.MarshalJSON(generated[0])); err != nil {
				return err
			}
		}
		if scene == "" {
			scene, _ = tx.GetMeta(ctx, "scene")
		}
		viewsJSON, err := json.Marshal(sceneViews)
		if err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "scene_views", string(viewsJSON)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "scene", scene); err != nil {
			return err
		}
		if sceneLocation != "" {
			if err := tx.SetMeta(ctx, "scene_location", sceneLocation); err != nil {
				return err
			}
		}
		if err := tx.SetMeta(ctx, "scene_version", strconv.FormatInt(sceneVersion, 10)); err != nil {
			return err
		}
		if err := tx.ClearScenePresence(ctx); err != nil {
			return err
		}
		for _, entityID := range sceneCharacters {
			found, err := tx.SetScenePresence(ctx, entityID)
			if err != nil {
				return err
			}
			if !found {
				return turn.ErrGenerationFailed
			}
		}
		if err := tx.SetMeta(ctx, "updated_at", wire.NowText()); err != nil {
			return err
		}
		completed, err := tx.CompleteRun(ctx, run.RunID, messageHead, wire.NowText())
		if err != nil {
			return err
		}
		if !completed {
			return ErrWorldBusy
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return messageHead, nil
}

func removeDir(path string) error { return os.RemoveAll(path) }

func cloneWorld(ctx context.Context, source *storage.WorldStore, targetPath, targetWorldID, targetName string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(targetPath)
	if _, err := source.Database().ExecContext(ctx, `VACUUM INTO ?`, targetPath); err != nil {
		return err
	}
	target, err := storage.OpenWorldDB(targetPath)
	if err != nil {
		return err
	}
	defer target.Close()
	return target.InTx(ctx, func(tx *storage.WorldTx) error {
		if err := tx.SetMeta(ctx, "world_id", targetWorldID); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "name", targetName); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "generation", "1")
	})
}
