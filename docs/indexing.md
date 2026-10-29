# Document indexing

Implemented through Checkpoint 3: persistent jobs, a single background worker, atomic upload enqueueing, stored passages and a read-authorized status API using the [bounded TXT/PDF extractor](extraction.md). FTS5, search, previews and analytics remain later checkpoints. Development remains localhost-only.

## State and transactions

One `document_index_jobs` row per document holds both current status and queue metadata. Migration 004 queues existing records without changing originals or access rights. Migration 005 adds private `document_passages` with source checksums/page/section references, backfills uploads missing jobs from Checkpoints 1/2, and requeues pre-worker `ready` rows that have no persisted passages. Existing failed/unsupported states are preserved. Migrations never open or change original files.

```text
pending -> processing -> ready | failed | unsupported
                 |
                 +----> pending (scheduled retry or startup recovery)
```

`Claim` atomically reserves the oldest due pending job, increments attempts and issues a fresh token. A partial index supports the query. Created/updated timestamps and retry deadlines are UTC Unix seconds. The server runs one worker; an idle worker polls every 500 ms. No connection or transaction is held during extraction, so ordinary API operations can use SQLite while parsing proceeds.

The worker validates output bounds/references, then publishes passages and `ready` status in one short transaction. `Finish` requires the processing token, captured checksum and current document checksum to match before any passage is changed. A stale/replaced/deleted attempt cannot overwrite current output. Publication failures roll everything back; the worker retains its bounded result and retries persistence every 500 ms until storage recovers or shutdown cancels it. This pauses subsequent indexing jobs during a database outage, while releasing SQLite between attempts.

Uploads commit document metadata, owner, job and audit event together. All accepted uploads get a status; unsupported types are classified without parsing. An identical-checksum enqueue is an idempotent no-op. A changed checksum invalidates the old token, resets attempts/error/deadline and clears old passages through a trigger. Document deletion cascades through jobs and passages. Replacement/deletion APIs do not exist yet; tests simulate those changes. Future replacement must enqueue in the document transaction.

Transient extraction failures retry after 10 and 20 seconds, with at most three claims including interrupted attempts. Unsupported/no-text results become `unsupported`; malformed, over-limit, missing or corrupt originals become `failed`. Extractor panics are caught and treated as transient failures. Error messages use fixed operational text; parser diagnostics, panic values, filenames, credentials and content are excluded.

At startup, `Recover` runs under the storage lock before the listener/worker: it invalidates abandoned tokens, retains attempt counts and requeues interrupted work, or marks exhausted jobs failed. Shutdown cancels extraction/polling and joins the worker before closing SQLite/storage. An unfinished claim survives for restart recovery. This permits repeated extraction after interruption; it does not promise exactly-once work. Originals are preserved regardless of indexing outcome.

## Status API

`GET /api/documents/{id}/indexing` requires the current session and document read permission. Its SQL reuses the owner/admin/grant predicate and requires the current source checksum. Permission revocation applies immediately. It returns `status`, `attempts`, `created_at`, `updated_at`, optional `next_attempt_at` and a sanitized `last_error`; no passages, names, checksums or attempt tokens. Missing and unauthorized IDs both return 404 for authenticated users. No public passage endpoint is provided.

## Limits and interview concepts

There is no search/preview UI or manual retry API yet. Terminal failures do not automatically restart. The queue stores current state, not attempt history. PDF extraction retains the Linux/Docker requirement; DOCX/OCR remain deferred. No performance benchmark, power-loss guarantee or production readiness is claimed. Rebuild/restart with the existing volume to apply migration 005; do not repeat account bootstrap or reset data.

The important concepts are additive transactional migrations, atomic queue claims, idempotency, attempt tokens that reject stale writes, and durable state versus process memory. Recovery permits work to run again after interruption; it does not promise exactly-once extraction. Source bytes remain independent of derived indexing state. Search authorization must still be enforced against current Milestone 2 permissions when the search API is implemented.

## Checkpoint 1 verification

- Go 1.26.10 in Docker: formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was 75.8%. New tests cover migration preservation, constraints, retry deadlines, restart recovery, stale/replaced/deleted sources, concurrent claims and transaction rollback.
- Frontend formatting, all 10 unit tests, TypeScript checks and production build passed. The existing desktop/mobile browser test passed using freshly cross-built native binaries and isolated synthetic data.
- Linux amd64/arm64 server builds and the isolated Docker authentication/upload/download/restart persistence test passed. ARM64 hardware execution was not tested. The regular developer volume was not modified.
- `git diff --check` passed. Existing CI already runs all Go packages plus frontend/browser/Compose checks on pushes and pull requests; no hosted CI run was triggered. Migration downgrade and power-loss recovery were not tested.

## Checkpoint 3 verification

- Go 1.26.10/Linux formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was **77.8%**. Tests cover real TXT extraction/publication, retries/exhaustion, panic sanitization, restart recovery, shutdown, concurrent-run rejection, stale/replaced/deleted work, invalid output and atomic database-failure recovery.
- API tests cover transactional document/job/audit rollback, automatic indexing, anonymous/cross-user denial, read grants and immediate revocation, forged cached administrator flags, source mismatch and exclusion of text/internal tokens. Migration tests cover earlier uploads and legacy job states while preserving existing document/account/grant/audit records.
- All **10 frontend tests**, formatting, TypeScript/production build and **1 desktop/mobile browser test** passed. Linux amd64/arm64 server and macOS amd64 server/CLI builds passed; ARM64 hardware was not tested.
- The isolated Compose smoke test verified automatic TXT indexing/status after restart alongside original-byte checksums, session persistence and logout rejection. It removed only its own synthetic project/volume. Interrupted processing was tested by cancellation, database close/reopen, recovery and a new worker attempt; the Compose restart does not deterministically interrupt a running extraction.
- No hosted CI run, performance benchmark, power-loss test or migration downgrade was performed. Existing CI includes the new Go tests and extended Compose script. The regular developer data volume was not modified.
