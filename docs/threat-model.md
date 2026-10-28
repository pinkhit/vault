# Milestone 1 threat model

**Unauthenticated development prototype. Synthetic documents only.** Any local user or process able to reach the listener can upload, list and download every document. Localhost is an exposure limit, not user authentication. Never use Tailscale Serve/Funnel, router forwarding or a public proxy with this milestone.

## Boundaries and assumptions

The browser and upload bytes are untrusted. The Go process validates HTTP inputs. SQLite and object storage are private backend resources under one configured directory or Docker volume. The host OS, local filesystem, Docker runtime, process owner and dependency supply chain are trusted. The extraction worker, Ollama and tailnet boundaries do not exist in this milestone.

Storage directories must be owned by the application and not concurrently edited by other processes. `os.Root` confines operations; strict random ID validation prevents traversal; symlink leaves are rejected for downloads and storage directories. These checks do not defend against a malicious host administrator, a compromised process owner, hard-link manipulation by a local actor, or arbitrary filesystem races. Complete production file-handling hardening remains later work.

| Threat | Implemented control | Remaining limit |
| --- | --- | --- |
| Unauthenticated access / cross-user leakage | Loopback binding and publishing; visible warning | No accounts or authorization; all local callers share the library |
| Drive-by browser access / DNS rebinding | Local Host allowlist, same-origin Origin check, cross-site Fetch Metadata rejection, no CORS | No sessions or production CSRF scheme; local non-browser callers are trusted |
| Traversal / malicious filenames | Validate raw multipart filename; reject slashes, controls, invalid UTF-8; use generated IDs | Filename is retained as display metadata; it may contain hostile-looking text |
| XSS / active uploaded HTML | React escaping, CSP, nosniff, attachment/octet-stream downloads | Downloaded files remain untrusted when opened in other software |
| Upload exhaustion | Default 25 MiB/file, bounded multipart overhead, 8 active requests, HTTP timeouts, streaming storage | No total storage quota, per-user limits or rate limit; local callers can fill the disk |
| SQL injection | Parameterized values, fixed queries, schema constraints | Dependency vulnerabilities and local DB tampering remain possible |
| Interrupted upload / crash | Private staging, sync before metadata, exclusive process lock, startup reconciliation | No distributed transaction; power-loss semantics depend on storage |
| Byte corruption | SHA-256 on upload, optional client digest, full verification at startup and download | SHA-256 is an integrity check, not proof of authenticity; host can alter DB and object together |
| Sensitive logging | Request IDs; no file content, filenames, URLs or query strings in access logs | Internal errors may include local filesystem paths; protect server logs |
| Container compromise | Non-root UID, read-only root filesystem, dropped capabilities, no privilege escalation | Host Docker control grants broad access; image tags are not digest pinned |
| Data loss | Persistent named volume, corruption fails closed | No automated backup/restore, redundancy, version history or production recovery guarantee |

No authentication, sessions, permissions, malware scanning, encryption at rest, audit trail, deletion, AI, or remote access is implemented. Production use requires the later security and operational milestones and real restore testing.
