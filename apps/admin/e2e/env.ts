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
// воркер и HTTPS-фикстура GeoNames (план B, Task 23) — их тоже собирает scripts/e2e-admin.sh
export const WORKER_BIN = path.join(ROOT, '.tools', 'e2e', `worker${exe}`);
export const GEOFIXTURE_BIN = path.join(ROOT, '.tools', 'e2e', `geofixture${exe}`);
// публичный api (план I-4, Task 11) — тоже собирает scripts/e2e-admin.sh, с тегом wf_e2e (тестовые порты капитанства)
export const API_BIN = path.join(ROOT, '.tools', 'e2e', `api${exe}`);

export const MAILPIT = { ui: 18026, smtp: 11026 } as const;
export const MAILPIT_URL = `http://127.0.0.1:${MAILPIT.ui}`;

// HTTPS-фикстура GeoNames (backend/cmd/geofixture): выгрузка RU из выдержек репозитория по путям download.geonames.org,
// сертификат — тестовый ключ httptest, корень — PEM в GEOFIXTURE_CA (воркер доверяет ему через
// WORKER_GEONAMES_CA_FILE). Путь собирает node, а не Git Bash: MSYS переписал бы значение с ведущим «/» (урок 27.10).
export const GEOFIXTURE = { port: 18190 } as const;
export const GEOFIXTURE_URL = `https://127.0.0.1:${GEOFIXTURE.port}`;
export const GEOFIXTURE_CA = path.join(ROOT, '.tools', 'e2e', 'geofixture-ca.pem');
const GEO_TESTDATA = path.join(
  ROOT,
  'backend',
  'internal',
  'geo',
  'internal',
  'source',
  'testdata',
);

export function fixtureArgs(): string[] {
  return [
    '--addr',
    `127.0.0.1:${GEOFIXTURE.port}`,
    '--data',
    GEO_TESTDATA,
    '--ca-out',
    GEOFIXTURE_CA,
  ];
}

// Пара «Vite + admin-api» на проект браузера. Chromium — cookie как в проде (__Host- + Secure: R6,
// Chromium принимает её на http://127.0.0.1). WebKit __Host- + Secure на http не принимает (R6) —
// у него своя пара с ADMIN_COOKIE_SECURE=false (конфиг процесса admin-api) на той же базе и Mailpit.
// prod (план UF1b.7, U9) — без Vite: admin-api сам отдаёт собранную админку (ADMIN_STATIC_DIR) с настоящим
// staticCSP; origin — адрес admin-api; браузер — Chromium. Сборку готовит scripts/e2e-admin.sh.
export type Pair = { api: number; secure: boolean } & (
  { vite: number; static?: never } | { static: true; vite?: never }
);
export const PAIRS = {
  chromium: { vite: 5174, api: 18181, secure: true },
  webkit: { vite: 5175, api: 18182, secure: false },
  prod: { api: 18183, secure: true, static: true },
} as const satisfies Record<string, Pair>;
export type ProjectName = keyof typeof PAIRS;

// Проекты по умолчанию (без WF_E2E_PROJECTS) — то же умолчание у scripts/e2e-admin.sh (страж env.test.ts):
// иначе локальный ./task ci прод-проект не гоняет (m20).
export const DEFAULT_PROJECTS: readonly ProjectName[] = ['chromium', 'webkit', 'prod'];

// Браузер проекта: prod — Chromium (собранная админка, cookie __Host- как в проде).
export const browserOf = (name: ProjectName): 'chromium' | 'webkit' =>
  name === 'webkit' ? 'webkit' : 'chromium';

// Города, которые scripts/e2e-admin.sh заводит после миграций (FLAG_CITIES, страж env.test.ts): матрица флагов в
// прод-сценарии — больше порога виртуализации столбцов (MATRIX_VIRTUALIZE_FROM × 3), в DOM — окно (G9-F, I-9).
export const E2E_FLAG_CITIES = 150;

// Собранная админка для prod — dist рядом с e2e (vite build из scripts/e2e-admin.sh)
export const ADMIN_DIST = path.join(ROOT, 'apps', 'admin', 'dist');

export const origin = (p: Pair) =>
  p.static ? `http://127.0.0.1:${p.api}` : `http://127.0.0.1:${p.vite}`;
export const apiOrigin = (p: Pair) => `http://127.0.0.1:${p.api}`;
// Имя cookie сессии — как httpapi/admin.CookieName(secure)
export const cookieName = (p: Pair) => (p.secure ? '__Host-wf_admin_session' : 'wf_admin_session');

