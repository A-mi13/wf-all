/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from 'vitest';

// Страж спеки UI-фундамента §4.3 (план UF2a.8): экран (src/features/**) не рисует загрузку сам — ни
// LoadingRows, ни «Загрузка…» (ключ states.loading): загрузка экрана — DataState со скелетоном по форме,
// списка — DataTable. Страж читает исходники как текст (как csp.test.ts).
const dir = join(import.meta.dirname, '../../features');
const sources = (readdirSync(dir, { recursive: true }) as string[])
  .map((f) => f.replaceAll('\\', '/'))
  .filter((f) => /\.tsx?$/.test(f) && !/\.(test|stories)\.tsx?$/.test(f));

const SELF_LOADING = /\bLoadingRows\b|states\.loading|['"]loading['"]|Загрузка…/;

const offenders = sources.filter((f) => SELF_LOADING.test(readFileSync(join(dir, f), 'utf8')));

test('стражу есть что читать', () => {
  expect(sources).toContain('geo/CitiesPage.tsx');
  expect(sources).toContain('me/MyAccessPage.tsx');
  expect(sources.some((f) => f.endsWith('.test.tsx'))).toBe(false);
});

// LoadingRows — деталь DataTable: из набора (ui/index.ts) экранам он не выдаётся.
test('LoadingRows не экспортируется из ui/index.ts', () => {
  const index = readFileSync(join(import.meta.dirname, '../index.ts'), 'utf8');
  expect(index).not.toMatch(/\bLoadingRows\b/);
});

test('экраны не рисуют загрузку сами — только DataState / DataTable', () => {
  // исключений нет: все экраны переведены в UF2a.10 (список ожидающих снят, ревью UF2a/III-S m5)
  expect(offenders).toEqual([]);
});
