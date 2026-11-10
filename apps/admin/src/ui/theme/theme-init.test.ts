/// <reference types="node" />
// Типы Node — только ради чтения public/theme-init.js и index.html с диска (как csp.test.ts).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tokens } from '@wf/tokens';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { parseTheme, THEME_STORAGE_KEY } from './context';

// public/theme-init.js — обычный (не модульный) скрипт в <head> до стилей (спека §4.12): тема до первого
// кадра без инлайн-скрипта (CSP admin-api — script-src 'self'). Файл не проходит сборку Vite и не попадает под
// csp.test.ts (тот читает src) — его стражи здесь и в прод-проекте живого e2e (UF1b.7). Тест исполняет текст
// файла в jsdom, как браузер.
const root = join(import.meta.dirname, '../../..');
const script = readFileSync(join(root, 'public/theme-init.js'), 'utf8');
const html = readFileSync(join(root, 'index.html'), 'utf8');

type WfTheme = { parse(raw: unknown): string; bg: Record<string, string> };
const wfTheme = () => (window as unknown as { __wfTheme?: WfTheme }).__wfTheme;

function run() {
  // как <script src> верхнего уровня: глобальная область, без модуля
  new Function(script)();
}

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  delete (window as unknown as { __wfTheme?: WfTheme }).__wfTheme;
});

describe('theme-init.js', () => {
  test('parse: известные значения как есть, незнакомое и пустое — system', () => {
    run();
    const parse = wfTheme()?.parse;
    expect(parse).toBeTypeOf('function');
    expect(parse?.('dark')).toBe('dark');
    expect(parse?.('light')).toBe('light');
    expect(parse?.('system')).toBe('system');
    for (const raw of ['x', '', null, undefined, 'LIGHT', ' dark', 42, {}]) {
      expect(parse?.(raw), String(raw)).toBe('system');
    }
  });

  test('скрипт и провайдер разбирают одинаково (одни правила до и после загрузки приложения)', () => {
    run();
    for (const raw of ['dark', 'light', 'system', 'x', '', null, undefined, 'Dark', 0, []]) {
      expect(wfTheme()?.parse(raw), String(raw)).toBe(parseTheme(raw));
    }
  });

  test('storage light → data-theme="light"', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light');
    run();
    expect(document.documentElement.dataset.theme).toBe('light');
  });

  test('явный выбор красит обе метки theme-color до React; system их не трогает; BG = tokens.color.bg', () => {
    const metas = (['dark', 'light'] as const).map((m) => {
      const el = document.createElement('meta');
      el.name = 'theme-color';
      el.media = `(prefers-color-scheme: ${m})`;
      el.content = tokens.color.bg[m];
      document.head.append(el);
      return el;
    });
    try {
      localStorage.setItem(THEME_STORAGE_KEY, 'system');
      run();
      expect(metas.map((m) => m.content)).toEqual([tokens.color.bg.dark, tokens.color.bg.light]);
      expect(wfTheme()?.bg).toEqual({ dark: tokens.color.bg.dark, light: tokens.color.bg.light });
      localStorage.setItem(THEME_STORAGE_KEY, 'dark');
      run();
      expect(metas.map((m) => m.content)).toEqual([tokens.color.bg.dark, tokens.color.bg.dark]);
    } finally {
      for (const m of metas) m.remove();
    }
  });

  test('мусор в storage → system', () => {
    localStorage.setItem(THEME_STORAGE_KEY, '{"theme":"dark"}');
    run();
    expect(document.documentElement.dataset.theme).toBe('system');
  });

  test('storage бросает (приватный режим) → system, без исключения', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    expect(run).not.toThrow();
    expect(document.documentElement.dataset.theme).toBe('system');
  });

  test('сам доступ к localStorage бросает → system, без исключения', () => {
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    expect(run).not.toThrow();
    expect(document.documentElement.dataset.theme).toBe('system');
  });

  // Vite копирует public/ как есть: синтаксис — тот, что понимают минимальные браузеры, без модулей
  test('обычный скрипт без модульного синтаксиса', () => {
    expect(script).not.toMatch(/^\s*(import|export)\s/m);
  });
});

describe('index.html', () => {
  const head = html.slice(0, html.indexOf('</head>'));

  test('статический data-theme="system": без JS тема — по системе', () => {
    expect(html).toMatch(/<html lang="ru" data-theme="system">/);
  });

  test('theme-init.js — файл в <head> до стилей, инлайн-скриптов нет', () => {
    const at = head.indexOf('<script src="/theme-init.js"></script>');
    expect(at).toBeGreaterThan(-1);
    expect(head.slice(0, at)).not.toMatch(/<link[^>]+stylesheet|<style/);
    // каждый <script> — с src (CSP script-src 'self' режет инлайн)
    for (const tag of html.match(/<script\b[^>]*>/g) ?? []) expect(tag).toMatch(/\ssrc="/);
  });

  test('две метки theme-color по системной теме с фоном bg своей темы', () => {
    expect(head).toContain(
      `<meta name="theme-color" media="(prefers-color-scheme: dark)" content="${tokens.color.bg.dark}" />`,
    );
    expect(head).toContain(
      `<meta name="theme-color" media="(prefers-color-scheme: light)" content="${tokens.color.bg.light}" />`,
    );
  });
});
