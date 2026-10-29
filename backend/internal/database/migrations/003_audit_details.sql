ALTER TABLE audit_events ADD COLUMN details TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(details));

-- Defense against accidental application-level history rewriting. A privileged
-- host/database owner can still drop these triggers; this is not tamper evidence.
CREATE TRIGGER audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append-only');
END;
