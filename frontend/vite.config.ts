import { defineConfig } from 'vitest/config';

export default defineConfig({
  base: '/miniapp/',
  build: { outDir: '../internal/web/miniapp_dist/public', emptyOutDir: true, sourcemap: false },
  server: { proxy: { '/api/miniapp': 'http://127.0.0.1:8880' } },
  test: { environment: 'jsdom', include: ['tests/**/*.test.ts'], maxWorkers: 1 },
});
