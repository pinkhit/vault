# Milestone 2 verification

Recorded on October 28, 2026. Milestone 1 is integrated into `dev` at `de404c3` (merge of PR #2); `feature/authentication` was created from the fetched, matching `dev`/`origin/dev`. The existing uncommitted README was carried forward and updated. No commits, pushes or merges were performed.

| Check actually run | Result |
| --- | --- |
| Go formatting and `go vet ./...` | Passed |
| `go test -race -coverpkg=./... -coverprofile=... ./...` | All packages passed; **73.4% combined statement coverage**, including command startup/interactive paths that have no unit coverage |
| Filename fuzzing, bounded five-second request | Passed; **152,985 executions** observed |
| Linux AMD64/ARM64 server builds with CGO disabled | Passed |
| Linux ARM64 initial-account CLI build | Passed; Linux AMD64 CLI also built in the Docker image |
| Native macOS AMD64 server and CLI cross-builds | Passed and executed in the browser test |
| Frontend Prettier check | Passed |
| Frontend unit tests | **10 passed** |
| Strict TypeScript and Vite build | Passed natively and in Docker |
| Browser end-to-end test | **1 passed** using headless local Chrome: initial sign-in, private upload with client SHA-256, reload/session persistence, identical download, checksum display, desktop/mobile layout, sign-out and signed-out reload; no page JavaScript errors |
| Compose configuration, image build and health check | Passed |
| Isolated Docker integration smoke test | Passed: random initial credentials in a disposable project, anonymous rejection, login, upload, session/file persistence across restart, identical bytes/checksum, logout and subsequent 401 |
| Shell/Python smoke harness syntax and `git diff --check` | Passed |
| Regular development container after upgrade | Healthy at `127.0.0.1:8080`; public health returns 200 and anonymous document listing returns 401. Existing volume retained; no initial account/password was installed by tests. |

The host does not have Go installed. Backend checks ran in a Go 1.26.10 Linux Docker test toolchain with the host's backend and shared fixtures mounted. Native Mac binaries were cross-compiled from the same source. A temporary `go run` shim launched those binaries for the unchanged Playwright server command; it is not part of the repository. Default setup and CI still use the real Go toolchain.

## Security regressions exercised

- Missing, malformed and forged sessions cannot list, upload, download, rename, manage permissions, manage accounts or inspect audit events; public health remains available.
- Users cannot access another user's bytes, names, checksums, permission lists or mutation endpoints through ID guessing or forged identity/admin headers.
- Explicit read/write/manage grants enforce their respective rights; owners and administrators retain their defined authority. Listing filters before pagination and immediately excludes revoked grants.
- Missing, forged and other-session CSRF tokens are denied. Login rejects absent/null/foreign origins, non-JSON content, unknown fields and malformed/multiple JSON values.
- Re-login rotates and revokes the presented session. Cookies have HttpOnly, SameSite Strict, Path and expiry attributes; Secure/__Host- mode is checked separately. SQLite contains token hashes rather than raw session cookies.
- Absolute and idle expiry, logout, all-device revocation, password changes/resets, disabled accounts and role changes invalidate access as intended. Session state survives database/server restart.
- Known/unknown login failures are generic; account and IP throttles apply, including attempts to spoof forwarding headers. Concurrent account changes cannot remove the last enabled administrator.
- Argon2id parameters, random salts, correct/incorrect passwords, Unicode and input bounds are tested.
- Audit endpoints are admin-only, correlate request IDs and omit passwords, session/CSRF tokens, names and content. Audit failures roll back uploads and grants.
- An actual database using only migration 001 and a real stored object was upgraded, kept inaccessible before account setup, assigned to the first administrator, and downloaded with identical original bytes/checksum.
- Original interrupted-upload, checksum, crash recovery, symlink/traversal and file-permission regressions still pass. A FIFO cannot block download verification; no-follow flags remain on the actual open.

Initial runs found and fixed a no-follow-open error classification mismatch and a TypeScript test option unsupported by Testing Library. The new disposable Docker test also exposed ephemeral host ports changing across restarts; the test now rediscovers and checks the loopback mapping. These failures are not counted as passes; the results above are from successful reruns.

## Limits of this evidence

No GitHub Actions run was triggered. No real ARM64 hardware, remote HTTPS/Tailscale access, MFA, password recovery, production backup/restore, power-loss simulation, penetration test or performance benchmark was performed. Secure cookie attributes are unit-tested; an end-to-end HTTPS deployment is not implemented. Interactive masked terminal password entry uses the established terminal library; automated container/browser setup exercises the stdin mode with synthetic credentials. Audit logs are not tamper-evident against the host owner. Existing authorized requests may finish after revocation; subsequent requests recheck current state.

The smoke test creates/removes only its own random Compose project/volume, never the regular developer volume. Browser test data lives in isolated OS temporary directories. Tests do not install default accounts or passwords into the regular application. See [authentication/setup](authentication.md), [threat model](threat-model.md), and the historical [Milestone 1 record](verification.md).
