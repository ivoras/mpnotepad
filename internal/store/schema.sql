CREATE TABLE IF NOT EXISTS documents (
  id              TEXT PRIMARY KEY,
  title           TEXT NOT NULL DEFAULT '',
  password_hash   TEXT,
  markdown_panel  INTEGER NOT NULL DEFAULT 0,
  yjs_state       BLOB,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS document_sessions (
  token         TEXT PRIMARY KEY,
  document_id   TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  created_at    INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_doc ON document_sessions(document_id);

CREATE TABLE IF NOT EXISTS document_versions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  document_id   TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  created_at    INTEGER NOT NULL,
  text          TEXT NOT NULL,
  char_count    INTEGER NOT NULL,
  byte_size     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_versions_doc_created ON document_versions(document_id, created_at DESC);
