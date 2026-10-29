# Document previews — Checkpoint 5B

Use **Preview** beside a library document or search result. Search results with PDF page references open that page. The native modal dialog supports keyboard navigation, Escape dismissal and focus restoration. Loading, errors, refresh and a **Download original** link are available. Unsupported files remain downloadable through the existing authenticated attachment endpoint.

## Browser content boundary

`GET /api/documents/{id}/preview?page=1` returns **JSON**, never an inline original document. The existing authenticated middleware and SQL document lookup enforce an enabled administrator, owner or explicit read/write/manage grant. Unauthorized and nonexistent objects both return the same 404. Read authorization is checked again after parsing; successful delivery requires a `document.preview` audit event. Denied access uses the existing denial audit path. Parser errors do not include source content, filenames or diagnostics.

The storage layer opens only confined, regular objects with no-follow and size/SHA-256 verification. No static directory exposes uploads. File extension, stored sniffed MIME type and current byte signature must agree with the selected parser; parsing then validates the actual structure. Unknown identity/filename/path parameters are rejected.

- TXT, [DOCX main-body text](docx.md) and formatted JSON are React text nodes inside `pre`. CSV cells are text nodes; formulas and links are inert strings. There is no HTML injection API.
- PNG, JPEG and WebP pass image-header dimension checks and full decoding, then are re-encoded as PNG. Original metadata, trailers and active markup are not returned. WebP uses the Go project's pinned [x/image decoder](https://pkg.go.dev/golang.org/x/image@v0.47.0/webp).
- PDF pages are rasterized with Poppler `pdfinfo`/`pdftoppm` and re-encoded as PNG. This provides an inline paged viewer without exposing PDF scripts, annotations, forms, links, attachments or embedded actions to a browser PDF plugin. Text selection, PDF form interaction, zoom tools and embedded links are intentionally unavailable. Original download remains available.
- The response is `application/json; charset=utf-8`, with `Content-Disposition: inline`, `nosniff`, `Cache-Control: no-store` and the existing CSP/frame denial headers. PNGs are embedded using the already-allowed `data:image/png;base64` source. No blob URLs, frames, object elements, executable preview origin or CSP relaxation are needed.

PDF HTTP ranges are unnecessary: the API requests one rendered page at a time. Existing original downloads continue to support ranges. Each page request rechecks the session and document access. Previously delivered content cannot be retracted by later revocation; a refresh/page change clears the old display before fetching and a 401 unmounts the authenticated browser. Requests are aborted on dismissal and stale replies cannot repopulate a dismissed viewer. No preview data is placed in browser persistent storage.

## Resource bounds and failure behavior

| Resource | Bound |
| --- | --- |
| Source | 25 MiB, checked before storage hashing; no writes to originals |
| Request preparation | 10-second context; one expensive preview at a time, busy responses fail fast |
| TXT | First 64 KiB, clipped at a UTF-8 boundary, explicit shortened notice |
| DOCX | Bounded ZIP/XML validation and at most 2 MiB derived text; first 64 KiB displayed at a UTF-8 boundary, explicit shortened notice; no Word layout or page numbers |
| CSV | At most 64 KiB input; 200 displayed rows, 32 columns, 4096 bytes per cell; extra rows produce a shortened notice |
| JSON | At most 64 KiB input, nesting depth 32, formatted output at most 256 KiB; excessive input fails rather than showing invalid partial JSON |
| Raster image | 8 MiB compressed input/output, 4 million pixels, each dimension at most 4096 |
| PDF | At most 200 pages; one page per request, long side at most 1600 pixels |
| Poppler subprocess | Linux only, 256 MiB virtual memory, 10 CPU seconds, zero regular-file output/core dumps, bounded stdout, cancellation/deadline kill and 1-second pipe wait |

Text must be UTF-8 with no binary controls except newline, carriage return and tab. TXT previews validate the displayed prefix; they do not assert validity of undisplayed bytes. CSV is comma-separated, accepts quoted/multiline and ragged rows, and does not infer a header or evaluate formulas. JSON formatting preserves number literals and key order. Whitespace-only/empty CSV has an empty table; empty uploads remain prohibited.

The image byte/pixel bounds constrain in-process decoding. Go image decoders do not support mid-decode cancellation; cancellation is checked before rendering and during PNG output. This is bounded work, not an OS memory sandbox for Go. JSON depth is checked before indentation to avoid amplification by deep nesting. Preview output is generated on demand and held in memory, without a disk cache or schema migration.

Errors: invalid parameters/page 400; authentication 401; hidden object 404; missing/corrupt source 409; resource bound 413; unsupported format 415; malformed supported file 422; busy/missing bounded PDF tools 503; processing deadline 504. Other failures return the existing generic 500. Failure never alters original bytes. No antivirus or malware-cleanliness claim is made for downloads.

## Deployment limits

The Docker image supplies Poppler, DejaVu fallback fonts and a prebuilt font cache, under its existing unprivileged user, read-only root, dropped capabilities and localhost mapping. Native Linux needs Poppler and a prepared font cache that the service account cannot write. Fontconfig may try to refresh caches even when they were built into an image; the production Compose read-only filesystem denies those writes without relaxing the parser limit. Tests use a read-only root too, with a separate executable temporary test workspace. Native macOS fails closed for PDF previews because the existing Linux address-space limits cannot be guaranteed; other supported previews still work. Use Docker on macOS.

