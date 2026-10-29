CREATE TABLE document_passages (
    document_id TEXT NOT NULL REFERENCES document_index_jobs(document_id) ON DELETE CASCADE,
    source_sha256 TEXT NOT NULL CHECK (
        length(source_sha256) = 64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    page INTEGER NOT NULL CHECK (page BETWEEN 0 AND 200),
    section INTEGER NOT NULL CHECK (section BETWEEN 1 AND 2048),
    text TEXT NOT NULL CHECK (length(CAST(text AS BLOB)) BETWEEN 1 AND 4096),
    PRIMARY KEY (document_id, page, section)
) STRICT;

CREATE TRIGGER document_index_source_changed
AFTER UPDATE OF source_sha256 ON document_index_jobs
WHEN OLD.source_sha256 <> NEW.source_sha256
BEGIN
    DELETE FROM document_passages WHERE document_id = NEW.document_id;
END;

-- A pre-worker 'ready' state has no persisted passages in this new table.
-- Re-extract those originals while retaining terminal failures/unsupported states.
UPDATE document_index_jobs
SET status = 'pending', attempts = 0, attempt_token = '', last_error = '',
    updated_at = unixepoch(), available_at = unixepoch()
WHERE status = 'ready';

-- Checkpoints 1/2 could accept uploads after migration 004 without enqueueing.
-- Backfill missing records without duplicating existing work.
INSERT INTO document_index_jobs(document_id, source_sha256)
SELECT id, sha256 FROM documents WHERE true
ON CONFLICT(document_id) DO NOTHING;
