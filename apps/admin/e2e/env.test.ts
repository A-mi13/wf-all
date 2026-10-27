// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  adminApiEnv,
  cleanEnv,
  cookieName,
  enabledProjects,
  mailpitEnv,
  PAIRS,
  pairOf,
  viteEnv,
} from './env';

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

  it('проекты: по умолчанию оба, список из WF_E2E_PROJECTS, незнакомый — ошибка', () => {
    expect(enabledProjects({})).toEqual(['chromium', 'webkit']);
    expect(enabledProjects({ WF_E2E_PROJECTS: 'chromium' })).toEqual(['chromium']);
    expect(() => enabledProjects({ WF_E2E_PROJECTS: 'firefox' })).toThrow(/firefox/);
    expect(() => pairOf('edge')).toThrow(/edge/);
  });

  it('имя cookie — как httpapi/admin.CookieName: __Host- только с Secure', () => {
    expect(cookieName(PAIRS.chromium)).toBe('__Host-wf_admin_session');
    expect(cookieName(PAIRS.webkit)).toBe('wf_admin_session');
  });
});
