# Document indexing

Implemented: migration 004, the internal `indexing` lifecycle helpers and a standalone [bounded TXT/PDF extractor](extraction.md). Worker startup, upload enqueueing, status APIs, FTS5, previews and analytics are not implemented yet. Development remains localhost-only.

## State and transactions

One `document_index_jobs` row per document holds the current indexing status and queue metadata. This avoids a separate status copy drifting from the queue. The document foreign key cascades deletion. Migration 004 queues existing records as `pending` without changing originals, ownership, grants, sessions or audit history. Job lifecycle helpers never open files; extraction uses the verified storage layer separately.

```text
pending -> processing -> ready | failed | unsupported
                 |
                 +----> pending (scheduled retry or startup recovery)
```

`Claim` atomically reserves the oldest eligible pending job, increments attempts and issues a fresh random attempt token. Its SQL statement releases the connection before extraction would begin. A partial index supports the due-job query. Created/updated timestamps and retry deadlines are UTC Unix seconds. Attempts count claims, including interrupted work; retry errors remain available until success or replacement.

`Finish` accepts a terminal result or schedules a retry. The update requires the processing token, captured SHA-256 and current document checksum all to match. A duplicate, recovered, replaced or deleted attempt returns `ErrStale`. Future extracted-text writes must share the finalization transaction and roll back on that error. `ready` denotes a successfully finalized attempt; Checkpoint 1 does not produce extracted text or search results.

`Enqueue` takes the caller's document transaction, enabling atomic upload/job creation in Checkpoint 3. An identical checksum is an idempotent no-op, including for failed/unsupported jobs. A changed checksum invalidates the old token and resets attempts/error/deadline while retaining the row's creation time. Current originals are immutable; tests simulate future replacement through SQL. Any future replacement implementation must call `Enqueue` in the same transaction as its document change.

`Recover` explicitly requeues processing rows after restart, clears their tokens and retains attempts with a sanitized interruption message. It must run under the existing exclusive data-directory lock before any workers start; it is not a live-worker lease timeout. Merely opening the database never resets work. Checkpoint 3 will connect recovery and implement bounded retries/concurrency/shutdown.

## Limits and interview concepts

There is no worker, extracted-text schema, public status endpoint or manual retry API yet. New uploads are not automatically enqueued. The queue stores current state, not an attempt history. Persist only sanitized operational errors (maximum 512 UTF-8 bytes through the helper), never filenames, paths, document text or credentials. No performance benchmark is claimed.

The important concepts are additive transactional migrations, atomic queue claims, idempotency, attempt tokens that reject stale writes, and durable state versus process memory. Recovery permits work to run again after interruption; it does not promise exactly-once extraction. Source bytes remain independent of derived indexing state. Search authorization must still be enforced against current Milestone 2 permissions when the search API is implemented.

## Checkpoint 1 verification

- Go 1.26.10 in Docker: formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was 75.8%. New tests cover migration preservation, constraints, retry deadlines, restart recovery, stale/replaced/deleted sources, concurrent claims and transaction rollback.
- Frontend formatting, all 10 unit tests, TypeScript checks and production build passed. The existing desktop/mobile browser test passed using freshly cross-built native binaries and isolated synthetic data.
- Linux amd64/arm64 server builds and the isolated Docker authentication/upload/download/restart persistence test passed. ARM64 hardware execution was not tested. The regular developer volume was not modified.
- `git diff --check` passed. Existing CI already runs all Go packages plus frontend/browser/Compose checks on pushes and pull requests; no hosted CI run was triggered. Migration downgrade and power-loss recovery were not tested.
