import { defineConfig } from '@playwright/test';

// Admin panel against the REAL Go API (no page.route mocks): the stand
// internal/web/admin_live_smoke_test.go (build tag adminsmoke) serves the
// embedded admin build, admin auth/CSRF, /admin/api and /sub on a temporary
// SQLite database. Start the stand first; see admin/live/README.md.
export default defineConfig({
  testDir: './admin/live',
  outputDir: './test-results/admin-live',
  fullyParallel: false,
  workers: 1,
  timeout: 120_000,
  use: {
    baseURL: process.env.ADMIN_LIVE_ORIGIN ?? 'https://localhost:18181',
    // The stand terminates TLS with a self-signed local test certificate.
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 900 },
    browserName: 'chromium',
    locale: 'ru-RU',
    timezoneId: 'UTC',
  },
});
