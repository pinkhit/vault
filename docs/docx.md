# DOCX — Checkpoint 7, first increment

DOCX uploads use the existing asynchronous worker and permission-filtered FTS5 search. The preview endpoint returns the same extracted main-body text as inert JSON, displayed in the existing text viewer. No frontend rendering library, extra process or dependency is needed. XLSX previews and broader hardening/end-to-end coverage remain separate increments for review.

## Supported representation

The reader accepts conventional `word/document.xml` packages with the standard DOCX content type and an internal root office-document relationship. It supports transitional and strict WordprocessingML namespaces and UTF-8 XML. It reads paragraph/run text, tabs, line breaks and table-cell text. It omits headers, footnotes, comments, deleted/moved-from text, field instructions, images and embedded objects. Field results already stored as ordinary text may appear, but fields are never evaluated. Formatting and Word pagination are unavailable; search uses page zero with one-based normalized section references. Previews show plain text, not a Word layout.

Microsoft documents the [paragraph/run/text structure](https://learn.microsoft.com/en-us/office/open-xml/word/how-to-open-and-add-text-to-a-word-processing-document) used here. ZIP/XML decoding uses Go's standard library; it does not follow external references or execute macros. A renamed generic ZIP, macro-enabled main content type, nonstandard main-part location or encrypted package fails safely. `.docm` remains unsupported. This is a deliberately bounded subset, not full Office conformance or malware scanning.

## Resource and security boundaries

| Resource | Bound |
| --- | --- |
| Verified original | 25 MiB; existing confined storage descriptor, size and SHA-256 checks |
| Archive | 256 entries; 64 KiB central directory; 16 MiB declared total expanded size |
| Parts actually decompressed | Content types/root relationships: 64 KiB each; main document: 4 MiB |
| XML | Depth 64 and 200,000 tokens per parsed part; DTD/entity declarations rejected |
| Derived text | 2 MiB; extraction also enforces existing passage count/size limits |
| Preview display | First 64 KiB at a Unicode boundary, with an explicit shortened notice |
| Runtime | Existing 10-second caller deadline; cancellation between decompression reads/XML tokens; one indexing worker and one preview slot |

The central-directory byte size is bounded before `archive/zip` allocates its file list; actual entry count is checked afterward. ZIP64, split archives, archive comments/trailers, duplicate entries, unsafe paths, symlinks, encryption and unsupported compression methods fail closed. Parts are never written to disk. Only three fixed parts are opened, and selected parts must reach EOF with valid ZIP checksums. Decompressed-byte limits apply even if ZIP size metadata is dishonest. Unknown or embedded parts are not opened. These limits bound work and allocations; they are not a measured peak-memory guarantee or an OS sandbox for Go. A single XML token can consume work before the next cancellation check, within the part-byte bound.

Failures return no partial extraction. Preview clipping occurs only after the complete supported main part has parsed successfully. Existing service-level authorization is applied before reading and again after preview generation; successful previews still require an audit write. No CSP, session, CSRF, SQL visibility or original-download behavior changes.

## Upgrade and review

Migration 007 resets only `unsupported format` jobs for `.docx` files stored with the sniffed ZIP media type. It leaves successful work, malformed/failed jobs, unrelated unsupported formats, metadata, ownership and grants intact. The migration runs once and survives reopening. Original bytes never change. Rebuild/restart with the existing volume; do not bootstrap another account or delete volumes.

For an interview, explain why MIME sniffing alone cannot validate a ZIP-based format, how compressed inputs amplify resource use, why plain text avoids browser-active Office content, why DOCX has section references rather than invented page numbers, and why enabling a parser needs a narrow migration for previously unsupported jobs.

Verification on October 29, 2026: Go formatting/vet and the full race suite passed (79.3% combined statement coverage); 35 frontend tests, formatting/build, all four Chrome flows and the existing isolated Docker restart/persistence check passed. DOCX cases cover Unicode/body text, malformed archives/XML, resource limits, cancellation, migration/reopening, real worker publication, search grant revocation, preview authorization/escaping and identical downloads. Existing CI includes these tests; hosted CI was not run. Full Office fidelity, actual peak memory, ARM hardware, Windows and HTTPS deployment remain unverified. XLSX and the separate hardening increment have not begun.
