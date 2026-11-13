import type { Locale } from '../i18n/messages';
import { parseTheme, type ThemePref } from '../ui/theme';

// Параметры витрины: тема, язык, «сейчас» (спека UI-фундамента §5.2, план UF3.1–UF3.2). Ладл пишет свою тему в тот же атрибут
// <html data-theme>, что и наш ThemeProvider, но значения у него light | dark | auto, а tokens.css знает
// light | dark | system: auto без перевода — всегда тёмная (проба UF3.1, п. 4).

// «Сейчас» историй (useNow, относительное время) — фиксированное: снимки не плывут.
export const SHOWCASE_NOW = new Date('2026-10-28T12:00:00Z');

export type LadleTheme = 'light' | 'dark' | 'auto';

export const ladleToTheme = (t: string): ThemePref => (t === 'auto' ? 'system' : parseTheme(t));

export const themeToLadle = (t: ThemePref): LadleTheme => (t === 'system' ? 'auto' : t);

// Язык витрины — наш параметр адреса ?locale=en (у Ладла переключателя языка нет; чужие параметры он
// сохраняет). Незнакомое — ru.
export const parseShowcaseLocale = (search: string): Locale =>
  new URLSearchParams(search).get('locale') === 'en' ? 'en' : 'ru';

// Мост атрибута темы и состояния Ладла. Атрибут пишут двое: Ладл (своё auto — после эффектов детей, поэтому
// наблюдатель, а не эффект) и история (ThemeSwitch → наш setTheme). Поэтому:
// - auto → system сразу (и если уже стоит);
// - любая другая наша тема, не совпавшая с темой Ладла, — onChange(тема Ладла): его кнопка и адрес следуют.
// Возвращает отписку.
export function bridgeTheme(
  html: HTMLElement,
  ladle: string,
  onChange: (t: LadleTheme) => void,
): () => void {
  const sync = () => {
    const v = html.dataset.theme;
    if (v === 'auto') {
      html.dataset.theme = 'system';
      return;
    }
    if (v === undefined) return;
    const next = themeToLadle(parseTheme(v));
    if (next !== ladle) onChange(next);
  };
  if (html.dataset.theme === 'auto') html.dataset.theme = 'system';
  const observer = new MutationObserver(sync);
  observer.observe(html, { attributes: true, attributeFilter: ['data-theme'] });
  return () => observer.disconnect();
}
