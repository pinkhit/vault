# SafeVault workspace and rename

The workspace uses green accents, a document sidebar, prominent search, a compact account menu and responsive file cards. Storage is a separate administrator view. Development banners and checksum disclosures have been removed from the interface; actionable errors, deletion confirmation and storage measurement limits remain. Operational limitations are still documented in the README and security guides.

Uploads report actual browser transfer progress. At 100% the interface shows “Finishing upload…” until the server responds and its checksum matches the original. Unknown transfer length uses an indeterminate bar. Errors retain server request references, and interrupted transfers advise refreshing before retrying. Downloads continue streaming through the browser's native download manager.

## Deployment identifiers

Configuration now uses the `SAFEVAULT_` prefix throughout. The Compose project is `safevault`, its image is `safevault-app:local`, and its default persistent volume is `safevault_vault-data`. Session cookies are `safevault_session` or `__Host-safevault_session` in Secure mode. The optional Mac collector uses `com.safevault.host-storage` and `~/Library/Application Support/SafeVault/`.

Existing deployments must update their environment variables and collector registration. Before starting under the new Compose project, stop the previous service, copy the complete data volume with ownership and permissions preserved, and compare file checksums. Never substitute an empty volume or repeat administrator bootstrap. Retain the stopped source volume until the migration has been reviewed. The database schema, account password hashes and document object layout are unchanged; the cookie rename requires signing in again.

The development Mac was migrated this way, with matching checksums, paths, permissions and ownership before startup. The previous data volume remains available as a rollback copy. The collector was registered under its new name, and the app remains published only on `127.0.0.1:8080`. Its existing private HTTPS URL and Tailscale Serve configuration are unchanged; Funnel is not enabled.

This rename updates working files and deployment identifiers. It does not rewrite existing Git or PR history.

## Verification — October 29, 2026

- Go formatting, vet and all backend race tests passed; combined statement coverage: **80.5%**. The Linux ARM64 server build also passed.
- All **49 frontend tests**, formatting, strict TypeScript and the production build passed.
- All **four Chrome integration flows** passed, covering desktop/mobile upload, exact downloads, deletion, search, safe previews, session revocation and administrator-only storage. Screenshots were inspected.
- A separate Chrome check throttled a real synthetic upload, observed an intermediate percentage, verified the stored checksum and confirmed progress disappeared after success.
- The disposable Compose smoke test passed, including restart persistence, interrupted indexing recovery, document integrity, previews, search, storage, sessions and configured HTTPS origin restrictions.
- Read-only checks of the running HTTPS app passed with normal certificate validation: SafeVault sign-in loads, anonymous and forged-identity access returns 401, and a foreign Origin returns 403.

No authenticated test mutations were made against the regular vault. Hosted CI, Mac mini deployment and production backup/restore remain unverified. The existing security and operational limits still apply.
