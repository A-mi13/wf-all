import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { colorOf, contrastRatio, mixSrgb, relativeLuminance } from '../scripts/contrast.mjs';

const tokens = JSON.parse(
  await readFile(new URL('../../../contracts/tokens/tokens.json', import.meta.url), 'utf8'),
);
const THEMES = ['dark', 'light'];
const HEX = /^#[0-9A-F]{6}([0-9A-F]{2})?$/i;

// Спека UI-фундамента §2.2: цвет — строка (одна на обе темы) или пара { dark, light }; ровно эти ключи.
function badColor(name, v) {
  if (typeof v === 'string') return HEX.test(v) ? null : `${name}: ${v}`;
  if (v === null || typeof v !== 'object') return `${name}: ${JSON.stringify(v)}`;
  const keys = Object.keys(v).sort().join(',');
  if (keys !== 'dark,light') return `${name}: ключи ${keys}`;
  return THEMES.every((t) => HEX.test(v[t])) ? null : `${name}: ${JSON.stringify(v)}`;
}

test('формула WCAG: белый 1, чёрный 0, белый к чёрному 21:1', () => {
  assert.equal(relativeLuminance('#FFFFFF'), 1);
  assert.equal(relativeLuminance('#000000'), 0);
  assert.equal(contrastRatio('#FFFFFF', '#000000'), 21);
  assert.equal(contrastRatio('#000000', '#FFFFFF'), 21, 'порядок аргументов не важен');
});

test('colorOf: строка — одна на обе темы, пара — по теме, незнакомое имя — ошибка', () => {
  const t = { color: { a: '#112233', b: { dark: '#000000', light: '#FFFFFF' } } };
  assert.equal(colorOf(t, 'a', 'dark'), '#112233');
  assert.equal(colorOf(t, 'a', 'light'), '#112233');
  assert.equal(colorOf(t, 'b', 'dark'), '#000000');
  assert.equal(colorOf(t, 'b', 'light'), '#FFFFFF');
  assert.throws(() => colorOf(t, 'c', 'dark'), /c/);
  assert.throws(() => colorOf(t, 'b', 'sepia'), /sepia/);
});

// color-mix(in srgb, A p%, B) — для стража CSS админки (UF1b.3): смесь вычисляется, а не пропускается.
test('mixSrgb: доля первого цвета в srgb, с округлением канала', () => {
  assert.equal(mixSrgb('#C6F24E', '#0B0F0D', 0.7), '#8EAE3A');
  assert.equal(mixSrgb('#FFFFFF', '#000000', 0.5), '#808080');
  assert.equal(mixSrgb('#123456', '#ABCDEF', 1), '#123456');
});

test('каждый цвет — #RRGGBB[AA] строкой или парой с обоими ключами', () => {
  const bad = Object.entries(tokens.color)
    .map(([k, v]) => badColor(k, v))
    .filter(Boolean);
  for (const [k, s] of Object.entries(tokens.shadow)) {
    const b = badColor(`shadow.${k}`, s.color);
    if (b) bad.push(b);
    if (!Number.isInteger(s.y) || !Number.isInteger(s.blur)) bad.push(`shadow.${k}: геометрия`);
  }
  assert.deepEqual(bad, []);
});

// Норма — число (обе темы) или { dark?, light? } — только названные темы: рамка выбранного сегмента
// selected-border в тёмной по замыслу декоративна (артборд 07), норма 3:1 — только у светлой (U8).
function normsOf(row) {
  const [, , norm] = row;
  if (typeof norm === 'number') return THEMES.map((t) => [t, norm]);
  assert.ok(norm && typeof norm === 'object', `норма: ${JSON.stringify(row)}`);
  const keys = Object.keys(norm);
  assert.ok(
    keys.length > 0 && keys.every((k) => THEMES.includes(k)),
    `темы нормы: ${JSON.stringify(row)}`,
  );
  assert.ok(
    keys.every((k) => typeof norm[k] === 'number'),
    `норма числом: ${JSON.stringify(row)}`,
  );
  return keys.map((t) => [t, norm[t]]);
}

