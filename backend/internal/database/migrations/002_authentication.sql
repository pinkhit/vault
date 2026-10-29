CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_seen INTEGER NOT NULL
) STRICT;
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expiry ON sessions(expires_at);

CREATE TABLE login_limits (
    key TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    reset_at INTEGER NOT NULL
) STRICT;

ALTER TABLE documents ADD COLUMN owner_id TEXT REFERENCES users(id);
CREATE INDEX documents_owner ON documents(owner_id);

CREATE TABLE document_permissions (
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level TEXT NOT NULL CHECK (level IN ('read', 'write', 'manage')),
    PRIMARY KEY (document_id, user_id)
) STRICT;
CREATE INDEX document_permissions_user ON document_permissions(user_id, document_id);

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY,
    created_at INTEGER NOT NULL,
    actor_id TEXT REFERENCES users(id),
    operation TEXT NOT NULL,
    object_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    request_id TEXT NOT NULL
) STRICT;