export function pairOf(project: string): Pair {
  if (!Object.hasOwn(PAIRS, project)) throw new Error(`e2e: неизвестный проект «${project}»`);
  return PAIRS[project as ProjectName];
}

// Сценарий импорта и активации (geo.spec.ts) — в одном проекте: место 526815 активируется в общей базе один раз за
// прогон. prod — Chromium с настоящей CSP (ловит securitypolicyviolation), без него — chromium; без обоих — не идёт.
export function geoProject(projects: readonly ProjectName[]): ProjectName | undefined {
  return (['prod', 'chromium'] as const).find((p) => projects.includes(p));
}

// Сценарии публичного api (план I-4, Task 11: ядро identity и капитанская заявка через api и админку) — в одном
// проекте, как geo: игроки и заявки живут в общей базе, счётчик очереди «Капитаны» — общий на прогон. prod — Chromium
// под настоящей CSP, без него — chromium; без обоих — не идут.
export function identityProject(projects: readonly ProjectName[]): ProjectName | undefined {
  return geoProject(projects);
}

// Публичный api прогона (cmd/api с тегом wf_e2e): свой порт, база wf_e2e под ролью api, без встроенного воркера —
// письма с кодами ставит в очередь mail, отправляет воркер прогона (кольцо identity общее).
export const PUBLIC_API = { port: 18184 } as const;
export const PUBLIC_API_URL = `http://127.0.0.1:${PUBLIC_API.port}`;
// Документы прогона (API_LEGAL_DOCUMENTS): версии и ссылки — не секрет; регистрация берёт их из getLegalDocuments.
export const E2E_LEGAL_DOCUMENTS =
  'terms=1.0.0|https://wf-e2e.test/legal/terms,privacy=1.0.0|https://wf-e2e.test/legal/privacy';

// Хранилище прогона (спека media §6.3, §6.4): fs — один каталог у api, воркера и admin-api, пути /_blob и /_media
// отдаёт публичный api прогона. Ключ подписи — один у трёх процессов (admin-api подписывает ссылки на закрытое, api их
// проверяет); не секрет: fs живёт только в dev и e2e, прод-образ его отвергает (WF_BUILD_KIND=prod).
const E2E_BLOB_ROOT = path.join(ROOT, '.tools', 'e2e', 'blob');
const E2E_BLOB_SIGN_KEY = Buffer.alloc(32, 'wf-e2e-blob-sign').toString('base64url');
function blobEnv(prefix: 'API_' | 'ADMIN_' | 'WORKER_'): NodeJS.ProcessEnv {
  return {
    [`${prefix}BLOB_BACKEND`]: 'fs',
    [`${prefix}BLOB_FS_ROOT`]: E2E_BLOB_ROOT,
    [`${prefix}BLOB_FS_BASE_URL`]: PUBLIC_API_URL,
    [`${prefix}BLOB_DEV_SIGN_KEY`]: E2E_BLOB_SIGN_KEY,
  };
}

// Проекты прогона: WF_E2E_PROJECTS (через запятую), по умолчанию DEFAULT_PROJECTS. CI — chromium,prod
// (браузер WebKit на раннере требует системных библиотек через apt).
export function enabledProjects(env: NodeJS.ProcessEnv = process.env): ProjectName[] {
  const raw = env.WF_E2E_PROJECTS?.trim();
  if (!raw) return [...DEFAULT_PROJECTS];
  const names = raw.split(',').map((s) => s.trim());
  for (const n of names) pairOf(n);
  return names as ProjectName[];
}

// Окружение процесса без dev-настроек админки, воркера, api и мигратора: ADMIN_*, WORKER_*, API_*, MIGRATOR_*
// (backend/.env — база dev, настоящий GeoNames, dev-ключи), адрес прокси Vite и любая другая *_DATABASE_URL, кроме
// строк прогона WF_E2E_* (их затирает RUN_SECRETS): новая роль базы в backend/.env не утечёт в прогон молча.
const DEV_PREFIXES = ['ADMIN_', 'WORKER_', 'API_', 'MIGRATOR_'] as const;
const isDevKey = (k: string) =>
  DEV_PREFIXES.some((p) => k.startsWith(p)) ||
  k === 'WF_ADMIN_API_ORIGIN' ||
  (k.endsWith('_DATABASE_URL') && !k.startsWith('WF_E2E_'));
export function cleanEnv(env: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  return Object.fromEntries(Object.entries(env).filter(([k]) => !isDevKey(k)));
}

