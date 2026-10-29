import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig } from '@playwright/test';

// Isolated synthetic data, separate from the developer's library. Kept in OS temp.
const dataDir = mkdtempSync(join(tmpdir(), 'safevault-browser-'));
// macOS can point at an isolated Linux Compose test app for bounded PDF rendering.
const externalURL = process.env.PW_BASE_URL;
if (externalURL) {
  const url = new URL(externalURL);
  if (
    url.protocol !== 'http:' ||
    !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)
  ) {
    throw new Error(
      'Browser tests require a localhost HTTP app with synthetic data.',
    );
  }
}
export default defineConfig({
  testDir: './e2e',
  workers: 1,
  use: {
    baseURL: externalURL ?? 'http://127.0.0.1:18080',
    launchOptions: { executablePath: process.env.PW_CHROME_EXECUTABLE_PATH },
    trace: 'retain-on-failure',
  },
  webServer: externalURL
    ? undefined
    : {
        command:
          'printf "%s\\n" "fictional-browser-password" | go run ./cmd/create-admin -username browser-admin -password-stdin && go run ./cmd/server',
        cwd: '../backend',
        url: 'http://127.0.0.1:18080/api/health',
        reuseExistingServer: false,
        timeout: 120_000,
        env: {
          SAFEVAULT_ADDR: '127.0.0.1:18080',
          SAFEVAULT_DATA_DIR: dataDir,
          SAFEVAULT_STATIC_DIR: '../frontend/dist',
          SAFEVAULT_CONTAINER: '',
          SAFEVAULT_COOKIE_SECURE: 'false',
          SAFEVAULT_PUBLIC_ORIGIN: '',
          SAFEVAULT_SESSION_TTL: '8h',
          SAFEVAULT_SESSION_IDLE_TTL: '30m',
          SAFEVAULT_MAX_UPLOAD_BYTES: '26214400',
        },
      },
});
