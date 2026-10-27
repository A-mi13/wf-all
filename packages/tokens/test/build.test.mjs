import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { buildCss, buildTs } from '../scripts/build.mjs';

const tokens = JSON.parse(
  await readFile(new URL('../../../contracts/tokens/tokens.json', import.meta.url), 'utf8'),
);
const css = buildCss(tokens);

test('цвета, шрифты, шкалы и движение — CSS-переменные', () => {
  assert.match(css, /--wf-color-accent: #C6F24E;/);
  assert.match(css, /--wf-font-display: 'Oswald', sans-serif;/);
  assert.match(css, /--wf-text-3xl: 32px;/);
  assert.match(css, /--wf-text-3xl-line: 36px;/);
  assert.match(css, /--wf-space-4: 16px;/);
  assert.match(css, /--wf-radius-button: 14px;/);
  assert.match(css, /--wf-duration-fast: 120ms;/);
  assert.match(css, /--wf-ease: cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(css, /color-scheme: dark;/);
});

// P21/R10: цвета макетов админки 07/08 и ширина переключения раскладки.
test('токены админки: фоны чипов и статусов, приглушённые тексты, breakpoint', () => {
  assert.match(css, /--wf-color-surface-sunken: #0E130F;/);
  assert.match(css, /--wf-color-chip-bg: #1F2A23;/);
  assert.match(css, /--wf-color-chip-accent-bg: #2C4434;/);
  assert.match(css, /--wf-color-success-bg: #1E2A17;/);
  assert.match(css, /--wf-color-warning-bg: #2A2113;/);
  assert.match(css, /--wf-color-danger-bg: #2A1614;/);
  assert.match(css, /--wf-color-danger-border: #5A3733;/);
  assert.match(css, /--wf-color-text-faint: #6E7B73;/);
  assert.match(css, /--wf-color-text-ghost: #4F5C55;/);
  assert.match(css, /--wf-breakpoint-mobile: 768px;/);
});

// P21: новые имена не перекрывают существующие — значения прежних токенов не меняются.
test('прежние токены текста не тронуты', () => {
  assert.match(css, /--wf-color-text-subtle: #7E8C83;/);
  assert.match(css, /--wf-color-text-disabled: #5E7266;/);
});

test('закоммиченные файлы совпадают с tokens.json', async () => {
  const onDisk = (f) => readFile(new URL(`../${f}`, import.meta.url), 'utf8');
  assert.equal(await onDisk('tokens.css'), css, 'запусти pnpm --filter @wf/tokens build');
  assert.equal(
    await onDisk('tokens.ts'),
    buildTs(tokens),
    'запусти pnpm --filter @wf/tokens build',
  );
});
