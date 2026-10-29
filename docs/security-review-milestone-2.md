# Milestone 2 adversarial security review

Reviewed the implementation based on `aa7480d` (`feat: add authentication and document authorization`) on `feature/authentication`, including its SQL, storage, HTTP middleware and session lifecycle. This is a source review plus adversarial integration testing with synthetic data, not a production certification. The existing local README edits were preserved. No commit, push, merge, Milestone 3, Tailscale or Ollama work was performed.

Three issues were reproduced with failing tests before fixes. No direct anonymous authentication bypass or cross-user document disclosure was reproduced in the tested endpoint matrix. That result does not prove all vulnerabilities absent.

## Confirmed findings and fixes

### 1. Administrator listings trusted an earlier role snapshot

**Medium: revocation consistency and privileged metadata exposure during an interleaving.** `/api/admin/users` and `/api/admin/audit` checked the role loaded by session middleware, then queried private metadata without rechecking current authority. Demotion or disablement between those operations could still return account/audit data.

`TestAdministratorMetadataRechecksRevokedAuthority` uses a deterministic SQLite trigger during session activity update to revoke the role/status and delete sessions after the role read but before the privileged query. Both endpoints returned 200 before the fix in both scenarios. They now return 403 without privileged metadata. This requires an already-authenticated administrator and a revocation interleaving; it was not a member-to-administrator or anonymous bypass.

Fixed in [account services](../backend/internal/auth/users.go) and the [audit endpoint](../backend/internal/api/auth.go): recheck enabled administrator status inside the same transaction as the metadata SELECT. Mutations already had transactional current-authority checks. Data already read/sent by an authorized request cannot be retracted.

### 2. Public static serving followed symlinks outside its web root

**Medium, conditional on static filesystem contents.** `http.Dir` and `http.ServeFile` followed symlinked assets/index files into private directories. An unauthenticated browser could retrieve their target bytes. Reproduction required a pre-existing unsafe static symlink or an actor able to alter the trusted static filesystem; uploads cannot create such links.

`TestStaticSymlinksCannotExposePrivateFiles` reproduced disclosures through `/` and `/assets/leak.txt` before the fix. [Static serving](../backend/internal/api/api.go) now uses `os.OpenRoot`, `Root.FS`, `ServeFileFS` and `FileServerFS`, closing the root after each request. Both regressions pass. A real HTTP traversal test also exercises redirects and encoded paths. Deliberately copying private files into the web root, replacing the configured root itself or controlling the host remains outside this protection.

### 3. Audit records could not reconstruct permission/account changes

**Medium: accountability gap.** Grant/revoke events omitted the recipient and previous/new permission; account events omitted previous/new role and enabled status. Existing actor/document IDs were insufficient to reconstruct authority changes. `TestPermissionAuditIdentifiesGranteeAndChanges` failed against the original implementation.

Added a typed server-side allowlist in [audit recording](../backend/internal/audit/audit.go). Permission events record the target user and before/after grant. Account updates record role/status transitions and only a boolean for password changes. The administrator API exposes these details. Mutation and evidence still commit together. Passwords, hashes, cookies, CSRF tokens, filenames and content are excluded.

[Migration 003](../backend/internal/database/migrations/003_audit_details.sql) adds JSON-valid details and rejects ordinary SQL UPDATE/DELETE of audit events. Existing history is preserved with `{}` details; unavailable historical facts are not invented. Regression tests verify migration preservation, details, SQL rewriting rejection and rollback when audit insertion fails. The triggers prevent accidental/application-level rewriting, not privileged database tampering: the host owner can drop them. No cryptographic log sealing or retention policy was added.

## Authorization predicates and endpoint coverage

The document model is flat and additive: ownership and enabled administrator authority grant full access; explicit `read`, `write` and `manage` grants add rights. A manager may delegate manage. A grant cannot transfer ownership or revoke administrator authority. These are intended rules, not escalation findings.

