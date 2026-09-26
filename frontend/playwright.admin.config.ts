import { defineConfig } from '@playwright/test';

// Browser admin panel, kept apart from the Mini App config (playwright.config.ts):
// its own build (`vite build --mode admin`), preview port, test directory and
// artifacts. The backend is mocked per test with page.route; the UTC time zone
// and ru-RU locale make formatted dates deterministic.
export default defineConfig({
  testDir: './admin/browser',
  outputDir: './test-results/admin',
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:4174',
    viewport: { width: 1280, height: 900 },
    browserName: 'chromium',
    locale: 'ru-RU',
    timezoneId: 'UTC',
  },
  webServer: {
    command: 'npm run build:admin && node node_modules/vite/bin/vite.js preview --mode admin --host 127.0.0.1 --port 4174 --strictPort',
    url: 'http://127.0.0.1:4174/',
    reuseExistingServer: false,
  },
});
