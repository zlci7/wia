package storage

// The schema of a world database. The statements are idempotent, and a database
// written by an earlier release is brought forward by ensureWorldSchema rather than
// by rewriting these.
//
// Every table here has a writer in this package or in the modules that own the
// table's meaning; a table nothing writes is a table that should not exist.
const worldSchema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS characters (
  entity_id TEXT PRIMARY KEY, definition_id TEXT NOT NULL, name TEXT NOT NULL,
  role TEXT NOT NULL, profile TEXT NOT NULL, knowledge TEXT NOT NULL, in_scene INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS entity_locations (
  entity_id TEXT PRIMARY KEY, location_id TEXT NOT NULL,
  source_event_id TEXT NOT NULL, updated_turn INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS entity_states (
  entity_id TEXT NOT NULL, state_id TEXT NOT NULL, value_json TEXT NOT NULL,
  source_event_id TEXT NOT NULL, updated_turn INTEGER NOT NULL, version INTEGER NOT NULL,
  PRIMARY KEY(entity_id,state_id)
);
CREATE TABLE IF NOT EXISTS state_changes (
  change_id TEXT PRIMARY KEY, entity_id TEXT NOT NULL, state_id TEXT NOT NULL,
  before_json TEXT NOT NULL, after_json TEXT NOT NULL, source_event_id TEXT NOT NULL,
  turn_seq INTEGER NOT NULL, effect_order INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_state_change_order ON state_changes(source_event_id,entity_id,state_id,effect_order);
CREATE TABLE IF NOT EXISTS relationships (
  subject_id TEXT NOT NULL, target_id TEXT NOT NULL, relation_type TEXT NOT NULL,
  value INTEGER NOT NULL, source_event_id TEXT NOT NULL, updated_turn INTEGER NOT NULL, version INTEGER NOT NULL,
  PRIMARY KEY(subject_id,target_id,relation_type)
);
CREATE TABLE IF NOT EXISTS relationship_changes (
  change_id TEXT PRIMARY KEY, subject_id TEXT NOT NULL, target_id TEXT NOT NULL, relation_type TEXT NOT NULL,
  before_value INTEGER NOT NULL, after_value INTEGER NOT NULL, source_event_id TEXT NOT NULL,
  proposal_source_event_id TEXT NOT NULL,
  turn_seq INTEGER NOT NULL, effect_order INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relationship_change_effect ON relationship_changes(source_event_id,subject_id,target_id,relation_type);
CREATE TABLE IF NOT EXISTS item_instances (
  instance_id TEXT PRIMARY KEY, definition_id TEXT NOT NULL,
  holder_id TEXT NOT NULL DEFAULT '', location_id TEXT NOT NULL DEFAULT '',
  source_event_id TEXT NOT NULL, updated_turn INTEGER NOT NULL, version INTEGER NOT NULL,
  CHECK ((holder_id != '' AND location_id = '') OR (holder_id = '' AND location_id != ''))
);
CREATE TABLE IF NOT EXISTS item_transfers (
  transfer_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL,
  before_holder_id TEXT NOT NULL, before_location_id TEXT NOT NULL,
  after_holder_id TEXT NOT NULL, after_location_id TEXT NOT NULL,
  source_event_id TEXT NOT NULL, turn_seq INTEGER NOT NULL, effect_order INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_item_transfer_order ON item_transfers(source_event_id,instance_id,effect_order);
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
CREATE TABLE IF NOT EXISTS action_resolutions (
  input_id TEXT NOT NULL, rule_id TEXT NOT NULL, roll INTEGER NOT NULL,
  target INTEGER NOT NULL, modifiers_json TEXT NOT NULL, settled_event_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, PRIMARY KEY(input_id,rule_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_action_resolution_input ON action_resolutions(input_id);
CREATE INDEX IF NOT EXISTS idx_messages_seq ON messages(seq);
CREATE INDEX IF NOT EXISTS idx_events_seq ON events(seq);
CREATE INDEX IF NOT EXISTS idx_perceptions_recipient_seq ON perceptions(recipient_id, seq);
CREATE INDEX IF NOT EXISTS idx_perceptions_source_recipient ON perceptions(source_event_id, recipient_id);
CREATE INDEX IF NOT EXISTS idx_memories_recipient_seq ON memories(recipient_id, seq);
`

const memorySchema = `
CREATE TABLE IF NOT EXISTS memory_sources (
 scope TEXT NOT NULL, seq INTEGER NOT NULL, source_id TEXT NOT NULL,
 event_id TEXT NOT NULL, run_id TEXT NOT NULL, actor TEXT NOT NULL,
 kind TEXT NOT NULL, content TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(scope,seq), UNIQUE(scope,source_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_source_event ON memory_sources(event_id,scope);
CREATE TABLE IF NOT EXISTS memory_digests (
 scope TEXT NOT NULL, revision INTEGER NOT NULL, epoch INTEGER NOT NULL,
 through_seq INTEGER NOT NULL, source_head INTEGER NOT NULL,
 content TEXT NOT NULL, states TEXT NOT NULL, sources TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(scope,revision)
);`

const correctionSchema = `
CREATE TABLE IF NOT EXISTS corrections (
 epoch INTEGER PRIMARY KEY, request_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
 kind TEXT NOT NULL, scope TEXT NOT NULL, target_id TEXT NOT NULL,
 original TEXT NOT NULL, replacement TEXT NOT NULL, created_at TEXT NOT NULL, scene_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS memory_jobs (
 epoch INTEGER PRIMARY KEY, status TEXT NOT NULL, completed INTEGER NOT NULL,
 scopes TEXT NOT NULL, error TEXT NOT NULL, updated_at TEXT NOT NULL
);`
