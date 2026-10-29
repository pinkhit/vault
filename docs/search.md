# Document search

`GET /api/search?q=printer&limit=20&offset=0` requires a live session. It searches filenames, metadata and published passages using SQLite FTS5. Checkpoint 5A adds the search interface to the existing document browser. Checkpoint 5B adds [authenticated file previews](previews.md) as a separate reviewable change. Search introduces no external service, AI or remote access.

## Search interface — Checkpoint 5A

Search runs on explicit submission, with 20 results per page. Enter submits; clear returns to the library, refreshes its data and restores focus to the search input. Loading, empty, invalid-query and retry states retain the submitted query label. Queries are encoded with `URLSearchParams`, sent with same-origin credentials and kept only in component memory. Changing a query resets pagination; the next-page control respects the API offset bound.

Each response is associated with its submission object and refresh revision. Old responses are hidden immediately on a new search/refresh, pending requests are aborted, and late responses cannot restore older snippets. Authentication failures unmount the authenticated browser and clear its search state. Authorization still happens on the server; the UI never filters results by owner. New requests recheck current access; there is no push notification for permission changes or retroactive erasure of already-read data.

Snippets and filenames use React text nodes. Source links use generated document IDs and the existing authenticated attachment endpoint; page/section references are shown when supplied. No HTML injection, inline file viewer, CSP relaxation or cookie changes were introduced. Results and the library show indexing indicators; refresh updates them without automatic polling. List/get/search metadata now includes optional `indexing_status` from the current-checksum job under the existing SQL authorization predicate, avoiding one status request per displayed document. An absent/current-source-mismatched job displays an unavailable state.

The form uses an associated label, keyboard submission, focus restoration, live status/error messages and responsive wrapping. Desktop and 390-pixel mobile screenshots were inspected; formal screen-reader/accessibility certification and real mobile hardware testing were not performed. The existing layout, upload workflow and download behavior remain in use.

## Query and response contract

- `q`: 1–512 UTF-8 bytes, at most eight words or double-quoted phrases. Words/phrases are combined with AND. Example: `"supplier invoice" INV-2026-001`.
- Each clause is passed as a quoted FTS literal in a bound SQL parameter. Boolean keywords are literal words, not operators. Unbalanced/glued quotes, control characters and bare FTS operators/column selectors are rejected. Quoted punctuation is literal input to the tokenizer; escaping embedded double quotes is not supported.
- `limit`: defaults to 20, range 1–50. `offset`: defaults to 0, range 0–10,000. Duplicate or unknown parameters are rejected. Invalid queries/parameters return 400, missing/expired sessions 401, detected context interruption 503, and internal failures a generic 500 without logging the query.
- The service applies a two-second query context. Token search is case-insensitive with Unicode61 diacritic handling. Hyphens/underscores remain token characters, so `INV-2026-001` matches a whole identifier rather than a prefix or Boolean expression. This also means parts of a hyphenated filename are not independent name tokens.

The response is `{ "results": [...], "has_more": false }`. Each hit contains the existing `document` metadata object, a plain-text `snippet`, `match` (`filename`, `metadata` or `content`), and page/section references when applicable. PDF pages are one-based; TXT omits page zero but retains its section. Snippets use up to 24 tokens and are capped at 768 UTF-8 bytes. They are untrusted text, never server-generated HTML; the search UI renders them as text nodes.

Metadata search covers document ID, filename, media type, size, checksum and creation timestamp/date. Names/metadata are available even when extraction is pending or unsupported. Content is available only from a ready job whose checksum and passage checksum match the current document. All query clauses must match the same metadata entry or passage; matches are not combined across separate pages/passages.

## Authorization, ranking and pagination

Both SQL search branches reuse the Milestone 2 enabled-user AND (administrator OR owner OR explicit grant) predicate. Authorization and source/readiness checks occur before rows enter the materialized hit set, document deduplication or pagination. Actor role flags from request/session snapshots do not override current database authority. Permission revocation affects subsequent searches without reindexing; already-read responses may finish under the existing request policy.

One best hit is returned per visible document. Filename matches rank above metadata, then content; capped row-local match frequency breaks relevance ties. Page/section select the best passage, and creation time/document ID provide deterministic final ordering. Raw ranks and global result totals are not exposed. `has_more` uses one extra authorized, deduplicated document; hidden rows cannot create empty pages or inflate it.