test('каждая строка contrast выполняется в своих темах', () => {
  assert.ok(tokens.contrast.length > 40, 'стражу есть что проверять');
  const bad = [];
  for (const row of tokens.contrast) {
    const [fg, bg] = row;
    for (const [theme, norm] of normsOf(row)) {
      const r = contrastRatio(colorOf(tokens, fg, theme), colorOf(tokens, bg, theme));
      if (r < norm) bad.push(`${fg} / ${bg} (${theme}): ${r.toFixed(2)} < ${norm}`);
    }
  }
  assert.deepEqual(bad, []);
});

// Решение пользователя по U8 (раунд 1 UF1b/I): в тёмной у выбранного сегмента прежний вид — рамка
// chip-accent-bg артборда 07, без лаймового кольца; в светлой — accent-ink, 3:1 к соседнему фону.
test('selected-border: тёмная — chip-accent-bg артборда, светлая — accent-ink с нормой 3', () => {
  assert.equal(colorOf(tokens, 'selected-border', 'dark'), '#2C4434');
  assert.equal(
    colorOf(tokens, 'selected-border', 'dark'),
    colorOf(tokens, 'chip-accent-bg', 'dark'),
  );
  assert.equal(colorOf(tokens, 'selected-border', 'light'), colorOf(tokens, 'accent-ink', 'light'));
  const rows = tokens.contrast.filter(([fg]) => fg === 'selected-border');
  assert.deepEqual(rows.map(([, bg]) => bg).sort(), ['pitch', 'surface-sunken']);
  for (const row of rows) assert.deepEqual(normsOf(row), [['light', 3]]);
});

// Ловушка плана: цвет с альфой (scrim, тени) — смесь с фоном, а не цвет текста; в contrast не входит.
test('в contrast нет цветов с прозрачностью', () => {
  const alpha = tokens.contrast
    .flatMap(([fg, bg]) => [fg, bg])
    .filter((n) => THEMES.some((t) => colorOf(tokens, n, t).length === 9));
  assert.deepEqual(alpha, []);
});

// Числа таблицы §2.6 — страж считает той же формулой, что спека.
test('выборочные числа §2.6', () => {
  const ratio = (fg, bg, theme) =>
    contrastRatio(colorOf(tokens, fg, theme), colorOf(tokens, bg, theme));
  assert.ok(Math.abs(ratio('text-subtle', 'surface-sunken', 'dark') - 5.33) <= 0.01);
  assert.ok(Math.abs(ratio('accent-ink', 'pitch', 'light') - 5.44) <= 0.01);
  assert.ok(Math.abs(ratio('text', 'bg', 'dark') - 16.92) <= 0.01);
  assert.ok(Math.abs(ratio('border-control', 'bg-elevated', 'light') - 4.29) <= 0.01);
});

// §2.6: text-subtle на chip-bg в тёмной — 4.22:1; пару не чинят значением (P21), а запрещают.
test('пары text-subtle × chip-bg в contrast нет', () => {
  const found = tokens.contrast.filter(([fg, bg]) => fg === 'text-subtle' && bg === 'chip-bg');
  assert.deepEqual(found, []);
});

// Ревью UF1a ⚠️: деталь акцента иконки — тень, а не блик: в обеих темах темнее и заливки accent,
// и accent-ink (роль акцента иконок, U7).
test('accent-detail темнее accent и accent-ink в обеих темах', () => {
  for (const theme of THEMES) {
    const detail = relativeLuminance(colorOf(tokens, 'accent-detail', theme));
    for (const base of ['accent', 'accent-ink'])
      assert.ok(
        detail < relativeLuminance(colorOf(tokens, base, theme)),
        `${theme}: accent-detail не темнее ${base}`,
      );
  }
});
