// Контраст по WCAG 2.2 (относительная яркость sRGB) — общий модуль стража токенов
// (packages/tokens/test/contrast.test.mjs) и стража CSS админки (apps/admin/src/ui/contrast-css.test.ts).
// Спека UI-фундамента §2.6. Альфа цвета (#RRGGBBAA) в расчёт не входит: цвета с прозрачностью
// (scrim, тени) — смесь с фоном, в таблицу contrast они не попадают.

const HEX = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i;

function channels(hex) {
  const m = HEX.exec(hex);
  if (!m) throw new Error(`цвет «${hex}» — не #RRGGBB[AA]`);
  const n = Number.parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

const linear = (c) => {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
};

export function relativeLuminance(hex) {
  const [r, g, b] = channels(hex).map(linear);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrastRatio(a, b) {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// Цвет токена в теме: строка — одна на обе темы, пара — { dark, light }.
export function colorOf(tokens, name, theme) {
  if (theme !== 'dark' && theme !== 'light') throw new Error(`тема «${theme}» — не dark/light`);
  const v = tokens.color[name];
  if (v === undefined) throw new Error(`нет цвета «${name}» в tokens.json`);
  return typeof v === 'string' ? v : v[theme];
}

// color-mix(in srgb, a p, b) — доля p (0…1) первого цвета, каналы sRGB округляются.
export function mixSrgb(a, b, p) {
  const ca = channels(a);
  const cb = channels(b);
  const hex = ca.map((c, i) =>
    Math.round(c * p + cb[i] * (1 - p))
      .toString(16)
      .padStart(2, '0'),
  );
  return `#${hex.join('').toUpperCase()}`;
}