| Endpoint | Authority enforced by implementation |
| --- | --- |
| `GET/HEAD /api/documents` | [List SQL](../backend/internal/documents/documents.go) joins the actor by bound user ID and applies enabled AND (administrator OR owner OR matching grant) before ORDER/LIMIT/OFFSET. `has_more` is computed only from visible rows. |
| `GET/HEAD /api/documents/{id}/download` | The same [visibility predicate](../backend/internal/authorization/permissions.go), plus bound document ID, precedes object opening, checksum verification and attachment/range/conditional response handling. |
| `POST /api/documents` | Live session plus CSRF; owner ID is taken from the server session. INSERT rechecks enabled status and audit commits in the same transaction. |
| `PATCH /api/documents/{id}` | Current enabled administrator, owner, write or manage authority checked in the rename transaction. |
| `GET /api/documents/{id}/permissions` | Current manage authority checked in the same transaction as grant metadata retrieval. |
| `PUT/DELETE /api/documents/{id}/permissions/{userID}` | Current manage authority checked in the mutation transaction; target and level validated; owner grant changes rejected. |
| Administrator account writes | Current enabled administrator checked in the mutation transaction; last-enabled-administrator invariant enforced. |
| Administrator account/audit reads | Current enabled administrator checked in the private query transaction after this review's fix. |

Values use SQL parameters; the interpolated visibility SQL is a fixed internal constant, never client input. Database state, rather than cached `IsAdmin` fields or client identity headers, governs document access. Schema checks restrict grant levels. Private document names, hashes, ownership and grant recipients are not fetched into unauthorized document responses. Missing and unauthorized IDs return the same 404 for authenticated callers. Anonymous/disabled sessions return 401.

## Verified protections

The new tests live in [authentication adversarial tests](../backend/internal/api/adversarial_test.go), [authorization matrix](../backend/internal/api/authorization_matrix_test.go), [filesystem tests](../backend/internal/api/filesystem_adversarial_test.go), [audit tests](../backend/internal/api/audit_adversarial_test.go) and [migration tests](../backend/internal/database/database_test.go). They extend the existing suite rather than substitute for it.

- **Authentication and fixation:** token hashes, CSRF tokens, user IDs, forged cookies, query tokens and identity/bearer headers cannot authenticate. Attacker-chosen session/CSRF values are not adopted. Successful re-login rotates/revokes the presented session; failed login does not replace or revoke a valid session.
- **Object authorization:** owner, administrator, read, write, manage, ungranted, anonymous and disabled identities exercise listing, downloads, rename and grant operations. Unauthorized HEAD, Range (valid/unsatisfiable), ETag/date conditional requests cannot leak checksum/name/attachment/range metadata. Pagination and `has_more` count visible rows only. Forged cached administrator flags do not override SQL authority.
- **Revocation and session lifecycle:** absolute/idle expiry, logout, all-device logout, password change/reset, disablement and role changes; logout affects the intended account/sessions, account session cap evicts the oldest, and persisted sessions survive restarts. Existing tests cover Secure/HttpOnly/SameSite/Path cookie attributes and hashed token storage.
- **CSRF and browser boundaries:** missing CSRF is denied for every authenticated mutation, including administrator writes and grant deletion. Existing tests reject forged/other-session tokens, missing/null/foreign login origins, foreign Hosts, cross-site Fetch Metadata, malformed JSON and inappropriate content types. No CORS is enabled.
- **Enumeration and timing mechanisms:** known, unknown, disabled and SQL-like usernames receive the same failed-login status/body and no cookie, including a correct password for a disabled account. Case/whitespace share the normalized account throttle. Source inspection confirms dummy Argon2 hashing and `crypto/subtle` key comparison in the installed password library. Persistent account/IP limits ignore spoofed forwarding headers.
- **Filesystem:** traversal/encoded separators/SQL-like IDs do not reveal private bytes over actual HTTP. A symlinked object is rejected even when its target matches the expected size/hash. Existing no-follow, FIFO/nonblocking, filename validation, crash reconciliation and integrity tests remain passing.
- **Audit integrity:** grant transitions identify recipient and old/new rights; role/password changes have allowlisted details; normal SQL cannot update/delete history or insert invalid JSON. Injected audit INSERT failures cannot publish account creation/promotion/password reset, password change, rename, logout, all-session revocation, login rotation or download bytes. Existing upload/grant rollback and credential-redaction tests remain passing.

## Checks actually executed

