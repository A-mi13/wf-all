/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Dev: запросы /v1 проксируются в admin-api — same-origin, без CORS.
// В проде тот же путь отдаёт реверс-прокси (спека деплоя).
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: { '/v1': 'http://127.0.0.1:8081' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
});
