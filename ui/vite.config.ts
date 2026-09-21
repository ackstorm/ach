/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'node:path';

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  // Embedded by internal/platformapi/console (go:embed all:dist); keep in
  // sync with the Dockerfile `ui` stage COPY.
  build: { outDir: '../internal/platformapi/console/dist', emptyOutDir: true },
  // Dev: platform-api on 8080 (make cluster-up + port-forward, or a local binary).
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/platform': 'http://localhost:8080',
      '/openwork': 'http://localhost:8080',
      '/api/den': 'http://localhost:8080',
    },
  },
  test: { environment: 'jsdom', globals: true, setupFiles: './src/test-setup.ts' },
});
