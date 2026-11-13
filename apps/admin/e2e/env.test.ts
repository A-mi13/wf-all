// @vitest-environment node
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NAV } from '../src/app/nav';
import { MATRIX_VIRTUALIZE_FROM } from '../src/ui/virtual/matrix';
import {
  adminApiEnv,
  apiOrigin,
  browserOf,
  cleanEnv,
  cookieName,
  DEFAULT_PROJECTS,
  E2E_FLAG_CITIES,
  enabledProjects,
  fixtureArgs,
  fixtureEnv,
  GEOFIXTURE,
  GEOFIXTURE_CA,
  GEOFIXTURE_URL,
  geoProject,
  mailpitEnv,
  origin,
  PAIRS,
  pairOf,
  ROOT,
  viteEnv,
  workerEnv,
} from './env';
import { PROD_SECTIONS } from './sections';

// Ключ прогона — как его выдаёт scripts/e2e-admin.sh: 32 байта в base64url.
const KEY = Buffer.alloc(32, 7).toString('base64url');
// Текст ошибки вызова; без ошибки — пустая строка.
function errorOf(fn: () => unknown): string {
  try {
    fn();
  } catch (e) {
    return e instanceof Error ? e.message : String(e);
  }
  return '';
}

const DB_URL = 'postgres://admin:admin@127.0.0.1:15432/wf_e2e?sslmode=disable';

