CREATE TABLE documents (
    id TEXT PRIMARY KEY CHECK (length(id) = 32 AND id NOT GLOB '*[^0-9a-f]*'),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    size INTEGER NOT NULL CHECK (size > 0),
    media_type TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX documents_created_at ON documents(created_at DESC, id DESC);
