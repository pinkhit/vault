-- External-content FTS needs stable integer keys: VACUUM may change implicit
-- rowids. Preserve passage data/references while adding an explicit primary key.
DROP TRIGGER document_index_source_changed;
ALTER TABLE document_passages RENAME TO legacy_document_passages;
CREATE TABLE document_passages (
    id INTEGER PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES document_index_jobs(document_id) ON DELETE CASCADE,
    source_sha256 TEXT NOT NULL CHECK (
        length(source_sha256) = 64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    page INTEGER NOT NULL CHECK (page BETWEEN 0 AND 200),
    section INTEGER NOT NULL CHECK (section BETWEEN 1 AND 2048),
    text TEXT NOT NULL CHECK (length(CAST(text AS BLOB)) BETWEEN 1 AND 4096),
    UNIQUE (document_id,page,section)
) STRICT;
INSERT INTO document_passages(id,document_id,source_sha256,page,section,text)
SELECT rowid,document_id,source_sha256,page,section,text FROM legacy_document_passages;
DROP TABLE legacy_document_passages;
CREATE TRIGGER document_index_source_changed
AFTER UPDATE OF source_sha256 ON document_index_jobs
WHEN OLD.source_sha256 <> NEW.source_sha256
BEGIN
    DELETE FROM document_passages WHERE document_id = NEW.document_id;
END;

CREATE VIRTUAL TABLE document_fts USING fts5(
    document_id UNINDEXED, name, metadata,
    tokenize = "unicode61 remove_diacritics 2 tokenchars '-_'"
);
CREATE VIRTUAL TABLE passage_fts USING fts5(
    text, content = 'document_passages', content_rowid = 'id',
    tokenize = "unicode61 remove_diacritics 2 tokenchars '-_'"
);

CREATE TRIGGER document_search_insert AFTER INSERT ON documents BEGIN
    INSERT INTO document_fts(document_id,name,metadata)
    VALUES (NEW.id, NEW.name, NEW.id || ' ' || NEW.media_type || ' ' || NEW.size || ' ' || NEW.sha256 || ' ' || NEW.created_at || ' ' || substr(NEW.created_at,1,10));
END;
CREATE TRIGGER document_search_update
AFTER UPDATE OF id,name,media_type,size,sha256,created_at ON documents BEGIN
    UPDATE document_fts SET document_id = NEW.id, name = NEW.name,
        metadata = NEW.id || ' ' || NEW.media_type || ' ' || NEW.size || ' ' || NEW.sha256 || ' ' || NEW.created_at || ' ' || substr(NEW.created_at,1,10)
    WHERE document_id = OLD.id;
END;
CREATE TRIGGER document_search_delete AFTER DELETE ON documents BEGIN
    DELETE FROM document_fts WHERE document_id = OLD.id;
END;

CREATE TRIGGER passage_search_insert AFTER INSERT ON document_passages BEGIN
    INSERT INTO passage_fts(rowid,text) VALUES (NEW.id,NEW.text);
END;
CREATE TRIGGER passage_search_delete AFTER DELETE ON document_passages BEGIN
    INSERT INTO passage_fts(passage_fts,rowid,text) VALUES ('delete',OLD.id,OLD.text);
END;
CREATE TRIGGER passage_search_update AFTER UPDATE ON document_passages BEGIN
    INSERT INTO passage_fts(passage_fts,rowid,text) VALUES ('delete',OLD.id,OLD.text);
    INSERT INTO passage_fts(rowid,text) VALUES (NEW.id,NEW.text);
END;

INSERT INTO document_fts(document_id,name,metadata)
SELECT id,name,id || ' ' || media_type || ' ' || size || ' ' || sha256 || ' ' || created_at || ' ' || substr(created_at,1,10)
FROM documents;
INSERT INTO passage_fts(passage_fts) VALUES ('rebuild');
