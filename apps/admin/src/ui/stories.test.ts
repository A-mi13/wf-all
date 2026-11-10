/// <reference types="node" />
// Типы Node — только ради этого стража (читает исходники с диска, как csp.test.ts).
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, posix } from 'node:path';
import ts from 'typescript';
import { describe, expect, test } from 'vitest';
import { storyList } from '../showcase/storyList';

// Страж историй (спека UI-фундамента §5.2, план UF3.4): каждый компонент, экспортируемый из ui/index.ts, показан
// историей РЯДОМ — файлом *.stories.tsx в каталоге модуля, который его определяет (истории состояний —
// states/States.stories.tsx, поля и формы — form/*). Исключения — списком с причиной; исключение без экспорта —
// падение (список не копит мёртвое). Плюс то, что ломает витрину Ладла целиком, а не одну историю: storyName и
// title только латиницей (Ладл строит из них имя JS-переменной — кириллица даёт ReferenceError на всех
// историях, проба UF3.3), «.stories.» только в имени *.stories.tsx (Ладл дописывает HMR-код в любой модуль с
// «.stories.» в пути, в том числе CSS — проба UF3.1), метки visual/layout — литералами (список снимков CI).

const UI = import.meta.dirname;
const rel = (f: string) => f.replaceAll('\\', '/');
const files = (readdirSync(UI, { recursive: true }) as string[]).map(rel);
const storyFiles = files.filter((f) => f.endsWith('.stories.tsx'));
const read = (f: string) => readFileSync(join(UI, f), 'utf8');

// Не компоненты с внешним видом или показаны иначе — с причиной.
const EXCEPTIONS: Record<string, string> = {
  ThemeProvider: 'провайдер: обёртка витрины (ShowcaseProvider) ставит его сама',
  ToastProvider: 'провайдер: обёртка витрины ставит его сама; тосты — toast/Toast.stories.tsx',
  SelectField: 'UF2a.6 заменяет нативный <select> своим списком — истории с новой реализацией',
  MultiSelectField: 'UF2a.6 переделывает вместе с SelectField — истории с новой реализацией',
  DesktopOnlyNotice: 'пометка ActionGate — показана в gate/ActionGate.stories.tsx (UF2a.1)',
};

type Export = { name: string; dir: string };

