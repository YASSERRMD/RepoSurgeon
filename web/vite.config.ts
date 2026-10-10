import { fileURLToPath, URL } from 'node:url';

import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

// The web app is served same-origin behind the web proxy in production. In
// development the Vite dev server proxies API and health traffic to the Go API
// so the app always uses relative URLs.
const apiTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8081';

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: apiTarget, changeOrigin: true },
      '/health': { target: apiTarget, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
