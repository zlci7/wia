package app

const personaSchema = `
CREATE TABLE IF NOT EXISTS personas (
  user_id TEXT NOT NULL, persona_id TEXT NOT NULL,
  name TEXT NOT NULL, profile TEXT NOT NULL,
  version INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, persona_id)
);
CREATE INDEX IF NOT EXISTS idx_personas_owner_updated ON personas(user_id, updated_at DESC);
`
