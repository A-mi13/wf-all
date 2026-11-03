import path from 'node:path';
import type { UserConfig } from 'vite';
import { afterEach, describe, expect, it, vi } from 'vitest';
import config, { adminApiOrigin } from './vite.config';
import { samePath, wfShots } from './vite.shots';

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

describe('режим shots (снимки для ревьюера, спека §5.4)', () => {
  const resolve = (mode: string, command: 'serve' | 'build' = 'serve') =>
    (typeof config === 'function' ? config({ mode, command }) : config) as UserConfig;
  const pluginNames = (c: UserConfig): string[] =>
    (c.plugins ?? [])
      .flat(Infinity as 1)
      .map((p) => (p && typeof p === 'object' && 'name' in p ? String(p.name) : ''));

  it('режим shots подключает плагин моков, test и production — нет', () => {
    expect(pluginNames(resolve('shots'))).toContain('wf-shots');
    expect(pluginNames(resolve('test'))).not.toContain('wf-shots');
    expect(pluginNames(resolve('production', 'build'))).not.toContain('wf-shots');
    expect(pluginNames(resolve('development'))).not.toContain('wf-shots');
  });

  it('у shots свой каталог кэша и порт 5176, без прокси в admin-api', () => {
    const shots = resolve('shots');
    expect(shots.cacheDir).toBe('node_modules/.vite-shots');
    expect(shots.server?.port).toBe(5176);
    expect(shots.server?.strictPort).toBe(true);
    expect(shots.server?.proxy).toBeUndefined();
    const dev = resolve('development');
    expect(dev.cacheDir).toBeUndefined();
    expect(dev.server?.port).toBe(5173);
  });
});

describe('плагин wf-shots', () => {
  const root = 'F:\\proj\\apps\\admin';
  const plugin = wfShots(import.meta.dirname);
  type Resolve = (s: string, i?: string) => Promise<{ id: string } | null>;
  const resolveId = (source: string, resolved: string | null) =>
    (
      plugin.resolveId as unknown as (
        this: { resolve: Resolve },
        s: string,
        i: string,
        o: object,
      ) => Promise<unknown>
    ).call({ resolve: async () => (resolved ? { id: resolved } : null) }, source, 'x', {});

  it('сравнение путей: слэши, регистр диска, расширение .ts, query', () => {
    expect(samePath(`${root}\\src\\test\\server.ts`, 'f:/proj/apps/admin/src/test/server')).toBe(
      true,
    );
    expect(
      samePath('F:/proj/apps/admin/src/test/server.ts?v=1', `${root}\\src\\test\\server.ts`),
    ).toBe(true);
    expect(samePath(`${root}\\src\\test\\server2.ts`, `${root}\\src\\test\\server.ts`)).toBe(false);
  });

  it('относительный импорт src/test/server и base → модули src/shots; прочие server — нет', async () => {
    const at = (p: string) => path.join(import.meta.dirname, p);
    const fwd = (p: string) => at(p).replaceAll('\\', '/');
    expect(await resolveId('./server', fwd('src/test/server.ts'))).toBe(at('src/shots/server.ts'));
    expect(await resolveId('../test/base', at('src/test/base.ts'))).toBe(at('src/shots/base.ts'));
    expect(await resolveId('./server', at('src/features/geo/server.ts'))).toBeNull();
    expect(await resolveId('react', '/node_modules/react/index.js')).toBeNull();
  });

  it('index.html: точка входа подменяется; нет её — ошибка, а не тихо обычная админка', () => {
    const transform = plugin.transformIndexHtml as (html: string) => string;
    expect(transform('<script type="module" src="/src/main.tsx"></script>')).toBe(
      '<script type="module" src="/src/shots/main.tsx"></script>',
    );
    expect(() => transform('<script src="/src/other.tsx"></script>')).toThrow(/точки входа/);
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
