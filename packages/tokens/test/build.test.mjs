import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { buildCss, buildTs } from '../scripts/build.mjs';

const tokens = JSON.parse(
  await readFile(new URL('../../../contracts/tokens/tokens.json', import.meta.url), 'utf8'),
);
const css = buildCss(tokens);

// Тело блока по его началу (без вложенных блоков внутри — их в tokens.css нет, кроме @media → :root).
function block(start) {
  const at = css.indexOf(start);
  assert.notEqual(at, -1, `нет блока «${start}»`);
  const open = css.indexOf('{', at);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    if (css[i] === '}' && --depth === 0) return css.slice(open + 1, i);
  }
  throw new Error(`блок «${start}» не закрыт`);
}

test('цвета, шрифты, шкалы и движение — CSS-переменные', () => {
  assert.match(css, /--wf-font-display: 'Oswald', sans-serif;/);
  assert.match(css, /--wf-text-3xl: 32px;/);
  assert.match(css, /--wf-text-3xl-line: 36px;/);
  assert.match(css, /--wf-space-4: 16px;/);
  assert.match(css, /--wf-radius-button: 14px;/);
  assert.match(css, /--wf-duration-fast: 120ms;/);
  assert.match(css, /--wf-ease: cubic-bezier\(0\.2, 0, 0, 1\);/);
});

// Спека UI-фундамента §2.1–§2.2: пара → light-dark(<светлое>, <тёмное>) — сначала светлое;
// одинаковое в обеих темах — строкой, без light-dark().
test('пара цветов — light-dark(светлое, тёмное); строка — как есть', () => {
  assert.match(css, /--wf-color-bg: light-dark\(#F3F6F3, #0B0F0D\);/);
  assert.match(css, /--wf-color-accent-ink: light-dark\(#3B6600, #C6F24E\);/);
  assert.match(css, /--wf-color-accent: #C6F24E;/);
  assert.match(css, /--wf-color-qr-ink: #0B0F0D;/);
});

test('тема — три правила color-scheme: по умолчанию тёмная, light и system по data-theme', () => {
  assert.match(block(':root {'), /^\s*color-scheme: dark;/);
  assert.match(block(":root[data-theme='light']"), /^\s*color-scheme: light;\s*$/);
  assert.match(block(":root[data-theme='system']"), /^\s*color-scheme: light dark;\s*$/);
  assert.doesNotMatch(css, /data-theme='dark'/, 'тёмная — умолчание :root, своего правила нет');
});

test('reduced motion обнуляет все --wf-duration-*', () => {
  const reduced = block('@media (prefers-reduced-motion: reduce)');
  const keys = Object.keys(tokens.duration);
  assert.ok(keys.length >= 3);
  for (const k of keys) assert.match(reduced, new RegExp(`--wf-duration-${k}: 0ms;`));
  assert.equal(reduced.match(/--wf-/g)?.length, keys.length, 'в блоке только длительности');
});

// light-dark() принимает только цвета: геометрия тени снаружи, внутри — два цвета (I1 спеки).
test('тени: геометрия снаружи, light-dark() только вокруг цвета', () => {
  const shadows = [...css.matchAll(/--wf-shadow-([\w-]+): ([^;]+);/g)];
  assert.deepEqual(
    shadows.map((m) => m[1]),
    Object.keys(tokens.shadow),
  );
  for (const [, name, value] of shadows) {
    assert.match(value, /^0 \d+px \d+px /, `${name}: геометрия числами`);
    for (const m of value.matchAll(/light-dark\(\s*(.)/g))
      assert.equal(m[1], '#', `${name}: за light-dark( — не цвет: ${value}`);
  }
  assert.match(css, /--wf-shadow-overlay: 0 24px 64px light-dark\(#0F171229, #0B0F0DCC\);/);
  assert.match(css, /--wf-shadow-toast: 0 16px 40px light-dark\(#0F171229, #0B0F0DCC\);/);
});

test('новые токены: ease-exit, container, density, breakpoint.compact, размеры каркаса', () => {
  assert.match(css, /--wf-ease-exit: cubic-bezier\(0\.3, 0, 1, 1\);/);
  assert.match(css, /--wf-container-narrow: 480px;/);
  assert.match(css, /--wf-container-wide: 720px;/);
  assert.match(css, /--wf-density-compact-row: 36px;/);
  assert.match(css, /--wf-density-normal-row: 48px;/);
  assert.match(css, /--wf-breakpoint-mobile: 768px;/);
  assert.match(css, /--wf-breakpoint-compact: 1024px;/);
  for (const k of ['sidebar', 'rail', 'palette', 'tabbar', 'table-header', 'toast'])
    assert.match(css, new RegExp(`--wf-size-${k}: \\d+px;`));
  assert.match(css, /--wf-size-sidebar: 236px;/);
  assert.match(css, /--wf-size-rail: 64px;/);
  assert.match(css, /--wf-size-palette: 640px;/);
  assert.doesNotMatch(css, /--wf-contrast/, 'contrast — данные стража, не CSS');
});

// P21: прежние имена и тёмные значения не меняются — тёмное значение каждого прежнего цвета —
// второй аргумент light-dark() (или строка, одинаковая в обеих темах).
const DARK_BEFORE = {
  bg: '#0B0F0D',
  'bg-elevated': '#141A17',
  pitch: '#16241C',
  border: '#253329',
  'border-strong': '#3B5241',
  text: '#EAF2EC',
  'text-secondary': '#C3D0C7',
  'text-muted': '#93A399',
  'text-subtle': '#7E8C83',
  'text-disabled': '#5E7266',
  'text-inverse': '#0B0F0D',
  accent: '#C6F24E',
  'accent-hover': '#D9FA85',
  success: '#4ADE80',
  warning: '#E8B858',
  danger: '#F97066',
  'slot-empty': '#1B241F',
  'slot-filled': '#2A3B2E',
  'surface-sunken': '#0E130F',
  'chip-bg': '#1F2A23',
  'chip-accent-bg': '#2C4434',
  'success-bg': '#1E2A17',
  'warning-bg': '#2A2113',
  'danger-bg': '#2A1614',
  'danger-border': '#5A3733',
  'text-faint': '#6E7B73',
  'text-ghost': '#4F5C55',
};

test('P21: прежние цвета — те же имена и те же тёмные значения', () => {
  const bad = Object.entries(DARK_BEFORE).filter(([k, dark]) => {
    const m = css.match(new RegExp(`--wf-color-${k}: ([^;]+);`));
    if (!m) return true;
    return m[1] !== dark && !new RegExp(`^light-dark\\(#[0-9A-F]{6,8}, ${dark}\\)$`).test(m[1]);
  });
  assert.deepEqual(bad, []);
});

test('tokens.ts: тот же JSON as const и тип ThemeName', () => {
  const ts = buildTs(tokens);
  assert.match(ts, /export const tokens = \{[\s\S]*\} as const;/);
  assert.match(ts, /"bg": \{\s*"dark": "#0B0F0D",\s*"light": "#F3F6F3"\s*\}/);
  assert.match(ts, /export type ThemeName = 'light' \| 'dark';/);
});

test('закоммиченные файлы совпадают с tokens.json', async () => {
  const onDisk = (f) => readFile(new URL(`../${f}`, import.meta.url), 'utf8');
  assert.equal(await onDisk('tokens.css'), css, 'запусти node packages/tokens/scripts/build.mjs');
  assert.equal(
    await onDisk('tokens.ts'),
    buildTs(tokens),
    'запусти node packages/tokens/scripts/build.mjs',
  );
});
