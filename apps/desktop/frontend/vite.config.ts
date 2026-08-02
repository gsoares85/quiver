import { resolve } from 'node:path';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The shared UI package is aliased to its TypeScript source so the app builds and
// type-checks without a separate build step for `@quiver/ui` during development.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@quiver/ui': resolve(import.meta.dirname, '../../../packages/ui/src/index.ts'),
    },
  },
});
