# Milestone 2 threat model

**Authenticated localhost development. Synthetic documents only.** Protected APIs require a live server-side session and document authorization. Localhost remains an exposure boundary, not a replacement for those checks. Never use Tailscale Serve/Funnel, router forwarding or a public proxy at this stage.

## Boundaries and assumptions

The browser and upload bytes are untrusted. The Go process validates HTTP inputs. SQLite and object storage are private backend resources under one configured directory or Docker volume. The host OS, local filesystem, Docker runtime, process owner and dependency supply chain are trusted. The extraction worker, Ollama and tailnet boundaries do not exist in this milestone.

Storage directories must be owned by the application and not concurrently edited by other processes. `os.Root` confines operations; strict random ID validation prevents traversal; symlink leaves are rejected for downloads and storage directories. Download descriptors use atomic no-follow/nonblocking opens plus descriptor type/size checks, so a substituted symlink/FIFO cannot be followed or block the verifier. These checks do not defend against a malicious host administrator, a compromised process owner, hard-link manipulation by a local actor, or arbitrary filesystem races. A privileged host/process owner can still modify files or SQLite directly; no application permission model can protect against that trusted operator. Production operational hardening remains later work.

| Threat | Implemented control | Remaining limit |
| --- | --- | --- |
| Unauthenticated access / cross-user leakage | Hashed server-managed sessions; owner/admin/explicit document grants; SQL filtering before pagination and file access; negative tests | Already authorized in-flight requests may finish; administrators have full authority by design |
| Drive-by browser access / DNS rebinding | Local Host allowlist, exact login Origin, session-bound CSRF on authenticated mutations, Fetch Metadata rejection, no CORS | Local host/process owner remains trusted; remote deployment is prohibited |
| Password guessing / hash theft | Argon2id with per-password salts, generic login failures and dummy hashes; persistent account/IP budgets; two concurrent login hashes | Known-account denial of service through temporary throttling remains possible; no MFA or distributed abuse defense |
| Session theft / fixation | Fresh random 256-bit tokens, hashed database storage, HttpOnly/SameSite Strict cookies, absolute/idle expiry, logout/all-device revocation; role/password/status changes revoke sessions | HTTP localhost explicitly disables Secure; TLS, MFA and remote access are later work |
| Permission escalation / stale grants | Server-side checks from database state, transactions around permission mutations, immutable ownership through grant APIs; role changes revoke sessions | Flat additive grants; delegated manage includes delegation; no folders/inheritance yet |
| Administrator lockout | Offline initial bootstrap under file lock; no default credentials/public registration; transactions preserve an enabled administrator | No tested lost-password/account-recovery workflow; host operator must protect credentials |
| Traversal / malicious filenames | Validate raw multipart filename; reject slashes, controls, invalid UTF-8; use generated IDs | Filename is retained as display metadata; it may contain hostile-looking text |
| XSS / active uploaded HTML | React escaping, CSP, nosniff, attachment/octet-stream downloads | Downloaded files remain untrusted when opened in other software |
| Upload exhaustion | Default 25 MiB/file, bounded multipart overhead, 8 active requests, HTTP timeouts, streaming storage | No total storage quota, per-user upload quota or upload rate limit; local callers can fill the disk |
| SQL injection | Parameterized values, fixed queries, schema constraints | Dependency vulnerabilities and local DB tampering remain possible |
| Interrupted upload / crash | Private staging, sync before metadata, exclusive process lock, startup reconciliation | No distributed transaction; power-loss semantics depend on storage |
| Byte corruption | SHA-256 on upload, optional client digest, full verification at startup and download | SHA-256 is an integrity check, not proof of authenticity; host can alter DB and object together |
| Sensitive logging | Request IDs; no file content, filenames, URLs or query strings in access logs | Internal errors may include local filesystem paths; protect server logs |
| Container compromise | Non-root UID, read-only root filesystem, dropped capabilities, no privilege escalation | Host Docker control grants broad access; image tags are not digest pinned |
| Data loss | Persistent named volume, corruption fails closed | No automated backup/restore, redundancy, version history or production recovery guarantee |

Security/document audit events are stored in SQLite with request IDs; mutations and their audit events commit together. Passwords, cookie/CSRF values, filenames and document contents are not recorded. Audit logs are not tamper-evident against a host administrator, and retention/rotation is not implemented.

Malware scanning, encryption at rest, deletion, AI, remote access, production backups/restoration, MFA and complete operational monitoring are not implemented. Production use still requires later milestones and restore testing. See [authentication details](authentication.md) for exact session, CSRF, permission and initial-account behavior.
