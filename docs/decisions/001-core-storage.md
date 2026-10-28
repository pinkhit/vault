# ADR 001: A single-process Go server with SQLite and local immutable objects

Status: accepted for Milestone 1.

The first milestone needs durable local file operations on an Intel laptop and a future Mac mini whose architecture is unknown. It must stay small enough to operate and explain without a cloud account.

Use Go's standard HTTP server for explicit routing, streaming uploads, context propagation, timeouts and a simple binary. Use React/strict TypeScript for an accessible browser UI. Serve built assets from Go to keep one runtime service and one browser origin. Vite is only needed for development and building.

Use SQLite with versioned embedded SQL migrations rather than a separate database service. Its transactional metadata and constraints fit a single-host repository. The pure-Go `modernc.org/sqlite` driver avoids CGO toolchain requirements on AMD64/ARM64 and includes SQLite capabilities needed for later search. It adds a larger dependency tree than a CGO driver; go.sum and checksummed Go module downloads track that tradeoff.

Keep original bytes outside SQLite under random server-generated IDs. Stream and hash to private staging files before publishing. A database row is the visibility boundary. Sync the complete object before inserting the row; reconcile filesystem leftovers at startup. This avoids trusting filenames as paths and avoids pretending two storage systems share a transaction.

Use a small modular monolith with concrete storage/database types. No generic repository framework, speculative service interfaces or empty future packages are necessary. Compose packages the UI/API into one image with one persistent named volume. It adds a Docker prerequisite but makes dependency and persistence setup repeatable. Native commands remain available for fast iteration.

Consequences: one writer process, serialized database access, full startup integrity checks and full verification before download. These are acceptable for this milestone, not claims about large-library performance. Authentication, authorization, operational backups and remote deployment require separate work. No decisions about Tailscale, model choice or permission inheritance are implemented yet.
