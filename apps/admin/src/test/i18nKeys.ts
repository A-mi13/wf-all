/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Разбор исходников на ключи переводов — общий для стражей «ключ из кода есть в ru.json» (i18n/keys.test.ts) и
// «ключ ru.json используется в коде» (i18n/labels.test.ts). Сообщения use-intl не типизированы, поэтому стражи
// читают исходники как текст: объявление X = useTranslations(<пространство>) (или без него), затем вызовы
// X(<литерал>), X.rich, X.markup, X.has в одинарных или двойных кавычках — ключ «пространство.литерал».
// Пространство вызова — ближайшее объявление X выше по файлу (у разных компонентов файла бывают разные `t`).
// Вызов шаблоном (`X(\`browsers.${b}\`)`) — динамический: его статическое начало (`ns.browsers.`) — префикс.

export type KeyRef = { file: string; key: string; has: boolean };

const DECL = /(?:const|let)\s+(\w+)\s*=\s*useTranslations\(([^)]*)\)/g;

const literal = (raw: string): string | null => /^(['"])([^'"]*)\1$/.exec(raw.trim())?.[2] ?? null;

export function scanKeys(file: string, source: string): { refs: KeyRef[]; prefixes: string[] } {
  const decls = [...source.matchAll(DECL)].map((m) => ({
    name: m[1]!,
    // '' — корень; литерал — пространство; шаблон или переменная — динамическое (null: вызовы не проверяются)
    ns: m[2]!.trim() === '' ? '' : literal(m[2]!),
    at: m.index,
  }));
  const refs: KeyRef[] = [];
  const prefixes: string[] = [];
  const nsAt = (name: string, at: number) =>
    decls.filter((d) => d.name === name && d.at < at).at(-1);
  const full = (ns: string, key: string) => (ns ? `${ns}.${key}` : key);
  for (const name of new Set(decls.map((d) => d.name))) {
    const call = new RegExp(
      `(?<![\\w.])${name}(\\.(?:rich|markup|has))?\\(\\s*(['"])([^'"\`$\\n]+)\\2`,
      'g',
    );
    for (const m of source.matchAll(call)) {
      const decl = nsAt(name, m.index);
      if (!decl || decl.ns === null) continue;
      refs.push({ file, key: full(decl.ns, m[3]!), has: m[1] === '.has' });
    }
    const template = new RegExp(
      `(?<![\\w.])${name}(?:\\.(?:rich|markup|has))?\\(\\s*\`([^\`$]*)\\$\\{`,
      'g',
    );
    for (const m of source.matchAll(template)) {
      const decl = nsAt(name, m.index);
      if (!decl || decl.ns === null) continue;
      prefixes.push(full(decl.ns, m[1]!));
    }
  }
  return { refs, prefixes };
}

// Исходники админки (src без тестов и сгенерированного API) — путь от src и текст.
export function adminSources(src: string): { file: string; source: string }[] {
  return (readdirSync(src, { recursive: true }) as string[])
    .map((f) => f.replaceAll('\\', '/'))
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) && !f.startsWith('api/gen/'))
    .map((file) => ({ file, source: readFileSync(join(src, file), 'utf8') }));
}

// Используется ли ключ (страж неиспользуемых ключей, план UF2a.11 V1 m5). Ключ «ns.остаток» используется, если в
// каком-то файле есть useTranslations(ns) (или корневой) и остаток стоит там строковым литералом — вызов
// t('остаток'), тернарник t(on ? 'enabled' : 'disabled'), таблица ключей — или началом шаблона
// (`statuses.${s}`: всё, что начинается с «statuses.»). Полный ключ литералом где угодно (labelKey меню) — тоже.
// Эвристика, а не разбор вызовов: литерал «остатка» засчитывается в ЛЮБОМ месте файла (сравнение статуса
// 'pending' «использует» approvals.pending). Мёртвый ключ с частым именем (title, label, empty, pending) рядом с
// таким литералом страж не поймает — он ловит ключи, которых в коде нет совсем (ревью UF2a/III-S m6).
export function usageIndex(files: { file: string; source: string }[]): (key: string) => boolean {
  const STRING = /'([^'\n]*)'|"([^"\n]*)"/g;
  const TEMPLATE = /`([\w.]*\.)\$\{/g;
  const index = files.map(({ source }) => ({
    ns: [...source.matchAll(DECL)]
      .map((m) => (m[2]!.trim() === '' ? '' : literal(m[2]!)))
      .filter((n): n is string => n !== null),
    strings: new Set([...source.matchAll(STRING)].map((m) => m[1] ?? m[2]!)),
    templates: [...source.matchAll(TEMPLATE)].map((m) => m[1]!),
  }));
  return (key) =>
    index.some(
      (f) =>
        f.strings.has(key) ||
        f.templates.some((p) => key.startsWith(p)) ||
        f.ns.some((ns) => {
          if (ns !== '' && !key.startsWith(`${ns}.`)) return false;
          const rest = ns === '' ? key : key.slice(ns.length + 1);
          return f.strings.has(rest) || f.templates.some((p) => rest.startsWith(p));
        }),
    );
}

// Все ключи-строки дерева сообщений («a.b.c»).
export function leafKeys(node: unknown, prefix = ''): string[] {
  if (typeof node === 'string') return [prefix];
  if (!node || typeof node !== 'object') return [];
  return Object.entries(node).flatMap(([k, v]) => leafKeys(v, prefix ? `${prefix}.${k}` : k));
}
