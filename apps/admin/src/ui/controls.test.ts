/// <reference types="node" />
// Типы Node — только ради этого стража (читает исходники с диска как текст, как csp.test.ts).
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from 'vitest';

// Контролы форм и кнопки — только компоненты src/ui (V1 п. 10, пользователь 28.10): экраны и каркас
// приложения не рисуют нативные <button>, <input>, <select>, <textarea> без обёртки — у них свой
// вид, своя доступность и своё поведение занятости. Нужен новый контрол — сначала в src/ui.
const root = join(import.meta.dirname, '..');
const screens = ['features', 'app'].flatMap((d) =>
  (readdirSync(join(root, d), { recursive: true }) as string[])
    .map((f) => `${d}/${f.replaceAll('\\', '/')}`)
    .filter((f) => f.endsWith('.tsx') && !/\.test\.tsx$/.test(f)),
);

test('стражу есть что читать', () => {
  expect(screens).toContain('features/geo/PlaceSearch.tsx');
  expect(screens).toContain('features/me/LocaleSwitcher.tsx');
  expect(screens).toContain('app/router.tsx');
});

test('экраны не рисуют нативные контролы — только компоненты src/ui', () => {
  const native = /<(button|input|select|textarea)[\s>/]/;
  const bad = screens.filter((f) => native.test(readFileSync(join(root, f), 'utf8')));
  expect(bad).toEqual([]);
});

// Нативный <select> — нигде в src, и в src/ui тоже (спека UI-фундамента §4.16): раскрытый список рисует
// браузер (системное колесо, чужой вид, светлый список в тёмной теме). Выпадающий список — свой Select.
const allSources = (readdirSync(root, { recursive: true }) as string[])
  .map((f) => f.replaceAll('\\', '/'))
  .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f));

test('нативного <select> нет нигде в src — только свой Select', () => {
  expect(allSources).toContain('ui/form/SelectField.tsx');
  const bad = allSources.filter((f) => /<select[\s>/]/.test(readFileSync(join(root, f), 'utf8')));
  expect(bad).toEqual([]);
});

// Дата — только свой DateField (спека UI-фундамента §4.17): браузерный календарь type="date" — чужой вид и
// неделя по настройке ОС, а не языка интерфейса.
test('экраны не рисуют type="date" — только DateField', () => {
  const bad = screens.filter((f) => /type=["']date["']/.test(readFileSync(join(root, f), 'utf8')));
  expect(bad).toEqual([]);
});
