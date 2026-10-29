# Bounded text extraction — Checkpoint 2

The internal `extraction.Extract` helper supports UTF-8 TXT and text-based PDF. It returns plain-text passages with one-based sections and PDF page numbers (TXT uses page zero). The helper itself does not mutate metadata; Checkpoint 3 connects it to the [durable indexing worker](indexing.md), which persists output transactionally. DOCX is deferred to Checkpoint 7; OCR is not implemented.

## File and resource boundaries

The input is a trusted metadata snapshot, never a filesystem path. The extractor checks the size, filename extension, declared media type and detected bytes. Files are opened through `storage.OpenVerifiedContext`: confined IDs, atomic no-follow/nonblocking opens, regular-file/size checks, SHA-256 verification and cancellation checks between bounded reads. The parser reads the same verified descriptor. Originals are never modified, and failures return no partial passages.

| Bound | Value |
| --- | --- |
| Original input | 25 MiB |
| Extracted UTF-8 output | 2 MiB |
| PDF pages | 200; request one additional page to detect overflow |
| Passages | 2,048 total, at most 4,096 UTF-8 bytes each |
| Total extraction deadline | 10 seconds, or the caller's earlier deadline |
| PDF process | 256 MiB virtual address space; 10 CPU seconds; no core dumps or regular-file output |

TXT extraction runs in Go. Normalization removes a leading UTF-8 BOM, standardizes line endings and whitespace, preserves paragraph boundaries and splits long passages without cutting Unicode code points. Identifiers and case are preserved. This is not Unicode NFC normalization or language-specific tokenization.

PDF extraction uses Poppler's `pdftotext`, with UTF-8 stdout and form-feed page boundaries. Blank pages retain their original positions. No filename, document text, password or arbitrary option is inserted into shell commands. A fixed shell wrapper applies resource limits and then replaces itself with the parser; the verified descriptor is stdin. The child receives a minimal environment. Diagnostics are discarded because parser errors can contain private content. A capped stdout writer cancels and reaps the process on overflow; caller cancellation/deadlines also kill it. I/O cleanup has a further one-second wait bound.

The Linux resource-limit contract is required for PDFs. Native macOS PDF extraction returns `ErrUnavailable`; use Docker on either Intel or Apple Silicon rather than silently running an unbounded parser. TXT remains portable. The existing container's non-root user, read-only root, dropped capabilities and memory limit remain enabled. This child process contains parser crashes/resource use; it is **not a complete filesystem/network sandbox** against a parser exploit. It runs with the application's UID, so keep the maintained Poppler package updated and retain the trusted-host assumptions. Kernel-blocked filesystem I/O cannot be forcibly interrupted by a Go context; storage cancellation is checked between reads.

## Dependencies, outcomes and tests

The runtime Docker image installs Alpine `poppler-utils`; GitHub Actions installs Ubuntu's `poppler-utils` before Go tests. No Go dependency was added. Rebuild the image when applying this checkpoint. For Linux-native development, install the distribution's Poppler utilities. Linux PDF tests fail if the dependency is missing; on other operating systems they explicitly skip and must also be run in Docker.

Errors distinguish unsupported formats, malformed/unparseable files, resource bounds, unavailable parser, cancellation/deadline and no extractable text. Storage corruption/missing-file errors remain distinguishable for worker failure handling. No-text PDFs (including image-only files) need OCR to become searchable; password-protected PDFs are not decrypted. Poppler may repair some damaged PDFs, and layout/font encoding can affect extracted text. There is no claim of perfect PDF validation or layout fidelity.

Tests use generated synthetic PDFs with valid cross-reference tables. They cover multiple pages/blank pages, malformed PDFs, empty/whitespace text, Unicode chunk boundaries, size/page/passage/output limits, unsupported/spoofed formats, corruption, traversal, symlinks, cancellation, a stalled parser, child resource settings/minimal environment, missing dependencies and diagnostic redaction. No private documents or external PDF-generation service is used.

For an interview, explain why a subprocess permits enforceable parser termination, how input/output/structure limits bound different kinds of work, why extraction releases no authorized HTTP response by itself, and why verified originals and derived passages have separate lifecycles. Resource limits and process separation do not imply a complete security sandbox or exactly-once indexing.

## Checkpoint 2 checks actually executed

- Go 1.26.10/Linux: formatting, `go vet ./...` and the complete race suite passed; combined statement coverage was **77.5%**. Real PDF tests used Poppler 25.12.0; controlled helper processes verified timeout/overflow termination and inherited limits/environment.
- Real TXT/PDF integration cases passed again inside the runtime image as UID 10001, with read-only root, dropped capabilities, no new privileges, no network, 512 MiB container memory and the existing no-exec temporary mount. An initial attempt to run the helper-instrumentation tests there failed because their temporary fake executables could not run on `/tmp`; those tests passed in the development image. The mount restriction was preserved and runtime verification selected the real-parser cases.
- All **10 frontend tests**, frontend formatting, TypeScript/production build and **1 desktop/mobile browser test** passed. Linux amd64/arm64 server builds and macOS amd64 server/CLI builds passed. Native PDF extraction and ARM64 hardware execution were not tested/supported by this checkpoint.
- The isolated Docker upload/download/checksum/restart/session-persistence test passed; it exercises existing file operations, not a background extraction worker. The regular developer volume/accounts were untouched.
- `git diff --check` passed. CI is configured to install Poppler and run the extraction tests with the existing checks on pushes/PRs; no hosted CI run was triggered. No hostile PDF corpus, exhaustive memory-exhaustion test or security-sandbox certification was performed.

References: the packaged [Poppler command manual](https://manpages.debian.org/trixie/poppler-utils/pdftotext.1.en.html) documents UTF-8 output/page breaks and the Linux [resource-limit manual](https://www.man7.org/linux/man-pages/man2/getrlimit.2.html) documents inherited CPU/address-space limits. Tests verify the behavior used here.
