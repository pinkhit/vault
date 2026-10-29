CREATE TABLE document_index_jobs (
    document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    source_sha256 TEXT NOT NULL CHECK (
        length(source_sha256) = 64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'processing', 'ready', 'failed', 'unsupported')
    ),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    attempt_token TEXT NOT NULL DEFAULT '' CHECK (
        attempt_token = '' OR (
            length(attempt_token) = 64 AND attempt_token NOT GLOB '*[^0-9a-f]*'
        )
    ),
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 512),
    created_at INTEGER NOT NULL DEFAULT (unixepoch()) CHECK (created_at > 0),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch()) CHECK (updated_at >= created_at),
    available_at INTEGER NOT NULL DEFAULT (unixepoch()) CHECK (available_at >= 0),
    CHECK ((status = 'processing') = (attempt_token <> '')),
    CHECK (status = 'pending' OR attempts > 0),
    CHECK (status <> 'failed' OR last_error <> ''),
    CHECK (status <> 'ready' OR last_error = '')
) STRICT;

CREATE INDEX document_index_jobs_pending
ON document_index_jobs(available_at, created_at, document_id)
WHERE status = 'pending';

-- Existing originals, ownership, grants and audit history are untouched.
INSERT INTO document_index_jobs(document_id, source_sha256)
SELECT id, sha256 FROM documents;
