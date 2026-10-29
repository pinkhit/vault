# Milestone 5 — private HTTPS access

Milestone 4 (local AI) is deferred at the user's request. Milestone 3 was merged into `dev` in PR #6 at `1ea8a24`; work continues on `feature/private-access`. The existing README guide was preserved. Each checkpoint stops for review and commit; no automatic commits, pushes or network exposure.

| Checkpoint | Scope |
| --- | --- |
| 1 — application origin | Explicit HTTPS origin, Secure-cookie requirement, unchanged localhost default, negative HTTP tests and local TLS-proxy integration |
| 2 — live private access | Configure native Tailscale Serve, verify HTTPS certificates/access rules, test from a second device and document shutdown/rollback |
| 3 — submission | Review CI, prepare synthetic demo/screenshots and concise deployment/security notes |

## Application configuration

Defaults remain localhost HTTP. An empty `FAMILYVAULT_PUBLIC_ORIGIN` keeps the existing loopback Host/Origin checks. Compose still publishes only `127.0.0.1`; setting an origin never opens a listener or enables Tailscale.

HTTPS mode requires **both** `FAMILYVAULT_PUBLIC_ORIGIN=https://device.tailnet.ts.net` (the exact URL, without a trailing slash, path, credentials, query or fragment) and `FAMILYVAULT_COOKIE_SECURE=true`. Startup rejects an invalid origin or non-Secure cookies. One hostname is accepted; no wildcard tailnet allowlist is introduced. Login and other browser Origin checks use that explicit HTTPS origin. The ordinary localhost UI/API is then rejected, with only GET/HEAD `/api/health` retained for Docker's local probe. Users must sign in through HTTPS; the cookie name becomes `__Host-familyvault_session`.

The proxy terminates TLS and forwards HTTP to the existing loopback-published backend while preserving Host. [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) provides private tailnet access and requires HTTPS certificates; access rules still apply. Its [TCP-backend proxy implementation](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go) preserves the incoming Host. This checkpoint does not enable Serve or Funnel, change tailnet rules, or configure a certificate.

The backend does not trust Forwarded/X-Forwarded-* or Tailscale identity headers for origin, sessions, authorization or login rate-limit identity. File access continues to require FamilyVault login and current database permissions. The socket-peer login budget is shared behind a proxy; this is an availability limitation, not a per-tailnet-user limiter. The local host, Docker network and TLS-terminating proxy remain trusted. Do not publish the backend on the LAN or bypass it with a file-serving proxy.

## Verification and limits

Checkpoint 1 exercises exact Host/Origin matching, invalid configuration, default-mode forwarded-header spoofing, login Origin requirements, health-only localhost access, and cross-site rejection. A real local TLS test proxy checks Secure/HttpOnly/SameSite cookie flags, successful session use over TLS, withholding the Secure cookie over HTTP, CSRF, cross-user read/delete denial, logout and revocation. The Compose smoke test also switches its synthetic app into HTTPS-origin mode to verify configuration wiring and healthy local probing.

Actual Tailscale TLS, certificate issuance, second-device access and access-rule denials remain unverified until Checkpoint 2. Existing backup/restore, production readiness and host trust limitations remain. Only synthetic documents are suitable for the demonstration; this does not establish production readiness.

Verification on October 29, 2026: full Go formatting/vet/race checks passed (**80.5% combined coverage**) and the Linux ARM64 server build passed. All **42 frontend tests**, frontend formatting/strict TypeScript/build, **four Chrome flows**, Python syntax and the expanded Compose smoke test passed. The initial cookie transport test failed because loopback origins receive special treatment; the corrected test uses certificate-verified `example.com` with connections routed locally and no disabled certificate validation. Hosted CI results have not been verified. Native Tailscale 1.104.4 reports this Mac connected and no Serve configuration; no tailnet rules or live exposure were changed.

Changed files: `backend/internal/config/{config.go,config_test.go}`, `backend/internal/api/{api.go,api_test.go,auth.go,middleware.go,origin_test.go}`, `backend/cmd/server/main.go`, `compose.yaml`, `.env.example`, `frontend/{src/App.tsx,playwright.config.ts}`, `tests/container_smoke.py`, `docs/{architecture.md,authentication.md,threat-model.md,private-access.md}` and the existing README guide.
