# ADR 002: Persistent sessions and flat document grants

Status: accepted for localhost Milestone 2.

Keep the modular monolith and SQLite rather than introducing an external identity service. Use the established Argon2id library for password hashes, standard cryptographic randomness for opaque session IDs, and SQLite for revocation/expiry. Store only hashes of authentication tokens, and use HttpOnly/SameSite cookies. Local HTTP explicitly disables Secure; a configurable Secure/__Host- mode is available but HTTPS deployment is not implemented.

Use session-bound synchronizer CSRF tokens for authenticated mutations and strict same-origin Origin validation at login. Do not rely on SameSite alone. Apply persistent account/IP throttling before password work and bound concurrent hashes. No public registration or default account exists; a local, offline, locked bootstrap command creates the initial administrator.

The current product has documents but no folders. Uploads belong to their authenticated uploader. Owners and enabled administrators have full document authority; explicit read/write/manage grants are additive. Manage includes delegation. No deny overrides, group permissions or folder inheritance are introduced. Permission-filtered SQL queries return only authorized metadata/bytes, and mutations check access inside the same transaction as the change and audit event. Missing and inaccessible document IDs are indistinguishable to callers.

Bootstrap assigns existing unowned Milestone 1 metadata to the initial administrator without changing original bytes. This is an explicit operator action, not automatic ownership assignment during migration. Account role/password/status updates revoke all sessions. Session and authorization state is read from the database for every request, so subsequent requests immediately reflect revocation; in-progress work may complete.

Keep audit events in the same SQLite database for transactional consistency. This makes them useful for debugging and review, not tamper-proof against the host owner. Keep account/permission management in tested APIs while adding only login/logout UI. Backups, remote HTTPS deployment, folders and retrieval remain later work.
