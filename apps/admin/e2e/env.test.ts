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
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', 'postgres://admin:admin@127.0.0.1:15432/wf_e2e');
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', 'k');
    const env = adminApiEnv(PAIRS.webkit);
    expect(env).not.toHaveProperty('ADMIN_MAIL_SMTP_USERNAME');
    expect(env).toMatchObject({
      ADMIN_HTTP_ADDR: '127.0.0.1:18182',
      ADMIN_ORIGIN: 'http://127.0.0.1:5175',
      ADMIN_COOKIE_SECURE: 'false',
      ADMIN_MAIL_SMTP_ADDR: '127.0.0.1:11026',
      ADMIN_DATABASE_URL: 'postgres://admin:admin@127.0.0.1:15432/wf_e2e',
      ADMIN_STAFF_SECRET_KEYS: 'k',
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
    vi.stubEnv('WF_E2E_ADMIN_DATABASE_URL', 'postgres://admin:admin@127.0.0.1:15432/wf_e2e');
    vi.stubEnv('WF_E2E_STAFF_SECRET_KEYS', 'секрет-прогона');
    const scrubbed = { WF_E2E_STAFF_SECRET_KEYS: '', WF_E2E_ADMIN_DATABASE_URL: '' };
    for (const env of [viteEnv(PAIRS.chromium), mailpitEnv(), adminApiEnv(PAIRS.webkit)]) {
      expect(env).toMatchObject(scrubbed);
      expect(JSON.stringify(env)).not.toContain('wf_e2e?');
    }
    expect(JSON.stringify(viteEnv(PAIRS.webkit))).not.toContain('секрет-прогона');
    expect(JSON.stringify(mailpitEnv())).not.toContain('секрет-прогона');
    // admin-api — только под своим именем
    expect(adminApiEnv(PAIRS.webkit).ADMIN_STAFF_SECRET_KEYS).toBe('секрет-прогона');
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