// Ключ сотрудников и строка подключения прогона (их ставит scripts/e2e-admin.sh) нужны только admin-api —
// и уже под именами ADMIN_* (adminApiEnv). Playwright запускает webServer с окружением поверх process.env
// (DEFAULT_ENVIRONMENT_VARIABLES, process.env, env сервера), поэтому «не передать» нельзя — значения
// затираются пустыми (ревью Task 16, M7): ни Vite, ни Mailpit, ни дочерние процессы их не видят.
// База воркера (WF_E2E_WORKER_DATABASE_URL) — так же: только воркеру, под именем WORKER_DATABASE_URL; кольцо
// identity (WF_E2E_IDENTITY_SECRET_KEYS, план I-2) — воркеру и api (одно кольцо: письма с кодами api собирает воркер)
// под именами WORKER_IDENTITY_SECRET_KEYS и API_IDENTITY_SECRET_KEYS. База, сид токенов, ключи антибота и устройств
// api (план I-4, Task 11) — только api, под именами API_*.
const RUN_SECRETS = {
  WF_E2E_STAFF_SECRET_KEYS: '',
  WF_E2E_ADMIN_DATABASE_URL: '',
  WF_E2E_WORKER_DATABASE_URL: '',
  WF_E2E_IDENTITY_SECRET_KEYS: '',
  WF_E2E_API_DATABASE_URL: '',
  WF_E2E_JWT_SEEDS: '',
  WF_E2E_HUMANCHECK_KEYS: '',
  WF_E2E_DEVICE_KEYS: '',
} as const;

function required(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(
      `e2e: нет ${name} — живой e2e запускается через ./task admin:e2e (scripts/e2e-admin.sh готовит базу, ключ и admin-api)`,
    );
  }
  return v;
}

// Значения прогона проверяются до старта (повторное ревью 27.10, Minor 1): Git Bash (MSYS)
// переписывает значение окружения с ведущим «/» в путь Windows («C:/Program Files/Git/…»), когда
// запускает не-MSYS программу (pnpm → node). scripts/e2e-admin.sh выдаёт ключ в base64url и исключает
// WF_E2E_* из конверсии (MSYS2_ENV_CONV_EXCL); здесь — страж на случай другого запуска. Значения в
// текст ошибки не попадают.
const BASE64URL = /^[A-Za-z0-9_-]+={0,2}$/;
const MIN_KEY_BYTES = 32; // как keys.NewRing

function runKey(name: string): string {
  const v = required(name);
  if (!BASE64URL.test(v) || Buffer.from(v, 'base64url').length < MIN_KEY_BYTES) {
    throw new Error(
      `e2e: ${name} — нужен ключ base64url не короче ${MIN_KEY_BYTES} байт (значение переписано или испорчено; в Git Bash — MSYS2_ENV_CONV_EXCL, см. scripts/e2e-admin.sh)`,
    );
  }
  return v;
}

