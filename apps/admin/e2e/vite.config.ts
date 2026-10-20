import { defineConfig, mergeConfig, type UserConfig } from 'vite';
import base from '../vite.config.ts';

// Vite живого e2e — та же конфигурация админки, но свой каталог кэша зависимостей на каждый сервер e2e
// (WF_E2E_VITE_CACHE_DIR, задаёт e2e/env.ts): два dev-сервера e2e (Chromium и WebKit) и ./task dev,
// оптимизируя зависимости одновременно в общий node_modules/.vite, ломали друг другу переименование
// каталога (EPERM на Windows). Порт и адрес admin-api — флагами и окружением (playwright.config.ts).
export default defineConfig((env) => {
  const resolved = (typeof base === 'function' ? base(env) : base) as UserConfig;
  return mergeConfig(resolved, {
    cacheDir: process.env.WF_E2E_VITE_CACHE_DIR || 'node_modules/.vite-e2e',
  });
});
