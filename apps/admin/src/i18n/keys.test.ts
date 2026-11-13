/// <reference types="node" />
import { join } from 'node:path';
import { expect, test } from 'vitest';
import ru from '../../messages/ru.json';
import { adminSources, scanKeys } from '../test/i18nKeys';

// Страж «ключ из кода есть в ru.json» (ревью UF2a/II-B, I4; m-r2 — t.rich/t.markup и двойные кавычки): сообщения
// use-intl не типизированы — удалённый или переименованный ключ tsc не видит, а экран или история рисуют сырой
// ключ с MISSING_MESSAGE. Разбор исходников — src/test/i18nKeys.ts. Ключи шаблоном — динамические, не
// проверяются; t.has(…) — проверка наличия, её ключа может и не быть.

const files = adminSources(join(import.meta.dirname, '..'));

function has(key: string): boolean {
  const v = key
    .split('.')
    .reduce<unknown>(
      (node, part) =>
        node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined,
      ru,
    );
  return typeof v === 'string';
}

function missing(file: string, source: string): string[] {
  return scanKeys(file, source)
    .refs.filter((r) => !r.has && !has(r.key))
    .map((r) => `${r.file}: ${r.key}`);
}

test('стражу есть что читать', () => {
  const sample =
    "const t = useTranslations('actions');\nconst x = t('save');\nconst y = t('nope');";
  expect(missing('x.tsx', sample)).toEqual(['x.tsx: actions.nope']);
  // ревью UF2a/II-B m-r2: t.rich / t.markup и двойные кавычки — тоже вызовы
  const rich =
    'const t = useTranslations("actions");\nt.rich(\'rich_nope\', {});\nt.markup("markup_nope", {});\nt("dq_nope");';
  expect(missing('y.tsx', rich)).toEqual([
    'y.tsx: actions.rich_nope',
    'y.tsx: actions.markup_nope',
    'y.tsx: actions.dq_nope',
  ]);
  expect(files.map((f) => f.file)).toContain('ui/toast/Toast.stories.tsx');
  expect(files.map((f) => f.file)).toContain('features/staff/StaffDrawer.tsx');
});

test('ключи переводов в коде есть в ru.json', () => {
  const found = files.flatMap((f) => missing(f.file, f.source));
  expect(found).toEqual([]);
});
