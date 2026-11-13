import { defineConfig, type PlaywrightTestConfig } from '@playwright/test';
import {
  ADMIN_API_BIN,
  adminApiEnv,
  apiOrigin,
  browserOf,
  cleanEnv,
  enabledProjects,
  fixtureArgs,
  fixtureEnv,
  GEOFIXTURE_BIN,
  GEOFIXTURE_URL,
  geoProject,
  MAILPIT,
  MAILPIT_BIN,
  MAILPIT_URL,
  mailpitEnv,
  origin,
  PAIRS,
  type ProjectName,
  viteEnv,
  WORKER_BIN,
  workerEnv,
} from './e2e/env';

// Живой e2e админки (Task 16): настоящие admin-api, Postgres (база wf_e2e) и Mailpit. Запуск — только
// ./task admin:e2e (scripts/e2e-admin.sh): Playwright стартует webServer ДО globalSetup, поэтому базу,
// миграции, сборку admin-api и ключ готовит скрипт.
//
// Dev-значения админки (backend/.env грузит корневой Taskfile) из окружения раннера убираются: их
// унаследовали бы webServer и подкоманды оператора.
const keep = cleanEnv();
for (const k of Object.keys(process.env)) {
  if (!Object.hasOwn(keep, k)) delete process.env[k];
}

const ci = Boolean(process.env.CI);
const projects = enabledProjects();

// Каждый сервер — свой процесс без обёртки pnpm (она не передаёт сигнал остановки). Порядок —
// Mailpit → admin-api → Vite: следующий стартует после готовности предыдущего (url). Уже занятый
// порт — ошибка, а не чужой сервер (reuseExistingServer: false): у него была бы другая база и ключ.
// prod — только admin-api: собранную админку (apps/admin/dist) отдаёт он сам; сборку делает
// scripts/e2e-admin.sh до Playwright (webServer стартует раньше globalSetup).
const servers: NonNullable<PlaywrightTestConfig['webServer']> = [
  {
    // без --database: база в памяти, исчезает с процессом — каждый прогон с пустым ящиком
    command: `"${MAILPIT_BIN}" --listen 127.0.0.1:${MAILPIT.ui} --smtp 127.0.0.1:${MAILPIT.smtp}`,
    url: `${MAILPIT_URL}/readyz`,
    env: mailpitEnv() as Record<string, string>,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: 'ignore',
  },
  // HTTPS-фикстура GeoNames (план B, Task 23): готова, когда PEM корня записан и /readyz отвечает (сертификат
  // тестовый — проверку готовности он не останавливает); воркер стартует после неё и доверяет корню из файла
  {
    command: [GEOFIXTURE_BIN, ...fixtureArgs()].map((a) => `"${a}"`).join(' '),
    url: `${GEOFIXTURE_URL}/readyz`,
    ignoreHTTPSErrors: true,
    env: fixtureEnv() as Record<string, string>,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: 'ignore',
  },
  // Воркер: исполняет задачу geo.import из общей базы. Порта нет — готовность по строке журнала «воркер запущен»
  // (уровень info); журнал — в вывод прогона: ошибка старта видна текстом, а не одним кодом выхода
  {
    command: `"${WORKER_BIN}"`,
    wait: { stderr: /воркер запущен/ },
    env: workerEnv() as Record<string, string>,
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'ignore',
  },
];
for (const name of projects) {
  const pair = PAIRS[name];
  servers.push({
    command: `"${ADMIN_API_BIN}"`,
    url: `${apiOrigin(pair)}/readyz`,
    env: adminApiEnv(pair) as Record<string, string>,
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'ignore',
  });
  if ('vite' in pair) {
    servers.push({
      command: `vite --config e2e/vite.config.ts --host 127.0.0.1 --port ${pair.vite} --strictPort`,
      url: origin(pair),
      env: viteEnv(pair) as Record<string, string>,
      reuseExistingServer: false,
      timeout: 120_000,
    });
  }
}

// Какие сценарии в каком проекте: полный сценарий §11 — Chromium; вход с протухшей cookie — в обоих
// браузерах; собранная админка под настоящей CSP (тема до первого кадра, обход разделов) — prod.
// Импорт и активация (geo.spec.ts) — в одном проекте (geoProject): prod (Chromium под настоящей CSP), без него —
// chromium; место GeoNames активируется в общей базе один раз за прогон.
const specs: Record<ProjectName, string[]> = {
  chromium: ['staff.spec.ts', 'stale-cookie.spec.ts'],
  webkit: ['stale-cookie.spec.ts'],
  prod: ['prod.spec.ts'],
};
const geo = geoProject(projects);
if (geo) specs[geo].push('geo.spec.ts');

export default defineConfig({
  testDir: './e2e',
  testMatch: '*.spec.ts',
  // Повторы запрещены: bootstrap той же почты второй раз — ошибка (спека §4.2), а шаги сценария
  // зависят друг от друга. Один исполнитель — сценарии общей базы не перемешиваются.
  retries: 0,
  workers: 1,
  fullyParallel: false,
  // вход ждёт следующий шаг TOTP (до 30 с) — несколько раз за тест
  timeout: 180_000,
  expect: { timeout: 15_000 },
  globalTimeout: 20 * 60_000,
  // без html-отчёта: playwright-report не создаётся; трейс упавшего теста — в test-results
  reporter: [['list']],
  forbidOnly: ci,
  use: {
    locale: 'ru-RU',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    // ожидания ответа и действий — с пределом: упавший шаг виден за секунды, а не по таймауту теста
    actionTimeout: 20_000,
    navigationTimeout: 30_000,
  },
  projects: projects.map((name) => ({
    name,
    testMatch: specs[name],
    use: { browserName: browserOf(name), baseURL: origin(PAIRS[name]) },
  })),
  webServer: servers,
});
