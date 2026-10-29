# Milestone 1 verification

Recorded during implementation on October 28, 2026, on macOS AMD64. Tests use fictional documents and temporary data directories. These are observed results, not a production-readiness claim.

After the readability review, Go formatting, static analysis and race/integration tests were repeated in a Linux Docker container. Frontend formatting, unit tests, the production build, the browser test against an isolated Docker container, and the Compose restart smoke test also passed again. The native/cross-build and bounded fuzzing results below are from the initial milestone validation.

| Check | Observed result |
| --- | --- |
| Frontend unit tests | 5 tests passed |
| TypeScript strict check and Vite production build | Passed |
| Frontend Prettier check | Passed |
| Browser end-to-end check using local headless Chrome | 1 test passed: UI load, upload with SHA-256, page reload, identical attachment download, checksum display, desktop/mobile layout, no JavaScript page errors |
| Go race/integration suite | Passed with the final `modernc.org/sqlite v1.61.0` pin |
| Go coverage | 77.0% combined statement coverage after the readability review, using `-coverpkg=./...`; includes untested startup/shutdown statements |
| Filename fuzzing | Passed, 120,923 executions in the observed bounded run; not a comprehensive proof of validation safety |
| Go static analysis | `go vet ./...` passed with the final dependency pin |
| Native Go server and Linux AMD64/ARM64 cross-builds | Passed with the final dependency pin; cross-builds used `CGO_ENABLED=0` |
| Compose configuration | Passed with Docker Compose 5.6.1, including the complete smoke test |
| Compose smoke script syntax | `sh -n tests/compose-smoke.sh` passed |
| Docker image build, health check and container restart persistence | **Passed:** Docker Desktop, Docker CLI 29.9.2 and Compose 5.6.1; built both frontend/backend stages, started healthy, served the homepage, uploaded with a matching SHA-256, restarted the container, listed the persisted document and downloaded identical original bytes |
| Docker host exposure | Smoke test confirmed the published mapping is `127.0.0.1:8080` |
| GitHub Actions | Configured, not executed here; no changes have been pushed |

The initial sandbox blocked the Go test's localhost listener. The successful HTTP integration and browser runs used the required environment permission to bind locally. Initial runs also found and led to fixes for a 404/405 method-routing error and mobile overflow caused by an absolutely positioned accessibility label. Multipart transfer-encoded uploads are explicitly rejected to preserve original-byte semantics.

The backend integration test uses a real HTTP server, SQLite and object storage. It uploads the fixture, checks metadata and original bytes, closes/reopens the database and storage, then downloads identical bytes. The process-termination test exits a subprocess without graceful cleanup, retains a completed record and verifies reconciliation of staged/unreferenced objects. The Docker smoke test separately verified the persistent named volume across an actual container restart. None of these tests simulates power loss.

The Docker acceptance check completed successfully after Docker was installed. To repeat it with Docker Desktop/Engine running, execute from the repository root:

```sh
./tests/compose-smoke.sh
```

The image started, the health check passed, and original bytes survived the container restart. The smoke script is also wired into CI. It leaves the app running at http://localhost:8080 and adds a synthetic fixture to the development volume; it never removes volumes. No commits, pushes or merges were performed.

No performance benchmarks, power-loss simulations, real-device ARM64 execution, production backup/restore tests, authentication tests or remote-access tests were performed. Authentication and remote access are not implemented in this milestone.
