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

// Экраны, ещё не переведённые на DataState: перевод — UF2a.10 (применение к экранам, спека §6.1). Список
// только сокращается: переведённый экран обязан уйти отсюда (иначе страж молча пропустит возврат).
const PENDING_UF2A10 = [
  'auth/SetupMfaPage.tsx',
  'geo/CityPage.tsx',
  'me/MySessionsCard.tsx',
  'me/SecondFactorCard.tsx',
  'staff/StaffDrawer.tsx',
];

const offenders = sources.filter((f) => SELF_LOADING.test(readFileSync(join(dir, f), 'utf8')));

test('стражу есть что читать', () => {
  expect(sources).toContain('geo/CitiesPage.tsx');
  expect(sources).toContain('me/MyAccessPage.tsx');
  expect(sources.some((f) => f.endsWith('.test.tsx'))).toBe(false);
});

test('экраны не рисуют загрузку сами — только DataState / DataTable', () => {
  expect(offenders.filter((f) => !PENDING_UF2A10.includes(f))).toEqual([]);
});

// Список не растёт молча: на момент UF2a.8 — 5 экранов (ревью m6); новый экран идёт через DataState.
test('список ожидающих UF2a.10 только сокращается', () => {
  expect(PENDING_UF2A10.length).toBeLessThanOrEqual(5);
});

test('список ожидающих UF2a.10 не устарел', () => {
  expect(PENDING_UF2A10.filter((f) => !offenders.includes(f))).toEqual([]);
});
