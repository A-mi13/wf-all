import { defineConfig } from '@playwright/test';

const ci = Boolean(process.env.CI);

// Порт сервера e2e — WF_WEB_PORT из окружения процесса (Playwright не читает .env.local), по
// умолчанию 3000: CI не меняется. Локально reuseExistingServer берёт любой сервер на порту, поэтому
// при занятом :3000 чужим приложением e2e задают другой порт: WF_WEB_PORT=3100 pnpm e2e.
function webPort(): number {
  const raw = process.env.WF_WEB_PORT?.trim();
  if (!raw) return 3000;
  // только десятичные цифры: Number() принял бы и «0x10», «1e3», «3000.0»
  const port = /^\d+$/.test(raw) ? Number(raw) : NaN;
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`WF_WEB_PORT: ожидается порт 1–65535, получено «${raw}»`);
  }
  return port;
}

const port = webPort();
const origin = `http://localhost:${port}`;

export default defineConfig({
  testDir: './e2e',
  // зависший сервер или тест падает за минуты, а не висит до таймаута раннера
  globalTimeout: ci ? 5 * 60_000 : 0,
  use: { baseURL: origin },
  webServer: {
    // next напрямую, без обёртки pnpm: она не передаёт сигнал остановки, и Playwright ждёт вечно.
    // В CI — собранная сборка (web:ci делает build перед e2e), локально — dev-сервер.
    command: `${ci ? 'next start' : 'next dev'} --port ${port}`,
    url: origin,
    reuseExistingServer: !ci,
    timeout: 120_000,
  },
});
