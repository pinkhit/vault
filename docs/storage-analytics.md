# Storage analytics — Checkpoint 6

Administrators can open **Storage overview** in the header, then use **Refresh storage**. Other accounts have no dashboard control, and the backend independently rejects their requests. The dashboard leads with a macOS-style host-storage bar, used/reclaimable/free counters and conservative additional-vault headroom. Logical document sizes, count and detected-type breakdown stay separate. Docker-visible capacity is in the expandable Docker volume limits section. Loading, errors, empty storage, unavailable capacity and low-space states are supported.

## Your MacBook and the Mac mini

Docker is storage management and isolation on the same physical machine; it is not an external storage service. Both the development MacBook and the planned Mac mini deployment can run the same app with Docker Desktop:

| Location | What it contains |
| --- | --- |
| Mac project directory | Source code, Git history, documentation, local configuration and development dependencies |
| Docker image | Built app, frontend assets and runtime dependencies; rebuilding updates this snapshot |
| Docker named volume | Original documents, SQLite accounts/sessions/permissions/audit history, indexing jobs and search data |
| Mac physical disk | The project directory and Docker Desktop's VM disk image, which contains Docker-managed storage |

The named volume persists across container restarts/rebuilds and ordinary `docker compose down`. Deleting the volume deletes its data. Rebuilding an image does not migrate documents between computers. The Mac mini needs its own deployment configuration and a separately planned, verified data transfer/restore; no deployment or migration is performed in this checkpoint.

