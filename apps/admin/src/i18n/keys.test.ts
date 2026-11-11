/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from 'vitest';
import ru from '../../messages/ru.json';

// Страж «ключ из кода есть в ru.json» (ревью UF2a/II-B, I4): сообщения use-intl не типизированы — удалённый или
// переименованный ключ tsc не видит, а экран или история рисуют сырой ключ с MISSING_MESSAGE. Страж читает
// исходники как текст: `const X = useTranslations('ns')` (или без пространства), затем вызовы `X('литерал'`
// — ключ `ns.литерал` обязан быть строкой в ru.json. Пространство вызова — ближайшее объявление X выше по файлу
// (у разных компонентов файла бывают разные `t`). Ключи шаблоном (`${…}`) — динамические, не проверяются.

const SRC = join(import.meta.dirname, '..');
const files = (readdirSync(SRC, { recursive: true }) as string[])
  .map((f) => f.replaceAll('\\', '/'))
  .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) && !f.startsWith('api/gen/'));

const DECL = /const\s+(\w+)\s*=\s*useTranslations\(([^)]*)\)/g;

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
  const decls = [...source.matchAll(DECL)].map((m) => ({
    name: m[1]!,
    // '' — корень; литерал — пространство; шаблон или переменная — динамическое (null: вызовы не проверяются)
    ns: m[2]!.trim() === '' ? '' : (/^'([^']*)'$/.exec(m[2]!.trim())?.[1] ?? null),
    at: m.index,
  }));
  const out: string[] = [];
  for (const name of new Set(decls.map((d) => d.name))) {
    const call = new RegExp(`(?<![\\w.])${name}\\(\\s*'([^'\`$]+)'`, 'g');
    for (const m of source.matchAll(call)) {
      const decl = decls.filter((d) => d.name === name && d.at < m.index).at(-1);
      if (!decl || decl.ns === null) continue;
      const key = decl.ns ? `${decl.ns}.${m[1]}` : m[1]!;
      if (!has(key)) out.push(`${file}: ${key}`);
    }
  }
  return out;
}

test('стражу есть что читать', () => {
  const sample =
    "const t = useTranslations('actions');\nconst x = t('save');\nconst y = t('nope');";
  expect(missing('x.tsx', sample)).toEqual(['x.tsx: actions.nope']);
  expect(files).toContain('ui/toast/Toast.stories.tsx');
  expect(files).toContain('features/staff/StaffDrawer.tsx');
});

test('ключи переводов в коде есть в ru.json', () => {
  const found = files.flatMap((f) => missing(f, readFileSync(join(SRC, f), 'utf8')));
  expect(found).toEqual([]);
});
