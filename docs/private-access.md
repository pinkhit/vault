# Milestone 5 — private HTTPS access

Milestone 4 (local AI) is deferred at the user's request. Milestone 3 was merged into `dev` in PR #6 at `1ea8a24`; work continues on `feature/private-access`. The existing README guide was preserved. Each checkpoint stops for review and commit; no automatic commits or pushes. Checkpoint 2 enables private HTTPS only after explicit user approval and Tailscale account consent.

| Checkpoint | Scope |
| --- | --- |
| 1 — application origin | Explicit HTTPS origin, Secure-cookie requirement, unchanged localhost default, negative HTTP tests and local TLS-proxy integration |
| 2 — live private access | Configure native Tailscale Serve, verify HTTPS certificates/access rules, test from a second device and document shutdown/rollback |
| 3 — submission | Review CI, prepare synthetic demo/screenshots and concise deployment/security notes |

## Application configuration

All examples use the current SafeVault identifiers. Existing deployments should follow the [rename and storage migration notes](ui-polish.md#deployment-identifiers) before switching projects. The verification records below describe their original checkpoints; current UI and rename checks are recorded separately in the [workspace verification](ui-polish.md#verification--october-29-2026).

Defaults remain localhost HTTP. An empty `SAFEVAULT_PUBLIC_ORIGIN` keeps the existing loopback Host/Origin checks. Compose still publishes only `127.0.0.1`; setting an origin never opens a listener or enables Tailscale.

HTTPS mode requires **both** `SAFEVAULT_PUBLIC_ORIGIN=https://device.tailnet.ts.net` (the exact URL, without a trailing slash, path, credentials, query or fragment) and `SAFEVAULT_COOKIE_SECURE=true`. Startup rejects an invalid origin or non-Secure cookies. One hostname is accepted; no wildcard tailnet allowlist is introduced. Login and other browser Origin checks use that explicit HTTPS origin. The ordinary localhost UI/API is then rejected, with only GET/HEAD `/api/health` retained for Docker's local probe. Users must sign in through HTTPS; the cookie name becomes `__Host-safevault_session`.

The proxy terminates TLS and forwards HTTP to the existing loopback-published backend while preserving Host. [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) provides private tailnet access and requires HTTPS certificates; access rules still apply. Its [TCP-backend proxy implementation](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go) preserves the incoming Host. Checkpoint 1 did not enable Serve or Funnel, change tailnet rules, or configure a certificate.

The backend does not trust Forwarded/X-Forwarded-* or Tailscale identity headers for origin, sessions, authorization or login rate-limit identity. File access continues to require SafeVault login and current database permissions. The socket-peer login budget is shared behind a proxy; this is an availability limitation, not a per-tailnet-user limiter. The local host, Docker network and TLS-terminating proxy remain trusted. Do not publish the backend on the LAN or bypass it with a file-serving proxy.

## Verification and limits

Checkpoint 1 exercises exact Host/Origin matching, invalid configuration, default-mode forwarded-header spoofing, login Origin requirements, health-only localhost access, and cross-site rejection. A real local TLS test proxy checks Secure/HttpOnly/SameSite cookie flags, successful session use over TLS, withholding the Secure cookie over HTTP, CSRF, cross-user read/delete denial, logout and revocation. The Compose smoke test also switches its synthetic app into HTTPS-origin mode to verify configuration wiring and healthy local probing.

At the end of Checkpoint 1, actual Tailscale TLS, certificate issuance, second-device access and access-rule denials remained unverified. Checkpoint 2 results below supersede those pending items only where explicitly tested. Existing backup/restore, production readiness and host trust limitations remain. Only synthetic documents are suitable for the demonstration; this does not establish production readiness.

Verification on October 29, 2026: full Go formatting/vet/race checks passed (**80.5% combined coverage**) and the Linux ARM64 server build passed. All **42 frontend tests**, frontend formatting/strict TypeScript/build, **four Chrome flows**, Python syntax and the expanded Compose smoke test passed. The initial cookie transport test failed because loopback origins receive special treatment; the corrected test uses certificate-verified `example.com` with connections routed locally and no disabled certificate validation. Hosted CI results have not been verified. Native Tailscale 1.104.4 reports this Mac connected and no Serve configuration; no tailnet rules or live exposure were changed.

Changed files: `backend/internal/config/{config.go,config_test.go}`, `backend/internal/api/{api.go,api_test.go,auth.go,middleware.go,origin_test.go}`, `backend/cmd/server/main.go`, `compose.yaml`, `.env.example`, `frontend/{src/App.tsx,playwright.config.ts}`, `tests/container_smoke.py`, `docs/{architecture.md,authentication.md,threat-model.md,private-access.md}` and the existing README guide.

## Checkpoint 2 — host-side Serve setup

Keep SafeVault in Docker and install/sign into native Tailscale on the host and a second device. Use the exact HTTPS device name shown by Tailscale (without a final DNS dot). On a Mac mini this will be the mini's name, not the development Mac's name. The host must remain awake with Docker and Tailscale running; this setup is not a production deployment or a guarantee of availability.

Set these two fields in the ignored local `.env`, preserving any other settings:

```dotenv
SAFEVAULT_PUBLIC_ORIGIN=https://device.tailnet.ts.net
SAFEVAULT_COOKIE_SECURE=true
```

Then run from the repository root:

```sh
docker compose up -d --build --wait
tailscale serve --bg --https=443 http://127.0.0.1:8080
tailscale serve status
```

Follow Tailscale's HTTPS consent flow if required. Serve uses a publicly trusted certificate; [certificate transparency exposes the device DNS name](https://tailscale.com/docs/how-to/set-up-https-certificates), not document contents. Do not enable Funnel or router port forwarding. Serve proxies the application port, never the data directory. The database and original files stay in `safevault_vault-data`; there is no data migration. Existing users sign in again through the HTTPS URL.

On the second device, connect Tailscale, open the HTTPS URL and sign in with a SafeVault account. Verify synthetic upload/search/preview/download and sign-out. A phone on cellular verifies access outside the LAN. With Tailscale disconnected, the private URL should be unreachable. A connected device without a SafeVault session must receive 401 for protected endpoints. This does not substitute for testing a device explicitly denied by tailnet policy.

Tailnet access rules govern which members/devices can reach HTTPS port 443; application grants govern which documents they can access. Review the actual policy in the Tailscale admin console before adding other users. The default tailnet policy may permit all members; no policy changes or policy-denial verification are implied by merely enabling Serve. Keep the existing backend port published only on `127.0.0.1`.

To stop remote access without deleting data:

```sh
tailscale serve --bg --https=443 off
docker compose stop app
```

To return to localhost HTTP, first disable Serve, then set `SAFEVAULT_PUBLIC_ORIGIN=` and `SAFEVAULT_COOKIE_SECURE=false` in `.env` and run `docker compose up -d --wait`. Do not use `down -v`. To resume private access, restore both HTTPS settings, start the app, then enable Serve again. Verify `tailscale serve status` after either change; background Serve survives terminal closure, and a stopped backend alone does not remove the proxy configuration.

## Checkpoint 2 verification — October 29, 2026

| Boundary | Actual result |
| --- | --- |
| Real certificate and browser transport | curl and Chrome accepted the issued certificate using normal trust validation, with no insecure flags. Chrome negotiated TLS 1.3, rendered the login page and reported a secure context; desktop/mobile login screenshots were inspected. |
| Unauthenticated HTTPS requests | Session, listing, search, storage, download, preview and indexing endpoints returned 401 through Serve. A foreign login Origin returned 403; forged identity headers returned 401. Existing CSP, no-store, nosniff and frame-denial headers remained present. |
| Exposure and persistence | Serve configuration contains only HTTPS 443 proxying to `127.0.0.1:8080`, with no AllowFunnel entry. Docker still publishes only loopback; the healthy app retains `safevault_vault-data`. |
| Rollback and resume | Disabling Serve made HTTPS unreachable. Switching to localhost HTTP restored the frontend with status 200. Restoring HTTPS mode and Serve restored the private endpoint and returned localhost frontend access to 403. The intended ignored `.env` settings were retained throughout. |
| Second device | The user reported successful phone sign-in/library access over cellular and the expected denial with Tailscale disconnected, then reconnected. This is user-reported verification, not an automated observation from the Mac. |

This increment changes runtime configuration, documentation and one stale sign-in warning from “Localhost development” to “Development demonstration”; authentication logic and dependencies are unchanged. The regular vault was used only for unauthenticated automated checks; no test accounts or documents were added. Checkpoint 1's full Go/local browser/Compose results were not rerun or relabelled as new results. This checkpoint ran the live HTTPS/browser/negative checks, rollback/resume checks and `git diff --check`; the warning change also passed all 42 frontend tests, formatting and the TypeScript/production build. No commits or pushes were made. The local `.env` is ignored by Git/Docker; an unrelated `.vscode` directory was left untouched.

The actual tailnet access policy was not edited or independently inspected; a policy-excluded-device denial test remains outstanding. The phone confirmation does not establish upload/preview/download coverage on every remote browser. HSTS, host sleep/reboot persistence, real Mac mini deployment, backup/restore and hosted CI verification remain outside this checkpoint. The app still requires SafeVault credentials even for connected tailnet devices.
