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

The approved follow-up cleanup refactor is recorded below as a separate increment. Ollama, Tailscale, folders, deletion APIs, backups and production deployment remain outside this checkpoint. For an interview, explain how durable state differs from process memory, why ready status and FTS passages commit together, how fault injection avoids timing-dependent tests, and why bounded input is not proof of parser isolation.

Changed files: `backend/internal/{office/package.go,docx/docx_test.go,api/api_test.go,indexing/worker_test.go,preview/preview_test.go}`, `tests/container_smoke.py`, `.github/workflows/ci.yml`, this record, and linked README/security/indexing notes.

Verification on October 29, 2026: Go formatting/vet and the full race suite passed (**80.0% combined statement coverage**); **37 frontend tests**, frontend formatting/strict TypeScript/production build, **four Chrome flows**, Python syntax and the expanded Compose smoke test passed. The existing filename fuzz target passed 12,599 executions with a five-second requested budget, and the Linux ARM64 server cross-build passed. New parser negatives were first observed failing, then passed after the guards were added. Hosted CI was not run. Synthetic Docker projects/volumes were removed; no commits, pushes or refactor were performed.

## Follow-up refactor

Starting from committed hardening `1947529`, consolidated API test request construction, Docker multipart uploads/indexing waits, UI-test deferred promises, DOCX fixture parts, Office-preview error mapping and byte formatting. Shared Office limits replace constants left only in DOCX tests. These code/test changes remove **58 net lines**; the pre-existing README guide is excluded from that count. No test cases were deleted. A declaration comparison retained all 108 Go test functions, and the same 37 frontend cases passed. Requests without an Origin header, exact indexing attempt counts, per-format malformed/limit errors and separate binary/decimal unit conventions are preserved.

Changed files: `backend/internal/api/{api_test.go,indexing_test.go,security_test.go}`, `backend/internal/docx/{docx.go,docx_test.go}`, `backend/internal/office/package.go`, `backend/internal/preview/preview.go`, `backend/internal/testutil/docx.go`, `frontend/src/{format.ts,test-utils.ts,DocumentPreview.test.tsx,DocumentSearch.test.tsx,StorageOverview.test.tsx}`, `tests/container_smoke.py` and these review notes. Permission predicates, audit transactions, schema, file layout and request-cancellation ownership remain explicit in their existing layers. Similar-looking security tests were retained where they exercise different boundaries.

Verification: full Go formatting/vet/race checks passed (**80.1% combined coverage**), dependency tidy reported no changes, and the Linux ARM64 build passed. Frontend formatting, all 37 tests, strict TypeScript/production build, all four Chrome flows and the refactored Docker recovery smoke test passed. Both byte formatters matched the committed implementation on 32 boundary checks. The coverage percentage changed with the implementation denominator; it is not additional security coverage. Hosted CI remains unverified.

## Structural test consolidation

The earlier 58-line pass consolidated helpers while retaining all tests; it did not establish that the remaining tests or abstractions were necessary. This second pass started at `eea0acd` and removed overlapping suites according to the contracts below. It changes tests and documentation, with no runtime behavior change.

| Contract | Authoritative coverage after consolidation |
| --- | --- |
| Static owner/admin/read/write/manage/anonymous/disabled access; private download headers and conditional requests; listing visibility | `TestDocumentEndpointAuthorizationMatrix` and `TestListingFiltersBeforePaginationAndAfterRevocation` |
| Upload ownership, forged client identity, self-escalation, real HTTP grant changes/delegation, owner invariance, invalid levels and revocation | `TestOwnershipAndGrantTransitions`; rename persistence is now explicitly checked |
| Missing, forged and same-user/other-session CSRF tokens on every authenticated mutation | `TestEveryAuthenticatedMutationRejectsInvalidCSRF`: one nine-endpoint matrix replaces two partially overlapping enumerations and expands invalid-token checks to admin mutations |
| Login Origin/JSON contract; generic known/unknown/disabled failures, canonical names, throttling/retry hints and recovery | `TestLoginOriginAndJSONContract` and `TestGenericAuthenticationFailureAndCanonicalThrottle`; the separate repeated throttling fixture is removed |
| Audit rollback of account/session/document/grant state; failed uploads preserve existing content and publish no new metadata/jobs/audit | `TestAuditFailureCannotPublishSecurityMutations` compares one state snapshot; `TestUploadJobAndAuditAreOneTransaction` now starts with a verified existing document |
| Common preview headers, parameters, hidden-object equivalence, forged role, revocation and expiry | `TestPreviewHTTPContractAndRevocation` and the role matrix; per-format authorization cross-products removed because all parsers receive an already-authorized descriptor from the same service |
| DOCX/XLSX extraction, malformed input, format correctness, search/status and original preservation | Existing parser suites, four browser flows and Docker recovery smoke test retained; duplicated format/status authorization fixtures removed |
| PDF/XLSX paging, old-content removal, disabled controls and request page | One two-case frontend navigation test retains both rendered formats and their specific content assertions |

The storage guide now links to current verification instead of repeating successive checkpoint run logs. Migration, in-flight revocation, crash recovery, database-failure and parser-limit cases remain separate because they exercise distinct failure boundaries. The runtime remains one Go server, SQLite and one worker. This review does not prove that every production abstraction is minimal; removing parser policies or adopting a full Office library would require a separate behavior/dependency decision.

Three deliberate regressions were tested only in disposable container copies: bypassing CSRF, removing the document visibility predicate, and committing a grant after audit failure. The consolidated matrices/snapshot test rejected all three. These are spot checks, not exhaustive mutation testing. No broken variant was written to the working implementation.

Verification: Go formatting, vet and the complete race suite passed (**80.1% combined statement coverage**); frontend formatting, **37 frontend tests**, strict TypeScript/production build, **four Chrome flows** and the Compose recovery smoke test passed. The production frontend bundle is unchanged. Hosted CI was not run. The pre-existing README changes were preserved; no commits or pushes were made.

The randomly named smoke project cleaned up successfully. Docker refused both normal and forced removal of the separate `safevault-structural-check` browser container, reporting a zombie PID; that synthetic container/volume remain pending cleanup after Docker recovers. Docker Desktop was not restarted because that would interrupt the regular application.

## Document deletion follow-up

Added uploader/admin deletion at the user's request after the cleanup review. The library and search share one confirmation/error-handling control. The server checks current enabled ownership/admin authority directly in the SQL DELETE; grants never confer deletion. Existing cascades, FTS triggers and startup reconciliation handle derived data and orphan cleanup, without a migration, dependency or new worker. See [permission and deletion semantics](authentication.md#permission-rules), including cleanup failure and secure-erasure limits.

Extended the existing role, CSRF, expiry and audit-rollback suites for DELETE. Focused integration cases verify owner/admin success, repeat/missing IDs, inaccessible deleted endpoints, original/derived-data removal, retained audit history, another original surviving, late indexing completion, cleanup recovery, symlink safety and invalid storage keys. Frontend checks cover hidden controls on shared documents, admin access, cancellation, retry, search refresh, session expiry and CSRF/204 handling.

Verification: Go formatting/vet and the full race suite passed (**80.3% combined statement coverage**), as did **42 frontend tests**, frontend formatting/strict TypeScript/build, **four Chrome flows** (now including real delete confirmation/cancellation and reload) and the existing Docker recovery smoke test. No hosted CI or production secure-erasure test was run. All synthetic test projects/volumes, including the previously stuck structural-review container, were removed successfully after Docker recovered. No commits or pushes were made.