describe('окружение e2e', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('cleanEnv убирает dev-настройки админки (ADMIN_*, адрес прокси Vite), прочее оставляет', () => {
    const env = cleanEnv({
      ADMIN_ORIGIN: 'http://127.0.0.1:5173',
      ADMIN_MAIL_SMTP_USERNAME: 'dev',
      WF_ADMIN_API_ORIGIN: 'http://127.0.0.1:8081',
      PATH: '/bin',
      API_HTTP_ADDR: '127.0.0.1:8080',
    });
    expect(env).toEqual({ PATH: '/bin', API_HTTP_ADDR: '127.0.0.1:8080' });
  });

  it('admin-api получает только значения e2e: свой порт, origin Vite пары, Mailpit e2e, база и ключ прогона', () => {
    vi.stubEnv('ADMIN_MAIL_SMTP_USERNAME', 'dev-user');
    vi.stubEnv('ADMIN_ORIGIN', 'http://127.0.0.1:5173');
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', KEY);
    const env = adminApiEnv(PAIRS.webkit);
    expect(env).not.toHaveProperty('ADMIN_MAIL_SMTP_USERNAME');
    expect(env).toMatchObject({
      ADMIN_HTTP_ADDR: '127.0.0.1:18182',
      ADMIN_ORIGIN: 'http://127.0.0.1:5175',
      ADMIN_COOKIE_SECURE: 'false',
      ADMIN_MAIL_SMTP_ADDR: '127.0.0.1:11026',
      ADMIN_DATABASE_URL: DB_URL,
      ADMIN_STAFF_SECRET_KEYS: KEY,
      ADMIN_STATIC_DIR: '',
    });
    expect(adminApiEnv(PAIRS.chromium)).toMatchObject({
      ADMIN_HTTP_ADDR: '127.0.0.1:18181',
      ADMIN_ORIGIN: 'http://127.0.0.1:5174',
      ADMIN_COOKIE_SECURE: 'true',
    });
  });

  it('без подготовки скриптом (нет базы и ключа e2e) — понятная ошибка', () => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', '');
    expect(() => adminApiEnv(PAIRS.chromium)).toThrow(/admin:e2e/);
  });

  // Повторное ревью 27.10, Minor 1 (флак e2e): Git Bash (MSYS) переписывает значение окружения с ведущим
  // «/» в путь Windows, когда запускает не-MSYS программу (pnpm → node): base64-ключ начинался с «/» в 1 из
  // 64 прогонов, и admin-api не стартовал с непонятной ошибкой кольца ключей. Скрипт выдаёт base64url и
  // исключает WF_E2E_* из конверсии; здесь — страж: ключ не base64url или короче 32 байт, адрес базы не
  // postgres:// — понятная ошибка до старта, без самого значения в тексте.
  it.each([
    ['переписанный MSYS (ведущий «/» → путь)', 'C:/Program Files/Git/' + KEY.slice(1)],
    ['стандартный base64 с «/» и «+»', '/' + KEY.slice(1, -1) + '+'],
    ['короче 32 байт', Buffer.alloc(16, 7).toString('base64url')],
    ['не base64', 'ключ прогона'],
  ])('ключ прогона: %s — ошибка без значения', (_, bad) => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', bad);
    const message = errorOf(() => adminApiEnv(PAIRS.chromium));
    expect(message).toMatch(/WF_E2E_STAFF_SECRET_KEYS.*base64url/);
    expect(message).not.toContain(bad);
  });

  it('ключ прогона base64url (в том числе с «-» и «_» в начале) — принят', () => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    for (const key of [KEY, '-' + KEY.slice(1), '_' + KEY.slice(1), KEY + '=']) {
      vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', key);
      expect(adminApiEnv(PAIRS.chromium).ADMIN_STAFF_SECRET_KEYS).toBe(key);
    }
  });

  it('адрес базы прогона не postgres:// (переписан) — ошибка без значения', () => {
    const bad = 'C:/Program Files/Git/admin:admin@127.0.0.1:15432/wf_e2e';
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', KEY);
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', bad);
    expect(errorOf(() => adminApiEnv(PAIRS.chromium))).toMatch(
      /WF_E2E_ADMIN_DATABASE_URL.*postgres:/,
    );
    expect(errorOf(() => adminApiEnv(PAIRS.chromium))).not.toContain('Program Files');
  });

  it('Vite пары проксирует в admin-api пары, кэш зависимостей — свой', () => {
    vi.stubEnv('WF_ADMIN_API_ORIGIN', 'http://127.0.0.1:8081');
    expect(viteEnv(PAIRS.chromium)).toMatchObject({
      WF_ADMIN_API_ORIGIN: 'http://127.0.0.1:18181',
      WF_E2E_VITE_CACHE_DIR: 'node_modules/.vite-e2e-5174',
    });
    expect(viteEnv(PAIRS.webkit).WF_E2E_VITE_CACHE_DIR).not.toBe(
      viteEnv(PAIRS.chromium).WF_E2E_VITE_CACHE_DIR,
    );
  });

  // Ревью Task 16, M7: ключ сотрудников и строка подключения прогона нужны только admin-api (уже как
  // ADMIN_*). Playwright складывает окружение сервера поверх process.env, поэтому сырые WF_E2E_* у Vite,
  // Mailpit и самого admin-api затираются пустыми значениями, а не просто не передаются.
  it('Vite, Mailpit и admin-api не наследуют ключ и базу прогона (WF_E2E_*)', () => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', KEY);
    const scrubbed = { WF_E2E_STAFF_SECRET_KEYS: '', WF_E2E_ADMIN_DATABASE_URL: '' };
    // сколько раз значение встречается среди значений окружения (повторное ревью 27.10, m7: прежняя
    // проверка искала строку, которой в окружении не было, и проходила всегда)
    const count = (env: NodeJS.ProcessEnv, v: string) =>
      Object.values(env).filter((x) => x === v).length;
    for (const env of [viteEnv(PAIRS.chromium), mailpitEnv()]) {
      expect(env).toMatchObject(scrubbed);
      expect(count(env, DB_URL)).toBe(0);
      expect(count(env, KEY)).toBe(0);
    }
    // admin-api — ровно один раз и только под своим именем
    const api = adminApiEnv(PAIRS.webkit);
    expect(api).toMatchObject(scrubbed);
    expect(count(api, DB_URL)).toBe(1);
    expect(count(api, KEY)).toBe(1);
    expect(api.ADMIN_DATABASE_URL).toBe(DB_URL);
    expect(api.ADMIN_STAFF_SECRET_KEYS).toBe(KEY);
  });

  it('проекты: по умолчанию chromium, webkit и prod, список из WF_E2E_PROJECTS, незнакомый — ошибка', () => {
    expect(enabledProjects({})).toEqual(['chromium', 'webkit', 'prod']);
    expect(enabledProjects({ WF_E2E_PROJECTS: ' ' })).toEqual(['chromium', 'webkit', 'prod']);
    expect(enabledProjects({ WF_E2E_PROJECTS: 'chromium,prod' })).toEqual(['chromium', 'prod']);
    expect(enabledProjects({ WF_E2E_PROJECTS: 'chromium' })).toEqual(['chromium']);
    expect(() => enabledProjects({ WF_E2E_PROJECTS: 'firefox' })).toThrow(/firefox/);
    expect(() => pairOf('edge')).toThrow(/edge/);
  });

  // Прод-проект (план UF1b.7, U9): собранная админка, отданная admin-api со staticCSP, без Vite — проверка
  // того, что dev не видит (theme-init.js до первого кадра, CSP, прод-CSS).
  it('проект prod: admin-api на 18183 отдаёт сборку (ADMIN_STATIC_DIR — только ему), origin — сам admin-api, Chromium', () => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', KEY);
    expect(PAIRS.prod.api).toBe(18183);
    expect(origin(PAIRS.prod)).toBe('http://127.0.0.1:18183');
    expect(origin(PAIRS.prod)).toBe(apiOrigin(PAIRS.prod));
    expect(adminApiEnv(PAIRS.prod)).toMatchObject({
      ADMIN_HTTP_ADDR: '127.0.0.1:18183',
      ADMIN_ORIGIN: 'http://127.0.0.1:18183',
      ADMIN_COOKIE_SECURE: 'true',
      ADMIN_STATIC_DIR: path.join(ROOT, 'apps', 'admin', 'dist'),
    });
    expect(adminApiEnv(PAIRS.chromium).ADMIN_STATIC_DIR).toBe('');
    expect(adminApiEnv(PAIRS.webkit).ADMIN_STATIC_DIR).toBe('');
    expect(cookieName(PAIRS.prod)).toBe('__Host-wf_admin_session');
    expect(browserOf('prod')).toBe('chromium');
    expect(browserOf('webkit')).toBe('webkit');
    // порты пар не пересекаются
    const ports = Object.values(PAIRS).flatMap((p) => ('vite' in p ? [p.vite, p.api] : [p.api]));
    expect(new Set(ports).size).toBe(ports.length);
  });

  // m20 (раунд 2 ревью плана): ./task admin:e2e идёт через scripts/e2e-admin.sh, который сам задаёт и
  // экспортирует умолчание проектов — оно обязано совпасть с умолчанием env.ts, иначе prod локально не идёт.
  it('умолчание проектов scripts/e2e-admin.sh = умолчание env.ts; порт 18183 — в проверке занятых портов', () => {
    const sh = readFileSync(path.join(ROOT, 'scripts', 'e2e-admin.sh'), 'utf8');
    const m = sh.match(/^PROJECTS="\$\{WF_E2E_PROJECTS:-([^}]*)\}"$/m);
    expect(m?.[1]).toBe(DEFAULT_PROJECTS.join(','));
    expect(sh).toMatch(/\*,prod,\*\) ports="\$ports 18183"/);
    // сборка — до Playwright, затем проверка dist (UF1b.4), только если prod в наборе
    const build = sh.indexOf('node_modules/.bin/vite build');
    const check = sh.indexOf('node scripts/check-admin-dist.mjs');
    const run = sh.indexOf('pnpm exec playwright test');
    expect(build).toBeGreaterThan(-1);
    expect(check).toBeGreaterThan(build);
    expect(run).toBeGreaterThan(check);
  });

  // Ревью UF1b/II: перед Playwright скрипт уходит в apps/admin — trap EXIT с относительным путём замка снимал
  // бы apps/admin/.tools/e2e/run.lock, а настоящий замок оставался после каждого прогона.
  it('замок прогона — абсолютный путь, заданный после перехода в корень репо', () => {
    const sh = readFileSync(path.join(ROOT, 'scripts', 'e2e-admin.sh'), 'utf8');
    const root = sh.indexOf('cd "$(dirname "$0")/.."');
    const lock = sh.search(/^LOCK="\$PWD\/\.tools\/e2e\/run\.lock"$/m);
    expect(root).toBeGreaterThan(-1);
    expect(lock).toBeGreaterThan(root);
    expect(sh).not.toMatch(/^LOCK=\.tools/m);
    // переход в apps/admin — после замка (иначе $PWD был бы не корень)
    expect(sh.indexOf('\ncd apps/admin')).toBeGreaterThan(lock);
  });

  // G9-F (I-9): прод-сценарий доказывает виртуализацию столбцов матрицы флагов под CSP — городов на стенде должно быть
  // заведомо больше порога (× 3), засев — после миграций и до сборки/Playwright, число — одно на скрипт и сценарий.
  it('засев городов для матрицы флагов: число = E2E_FLAG_CITIES ≥ порог × 3, после миграций', () => {
    const sh = readFileSync(path.join(ROOT, 'scripts', 'e2e-admin.sh'), 'utf8');
    expect(sh.match(/^FLAG_CITIES=(\d+)$/m)?.[1]).toBe(String(E2E_FLAG_CITIES));
    expect(E2E_FLAG_CITIES).toBeGreaterThanOrEqual(MATRIX_VIRTUALIZE_FROM * 3);
    const migrate = sh.indexOf('go run ./cmd/migrate up');
    const seed = sh.indexOf('generate_series(1, $FLAG_CITIES)');
    expect(migrate).toBeGreaterThan(-1);
    expect(seed).toBeGreaterThan(migrate);
    expect(sh.indexOf('pnpm exec playwright test')).toBeGreaterThan(seed);
  });

  it('обход прод-проекта — все разделы меню', () => {
    expect([...PROD_SECTIONS].sort()).toEqual(
      NAV.flatMap((g) => g.items.map((i) => i.path)).sort(),
    );
  });

  // План B, Task 23: импорт и активация поверх настоящего воркера и HTTPS-фикстуры GeoNames. Корневой Taskfile грузит
  // backend/.env — dev-значения WORKER_* (база wf, настоящий download.geonames.org) не должны дойти ни до воркера, ни
  // до других процессов прогона: e2e не ходит во внешнюю сеть и не пишет в dev-базу.
  it('cleanEnv убирает и dev-настройки воркера (WORKER_*)', () => {
    expect(
      cleanEnv({
        WORKER_DATABASE_URL: 'postgres://worker:worker@localhost:15432/wf',
        WORKER_GEONAMES_BASE_URL: 'https://download.geonames.org',
        PATH: '/bin',
      }),
    ).toEqual({ PATH: '/bin' });
  });

  const WORKER_DB = 'postgres://worker:worker@127.0.0.1:15432/wf_e2e?sslmode=disable';

  it('воркер получает только значения e2e: база прогона, фикстура GeoNames с её корнем, Mailpit e2e; внешней сети нет', () => {
    vi.stubEnv(
      'WORKER_DATABASE_URL',
      'postgres://worker:worker@localhost:15432/wf?sslmode=disable',
    );
    vi.stubEnv('WORKER_GEONAMES_BASE_URL', 'https://download.geonames.org');
    vi.stubEnv('WORKER_MAIL_SMTP_USERNAME', 'dev-user');
    vi.stubEnv('WF_E2E_WORKER_DATABASE_URL', WORKER_DB);
    const env = workerEnv();
    expect(env).not.toHaveProperty('WORKER_MAIL_SMTP_USERNAME');
    expect(env).toMatchObject({
      WORKER_DATABASE_URL: WORKER_DB,
      WORKER_GEONAMES_BASE_URL: 'https://127.0.0.1:18190',
      WORKER_GEONAMES_CA_FILE: path.join(ROOT, '.tools', 'e2e', 'geofixture-ca.pem'),
      WORKER_MAIL_SMTP_ADDR: '127.0.0.1:11026',
      WORKER_MAIL_SMTP_TLS: 'none',
    });
    expect(GEOFIXTURE_URL).toBe(`https://127.0.0.1:${GEOFIXTURE.port}`);
    expect(env.WORKER_GEONAMES_CA_FILE).toBe(GEOFIXTURE_CA);
    // ни одно значение окружения воркера не ведёт к настоящему GeoNames
    expect(Object.values(env).filter((v) => v?.includes('geonames.org'))).toEqual([]);
    // путь PEM собран node, а не Git Bash: абсолютный путь этой ОС, MSYS его не переписывает
    expect(path.isAbsolute(GEOFIXTURE_CA)).toBe(true);
    expect(fixtureArgs()).toEqual([
      '--addr',
      `127.0.0.1:${GEOFIXTURE.port}`,
      '--data',
      path.join(ROOT, 'backend', 'internal', 'geo', 'internal', 'source', 'testdata'),
      '--ca-out',
      GEOFIXTURE_CA,
    ]);
  });

  it('адрес базы воркера не postgres:// (переписан) — ошибка без значения', () => {
    const bad = 'C:/Program Files/Git/worker:worker@127.0.0.1:15432/wf_e2e';
    vi.stubEnv('WF_E2E_WORKER_DATABASE_URL', bad);
    const message = errorOf(() => workerEnv());
    expect(message).toMatch(/WF_E2E_WORKER_DATABASE_URL.*postgres:/);
    expect(message).not.toContain('Program Files');
  });

  it('база воркера прогона — только у воркера: Vite, Mailpit, фикстура и admin-api её не наследуют', () => {
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', DB_URL);
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', KEY);
    vi.stubEnv('WF_E2E_WORKER_DATABASE_URL', WORKER_DB);
    for (const env of [
      viteEnv(PAIRS.chromium),
      mailpitEnv(),
      fixtureEnv(),
      adminApiEnv(PAIRS.prod),
    ]) {
      expect(env.WF_E2E_WORKER_DATABASE_URL).toBe('');
      expect(Object.values(env)).not.toContain(WORKER_DB);
    }
    const w = workerEnv();
    expect(Object.values(w).filter((v) => v === WORKER_DB)).toHaveLength(1);
    expect(Object.values(w)).not.toContain(KEY);
    expect(Object.values(w)).not.toContain(DB_URL);
    expect(fixtureEnv()).toMatchObject({
      WF_E2E_STAFF_SECRET_KEYS: '',
      WF_E2E_ADMIN_DATABASE_URL: '',
    });
  });

  // Импорт и активация идут в общей базе один раз за прогон (место 526815 активируется однажды): сценарий — в одном
  // проекте. prod — Chromium с настоящей CSP (ловит securitypolicyviolation), без него — chromium.
  it('сценарий geo — один проект: prod, без него chromium, без обоих — нет', () => {
    expect(geoProject(['chromium', 'webkit', 'prod'])).toBe('prod');
    expect(geoProject(['chromium', 'prod'])).toBe('prod');
    expect(geoProject(['chromium', 'webkit'])).toBe('chromium');
    expect(geoProject(['webkit'])).toBeUndefined();
  });

  it('скрипт: порт фикстуры — в проверке занятых, воркер и фикстура собраны до Playwright, база воркера — вне MSYS', () => {
    const sh = readFileSync(path.join(ROOT, 'scripts', 'e2e-admin.sh'), 'utf8');
    expect(sh).toMatch(new RegExp(`^ports="18026 11026 ${GEOFIXTURE.port}"$`, 'm'));
    const run = sh.indexOf('pnpm exec playwright test');
    for (const build of ['./cmd/worker', './cmd/geofixture']) {
      const at = sh.indexOf(`go build -o "../.tools/e2e/${build.slice(6)}$exe" ${build}`);
      expect(at, build).toBeGreaterThan(-1);
      expect(run).toBeGreaterThan(at);
    }
    expect(sh).toMatch(/^export WF_E2E_WORKER_DATABASE_URL="postgres:\/\/worker:worker@/m);
    expect(sh).toMatch(/MSYS2_ENV_CONV_EXCL="[^"]*WF_E2E_WORKER_DATABASE_URL/);
    // dev WORKER_* убираются из окружения прогона, как ADMIN_*
    expect(sh).toMatch(/ADMIN_\* \| WORKER_\* \| WF_ADMIN_API_ORIGIN\) unset/);
  });

  it('имя cookie — как httpapi/admin.CookieName: __Host- только с Secure', () => {
    expect(cookieName(PAIRS.chromium)).toBe('__Host-wf_admin_session');
    expect(cookieName(PAIRS.webkit)).toBe('wf_admin_session');
  });
});
