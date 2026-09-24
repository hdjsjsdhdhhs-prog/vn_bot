import { defineConfig } from 'vitest/config';

// `vite build --mode admin` builds the browser admin panel from ./admin into its
// own output. Relative asset URLs keep it independent of the final mount path.
const admin = {
  root: 'admin',
  base: './',
  build: { outDir: '../../internal/web/admin_dist/public', emptyOutDir: true, sourcemap: false },
};

export default defineConfig(({ mode }) => mode === 'admin' ? admin : {
  base: '/miniapp/',
  build: { outDir: '../internal/web/miniapp_dist/public', emptyOutDir: true, sourcemap: false },
  server: { proxy: { '/api/miniapp': 'http://127.0.0.1:8880' } },
  test: { environment: 'jsdom', include: ['tests/**/*.test.ts'], maxWorkers: 1 },
});
