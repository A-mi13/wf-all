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

test('закоммиченные файлы совпадают с tokens.json', async () => {
  const onDisk = (f) => readFile(new URL(`../${f}`, import.meta.url), 'utf8');
  assert.equal(await onDisk('tokens.css'), css, 'запусти pnpm --filter @wf/tokens build');
  assert.equal(await onDisk('tokens.ts'), buildTs(tokens), 'запусти pnpm --filter @wf/tokens build');
});