Backend checks ran in the local Go 1.26.10 Alpine development image because the host has no Go toolchain. Frontend checks used host Node 24/npm. Browser tests used installed Google Chrome and freshly cross-built native Go binaries with isolated temporary data. Tests did not bootstrap or alter the regular developer account/data volume.

| Check | Actual result |
| --- | --- |
| Go formatting and `go vet ./...` | Passed |
| `go test -race -count=1 -coverpkg=./... -coverprofile=... ./...` | All packages passed; **75.2% combined statement coverage**. Command startup/interactive code has no unit coverage. |
| `FuzzValidateName`, five-second requested duration | Passed; **152,226 executions** observed |
| CGO-disabled Linux amd64 and arm64 server builds | Passed; ARM64 hardware execution not tested |
| macOS amd64 server and administrator CLI cross-builds | Passed; native binaries used by the browser integration test |
| `npm run format:check`, `npm test`, `npm run build` | Passed; **10 frontend tests**, TypeScript/build successful |
| `npm run test:e2e` | **1 browser test passed**, desktop/mobile login/upload/reload/exact download/logout flow |
| `./tests/compose-smoke.sh` | Passed: real image, loopback publishing, isolated bootstrap/login, unauthorized rejection, checksum upload/download, container restart and session/file persistence, logout rejection. Only its own temporary project/volume removed. |
| `govulncheck` via `golang.org/x/vuln` v1.9.0, including verbose inspection | **0 reachable**, **0 imported-package** vulnerabilities; **1 module-only advisory**, described below |
| `npm audit --audit-level=high` | **0 vulnerabilities** reported |
| `git diff --check` | Passed |

The Go module advisory is [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), for the deprecated, unmaintained `golang.org/x/crypto/openpgp` family, with no known fixed version. SafeVault requires `x/crypto` v0.58.0 for Argon2id but does not import or call OpenPGP; the scanner reports this at module level only. This is not a completely advisory-free dependency tree. Do not introduce those OpenPGP packages. Advisory results are a point-in-time check, not a supply-chain guarantee.

Initial negative tests for the three findings failed before the fixes; only successful final reruns are listed as passes. One later Docker rerun omitted the repository fixture mount and failed to open `tests/fixtures/fictional-invoice.txt`; the complete suite was rerun with that required mount restored. The earlier [Milestone 2 verification record](verification-milestone-2.md) remains a separate historical baseline, including its original coverage/fuzz numbers. No GitHub CI run was triggered.

## Unverified and deployment-dependent protections

- **Timing resistance:** equal error bodies, dummy hashing and constant-time key comparison are verified mechanisms. No controlled statistical end-to-end timing evaluation or formal constant-time proof was performed. Database lookups, throttling and scheduling may still create observable differences.
- **HTTPS:** localhost development still uses plain HTTP and non-Secure cookies. Secure/`__Host-` cookie attributes are tested in isolation; end-to-end browser delivery over TLS, transport confidentiality/integrity, HSTS, HTTPS Origin validation and trusted-proxy handling remain unimplemented/unverified. Current login Origin policy deliberately requires `http://<host>`. Setting the cookie flag alone does not supply TLS or make remote deployment safe.
- **Concurrency and host trust:** deterministic revocation ordering and race-detector checks are not exhaustive schedule exploration. Already authorized/read requests can finish after revocation. No exhaustive filesystem race/hard-link analysis or protection against malicious host/Docker/process owners is claimed. Such an owner can rewrite DB/content, remove audit triggers or alter served assets.
- **Operations:** audit/storage have no total quota or retention, so disk exhaustion remains possible. No production backup/restore drill, power-loss simulation, ARM64 hardware execution, MFA/recovery workflow, production proxy deployment or external penetration test was performed. Audit history is not tamper-evident against the host owner.

Keep the application localhost-only with synthetic documents. The regular running developer container was not recreated as part of this review. To apply these working-tree fixes and migration to that instance after review, run `docker compose up --build -d --wait`, preserving the existing named volume and accounts; do not repeat bootstrap or reset the database. No downgrade procedure has been validated.

Review criteria were informed by the OWASP [authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html), [authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) and [logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) guidance, alongside Go's [vulnerability tooling documentation](https://go.dev/doc/security/vuln/). Findings above come from the actual implementation and reproductions, not documentation assertions.
