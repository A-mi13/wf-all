import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Окружение живого e2e (Task 16): свои порты (прогон не задевает ./task dev: 5173/8081/18025), своя база
// wf_e2e и одноразовый ключ сотрудников — их готовит scripts/e2e-admin.sh и передаёт переменными WF_E2E_*.
// admin-api получает ТОЛЬКО окружение отсюда: dev-значения ADMIN_* из backend/.env (корневой Taskfile
// грузит его dotenv) вычищаются (cleanEnv).

export const ROOT = fileURLToPath(new URL('../../..', import.meta.url));

const exe = process.platform === 'win32' ? '.exe' : '';
// admin-api собирает scripts/e2e-admin.sh; Mailpit — scripts/bootstrap.sh (тот же, что у ./task mail)
export const ADMIN_API_BIN = path.join(ROOT, '.tools', 'e2e', `admin-api${exe}`);
export const MAILPIT_BIN = path.join(ROOT, '.tools', 'bin', `mailpit${exe}`);

export const MAILPIT = { ui: 18026, smtp: 11026 } as const;
export const MAILPIT_URL = `http://127.0.0.1:${MAILPIT.ui}`;

// Пара «Vite + admin-api» на проект браузера. Chromium — cookie как в проде (__Host- + Secure: R6,
// Chromium принимает её на http://127.0.0.1). WebKit __Host- + Secure на http не принимает (R6) —
// у него своя пара с ADMIN_COOKIE_SECURE=false (конфиг процесса admin-api) на той же базе и Mailpit.
export type Pair = { vite: number; api: number; secure: boolean };
export const PAIRS = {
  chromium: { vite: 5174, api: 18181, secure: true },
  webkit: { vite: 5175, api: 18182, secure: false },
} as const satisfies Record<string, Pair>;
export type ProjectName = keyof typeof PAIRS;

export const origin = (p: Pair) => `http://127.0.0.1:${p.vite}`;
export const apiOrigin = (p: Pair) => `http://127.0.0.1:${p.api}`;
// Имя cookie сессии — как httpapi/admin.CookieName(secure)
export const cookieName = (p: Pair) => (p.secure ? '__Host-wf_admin_session' : 'wf_admin_session');

export function pairOf(project: string): Pair {
  if (!Object.hasOwn(PAIRS, project)) throw new Error(`e2e: неизвестный проект «${project}»`);
  return PAIRS[project as ProjectName];
}

// Проекты прогона: WF_E2E_PROJECTS (через запятую), по умолчанию оба. CI — только chromium (браузер
// WebKit на раннере требует системных библиотек через apt).
export function enabledProjects(env: NodeJS.ProcessEnv = process.env): ProjectName[] {
  const raw = env.WF_E2E_PROJECTS?.trim();
  if (!raw) return ['chromium', 'webkit'];
  const names = raw.split(',').map((s) => s.trim());
  for (const n of names) pairOf(n);
  return names as ProjectName[];
}

// Окружение процесса без dev-настроек админки: ADMIN_* и адрес прокси Vite.
export function cleanEnv(env: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  return Object.fromEntries(
    Object.entries(env).filter(([k]) => !k.startsWith('ADMIN_') && k !== 'WF_ADMIN_API_ORIGIN'),
  );
}

// Ключ сотрудников и строка подключения прогона (их ставит scripts/e2e-admin.sh) нужны только admin-api —
// и уже под именами ADMIN_* (adminApiEnv). Playwright запускает webServer с окружением поверх process.env
// (DEFAULT_ENVIRONMENT_VARIABLES, process.env, env сервера), поэтому «не передать» нельзя — значения
// затираются пустыми (ревью Task 16, M7): ни Vite, ни Mailpit, ни дочерние процессы их не видят.
const RUN_SECRETS = { WF_E2E_STAFF_SECRET_KEYS: '', WF_E2E_ADMIN_DATABASE_URL: '' } as const;

function required(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(
      `e2e: нет ${name} — живой e2e запускается через ./task admin:e2e (scripts/e2e-admin.sh готовит базу, ключ и admin-api)`,
    );
  }
  return v;
}

// Окружение admin-api пары: сервер и подкоманды оператора (staff bootstrap) — одно и то же.
export function adminApiEnv(p: Pair): NodeJS.ProcessEnv {
  return {
    ...cleanEnv(),
    ...RUN_SECRETS,
    ADMIN_HTTP_ADDR: `127.0.0.1:${p.api}`,
    ADMIN_DATABASE_URL: required('WF_E2E_ADMIN_DATABASE_URL'),
    ADMIN_LOG_FORMAT: 'text',
    // журнал запросов admin-api в выводе прогона — шум; предупреждения и ошибки остаются
    ADMIN_LOG_LEVEL: 'warn',
    ADMIN_DOCS_ENABLED: 'false',
    ADMIN_TRUSTED_PROXIES: '',
    ADMIN_CLIENT_IP_HEADER: '',
    ADMIN_RATE_LIMITS: '',
    ADMIN_ORIGIN: origin(p),
    ADMIN_STAFF_SECRET_KEYS: required('WF_E2E_STAFF_SECRET_KEYS'),
    ADMIN_IP_ALLOWLIST: '',
    ADMIN_COOKIE_SECURE: String(p.secure),
    // статику отдаёт Vite (dev-сервер e2e), не admin-api
    ADMIN_STATIC_DIR: '',
    ADMIN_MAIL_SMTP_ADDR: `127.0.0.1:${MAILPIT.smtp}`,
    ADMIN_MAIL_FROM: 'WhereFootball e2e <noreply@wf-e2e.test>',
    ADMIN_MAIL_SMTP_TLS: 'none',
  };
}

// Окружение Vite пары: /v1 проксируется в admin-api этой пары; кэш зависимостей — свой (e2e/vite.config.ts).
export function viteEnv(p: Pair): NodeJS.ProcessEnv {
  return {
    ...cleanEnv(),
    ...RUN_SECRETS,
    WF_ADMIN_API_ORIGIN: apiOrigin(p),
    WF_E2E_VITE_CACHE_DIR: `node_modules/.vite-e2e-${p.vite}`,
  };
}

// Окружение Mailpit: поверх process.env раннера (см. RUN_SECRETS) — без ключа и базы прогона.
export function mailpitEnv(): NodeJS.ProcessEnv {
  return { ...RUN_SECRETS };
}