Global BM25/IDF statistics are deliberately unused because restricted documents could otherwise affect visible ranking. The ranking inputs here are local to matching visible rows. Tests compare the complete result payload before/after adding restricted, higher-ranked matches. This is not a claim of timing-oblivious execution or a production performance benchmark. Offset pagination reflects current data/permissions rather than a frozen multi-request snapshot.

## Index maintenance and upgrades

Migration 006 backfills metadata and existing passages without changing originals, job status/attempt counts or access rights. Metadata FTS rows join by document ID. Passages gain an explicit integer primary key while retaining document/page/section/content/checksum values; external-content FTS uses that stable key. This avoids relying on implicit rowids, which SQLite [may change during VACUUM](https://www.sqlite.org/lang_vacuum.html).

SQL triggers maintain filename/metadata indexes on document insert/update/delete and passage indexes on passage insert/update/delete. They share the existing upload, rename, source invalidation and worker publication transactions: rollback also rolls back search changes. Passage text is reused through FTS external content instead of another full text copy. Current authorization/source predicates remain mandatory even when an index entry exists.

Rebuild/restart with the existing named volume to apply the migration; do not reset data or repeat account bootstrap. The regular developer volume was not modified during verification. Index corruption/repair operations, downgrade, production benchmarking and backup restoration have not been validated. Substring/prefix search, stemming, cross-passage conjunctions, OCR and semantic retrieval are outside this checkpoint.

For an interview, explain the difference between an inverted index and authorization, why pagination must follow permission filtering, how transactional triggers prevent index drift, why stable keys matter for external content, and why ranking based on hidden corpus statistics can leak information. The [SQLite FTS5 documentation](https://www.sqlite.org/fts5.html) describes the tokenizer, literal syntax, snippets and external-content mechanisms used here.

## Checkpoint 4 verification

- Go 1.26.10/Linux formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was **78.9%**. Tests cover metadata/content/identifier matching, page references, document deduplication, ranking, pagination, current grants/revocation, disabled accounts and forged cached roles. Adding higher-ranked restricted documents leaves another user's complete search payload unchanged.
- Migration/backfill, rename/audit rollback, unfinished/current-source filtering, replacement/deletion, FTS integrity after `VACUUM`, malformed queries, injection attempts, cancellation and query-log redaction tests passed. The bounded literal-parser fuzz run passed with **58,513 executions** observed in its five-second requested duration.
- All **10 frontend tests**, formatting, TypeScript/production build and **1 desktop/mobile browser regression test** passed. The browser test exercises existing file operations, not a new search UI. Linux amd64/arm64 server and macOS amd64 server/CLI builds passed; ARM64 hardware was not tested.
- The isolated Compose test verified authenticated filename, invoice-identifier and extracted-content search after restart, original-byte checksums, persisted sessions and logout rejection. Only its own synthetic project/volume was removed; the regular developer accounts/data were untouched.
- `git diff --check` and Python syntax checks passed. Existing CI includes the new Go tests/fuzz seeds and extended Compose test on pushes/PRs. No hosted CI run, production benchmark, downgrade/restore drill or statistical timing evaluation was performed.

## Checkpoint 5A verification

- Frontend formatting, **19 unit/integration tests**, strict TypeScript checks and production build passed. Tests cover shared-document rendering, encoded requests, literal malicious-looking text, stale/aborted responses, query changes/pagination, refresh, UTF-8 bounds, empty/error/retry states, indexing indicators and sign-out/session-expiry cleanup. An initial build exposed typing errors in new tests; they were corrected before the successful rerun.
- Go formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was **78.9%**. Additional current-source status projection tests passed, including omission of obsolete job state. Linux amd64/arm64 server and macOS amd64 server/CLI builds passed.
- **Two browser flows passed** with installed Chrome and freshly built native binaries: existing file operations plus real indexed-text search, literal untrusted filenames/snippets, source-byte downloads, desktop/mobile layout, empty/error recovery and clearing results after server-side session revocation. PDF reference display is covered by UI fixtures; no inline PDF preview was added.
- The isolated Docker persistence/search test passed with the rebuilt UI. It touched only its own synthetic project/volume. `git diff --check` passed; no hosted CI run was triggered. The existing CI workflow runs the new tests. At that Checkpoint 5A verification, file previews had not started; their implementation is documented in [previews](previews.md).
