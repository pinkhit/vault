# Milestone 3 hardening — Checkpoint 7

Reviewed implementation at `2cf8268` and extended existing tests without beginning the separate cleanup refactor. No new application feature, endpoint, migration or runtime dependency was added. The existing uncommitted README guide was preserved.

## Confirmed gaps and fixes

Two new negative cases failed before the fix: Office archives accepted an entry named exactly `..`, and the XML reader accepted duplicate attributes. Both now return the existing malformed-document error through the shared DOCX/XLSX reader. Nothing was unpacked or fetched in either reproduction; these were validation-policy gaps, not demonstrated filesystem escapes or authorization bypasses. Production changes are limited to those guards.

## Coverage reviewed

| Boundary | Verification |
| --- | --- |
| Process crash after a durable claim | Existing subprocess test now exits without cleanup after claiming a real uploaded TXT, reopens storage/SQLite, recovers it on attempt two and verifies searchable content |
| Shutdown during failed publication | New case cancels the worker after an injected passage-write failure, verifies no partial text or ready state, then recovers and publishes on restart |
| Docker recovery | Existing smoke test uploads TXT/PDF/DOCX/XLSX and malformed DOCX, asserts clean server exit, then seeds exact processing/pending states in an offline copy of its own synthetic database before restarting; checks attempt counts, search, previews, sessions, totals and identical original bytes |
| Database failures, stale/deleted work | Existing transaction rollback, publication retry, checksum/attempt fencing, deletion during extraction and retry-budget tests retained |
| Search permissions and deletion | Existing owner/admin/grant tests filter in SQL before ranking/pagination, hide restricted counts/snippets, honor revocation and remove deleted metadata/passages from FTS; deletion is a database-fixture scenario, not a new deletion API |
| Untrusted previews and resource limits | Existing malformed PDF/DOCX/XLSX/image/JSON/CSV, integrity, symlink, cancellation, process, pixel, XML, output, row/column and cell-limit cases retained; added CSV BOM/CRLF/ragged-row case |
| Storage failures | Existing provider failure/cache recovery, stale/invalid host snapshot, demotion during sampling and audit-failure cases retained; unknown capacity stays unknown |
| Sessions, CSRF, audit and cross-user access | Existing Milestone 2 and preview/search/storage negative suites retained; no security boundary relaxed |

The smoke test now requires a host `sqlite3` CLI with STRICT-table support (3.37+); CI installs it alongside PDF test dependencies. This is a test-only tool. The database copy/fault injection touches only the randomly named disposable project while its app is stopped; it is not a production backup/restore procedure. The subprocess test and seeded-container test cover distinct failure boundaries. No timing race against a fast live parser is used to claim recovery coverage.

## Limits and next step

Input/output/structure limits are tested, but peak memory, a hostile file corpus, all browser engines, power loss and native parser exploitation have not been established. Docker recovery uses deterministic offline fault injection; it does not kill a live Poppler process at a chosen instruction. Real ARM/Windows execution and hosted CI remain unverified. HTTPS transport, Secure-cookie delivery, proxy Origin policy and deployment headers require the later HTTPS deployment work. Original downloads remain untrusted when opened in other software.

The cleanup refactor is still pending approval as a separate commit. Ollama, Tailscale, folders, deletion APIs, backups and production deployment remain outside this checkpoint. For an interview, explain how durable state differs from process memory, why ready status and FTS passages commit together, how fault injection avoids timing-dependent tests, and why bounded input is not proof of parser isolation.

Changed files: `backend/internal/{office/package.go,docx/docx_test.go,api/api_test.go,indexing/worker_test.go,preview/preview_test.go}`, `tests/container_smoke.py`, `.github/workflows/ci.yml`, this record, and linked README/security/indexing notes.

Verification on October 29, 2026: Go formatting/vet and the full race suite passed (**80.0% combined statement coverage**); **37 frontend tests**, frontend formatting/strict TypeScript/production build, **four Chrome flows**, Python syntax and the expanded Compose smoke test passed. The existing filename fuzz target passed 12,599 executions with a five-second requested budget, and the Linux ARM64 server cross-build passed. New parser negatives were first observed failing, then passed after the guards were added. Hosted CI was not run. Synthetic Docker projects/volumes were removed; no commits, pushes or refactor were performed.
