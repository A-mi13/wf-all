import { useMemo } from 'react';
import { type ColorName, colorsFor } from './colors';
import { useTheme } from './context';

// Цвета текущей темы для JS (графики, <meta name="theme-color">; спека §2.8): tokens.color[имя][resolved].
export function useThemeColors(): Record<ColorName, string> {
  const { resolved } = useTheme();
  return useMemo(() => colorsFor(resolved), [resolved]);
}
