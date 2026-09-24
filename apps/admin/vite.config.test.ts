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
