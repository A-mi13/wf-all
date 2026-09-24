/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

// Куда dev-сервер проксирует /v1. Серверная переменная: без префикса VITE_ в сборку не попадает.
export function adminApiOrigin(env: Record<string, string | undefined>): string {
  return env.WF_ADMIN_API_ORIGIN || 'http://127.0.0.1:8081';
}

// Dev: запросы /v1 проксируются в admin-api — same-origin, без CORS.
// В проде тот же путь отдаёт реверс-прокси (спека деплоя).
export default defineConfig(({ mode }) => ({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    // loadEnv: .env/.env.local рядом с конфигом; переменная окружения процесса важнее файлов
    proxy: { '/v1': adminApiOrigin(loadEnv(mode, import.meta.dirname, 'WF_')) },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
}));
