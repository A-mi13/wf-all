import { defineConfig } from '@playwright/test';

const ci = Boolean(process.env.CI);

export default defineConfig({
  testDir: './e2e',
  // зависший сервер или тест падает за минуты, а не висит до таймаута раннера
  globalTimeout: ci ? 5 * 60_000 : 0,
  use: { baseURL: 'http://localhost:3000' },
  webServer: {
    // next напрямую, без обёртки pnpm: она не передаёт сигнал остановки, и Playwright ждёт вечно.
    // В CI — собранная сборка (web:ci делает build перед e2e), локально — dev-сервер.
    command: ci ? 'next start --port 3000' : 'next dev --port 3000',
    url: 'http://localhost:3000',
    reuseExistingServer: !ci,
    timeout: 120_000,
  },
});
