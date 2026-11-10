import { type ThemeName, tokens } from '@wf/tokens';

export type ColorName = keyof typeof tokens.color;

// Цвета темы из tokens.ts (спека §2.8): пара — значение своей темы, строка — одна на обе. Без DOM:
// getComputedStyle незарегистрированного custom property вернул бы строку light-dark(#…, #…), а не цвет.
export function colorsFor(theme: ThemeName): Record<ColorName, string> {
  const out = {} as Record<ColorName, string>;
  for (const [name, value] of Object.entries(tokens.color) as [
    ColorName,
    string | Record<ThemeName, string>,
  ][]) {
    out[name] = typeof value === 'string' ? value : value[theme];
  }
  return out;
}
