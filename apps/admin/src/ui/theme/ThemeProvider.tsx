import type { ThemeName } from '@wf/tokens';
import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from 'react';
import { colorsFor } from './colors';
import { parseTheme, THEME_STORAGE_KEY, ThemeContext, type ThemePref } from './context';

// Тема админки (спека UI-фундамента §4.12): выбор — localStorage['wf.theme'] (свойство браузера, не сотрудника:
// выход и смена сотрудника его не стирают, U19), применение — атрибут <html data-theme>, по которому
// tokens.css ставит color-scheme, а light-dark() выбирает цвет. До первого кадра атрибут ставит
// public/theme-init.js; провайдер при монтировании берёт его (источник истины — то, что уже на экране), а не
// читает storage заново. Запись — только dataset (не element.style: CSP). Смена — мгновенно, без анимации.

const DARK_QUERY = '(prefers-color-scheme: dark)';
const darkMedia = () =>
  typeof window.matchMedia === 'function' ? window.matchMedia(DARK_QUERY) : null;

function subscribeSystem(onChange: () => void): () => void {
  const m = darkMedia();
  m?.addEventListener('change', onChange);
  return () => m?.removeEventListener('change', onChange);
}
// Без matchMedia (jsdom, старый движок) — тёмная: так выглядела админка до двух тем.
const systemIsDark = () => darkMedia()?.matches ?? true;

// Исходные content меток theme-color из index.html — до первой правки; переживают перемонтирование
// провайдера (иначе «исходным» стал бы цвет явной темы).
const originalColor = new WeakMap<HTMLMetaElement, string>();

function paintThemeColor(theme: ThemePref, bg: string) {
  for (const meta of document.head.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    if (!originalColor.has(meta)) originalColor.set(meta, meta.content);
    meta.content = theme === 'system' ? (originalColor.get(meta) ?? meta.content) : bg;
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setState] = useState<ThemePref>(() =>
    parseTheme(document.documentElement.dataset.theme),
  );
  const systemDark = useSyncExternalStore(subscribeSystem, systemIsDark, () => true);
  const resolved: ThemeName = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme;

  const apply = useCallback((t: ThemePref) => {
    document.documentElement.dataset.theme = t;
    setState(t);
  }, []);

  const setTheme = useCallback(
    (t: ThemePref) => {
      try {
        localStorage.setItem(THEME_STORAGE_KEY, t);
      } catch {
        // приватный режим или переполнение: тема меняется до перезагрузки, выбор не запомнится
      }
      apply(t);
    },
    [apply],
  );

  // Другая вкладка сменила тему (или стёрла storage — key === null): применить её выбор.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key !== null && e.key !== THEME_STORAGE_KEY) return;
      apply(parseTheme(e.newValue));
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, [apply]);

  // Цвет панели браузера: явный выбор — bg этой темы в обеих метках (у каждой свой media), system — исходные.
  useEffect(() => {
    paintThemeColor(theme, colorsFor(resolved).bg);
  }, [theme, resolved]);

  const value = useMemo(() => ({ theme, resolved, setTheme }), [theme, resolved, setTheme]);
  return <ThemeContext value={value}>{children}</ThemeContext>;
}
