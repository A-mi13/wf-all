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
// читает storage заново (storage читается только по событию другой вкладки). Запись — только dataset (не
// element.style: CSP). Смена — мгновенно, без анимации.

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

// Метки theme-color (по одной на системную тему, своим media): явный выбор — bg этой темы в обеих (первый кадр
// красит public/theme-init.js, ревью UF1b/II m6), system — bg темы своего media, как в index.html (страж
// theme-init.test.ts › «две метки theme-color»): значения из tokens.ts, без «исходных» из DOM.
function paintThemeColor(theme: ThemePref) {
  for (const meta of document.head.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    const own: ThemeName = meta.media.includes('light') ? 'light' : 'dark';
    meta.content = colorsFor(theme === 'system' ? own : theme).bg;
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

  // Другая вкладка сменила тему (или стёрла storage — key === null): применить ТЕКУЩЕЕ значение storage, а не
  // e.newValue — при почти одновременных переключениях в двух вкладках событие несёт уже устаревшую запись
  // (ревью UF1b/II m1). Storage недоступен — system.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key !== null && e.key !== THEME_STORAGE_KEY) return;
      let raw: string | null = null;
      try {
        raw = localStorage.getItem(THEME_STORAGE_KEY);
      } catch {
        raw = null;
      }
      apply(parseTheme(raw));
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, [apply]);

  // Цвет панели браузера: явный выбор — bg этой темы в обеих метках, system — bg темы своего media.
  useEffect(() => {
    paintThemeColor(theme);
  }, [theme]);

  const value = useMemo(() => ({ theme, resolved, setTheme }), [theme, resolved, setTheme]);
  return <ThemeContext value={value}>{children}</ThemeContext>;
}
