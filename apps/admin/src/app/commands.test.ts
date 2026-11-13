/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, test, vi } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';
import { meModerator, meOwner } from '../test/me';
import { COMMANDS, type CommandContext, sectionsFor } from './commands';
import { NAV, navFor } from './nav';
import { isPanelKey, paths } from './paths';

// Реестр команд палитры (спека UI-фундамента §4.1, план UF4.3; m17). features/*/commands.ts попадают в основной кусок
// сборки: импортируют только пути, типы, ключи локалей, хуки прав и реестр «только с компьютера» — экран в импорте
// склеил бы ленивый раздел с основным куском.

const SRC = join(import.meta.dirname, '..');
const ALLOWED = new Set([
  '../../app/paths',
  '../../app/commands', // только типы (import type)
  '../../app/desktopOnly',
  '../../app/permissions',
  '../../app/session',
  '@wf/icons', // только тип IconName
]);

const featureFiles = () =>
  readdirSync(join(SRC, 'features'), { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .map((d) => `features/${d.name}/commands.ts`)
    .filter((f) => {
      try {
        readFileSync(join(SRC, f));
        return true;
      } catch {
        return false;
      }
    });

// Все виды импорта: from '…', import '…' (побочный), import('…') и import(`…`), export … from '…'.
const SPECIFIER = /(?:\bfrom\s*|\bimport\s*\(?\s*)(['"`])([^'"`]+)\1/g;
function importsOf(source: string): string[] {
  return [...source.matchAll(SPECIFIER)].map((m) => m[2]!);
}

function lookup(messages: unknown, key: string): unknown {
  return key
    .split('.')
    .reduce<unknown>(
      (node, part) =>
        typeof node === 'object' && node !== null
          ? (node as Record<string, unknown>)[part]
          : undefined,
      messages,
    );
}

const ctx = (): CommandContext => ({
  navigate: vi.fn(),
  setTheme: vi.fn(),
  setLocale: vi.fn(),
  setDensity: vi.fn(),
  openShortcuts: vi.fn(),
  logout: vi.fn(),
});

describe('реестр команд', () => {
  test('файлы features/*/commands.ts импортируют только разрешённое', () => {
    const files = featureFiles();
    expect(files.sort()).toEqual(
      [
        'features/approvals/commands.ts',
        'features/audit/commands.ts',
        'features/geo/commands.ts',
        'features/me/commands.ts',
        'features/staff/commands.ts',
      ].sort(),
    );
    for (const file of files) {
      const bad = importsOf(readFileSync(join(SRC, file), 'utf8')).filter((s) => !ALLOWED.has(s));
      expect(bad, file).toEqual([]);
    }
  });

  test('разбор импортов видит все виды', () => {
    expect(
      importsOf(
        "import { A } from '../a';\nimport '../b';\nconst c = import('../c');\nexport { d } from \"../d\";\nimport(`../e`);",
      ),
    ).toEqual(['../a', '../b', '../c', '../d', '../e']);
  });

  test('app/commands.ts собирает каждый features/*/commands.ts', () => {
    const source = readFileSync(join(SRC, 'app/commands.ts'), 'utf8');
    const imported = importsOf(source);
    for (const file of featureFiles()) {
      expect(imported, file).toContain(`../${file.replace(/\.ts$/, '')}`);
    }
  });

  test('id уникальны, labelKey и keywordsKey есть в ru и en', () => {
    const ids = COMMANDS.map((c) => c.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const c of COMMANDS) {
      for (const key of [c.labelKey, c.keywordsKey].filter((k): k is string => Boolean(k))) {
        expect(typeof lookup(ru, key), `ru ${c.id}: ${key}`).toBe('string');
        expect(typeof lookup(en, key), `en ${c.id}: ${key}`).toBe('string');
      }
    }
  });

  test('разделы — ровно пункты меню и «Главная», клавиши — по таблице §4.2', () => {
    const sections = COMMANDS.filter((c) => c.group === 'section');
    const navPaths = NAV.flatMap((g) => g.items.map((i) => i.path));
    const own = new Set<string | undefined>(sections.map((c) => c.path));
    for (const p of navPaths) expect(own.has(p), p).toBe(true);
    for (const c of sections) expect([...navPaths, paths.root], c.id).toContain(c.path);
    const nav = new Map(NAV.flatMap((g) => g.items).map((i) => [i.path, i]));
    for (const c of sections) {
      const item = c.path ? nav.get(c.path) : undefined;
      if (!item) continue;
      // право раздела — то же, что у пункта меню
      expect(c.permission ?? null, c.id).toBe(item.permission);
    }
    const keys = Object.fromEntries(sections.map((c) => [c.path, c.shortcut]));
    expect(keys).toEqual({
      [paths.root]: 'g h',
      [paths.audit]: 'g a',
      [paths.staff]: 'g r',
      [paths.approvals]: 'g p',
      [paths.me]: 'g m',
      [paths.cities]: 'g c',
      [paths.countries]: 'g o',
      [paths.imports]: 'g i',
      [paths.versions]: 'g v',
      [paths.flags]: 'g f',
    });
  });

  test('sectionsFor — ровно разделы меню сотрудника, «Главная» — подпись пункта меню', () => {
    for (const me of [meOwner, meModerator]) {
      const menu = navFor(me).flatMap((g) => g.items.map((i) => i.path));
      expect(sectionsFor(me).map((c) => c.path)).toEqual(menu);
    }
    expect(COMMANDS.find((c) => c.path === paths.root)?.labelKey).toBe('nav.home');
  });

  test('плотность таблиц — только с мышью (fineOnly)', () => {
    const density = COMMANDS.filter((x) => x.id.startsWith('density.'));
    expect(density).toHaveLength(2);
    for (const c of density) expect(c.fineOnly, c.id).toBe(true);
  });

  test('раздел — переход на свой путь', () => {
    for (const c of COMMANDS.filter((x) => x.group === 'section')) {
      const context = ctx();
      c.run(context);
      expect(context.navigate, c.id).toHaveBeenCalledWith({ to: c.path });
    }
  });

  test('действие, которому нужен экран, — переход с ключом панели (работает из адреса и после F5)', () => {
    const invite = COMMANDS.find((c) => c.id === 'staff.invite');
    const activate = COMMANDS.find((c) => c.id === 'geo.activate');
    const a = ctx();
    invite?.run(a);
    expect(a.navigate).toHaveBeenCalledWith({ to: paths.staff, search: { invite: 1 } });
    expect(invite).toMatchObject({ permission: 'staff.roles.grant', desktopOnly: true });
    const b = ctx();
    activate?.run(b);
    expect(b.navigate).toHaveBeenCalledWith({ to: paths.cities, search: { activate: 1 } });
    expect(activate).toMatchObject({ permission: 'geo.cities.activate' });
    // G9 Task 20: «Запустить импорт» — окно запуска в адресе (?start=1)
    const runImport = COMMANDS.find((c) => c.id === 'geo.imports.run');
    const d = ctx();
    runImport?.run(d);
    expect(d.navigate).toHaveBeenCalledWith({ to: paths.imports, search: { start: 1 } });
    expect(runImport).toMatchObject({ group: 'action', permission: 'geo.imports.run' });
    for (const c of COMMANDS) {
      const context = ctx();
      c.run(context);
      for (const [call] of vi.mocked(context.navigate).mock.calls) {
        for (const key of Object.keys(call.search ?? {})) expect(isPanelKey(key), c.id).toBe(true);
      }
    }
  });

  test('общие команды: тема, язык, плотность, горячие клавиши, выход', () => {
    const run = (id: string) => {
      const context = ctx();
      const c = COMMANDS.find((x) => x.id === id);
      expect(c, id).toBeDefined();
      c?.run(context);
      return context;
    };
    expect(run('theme.light').setTheme).toHaveBeenCalledWith('light');
    expect(run('theme.dark').setTheme).toHaveBeenCalledWith('dark');
    expect(run('theme.system').setTheme).toHaveBeenCalledWith('system');
    expect(run('locale.ru').setLocale).toHaveBeenCalledWith('ru');
    expect(run('locale.en').setLocale).toHaveBeenCalledWith('en');
    expect(run('density.compact').setDensity).toHaveBeenCalledWith('compact');
    expect(run('density.normal').setDensity).toHaveBeenCalledWith('normal');
    expect(run('shortcuts').openShortcuts).toHaveBeenCalled();
    expect(run('logout').logout).toHaveBeenCalled();
  });
});
