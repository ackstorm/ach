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
  // emptyOutDir wipes the embed directory, including the tracked .gitkeep that
  // keeps it present for `go:embed all:dist` before the console is ever built.
  // The npm "postbuild" hook restores it — NOT the Makefile, because release.yml
  // calls `npm run build` directly and a goreleaser run aborts on a dirty tree
  // ("D .../dist/.gitkeep", which is how v0.9.11 died). The hook covers the
  // `build` script only: a bare `npx vite build`, or a build that fails AFTER
  // emptyOutDir has run, still leaves the keep-file deleted. release.yml asserts
  // a clean tree right after the console build for exactly that reason.
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
