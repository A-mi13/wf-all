/// <reference types="node" />
// Типы Node — только ради этого стража (читает исходники с диска), как src/ui/csp.test.ts.
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { describe, expect, test } from 'vitest';

// Режим shots (спека §5.4, план UF1a.1, U4): моки MSW в браузере живут только в src/shots и src/test.
// Обычная сборка начинается с src/main.tsx — её граф импортов (статических и динамических import(),
// после UF2b.9 разделы ленивые) не должен дотянуться ни до msw, ни до src/shots, ни до src/test, а
// сервис-воркер MSW не лежит в public/ (admin-api отдал бы его в проде). Сборку тест не запускает (U4):
// вторая линия — проверка dist в admin:ci (UF1b.4).
const src = join(import.meta.dirname, '..');
const root = join(src, '..');
const rel = (f: string) => relative(src, f).replaceAll('\\', '/');

// Спецификаторы модуля: import … from, export … from, import 'x', import('x').
function specifiers(code: string): string[] {
  const out: string[] = [];
  const rules = [
    /\b(?:import|export)\s[^'"`;]*?\sfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ];
  for (const re of rules) for (const m of code.matchAll(re)) out.push(m[1]!);
  return out;
}

function resolveFile(from: string, spec: string): string | null {
  const base = join(dirname(from), spec.replace(/\?.*$/, ''));
  const tries = [
    base,
    `${base}.ts`,
    `${base}.tsx`,
    `${base}.d.ts`,
    join(base, 'index.ts'),
    join(base, 'index.tsx'),
  ];
  return tries.find((f) => existsSync(f) && /\.(tsx?|css|json)$/.test(f)) ?? null;
}

// Граф от точки входа: файлы src (относительно src) и внешние пакеты.
function graph(entry: string): { files: Set<string>; packages: Set<string> } {
  const files = new Set<string>();
  const packages = new Set<string>();
  const queue = [entry];
  while (queue.length > 0) {
    const file = queue.pop()!;
    if (files.has(rel(file))) continue;
    files.add(rel(file));
    if (!/\.tsx?$/.test(file)) continue;
    for (const spec of specifiers(readFileSync(file, 'utf8'))) {
      if (!spec.startsWith('.')) {
        packages.add(spec);
        continue;
      }
      const next = resolveFile(file, spec);
      if (!next) throw new Error(`${rel(file)}: не найден импорт ${spec}`);
      queue.push(next);
    }
  }
  return { files, packages };
}

describe('разбор импортов стража', () => {
  test('статические, побочные, export from и динамические import()', () => {
    const code = [
      "import a from './a';",
      "import {\n  b,\n  c,\n} from '../b';",
      "import type { T } from './t';",
      "export { d } from './d';",
      "import './styles';",
      "const lazy = () => import('./lazy');",
      "const m = await import( 'msw/browser' );",
    ].join('\n');
    expect(specifiers(code).sort()).toEqual(
      ['./a', '../b', './t', './d', './styles', './lazy', 'msw/browser'].sort(),
    );
  });
});

describe('обычная сборка без моков', () => {
  const main = graph(join(src, 'main.tsx'));

  test('стражу есть что читать', () => {
    expect(main.files).toContain('App.tsx');
    expect(main.files).toContain('app/router.tsx');
    expect(main.files).toContain('features/geo/CitiesPage.tsx');
    expect(main.packages).toContain('react-dom/client');
  });

  test('стили — через общий модуль app/styles.ts (тот же набор, что у режима shots)', () => {
    expect(main.files).toContain('app/styles.ts');
    expect(graph(join(src, 'shots/main.tsx')).files).toContain('app/styles.ts');
  });

  test('граф от src/main.tsx не содержит msw, src/shots и src/test', () => {
    const mocks = [...main.packages].filter(
      (p) => p === 'msw' || p.startsWith('msw/') || p === 'openapi-msw',
    );
    const files = [...main.files].filter((f) => f.startsWith('shots/') || f.startsWith('test/'));
    expect([...mocks, ...files]).toEqual([]);
  });

  test('сервис-воркера MSW нет в public/ (его отдаёт только плагин режима shots)', () => {
    expect(existsSync(join(root, 'public', 'mockServiceWorker.js'))).toBe(false);
  });
});