function runDatabaseUrl(name: string): string {
  const v = required(name);
  if (!v.startsWith('postgres://') && !v.startsWith('postgresql://')) {
    throw new Error(
      `e2e: ${name} — нужен адрес postgres:// (значение переписано или испорчено; в Git Bash — MSYS2_ENV_CONV_EXCL, см. scripts/e2e-admin.sh)`,
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
    ADMIN_DATABASE_URL: runDatabaseUrl('WF_E2E_ADMIN_DATABASE_URL'),
    ADMIN_LOG_FORMAT: 'text',
    // журнал запросов admin-api в выводе прогона — шум; предупреждения и ошибки остаются
    ADMIN_LOG_LEVEL: 'warn',
    ADMIN_DOCS_ENABLED: 'false',
    ADMIN_TRUSTED_PROXIES: '',
    ADMIN_CLIENT_IP_HEADER: '',
    // Лимиты — умолчания admin-api (ratelimit.AdminRules: фоновые GET с X-WF-Background — свой класс, всплеск
    // default.user 120): обход разделов prod.spec — живой страж этих умолчаний (итоговое ревью I-4, I1)
    ADMIN_RATE_LIMITS: '',
    ADMIN_ORIGIN: origin(p),
    ADMIN_STAFF_SECRET_KEYS: runKey('WF_E2E_STAFF_SECRET_KEYS'),
    ADMIN_IP_ALLOWLIST: '',
    ADMIN_COOKIE_SECURE: String(p.secure),
    // статику отдаёт Vite (dev-сервер e2e), а у prod — сам admin-api (собранная админка, staticCSP)
    ADMIN_STATIC_DIR: p.static ? ADMIN_DIST : '',
    ADMIN_MAIL_SMTP_ADDR: `127.0.0.1:${MAILPIT.smtp}`,
    ADMIN_MAIL_FROM: 'WhereFootball e2e <noreply@wf-e2e.test>',
    ADMIN_MAIL_SMTP_TLS: 'none',
    ...blobEnv('ADMIN_'),
  };
}

// Окружение Vite пары: /v1 проксируется в admin-api этой пары; кэш зависимостей — свой (e2e/vite.config.ts).
export function viteEnv(p: Pair & { vite: number }): NodeJS.ProcessEnv {
  return {
    ...cleanEnv(),
    ...RUN_SECRETS,
    WF_ADMIN_API_ORIGIN: apiOrigin(p),
    WF_E2E_VITE_CACHE_DIR: `node_modules/.vite-e2e-${p.vite}`,
  };
}

// Окружение воркера (план B, Task 23): база прогона под ролью worker, источник GeoNames — только фикстура со своим
// корнем (внешней сети прогон не трогает), почта — Mailpit e2e (WORKER_MAIL_* воркеру обязательны), кольцо identity —
// одноразовый ключ прогона (обязательно воркеру, план I-2 Task 4).
export function workerEnv(): NodeJS.ProcessEnv {
  return {
    ...cleanEnv(),
    ...RUN_SECRETS,
    WORKER_DATABASE_URL: runDatabaseUrl('WF_E2E_WORKER_DATABASE_URL'),
    WORKER_IDENTITY_SECRET_KEYS: runKey('WF_E2E_IDENTITY_SECRET_KEYS'),
    WORKER_LOG_FORMAT: 'text',
    WORKER_GEONAMES_BASE_URL: GEOFIXTURE_URL,
    WORKER_GEONAMES_CA_FILE: GEOFIXTURE_CA,
    WORKER_MAIL_SMTP_ADDR: `127.0.0.1:${MAILPIT.smtp}`,
    WORKER_MAIL_FROM: 'WhereFootball e2e <noreply@wf-e2e.test>',
    WORKER_MAIL_SMTP_TLS: 'none',
    ...blobEnv('WORKER_'),
  };
}

// Окружение публичного api (план I-4, Task 11): база прогона под ролью api, одноразовые ключи прогона, кольцо identity —
// то же, что у воркера; без встроенного воркера (фон — воркер прогона), без прокси, BFF, базы города по IP и
// /.well-known; лимиты — по умолчанию (риск входа и регистрации, PoW — решает клиент e2e/api.ts).
export function apiEnv(): NodeJS.ProcessEnv {
  return {
    ...cleanEnv(),
    ...RUN_SECRETS,
    API_HTTP_ADDR: `127.0.0.1:${PUBLIC_API.port}`,
    API_DATABASE_URL: runDatabaseUrl('WF_E2E_API_DATABASE_URL'),
    API_LOG_FORMAT: 'text',
    API_LOG_LEVEL: 'warn',
    API_DOCS_ENABLED: 'false',
    API_JWT_SEEDS: runKey('WF_E2E_JWT_SEEDS'),
    API_HUMANCHECK_KEYS: runKey('WF_E2E_HUMANCHECK_KEYS'),
    API_IDENTITY_SECRET_KEYS: runKey('WF_E2E_IDENTITY_SECRET_KEYS'),
    API_IDENTITY_DEVICE_KEYS: runKey('WF_E2E_DEVICE_KEYS'),
    API_IDENTITY_TOTP_ISSUER: 'WF e2e',
    API_LEGAL_DOCUMENTS: E2E_LEGAL_DOCUMENTS,
    API_EMBED_WORKER: 'false',
    API_TRUSTED_PROXIES: '',
    API_CLIENT_IP_HEADER: '',
    API_BFF_NETS: '',
    API_BFF_SECRETS: '',
    API_IPGEO_DB: '',
    API_WELLKNOWN_APPLE_APP_IDS: '',
    API_WELLKNOWN_ANDROID: '',
    API_RATE_LIMITS: '',
    ...blobEnv('API_'),
  };
}

// Окружение фикстуры: поверх process.env раннера — без ключа и баз прогона.
export function fixtureEnv(): NodeJS.ProcessEnv {
  return { ...RUN_SECRETS };
}

// Окружение Mailpit: поверх process.env раннера (см. RUN_SECRETS) — без ключа и базы прогона.
export function mailpitEnv(): NodeJS.ProcessEnv {
  return { ...RUN_SECRETS };
}
