/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';
import { wfShots } from './vite.shots.ts';

const CSS_TARGETS = ['chrome123', 'firefox125', 'safari17.5', 'ios17.5'] as const;

// Шрифты — всегда файлами: мелкий woff/woff2 (< 4 КБ, подмножества @fontsource) Vite встроил бы в CSS как
// data:font/…, а staticCSP admin-api — font-src 'self' (нашёл прод-проект e2e, UF1b.7). Прочее — по умолчанию.
const FONT = /\.(woff2?|ttf|otf|eot)$/i;
export const assetsInlineLimit = (file: string): false | undefined =>
  FONT.test(file) ? false : undefined;

// Куда dev-сервер проксирует /v1. Серверная переменная: без префикса VITE_ в сборку не попадает.
export function adminApiOrigin(env: Record<string, string | undefined>): string {
  return env.WF_ADMIN_API_ORIGIN || 'http://127.0.0.1:8081';
}

// Dev: запросы /v1 проксируются в admin-api — same-origin, без CORS.
// В проде тот же путь отдаёт реверс-прокси (спека деплоя).
// Режим shots (`vite --mode shots`, спека §5.4): админка с моками MSW в браузере для снимков
// (scripts/admin-shots.mjs) — плагин wf-shots, без прокси, свой порт и свой каталог кэша (два
// dev-сервера в общий node_modules/.vite дают EPERM на Windows, как у e2e/vite.config.ts).
export default defineConfig(({ mode }) => {
  const shots = mode === 'shots';
  return {
    plugins: shots ? [react(), wfShots()] : [react()],
    cacheDir: shots ? 'node_modules/.vite-shots' : undefined,
    // точка входа shots подменяется в index.html плагином — сканер зависимостей её бы не увидел
    optimizeDeps: shots ? { entries: ['src/shots/main.tsx'] } : undefined,
    build: {
      // Минимальные браузеры админки (спека UI-фундамента §2.1): Firefox 125 — Popover API, iOS 17.5 —
      // ниже Lightning CSS заменяет light-dark() полифилом --lightningcss-* (прод-CSS ≠ dev-CSS).
      // Сборку сверяет scripts/check-admin-dist.mjs в admin:ci.
      cssTarget: [...CSS_TARGETS],
      assetsInlineLimit,
    },
    server: {
      host: '127.0.0.1',
      port: shots ? 5176 : 5173,
      strictPort: true,
      // loadEnv: .env/.env.local рядом с конфигом; переменная окружения процесса важнее файлов
      proxy: shots
        ? undefined
        : { '/v1': adminApiOrigin(loadEnv(mode, import.meta.dirname, 'WF_')) },
    },
    test: {
      // e2e/*.spec.ts — сценарии Playwright (живой e2e): Vitest берёт только *.test.*
      include: ['src/**/*.test.{ts,tsx}', 'e2e/**/*.test.ts', '*.test.ts'],
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts'],
    },
  };
});
