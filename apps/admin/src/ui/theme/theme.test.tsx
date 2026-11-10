/// <reference types="node" />
// Типы Node — только ради чтения public/theme-init.js («после перезагрузки» исполняет настоящий скрипт).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { act, renderHook } from '@testing-library/react';
import { tokens } from '@wf/tokens';
import { type ReactNode, StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { clearDrafts } from '../../app/draft';
import { THEME_STORAGE_KEY, useTheme } from './context';
import { ThemeProvider } from './ThemeProvider';
import { useThemeColors } from './useThemeColors';

// Тема (спека UI-фундамента §4.12, §2.8; план UF1b.5): источник истины при монтировании — атрибут
// data-theme, поставленный public/theme-init.js; выбор пишет storage и атрибут; другая вкладка — событие
// storage; resolved — по matchMedia с подпиской (без matchMedia — dark).

const html = document.documentElement;
const initScript = readFileSync(join(import.meta.dirname, '../../../public/theme-init.js'), 'utf8');

const wrapper = ({ children }: { children: ReactNode }) => (
  <StrictMode>
    <ThemeProvider>{children}</ThemeProvider>
  </StrictMode>
);
const mount = () => renderHook(() => ({ ...useTheme(), colors: useThemeColors() }), { wrapper });

// matchMedia системной темы: управляемое значение и событие change, как в браузере.
function systemScheme(initialDark: boolean) {
  let dark = initialDark;
  const listeners = new Set<() => void>();
  const mql = {
    get matches() {
      return dark;
    },
    media: '(prefers-color-scheme: dark)',
    addEventListener: (_: 'change', cb: () => void) => listeners.add(cb),
    removeEventListener: (_: 'change', cb: () => void) => listeners.delete(cb),
  };
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => {
      if (q !== '(prefers-color-scheme: dark)') throw new Error(`matchMedia: ${q}`);
      return mql;
    }),
  );
  return {
    listeners,
    set(next: boolean) {
      dark = next;
      act(() => listeners.forEach((cb) => cb()));
    },
  };
}

// Другая вкладка: сначала запись в общий storage, затем событие (как в браузере).
function otherTab(key: string | null, newValue: string | null) {
  if (key === null) localStorage.clear();
  else if (newValue !== null) localStorage.setItem(key, newValue);
  act(() => {
    window.dispatchEvent(new StorageEvent('storage', { key, newValue, storageArea: localStorage }));
  });
}

beforeEach(() => {
  html.dataset.theme = 'system'; // как статический атрибут index.html
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
  delete html.dataset.theme;
  for (const m of document.head.querySelectorAll('meta[name="theme-color"]')) m.remove();
});

describe('ThemeProvider', () => {
  test('по умолчанию system; без matchMedia — resolved dark', () => {
    const { result } = mount();
    expect(result.current.theme).toBe('system');
    expect(result.current.resolved).toBe('dark');
  });

  test('без атрибута (скрипт не исполнился) — system', () => {
    delete html.dataset.theme;
    const { result } = mount();
    expect(result.current.theme).toBe('system');
  });

  test('начальное значение — из атрибута, который поставил скрипт, не из storage', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'dark');
    html.dataset.theme = 'light';
    const { result } = mount();
    expect(result.current.theme).toBe('light');
    expect(result.current.resolved).toBe('light');
  });

  test('мусор в атрибуте — system', () => {
    html.dataset.theme = 'sepia';
    expect(mount().result.current.theme).toBe('system');
  });

  test('выбор пишет storage и атрибут', () => {
    const { result } = mount();
    act(() => result.current.setTheme('light'));
    expect(result.current.theme).toBe('light');
    expect(result.current.resolved).toBe('light');
    expect(html.dataset.theme).toBe('light');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light');
    act(() => result.current.setTheme('system'));
    expect(html.dataset.theme).toBe('system');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('system');
  });

  test('storage бросает (приватный режим, переполнение) — тема меняется, ошибки нет', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError');
    });
    const { result } = mount();
    expect(() => act(() => result.current.setTheme('dark'))).not.toThrow();
    expect(result.current.theme).toBe('dark');
    expect(html.dataset.theme).toBe('dark');
  });

  test('событие storage другой вкладки применяется; чужой ключ — нет; clear() — system', () => {
    const { result } = mount();
    otherTab(THEME_STORAGE_KEY, 'light');
    expect(result.current.theme).toBe('light');
    expect(html.dataset.theme).toBe('light');
    otherTab('wf.admin.density', 'dark');
    expect(result.current.theme).toBe('light');
    otherTab(THEME_STORAGE_KEY, 'нечто');
    expect(result.current.theme).toBe('system');
    otherTab(THEME_STORAGE_KEY, 'dark');
    otherTab(null, null); // localStorage.clear() в другой вкладке
    expect(result.current.theme).toBe('system');
    expect(html.dataset.theme).toBe('system');
  });

  // Ревью UF1b/II m1: две вкладки переключили почти одновременно — A пишет light, B — dark. Событие несёт
  // значение своей записи, а в storage уже последняя: вкладка берёт текущее значение storage, иначе A и B
  // разошлись бы с сохранённым до следующего переключения.
  test('событие storage: применяется текущее значение storage, а не устаревшее newValue', () => {
    const { result } = mount();
    localStorage.setItem(THEME_STORAGE_KEY, 'dark');
    act(() => {
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: THEME_STORAGE_KEY,
          newValue: 'light',
          storageArea: localStorage,
        }),
      );
    });
    expect(result.current.theme).toBe('dark');
  });

  test('событие storage при недоступном storage — system, без ошибки', () => {
    const { result } = mount();
    act(() => result.current.setTheme('dark'));
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    expect(() => otherTab(THEME_STORAGE_KEY, 'light')).not.toThrow();
    expect(result.current.theme).toBe('system');
  });

  test('после размонтирования событие storage не применяется (подписка снята)', () => {
    const { unmount } = mount();
    unmount();
    otherTab(THEME_STORAGE_KEY, 'light');
    expect(html.dataset.theme).toBe('system');
  });

  test('system + тёмная система → resolved dark, смена системной → light; явный выбор системе не следует', () => {
    const scheme = systemScheme(true);
    const { result, unmount } = mount();
    expect(result.current.resolved).toBe('dark');
    scheme.set(false);
    expect(result.current.resolved).toBe('light');
    act(() => result.current.setTheme('dark'));
    scheme.set(false);
    expect(result.current.resolved).toBe('dark');
    unmount();
    expect(scheme.listeners.size).toBe(0);
  });

  test('после перезагрузки: theme-init.js ставит выбранную тему, провайдер её берёт', () => {
    const first = mount();
    act(() => first.result.current.setTheme('light'));
    first.unmount();
    html.dataset.theme = 'system'; // новая загрузка: статический атрибут index.html
    new Function(initScript)();
    expect(mount().result.current.theme).toBe('light');
  });

  test('выход и смена сотрудника тему не стирают (U19: предпочтение браузера)', () => {
    const { result } = mount();
    act(() => result.current.setTheme('dark'));
    clearDrafts();
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');
  });
});