Poppler remains a trusted native dependency. Resource limits are not a separate UID, network sandbox or filesystem jail for a compromised parser. Go and Poppler security updates remain necessary. Exploit resistance to unknown parser vulnerabilities, exact process peak memory, statistical timing behavior, real ARM hardware and all browser engines have not been established. The current image is a development build.

DOCX text previews were added in Checkpoint 7's first increment; XLSX previews remain a separate increment. HTML, SVG, GIF, general archives and all unrecognized formats use download fallback. Encrypted PDFs have no password-entry workflow and fail safely. No OCR, Ollama or remote access is added.

The app remains localhost HTTP. TLS transport protection, production host/origin configuration, Secure cookies and deployment security headers must be validated together during the later HTTPS deployment work. Existing CSRF and session protections have not been relaxed.

## Verification

Recorded October 29, 2026 on macOS Intel, with Go checks in Linux Docker and installed Chrome against an isolated, freshly built Compose app. All fixtures contain synthetic content only.

| Check | Actual result |
| --- | --- |
| Go formatting and `go vet ./...` | Passed |
| Complete `go test -race -coverpkg=./...` suite | Passed; **80.0% combined statement coverage** |
| Backend preview negatives | Passed: anonymous/expired/revoked/disabled sessions, owner/admin/read/write/manage matrix, hidden/nonexistent response equivalence, forged cached admin role, grant revocation including during real PDF rendering, cross-site denial, invalid/duplicate/unknown parameters, audit failure, corruption and matching symlink rejection |
| Parser checks | Passed: real two-page PDF raster selection/dimensions, malformed PDF/image/JSON/CSV, PNG/JPEG/WebP decoding, stripped image trailers, UTF-8 clipping, JSON depth/input limits, CSV row/column/cell bounds, image pixel/dimension bounds, parser deadline/output cap, concurrency rejection and diagnostic redaction |
| Frontend | **25 tests passed**, Prettier check passed, strict TypeScript and production build passed |
| Chrome end-to-end | **3 flows passed**: original file operations; existing secure search; new TXT/JSON/CSV/PNG/WebP/PDF previews, page-linked search preview, literal malicious filenames/text/formulas, unsupported fallback, large-text shortening, identical downloads, keyboard/Escape/focus restoration, desktop/mobile and actual session revocation |
| Docker persistence | Passed: text/PDF previews after restart in the unprivileged read-only runtime, persisted indexing/search/sessions, identical original TXT/PDF bytes, logout rejection and localhost-only publication |
| Cross-builds | Linux amd64/arm64 and macOS amd64 server builds passed; no ARM hardware run |
| Review checks | `git diff --check` and Python syntax passed; desktop/mobile PDF screenshots inspected |
| Hosted CI | Not run here; existing workflow includes these tests, with PDF fonts/cache setup added |

Initial runs exposed and corrected missing test imports/fixture mounts, absent fallback fonts, font-cache writes under a root development container and modal keyboard focus escaping. The successful backend run uses a read-only root, matching the deployment boundary; executable temporary storage is enabled only for compiling/running test binaries, not for the application container. Tests do not weaken parser limits. All three browser flows passed after the focus fix.

Browser/smoke tests use separate synthetic Compose projects, never the developer's accounts or document volume. After verification, the regular localhost app was recreated with the tested image and became healthy; its existing accounts and document volume were retained. The served JavaScript bundle was checked for preview controls. Temporary browser/smoke projects and their synthetic volumes were cleaned up. No commits, pushes, merges or Checkpoint 6 work were performed.

## Files for review

- Backend: `backend/go.mod`, `backend/go.sum`; `internal/api/{api.go,preview.go,preview_test.go,authorization_matrix_test.go}`; `internal/documents/preview.go`; `internal/preview/{preview.go,pdf.go,preview_test.go}`; `internal/pdfutil/run.go`; `internal/extraction/{pdf.go,extract_test.go}`; `internal/testutil/pdf.go`.
- Frontend: `src/{App.tsx,App.test.tsx,DocumentLibrary.tsx,DocumentSearch.tsx,DocumentSearch.test.tsx,DocumentPreview.tsx,DocumentPreview.test.tsx,api.ts,styles.css,test-setup.ts}`; `e2e/documents.spec.ts`; `playwright.config.ts`.
- Packaging/tests: `Dockerfile`, `.github/workflows/ci.yml`, `tests/container_smoke.py`, `tests/fixtures/fictional-preview.{pdf,png,webp}`.
- Documentation: `README.md`, `docs/{previews.md,architecture.md,search.md,threat-model.md}`. README already had an uncommitted project guide at checkpoint start; that content was preserved and its scope/preview references updated. Review it before staging the entire file.

For an interview, explain why authentication and object authorization are separate checks, why an inert rendering boundary is stronger than trusting an uploaded MIME type, how dimensions/depth can amplify small inputs, why subprocess limits differ from application contexts, and why previews must preserve the original bytes.
