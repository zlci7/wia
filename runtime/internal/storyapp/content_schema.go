package storyapp

// M3 content workspace storage. Tables are additive and created with IF NOT
// EXISTS so an existing application database keeps working unchanged; the draft
// payload is the publication target and never the world's authority.
const contentSchema = `
CREATE TABLE IF NOT EXISTS personas (
  user_id TEXT NOT NULL, persona_id TEXT NOT NULL,
  name TEXT NOT NULL, profile TEXT NOT NULL,
  version INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, persona_id)
);
CREATE INDEX IF NOT EXISTS idx_personas_owner_updated ON personas(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS content_projects (
  user_id TEXT NOT NULL, project_id TEXT NOT NULL, game_id TEXT NOT NULL,
  title TEXT NOT NULL, current_revision TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL, deleted_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, project_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_content_projects_game ON content_projects(user_id, game_id);

CREATE TABLE IF NOT EXISTS content_drafts (
  user_id TEXT NOT NULL, draft_id TEXT NOT NULL, project_id TEXT NOT NULL,
  base_revision TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL, status TEXT NOT NULL,
  payload_json TEXT NOT NULL, source_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, draft_id)
);
CREATE INDEX IF NOT EXISTS idx_content_drafts_project ON content_drafts(user_id, project_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS content_draft_assets (
  user_id TEXT NOT NULL, draft_id TEXT NOT NULL, asset_id TEXT NOT NULL,
  relative_name TEXT NOT NULL, media_type TEXT NOT NULL,
  byte_size INTEGER NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL,
  digest TEXT NOT NULL, staged_path TEXT NOT NULL, created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, draft_id, asset_id)
);

CREATE TABLE IF NOT EXISTS content_revisions (
  user_id TEXT NOT NULL, game_id TEXT NOT NULL, revision TEXT NOT NULL,
  digest TEXT NOT NULL, path TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id, revision)
);
CREATE INDEX IF NOT EXISTS idx_content_revisions_owner ON content_revisions(user_id, revision);

CREATE TABLE IF NOT EXISTS content_operations (
  user_id TEXT NOT NULL, request_key TEXT NOT NULL, request_hash TEXT NOT NULL,
  operation_id TEXT NOT NULL, kind TEXT NOT NULL, target_id TEXT NOT NULL,
  stage TEXT NOT NULL, status TEXT NOT NULL,
  result_json TEXT NOT NULL DEFAULT '', safe_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, request_key)
);
CREATE INDEX IF NOT EXISTS idx_content_operations_owner ON content_operations(user_id, updated_at DESC);
`
