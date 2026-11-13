import type { ThemeName } from '@wf/tokens';
import { createContext, useContext } from 'react';

// Выбор темы — localStorage['wf.theme'] (спека UI-фундамента §4.12, U22): system | light | dark.
export type ThemePref = 'system' | 'light' | 'dark';
export const THEME_STORAGE_KEY = 'wf.theme';

// Незнакомое и пустое — system. Те же правила — в public/theme-init.js (страж theme-init.test.ts).
export function parseTheme(raw: unknown): ThemePref {
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system';
}

export type ThemeApi = {
  theme: ThemePref; // выбор
  resolved: ThemeName; // что на экране: system — по prefers-color-scheme
  setTheme: (t: ThemePref) => void;
};

export const ThemeContext = createContext<ThemeApi | null>(null);

export function useTheme(): ThemeApi {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme: нет ThemeProvider выше по дереву (он стоит в AppProviders)');
  return ctx;
}
