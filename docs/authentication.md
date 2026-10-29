# Accounts, sessions and document permissions

Milestone 2 is localhost-only development. Use synthetic documents. No Tailscale, Ollama, remote deployment or production backup workflow is implemented.

## First administrator and upgrades

Stop the server before creating the first account. With the image built:

```sh
docker compose stop app
docker compose run --rm --no-deps --entrypoint /app/create-admin app -username admin
docker compose up -d --wait
```

The command prompts twice without echoing the password. There is no default password and no public setup endpoint. It refuses setup if any account exists, and the storage lock prevents running it beside the server. For automation only, `-password-stdin` accepts a private pipe/file; do not pass passwords as arguments or environment variables. Native equivalent: `FAMILYVAULT_DATA_DIR=../data go run ./cmd/create-admin -username admin` from `backend/`.

Migration 002 adds users, sessions, permission grants, login limits, audit events and nullable document ownership. Until bootstrap, no ordinary API access is possible. Bootstrap assigns unowned Milestone 1 metadata to the first administrator in one transaction. Original objects and checksums do not change. Subsequent uploads always receive the authenticated uploader's ID. Never delete the volume or reset the database to perform an upgrade. Production migration rollback and complete backup/restore remain untested.

Migration 003 adds validated JSON audit details and triggers that reject ordinary SQL updates/deletes of audit history. Existing events retain their original fields and receive `{}` details; missing historical recipients or transitions cannot be reconstructed. Rebuild/restart the application to apply this migration automatically. No repeat bootstrap or volume reset is needed.

## Passwords and sessions

Usernames are normalized to lowercase ASCII and must be 3–64 letters/digits/dots/underscores/hyphens, beginning with a letter or digit. Passwords require at least 15 Unicode characters and at most 1024 UTF-8 bytes; they are not trimmed or silently truncated.

