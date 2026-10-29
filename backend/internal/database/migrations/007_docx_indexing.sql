-- Retry only documents previously rejected because DOCX was unsupported.
-- Preserve successful work, exhausted failures, originals and permissions.
UPDATE document_index_jobs
SET status = 'pending', attempts = 0, attempt_token = '', last_error = '',
    updated_at = unixepoch(), available_at = unixepoch()
WHERE status = 'unsupported' AND last_error = 'unsupported format'
  AND document_id IN (
      SELECT id FROM documents
      WHERE lower(substr(name, -5)) = '.docx' AND media_type = 'application/zip'
  );