describe('useThemeColors', () => {
  test('при resolved = light — значения светлой темы из tokens.ts, не light-dark()', () => {
    html.dataset.theme = 'light';
    const { colors } = mount().result.current;
    expect(colors.bg).toBe('#F3F6F3');
    expect(colors['accent-ink']).toBe(tokens.color['accent-ink'].light);
    expect(colors.accent).toBe(tokens.color.accent); // строка — одна на обе темы
    for (const v of Object.values(colors)) expect(v).toMatch(/^#[0-9A-F]{6}([0-9A-F]{2})?$/i);
  });

  test('при resolved = dark — тёмные значения; все имена tokens.color', () => {
    html.dataset.theme = 'dark';
    const { colors } = mount().result.current;
    expect(colors.bg).toBe(tokens.color.bg.dark);
    expect(Object.keys(colors).sort()).toEqual(Object.keys(tokens.color).sort());
  });
});

describe('theme-color', () => {
  function metas() {
    const dark = document.createElement('meta');
    dark.name = 'theme-color';
    dark.media = '(prefers-color-scheme: dark)';
    dark.content = '#010101';
    const light = document.createElement('meta');
    light.name = 'theme-color';
    light.media = '(prefers-color-scheme: light)';
    light.content = '#FEFEFE';
    document.head.append(dark, light);
    return { dark, light };
  }

  // Ревью UF1b/II m6: system — bg темы своего media (как в index.html), а не «запомненное» из DOM: первый кадр
  // явной темы красит уже theme-init.js, и исходным из DOM стал бы цвет явной темы.
  const BY_MEDIA = [tokens.color.bg.dark, tokens.color.bg.light];

  test('явный dark → обе метки тёмный bg; явный light → светлый; system → bg темы своего media', () => {
    const m = metas();
    const { result } = mount();
    expect([m.dark.content, m.light.content]).toEqual(BY_MEDIA);
    act(() => result.current.setTheme('dark'));
    expect([m.dark.content, m.light.content]).toEqual([tokens.color.bg.dark, tokens.color.bg.dark]);
    act(() => result.current.setTheme('light'));
    expect([m.dark.content, m.light.content]).toEqual([
      tokens.color.bg.light,
      tokens.color.bg.light,
    ]);
    act(() => result.current.setTheme('system'));
    expect([m.dark.content, m.light.content]).toEqual(BY_MEDIA);
  });

  test('первый кадр с явной темой (метки уже покрасил theme-init.js) → возврат к system — bg своего media', () => {
    const m = metas();
    localStorage.setItem(THEME_STORAGE_KEY, 'light');
    new Function(initScript)(); // атрибут light и обе метки — светлый bg до React
    expect([m.dark.content, m.light.content]).toEqual([
      tokens.color.bg.light,
      tokens.color.bg.light,
    ]);
    const { result } = mount();
    act(() => result.current.setTheme('system'));
    expect([m.dark.content, m.light.content]).toEqual(BY_MEDIA);
  });
});