Passwords use the established [alexedwards/argon2id wrapper](https://github.com/alexedwards/argon2id), with 19 MiB memory, two passes, one lane, a random 16-byte salt and a 32-byte key. These parameters follow the [OWASP password-storage baseline](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html). Passwords and raw session tokens are never stored in the database or logs.

Successful login creates a new random 256-bit session token and an independent CSRF token. SQLite stores SHA-256 of the session token, the CSRF token, user ID, absolute expiry and last activity. The CSRF token is not an authentication credential. Only the raw session token goes into the HttpOnly, SameSite=Strict, Path=/ cookie; no Domain is set. Re-login revokes the presented previous session, so an existing token is never promoted into an authenticated session. Up to ten active sessions per account are retained.

Default expiry is eight hours absolute and thirty minutes idle. Each authenticated request checks the database, account status and both expiries; activity cannot extend the absolute deadline. Sessions survive server/container restarts. Logout revokes the current session. All-device logout, password change, administrator password reset, account disablement and role changes revoke every session for that account. Re-enabling an account does not resurrect old sessions. Requests already authorized and in progress may finish; subsequent requests use current permissions/session state.

Local HTTP development explicitly uses `FAMILYVAULT_COOKIE_SECURE=false`. Setting it true uses Secure and the `__Host-familyvault_session` name, but does not create HTTPS. HTTPS deployment is a later milestone. Do not expose either mode remotely yet. This model follows the [OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html).

## CSRF and brute-force protection

Login requires JSON and an exact `Origin: http://<request-host>` header, including the port when present. This also applies to command-line clients. Authenticated mutations require `X-CSRF-Token` matching the current session. `GET /api/auth/session` supplies it to an already authenticated same-origin browser. Missing/foreign/null origins are rejected at login; foreign origins and cross-site Fetch Metadata are rejected globally. CORS is not enabled. Cookies alone never authorize a state-changing request.

Five login attempts per normalized account and fifty per socket peer IP are allowed per fifteen-minute window. Limits persist in SQLite and ignore `X-Forwarded-For`. A successful login resets the account budget; the IP budget still limits repeated successful logins. Unknown accounts use a dummy Argon2id hash, and invalid/disabled/unknown credentials receive the same error. Two concurrent login hashes and eight overall requests bound resource consumption. The bounded limiter table fails closed at capacity. Failed logins can temporarily lock out a known account; this is a documented availability tradeoff, not a distributed abuse-defense system.

## Permission rules

| Authority | Read/list/download | Rename | Grant/revoke permissions | Manage accounts/audit |
| --- | --- | --- | --- | --- |
| Document owner | Yes | Yes | Yes | No |
| Explicit `read` | Yes | No | No | No |
| Explicit `write` | Yes | Yes | No | No |
| Explicit `manage` | Yes | Yes | Yes | No |
| Enabled administrator | All documents | All documents | All documents | Yes |
| Other/disabled/anonymous | No | No | No | No |

Grants are additive, per document, with no deny overrides or inheritance. Owners cannot be changed through the grant endpoint, and revoking a grant does not remove ownership or administrator access. A delegated manager may grant `manage` to others. Account disablement preserves document ownership and grants while blocking their use. Folders do not exist yet, so folder inheritance is deliberately not invented here.

Filtering happens in SQL before listing pagination and before opening a file for download. ID secrecy and frontend controls are not authorization. Missing and unauthorized document IDs both return 404 without names, checksums or attachment headers. Permission checks and mutations share a transaction; no permission cache needs invalidation.

Administrator account/audit listings also recheck the actor's current enabled administrator status in the same transaction as the private query. They do not rely solely on the earlier session middleware snapshot. Revocation cannot retract data already read or sent by an in-flight request.

## API

All authenticated writes require the session cookie and `X-CSRF-Token`. JSON endpoints reject unknown fields, multiple JSON values and oversized bodies. A 401 means sign in again; 403 means CSRF/admin denial; document 404 intentionally hides object existence. Error responses carry a request ID.

| Method/path | Request / result |
| --- | --- |
| `POST /api/auth/login` | `{ "username": "...", "password": "..." }`; sets cookie and returns session/user/CSRF/expiry |
| `GET /api/auth/session` | Current user, CSRF token and absolute expiry |
| `POST /api/auth/logout` | Revokes current session and clears cookie |
| `POST /api/auth/revoke-sessions` | Revokes all own sessions and clears cookie |
| `POST /api/auth/password` | `{ "current_password": "...", "new_password": "..." }`; revokes all own sessions |
| `GET /api/admin/users` | Account metadata only, never password hashes |
| `POST /api/admin/users` | `{ "username": "...", "password": "...", "is_admin": false }` |
| `PATCH /api/admin/users/{id}` | Any of `enabled`, `is_admin`, `password`; revokes that account's sessions |
| `GET /api/admin/audit` | Most recent 100 security/document events |
| `PATCH /api/documents/{id}` | `{ "name": "fictional-invoice.txt" }`; requires write |
| `GET /api/documents/{id}/permissions` | Explicit grants; requires manage |
| `PUT /api/documents/{id}/permissions/{userID}` | `{ "level": "read" }`, `write` or `manage` |
| `DELETE /api/documents/{id}/permissions/{userID}` | Revokes explicit grant |

At least one enabled administrator must remain. Password reset, disabling and demotion cannot be used to bypass that invariant. No account deletion, public registration, owner transfer, document content replacement, email recovery or MFA is provided.

For an administrator using curl, first create a private directory and write the login JSON with a local editor. Do not put real passwords directly into shell arguments/history:

```sh
umask 077
client_dir="$(mktemp -d)"
# Create $client_dir/login.json privately using a local editor; use the login schema above.
curl -fsS -c "$client_dir/cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  --data-binary "@$client_dir/login.json" http://localhost:8080/api/auth/login \
  -o "$client_dir/session.json"
csrf="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$client_dir/session.json")"
curl -fsS -b "$client_dir/cookies" http://localhost:8080/api/admin/users
# Create $client_dir/new-user.json privately, using the account schema above.
curl -fsS -b "$client_dir/cookies" -H "X-CSRF-Token: $csrf" \
  -H 'Content-Type: application/json' --data-binary "@$client_dir/new-user.json" \
  http://localhost:8080/api/admin/users
```

Treat the cookie jar and JSON files as private runtime data; never commit them. Normal browser use stores the session cookie as HttpOnly and keeps the CSRF token only in memory.

## Audit and operational limits

Account creation/change, login outcomes, logout/revocation, uploads, downloads, rename, permission changes and access/CSRF denials produce audit events. Events store UTC time, actor ID when known, operation, affected opaque ID, outcome and request ID. No passwords, session tokens, CSRF tokens, filenames or document bytes are recorded. Mutation audit records share the metadata transaction: an audit failure rolls the mutation back. A failed upload may leave an invisible orphan, handled by existing startup reconciliation. A download event means permission/integrity checks passed, not that the recipient received every byte.

The administrator audit API returns a `details` object. Permission changes include `target_user_id`, `permission_before` and `permission_after` (empty string means no explicit grant). Account updates include the target ID, before/after administrator and enabled flags, and a `password_changed` boolean. Details use an explicit server-side field allowlist, never arbitrary request JSON. Migration 003 prevents ordinary SQL history rewriting; a privileged database owner can remove those triggers. There is no retention/rotation policy yet.

Logs are not tamper-evident against a host administrator. There is no tested production recovery or lost-last-administrator reset path; do not edit hashes or sessions manually as a deployment procedure. Production readiness also needs tested backups/restoration, deployment/TLS, quotas, monitoring and broader security review.

See the [Milestone 2 adversarial review](security-review-milestone-2.md) for reproduced findings, regression tests, actual check results and remaining verification limits.
