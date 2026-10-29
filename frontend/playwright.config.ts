import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { defineConfig } from '@playwright/test';

// Isolated synthetic data, separate from the developer's library. Kept in OS temp.
const dataDir = mkdtempSync(join(tmpdir(), 'familyvault-browser-'));
export default defineConfig({
  testDir: './e2e',
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:18080',
    launchOptions: { executablePath: process.env.PW_CHROME_EXECUTABLE_PATH },
    trace: 'retain-on-failure',
  },
  webServer: {
    command:
      'printf "%s\\n" "fictional-browser-password" | go run ./cmd/create-admin -username browser-admin -password-stdin && go run ./cmd/server',
    cwd: '../backend',
    url: 'http://127.0.0.1:18080/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
    env: {
      FAMILYVAULT_ADDR: '127.0.0.1:18080',
      FAMILYVAULT_DATA_DIR: dataDir,
      FAMILYVAULT_STATIC_DIR: '../frontend/dist',
      FAMILYVAULT_CONTAINER: '',
      FAMILYVAULT_COOKIE_SECURE: 'false',
      FAMILYVAULT_SESSION_TTL: '8h',
      FAMILYVAULT_SESSION_IDLE_TTL: '30m',
      FAMILYVAULT_MAX_UPLOAD_BYTES: '26214400',
    },
  },
});