[Docker's macOS documentation](https://docs.docker.com/desktop/troubleshoot-and-support/faqs/macfaqs/) explains its VM disk image. The filesystem inside that VM can report capacity independently of the space actually available on the Mac's physical disk. A sparse VM disk image's maximum size is also different from its allocated size. These measurements must not be substituted for each other.

## Measurement contract

`GET /api/admin/storage` requires a live administrator session and accepts **no parameters**, including paths or user identities. It returns:

- `documents.count`, `documents.logical_bytes`, `documents.by_type`: fresh SQLite metadata aggregation across all finalized documents, including every owner. Each type has `media_type`, `count` and `logical_bytes`, ordered by size descending then MIME type. The values contain no filenames, document IDs, owner identities, checksums or contents.
- `host_storage`: optional validated macOS sample (`version`, `collected_at`, `total_bytes`, `free_bytes`, `available_including_reclaimable_bytes`); null when unavailable.
- `growth_available_bytes`: conservative additional-vault estimate, or null if either limit is unknown.
- `filesystem.source`: `container-filesystem` for the image's configured container runtime, otherwise `native-filesystem`. This is a provenance label, not a permission control or automatic detection of the host operating system.
- `filesystem.checked_at`: UTC timestamp of the last measurement attempt, reused while the physical sample is cached.
- `filesystem.capacity`: exact integer byte fields `total_bytes`, `used_bytes`, `available_bytes`, `reserved_bytes`, and `low_space`; **null** when the measurement fails. Document totals remain available. A failed refresh does not retain an older successful capacity as if it were current.

Logical document bytes are the sum of finalized `documents.size` records. They exclude SQLite/WAL/search-index files, staging data, directory/file allocation overhead and unrelated filesystem data. This is not an allocated-byte measurement or a scan of object integrity. The grouping uses recorded content-sniffed MIME types, so CSV/JSON/TXT can share `text/plain`; it does not infer types from filenames or parse documents again.

Capacity comes from `Fstatfs` on the fixed, confined `objects` directory descriptor. The open requires a directory and rejects symlinks; no path from an HTTP request reaches the provider. Linux uses fragment units when reported, with transfer-block fallback; macOS uses its reported block unit. Invalid or overflowing figures are rejected.

For block unit B and reported counts:

- Total = B × total blocks.
- Used = B × (total blocks − free blocks).
- Available = B × blocks available to an unprivileged account.
- Reserved/unavailable free space = B × (free blocks − available blocks).

Used + available + reserved equals total. The bar represents **used / total**; reserved space is labelled separately. Other applications, databases and Docker resources may share the measured filesystem. These figures are neither FamilyVault-only allocated disk usage nor a quota guaranteed to this container. Low space means available bytes below **1 GiB OR 10%** of total; exact thresholds are not treated as below them. This is an advisory snapshot, not an upload reservation, host alerting agent or guarantee against running out of space.

## Platform and deployment providers

| Runtime | Implemented provider and meaning |
| --- | --- |
| Docker Desktop on MacBook or Mac mini | Linux container-visible document filesystem; explicitly does not measure the Mac's physical free space or Docker disk-image allocation |
| Native macOS | Filesystem containing the native document directory, reported by macOS; APFS shared space/quota/purgeable-space rules still apply |
| Native Linux / Linux Docker Engine | Filesystem visible to the process at the document directory; can be shared with other workloads |
| Other targets | Capacity unavailable; no fabricated values |

The corrected dashboard adds a native macOS collector while keeping both originals and SQLite in the existing named volume. Only its small numeric JSON snapshot is bind-mounted read-only into the app; there is no host-disk or Docker-socket mount, host command endpoint or storage migration. [Storage-layout measurements and future Finder requirements](storage-performance.md) record the tradeoffs.

## macOS host collector

On the MacBook or Mac mini, with Docker Desktop running:

```sh
./scripts/macos-storage.sh start
docker compose up --build -d --wait
./scripts/macos-storage.sh status
# Remove the user-level timer when no longer wanted:
./scripts/macos-storage.sh stop
```

`start` builds a native Intel/ARM executable using Docker (no host Go/Xcode install), generates and validates its plist, then registers `~/Library/LaunchAgents/com.familyvault.host-storage.plist`. launchd invokes it once at login and every 30 seconds under the current Mac user. The executable collects and publishes a single sample per invocation; it has no separate polling loop. Its native executable and snapshots live under `~/Library/Application Support/FamilyVault` so macOS privacy controls do not block execution/writes in a Documents checkout. The ignored `.runtime/host-storage` link points to the numeric snapshot directory for the read-only container mount. It reads no document bytes. Logs are in the same Application Support directory. Paths for this checkout are absolute; rerun setup after moving the project. The `stop` command unloads and removes only that collector's own registration. An existing registration pointing to a different executable is not overwritten. Docker Desktop and this user login must remain available; no system daemon or remote deployment is installed.

The default measured file is Docker Desktop's usual `Docker.raw`. If its location was changed in Docker settings, explicitly supply the matching path:

```sh
FAMILYVAULT_DOCKER_DISK='/Volumes/External/Docker/Docker.raw' ./scripts/macos-storage.sh start
```

The collector measures the Mac filesystem containing that disk image, including an external disk when configured. It invokes a fixed local Foundation query through macOS `osascript`; the configured path is a separate argument, never executable source. Native macOS supplies total capacity, free-now capacity and its available-including-reclaimable estimate. Used excluding reclaimable = total − available-including-reclaimable; reclaimable = available-including-reclaimable − free-now. Those three segments sum to total. macOS may change its reclaimable estimate and other storage views may categorize snapshots/caches differently. Disk values use decimal units (1 TB = 1,000 GB).

The backend accepts only the versioned timestamp/numeric sample schema, bounded to 4 KiB. It rejects non-regular/symlinked files, unknown fields, invalid bounds, future-dated data and samples older than 90 seconds. Failed collection atomically publishes an unavailable sample. Missing/stale/invalid input displays **Mac storage unavailable** and unknown growth headroom, never a fallback to Docker's larger virtual capacity. Snapshot data is subject to the same administrator and audit checks as the rest of the report.

Additional headroom is a conservative estimate from the smaller of host free-now and Docker-volume available bytes; reclaimable space is excluded. This is shared, not reserved, and future document/index/database overhead consumes it too. It is not an enforced quota or exact maximum: Docker may reuse already-allocated free blocks, APFS quotas/snapshots and other workloads can change limits. When either measurement is unavailable, headroom is unknown.

Exact physical Mac bytes attributable to one Docker volume cannot be isolated from the shared sparse VM disk image. At the user's request, the bar now includes an explicitly estimated vault segment using logical document bytes as a proxy. The other-used segment is also labelled estimated: measured used space minus this vault estimate. The estimate is capped at measured used space, so inconsistent/stale measurements or compression cannot create negative other-used values or an overflowing bar. Measured total, reclaimable/free counters and growth headroom are unchanged. The actual physical split remains unknown; database/index overhead, filesystem allocation, compression and snapshots may make it differ. The legend shows exact proxy bytes in decimal units, and very small nonzero estimates get an explicitly explained position marker without inflating the segment width. No arbitrary overhead multiplier is applied. This preserves the original document I/O path. No folders, filename exports or deduplication have been added.

## Authorization, performance and failure behavior

Middleware checks the authenticated administrator role, and the service checks current `users.is_admin AND users.enabled` in SQLite before the aggregate query. Authorization and grouping share a read transaction. After physical sampling, it rechecks current authority and records `storage.analytics` in an audit transaction before releasing a report. Forged/stale role flags and demotion during sampling cannot grant access. Session checks happen at request admission; already-delivered data cannot be retracted. A later refresh clears old metrics immediately and session expiry returns the UI to sign-in.

Responses use the existing `no-store`, CSP, no-frame and `nosniff` headers. A cache exists only for the filesystem sample, not the authorization decision or whole report. It is shared safely across requests for **30 seconds**, including failures, with one sample per expiry. Clock rollback invalidates the sample. A dashboard refresh does not bypass this cache; its timestamp makes that visible. Logical totals are always read afresh.

A single grouped SQLite query scans metadata for the intended thousands-of-documents workload. No filesystem walk, file hashing, extra queue, schema migration or materialized-counter maintenance is needed. The filesystem call holds no SQLite transaction. The service applies a two-second database context; a kernel filesystem-stat syscall cannot be interrupted by that Go context. The supported storage target is local storage, not a potentially hanging network filesystem. No production latency or throughput benchmark has been claimed.

Missing/expired/revoked sessions return 401; non-admins 403; parameters 400; database/audit errors the existing generic 500. Capacity-provider errors become `capacity: null` without exposing diagnostics or paths. An audit failure releases no totals. Unsupported capacity platforms do not affect document operations. The dashboard displays server-provided warnings and never filters private document records in JavaScript.

## Verification

Recorded October 29, 2026 on macOS Intel, with Go 1.26.10 checks in the Linux Docker development image and installed Chrome against an isolated Compose application. Fixtures contain synthetic data only.

| Check | Actual result |
| --- | --- |
| Go formatting/static analysis | `gofmt` and `go vet ./...` passed |
| Complete Go race suite | Passed, **80.2% combined statement coverage** |
| Permission/integration tests | Passed: anonymous/expired/revoked/disabled accounts, read/write/manage grants denied, forged cached admin role rejected, demotion during sampling, query/path rejection, no document metadata/host-path leakage, audit failure withholding metrics, empty data and all-owner totals |
| Provider/cache tests | Passed: reserved-space arithmetic, low-space thresholds, invalid/overflowing figures, directory symlink rejection, successful/failing cache expiry, recovery, clock rollback and concurrent refresh sampling once |
| Frontend | **31 tests passed**, strict TypeScript/production build passed, Prettier passed |
| Browser | **4 Chrome flows passed**: original files, secure search, safe previews and administrator storage. Storage assertions cover upload/refreshed counts and bytes, desktop/mobile layout, synthetic capacity-unavailable/HTTP-error recovery, real non-admin manage-grant rejection and session revocation. Desktop/mobile screenshots inspected |
| Docker persistence | Passed: real container capacity/source/accounting and logical totals after restart, plus indexing/search, TXT/PDF previews, exact original bytes and logout rejection |
| Cross-builds | Server builds passed for Linux amd64/arm64 and macOS amd64/arm64; ARM hardware not tested |
| Native macOS provider | **3 capacity tests passed on this Mac** using a cross-compiled test binary: block accounting/warnings, actual native filesystem sampling and symlink rejection. This is not host monitoring for a Dockerized application |
| Review checks | `git diff --check` and Python syntax passed |
| Hosted CI | Not run here; existing workflow includes the new backend/frontend/browser and Compose tests |

Initial frontend verification caught a test that asserted the loading status before the asynchronous unavailable-state response arrived; it was corrected to await that state. A formatting check also required a second pass over the new browser test. No application security boundary was relaxed. Successful frontend checks used the project's installed dependencies. The mobile table headings were adjusted after screenshot review and checked again.

Browser/smoke tests used isolated synthetic volumes; those test projects and volumes were cleaned up. The regular localhost app was then recreated with the tested image, retaining its existing accounts and document volume. Its health endpoint and served dashboard bundle were verified. No performance benchmark, physical Mac disk collector, real ARM execution, new deployment, backup/restore drill or HTTPS validation was performed. No commits or pushes were made, and Checkpoint 7 has not begun.

## Files for review

- Backend: `backend/cmd/server/main.go`, `backend/go.mod`, `backend/internal/analytics/{analytics.go,analytics_test.go}`, `backend/internal/api/{api.go,api_test.go,analytics.go,analytics_test.go}`, `backend/internal/storage/capacity{,_unix,_linux,_darwin,_other,_test}.go`.
- Frontend: `frontend/src/{StorageOverview.tsx,StorageOverview.test.tsx,App.tsx,App.test.tsx,api.ts,format.ts,styles.css}`, `frontend/e2e/documents.spec.ts`.
- Persistence test: `tests/container_smoke.py`.
- Documentation: `README.md`, `docs/{storage-analytics.md,architecture.md,threat-model.md}`. README contained an uncommitted project guide at checkpoint start; it was preserved, with the scope/feature/API references updated. Review that existing content before staging the file.

For an interview, explain logical versus allocated bytes, process-visible versus host filesystems, free versus available/reserved blocks, why aggregate endpoints need administrator authorization, why failure is not zero, and why caching measurements must never cache permissions.

## Host-storage correction verification

The earlier Checkpoint 6 result above is historical. The correction adds host readings, a separate bar and a collector after the Docker-only dashboard was found insufficient for the administrator's system-storage need. The corrected frontend passed 32 tests, strict TypeScript/build and formatting. All four Chrome flows and the Docker persistence check passed again; screenshots showed real macOS capacity, reclaimable and free-now counters. The native collector and generated LaunchAgent plist were executed/validated on this Mac. New tests cover missing/invalid/stale/future/unknown-field/symlinked snapshots, host-vs-Docker growth limits and authorization. The final full Go vet/race suite passed with **77.8% combined coverage**, including the new CLI startup paths in the denominator; Linux CI does not exercise macOS launchd. Linux ARM64 and macOS Intel/ARM64 server builds and the ARM64 collector cross-build passed. Real ARM hardware and Windows deployment were not tested. The user-level LaunchAgent was installed, its successful exit and repeated timestamp updates were verified on this Mac, and the running app was updated using its existing document volume. An initial workspace/Documents registration exited with EX_CONFIG; moving only the collector executable and numeric snapshots to Application Support resolved it. The exact physical used/free categories are native macOS estimates, not a promise to reproduce every System Settings categorization byte-for-byte. A background process started from the tool session did not survive session cleanup, so the supported continuous setup uses the macOS user-level timer. No hosted CI result is claimed.

Correction files additionally include `backend/cmd/host-storage/main.go`, `backend/internal/hoststorage/{sample.go,sample_test.go}`, `scripts/macos-storage.sh`, `Dockerfile`, `compose.yaml`, `.env.example`, `.gitignore`, `.dockerignore` and `docs/storage-performance.md`. Build artifacts and the exact per-user plist stay under ignored `.runtime/`; the active collector executable/snapshots are installed under user Application Support.

## Platform portability

The FamilyVault server/frontend/extraction stack remains a Linux-container application. Host metrics are optional: a missing collector cannot block authentication, uploads, downloads, indexing, search or previews. The included host collector runs on macOS only; Windows deployment with Docker Desktop Linux containers is intended but has not been tested, and accurate Windows host capacity needs a separate native provider. Without one, host capacity/headroom are unknown and the UI uses a generic host-unavailable state. No storage migration was made for this correction.

Final live verification: the regular localhost app was healthy and its unprivileged container user successfully read the active Application Support snapshot. Eleven scheduled collector runs had completed with exit code 0 and advancing timestamps at the observed check. The frontend bundle served the corrected host-storage view. Vault data/accounts were preserved; synthetic test projects/volumes and benchmark data were cleaned up. Hosted CI and Windows runtime checks remain unverified.

## Logical-byte vault share estimate

The Mac storage bar has four categories: **FamilyVault (estimated)**, **Other used (estimated)**, **Reclaimable by macOS**, and **Free now**. Vault estimate = min(logical document bytes, measured used bytes); other used = measured used − vault estimate. Total used remains the macOS measurement. Remaining other-used space includes uncounted vault overhead. A zero-size vault has no marker; a nonzero vault below the bar's visible scale has a purple position marker and explanatory text, with actual proportional geometry unchanged. Estimates disappear when host measurements are unavailable. The original logical count remains visible even if the displayed estimate is capped. This change affects display only; storage layout, permissions and upload/download I/O are unchanged.

Estimate display verification: all **35 frontend tests**, Prettier and strict TypeScript/production build passed. Tests cover proportional segment totals, zero/tiny vaults, capping inconsistent logical sizes and unavailable host data. Three existing Chrome flows passed; the storage flow passed after correcting an ambiguous text selector introduced by the new explanatory copy. Desktop/mobile screenshots were produced and the desktop estimate/marker layout inspected. The final image was applied to localhost with the existing accounts/data volume; health and served estimated labels were checked. Backend code was unchanged for this display addition, so the prior Go results were not rerun or relabelled as a new run. No commits or pushes were made.

## Maintenance cleanup verification

Removed unused progress styling, the collector's redundant polling loop/flag and obsolete installer compatibility. The collector now takes one sample per invocation; launchd owns its 30-second schedule. Shared API upload and browser login helpers replace duplicated setup without removing security or migration cases. Updated stale search/indexing comments.

Go vet and the complete Go race suite passed (**78.3% combined statement coverage**); the API race suite passed again after consolidating helpers. All **35 frontend tests**, formatting, strict TypeScript/build, **four Chrome flows** and the isolated Docker persistence smoke test passed. Dependency tidy reported no changes. Linux ARM64/macOS Intel server and macOS ARM64 collector cross-builds passed. The installed collector configuration contains no obsolete flag; scheduled runs exited successfully with fresh snapshots. Hosted CI was not run. No commits or pushes were made, and Checkpoint 7 has not begun.
