import path from 'node:path';
import type { UserConfig } from 'vite';
import { afterEach, describe, expect, it, vi } from 'vitest';
import config, { adminApiOrigin } from './vite.config';

function proxyTarget(): unknown {
  const resolved = (
    typeof config === 'function' ? config({ mode: 'test', command: 'serve' }) : config
  ) as UserConfig;
  return resolved.server?.proxy?.['/v1'];
}

describe('dev-прокси /v1 → admin-api', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('по умолчанию — 127.0.0.1:8081', () => {
    expect(adminApiOrigin({})).toBe('http://127.0.0.1:8081');
    expect(adminApiOrigin({ WF_ADMIN_API_ORIGIN: '' })).toBe('http://127.0.0.1:8081');
  });

  it('адрес из WF_ADMIN_API_ORIGIN', () => {
    expect(adminApiOrigin({ WF_ADMIN_API_ORIGIN: 'http://127.0.0.1:9081' })).toBe(
      'http://127.0.0.1:9081',
    );
  });

  it('конфиг Vite берёт адрес из окружения', () => {
    vi.stubEnv('WF_ADMIN_API_ORIGIN', 'http://127.0.0.1:9081');
    expect(proxyTarget()).toBe('http://127.0.0.1:9081');
  });
});

describe('Vitest не берёт сценарии Playwright', () => {
  const include = (): string[] => {
    const resolved = (
      typeof config === 'function' ? config({ mode: 'test', command: 'serve' }) : config
    ) as UserConfig & { test?: { include?: string[] } };
    return resolved.test?.include ?? [];
  };
  const picked = (file: string) => include().some((g) => path.matchesGlob(file, g));

  it('юнит-тесты src, e2e/*.test.ts и тест конфига — да', () => {
    expect(picked('src/features/auth/LoginPage.test.tsx')).toBe(true);
    expect(picked('src/app/guard.test.ts')).toBe(true);
    expect(picked('e2e/totp.test.ts')).toBe(true);
    expect(picked('vite.config.test.ts')).toBe(true);
  });

  it('e2e/*.spec.ts (Playwright) — нет', () => {
    expect(picked('e2e/staff.spec.ts')).toBe(false);
  });
});