// Значимые экспорты index.ts: имена с заглавной (компоненты), без type-экспортов и КОНСТАНТ; dir — каталог
// модуля относительно src/ui ('' — сам src/ui).
function componentExports(indexSource: string, isDir: (spec: string) => boolean): Export[] {
  const sf = ts.createSourceFile('index.ts', indexSource, ts.ScriptTarget.Latest, false);
  const out: Export[] = [];
  for (const st of sf.statements) {
    if (!ts.isExportDeclaration(st) || st.isTypeOnly || !st.moduleSpecifier) continue;
    if (!ts.isStringLiteral(st.moduleSpecifier) || !st.exportClause) continue;
    if (!ts.isNamedExports(st.exportClause)) continue;
    const spec = st.moduleSpecifier.text;
    const dir = isDir(spec) ? posix.normalize(spec) : posix.dirname(spec);
    for (const el of st.exportClause.elements) {
      const name = el.name.text;
      if (el.isTypeOnly || !/^[A-Z]/.test(name) || /^[A-Z0-9_]+$/.test(name)) continue;
      out.push({ name, dir: dir === '.' ? '' : dir.replace(/^\.\//, '') });
    }
  }
  return out;
}

// Значения (не типы), импортированные файлом историй; «рядом» решает каталог файла (uncovered).
function localImports(source: string): Set<string> {
  const sf = ts.createSourceFile('x.tsx', source, ts.ScriptTarget.Latest, false, ts.ScriptKind.TSX);
  const names = new Set<string>();
  for (const st of sf.statements) {
    if (!ts.isImportDeclaration(st) || !ts.isStringLiteral(st.moduleSpecifier)) continue;
    if (st.importClause?.isTypeOnly) continue;
    const bindings = st.importClause?.namedBindings;
    if (bindings && ts.isNamedImports(bindings)) {
      for (const el of bindings.elements) if (!el.isTypeOnly) names.add(el.name.text);
    }
  }
  return names;
}

// Компоненты без истории рядом: имя → каталог. stories — путь файла историй → исходник.
function uncovered(
  exports: Export[],
  stories: Record<string, string>,
  exceptions: Record<string, string>,
): string[] {
  const shown = new Map<string, Set<string>>();
  for (const [file, src] of Object.entries(stories)) {
    const dir = posix.dirname(file) === '.' ? '' : posix.dirname(file);
    const set = shown.get(dir) ?? new Set<string>();
    for (const n of localImports(src)) set.add(n);
    shown.set(dir, set);
  }
  return exports
    .filter((e) => !(e.name in exceptions) && !shown.get(e.dir)?.has(e.name))
    .map(
      (e) =>
        `${e.name} (src/ui/${e.dir ? `${e.dir}/` : ''}) — нет *.stories.tsx рядом, который его показывает`,
    );
}

// Исключения, которых нет среди экспортов (компонент удалён или переименован).
function stale(exports: Export[], exceptions: Record<string, string>): string[] {
  const names = new Set(exports.map((e) => e.name));
  return Object.keys(exceptions).filter((n) => !names.has(n));
}

// storyName и title не латиницей — Ладл строит из них имя переменной (проба UF3.3).
function nonAsciiNames(stories: Record<string, string>): string[] {
  const bad: string[] = [];
  for (const [file, src] of Object.entries(stories)) {
    for (const m of src.matchAll(/\.storyName\s*=\s*(['"`])(.*?)\1/g)) {
      if (!/^[A-Za-z0-9 ]+$/.test(m[2] ?? '')) bad.push(`${file}: storyName «${m[2]}»`);
    }
    for (const m of src.matchAll(/\btitle:\s*(['"`])(.*?)\1/g)) {
      if (!/^[A-Za-z0-9 /]+$/.test(m[2] ?? '')) bad.push(`${file}: title «${m[2]}»`);
    }
  }
  return bad;
}

const isDir = (spec: string) => {
  const p = join(UI, spec);
  return existsSync(p) && statSync(p).isDirectory();
};
const exportsNow = componentExports(read('index.ts'), isDir);
const storiesNow = Object.fromEntries(storyFiles.map((f) => [f, read(f)]));

describe('страж историй', () => {
  test('стражу есть что читать', () => {
    expect(exportsNow.map((e) => e.name)).toEqual(
      expect.arrayContaining([
        'Button',
        'Modal',
        'DataTable',
        'AppShell',
        'EmptyState',
        'ThemeSwitch',
      ]),
    );
    expect(exportsNow.find((e) => e.name === 'Modal')?.dir).toBe('overlay');
    expect(exportsNow.find((e) => e.name === 'Button')?.dir).toBe('');
    expect(exportsNow.map((e) => e.name)).not.toContain('SEARCH_DEBOUNCE_MS');
    expect(exportsNow.map((e) => e.name)).not.toContain('ButtonProps');
    expect(storyFiles).toContain('Button.stories.tsx');
    expect(storyFiles).toContain('overlay/Modal.stories.tsx');
  });

  test('каждый компонент из ui/index.ts — с историей рядом', () => {
    expect(uncovered(exportsNow, storiesNow, EXCEPTIONS)).toEqual([]);
  });

  test('исключения — только для того, что экспортируется', () => {
    expect(stale(exportsNow, EXCEPTIONS)).toEqual([]);
    expect(stale([{ name: 'Button', dir: '' }], { Button: 'а', Gone: 'б' })).toEqual(['Gone']);
  });

  test('история в другом каталоге не засчитывается; без истории — имя и каталог в списке', () => {
    const exp: Export[] = [
      { name: 'Button', dir: '' },
      { name: 'Modal', dir: 'overlay' },
      { name: 'Drawer', dir: 'overlay' },
    ];
    const stories = {
      // Button показан только в истории оверлея, Drawer — только в истории из src/ui через ./overlay
      'overlay/Modal.stories.tsx':
        "import { Button } from '../Button';\nimport { Modal } from './Modal';",
      'Panel.stories.tsx': "import { Drawer } from './overlay';",
    };
    expect(uncovered(exp, stories, {})).toEqual([
      'Button (src/ui/) — нет *.stories.tsx рядом, который его показывает',
      'Drawer (src/ui/overlay/) — нет *.stories.tsx рядом, который его показывает',
    ]);
    expect(uncovered(exp, stories, { Button: 'причина', Drawer: 'причина' })).toEqual([]);
  });

  test('type-импорт историю не засчитывает', () => {
    expect([
      ...localImports("import { type Column } from './model';\nimport type { X } from './x';"),
    ]).toEqual([]);
  });

  test('storyName и title — латиницей: кириллица роняет всю витрину Ладла', () => {
    expect(nonAsciiNames(storiesNow)).toEqual([]);
  });

  test('проверка имён ловит кириллицу и знаки в storyName и title', () => {
    const src = [
      "export default { title: 'UI/Кнопка' } satisfies StoryMeta;",
      "Default.storyName = 'default state';",
      "Busy.storyName = 'Занята';",
      "Odd.storyName = 'a: b';",
    ].join('\n');
    expect(nonAsciiNames({ 'X.stories.tsx': src })).toEqual([
      'X.stories.tsx: storyName «Занята»',
      'X.stories.tsx: storyName «a: b»',
      'X.stories.tsx: title «UI/Кнопка»',
    ]);
  });

  test('«.stories.» — только в имени *.stories.tsx (Ладл дописывает HMR-код в такие модули, и в CSS)', () => {
    expect(files.filter((f) => f.includes('.stories.') && !f.endsWith('.stories.tsx'))).toEqual([]);
  });

  test('метки visual/layout в историях — литералы: список снимков CI собирается', () => {
    const meta = {
      stories: Object.fromEntries(
        storyFiles.map((f) => [f, { name: f, filePath: f, namedExport: '_' }]),
      ),
    };
    expect(() => storyList(meta, read)).not.toThrow();
  });
});
