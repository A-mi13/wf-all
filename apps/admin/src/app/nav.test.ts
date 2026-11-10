import { describe, expect, test } from 'vitest';
import { meModerator, meOwner } from '../test/me';
import { findNavItem } from '../ui/shell/types';
import { NAV, navFor, startPath } from './nav';
import { AUTH_PATHS, paths } from './paths';
import type { Me } from './permissions';

// Меню «Справочников» (спека §9.4, план B Task 17): пять разделов после «Безопасности», каждый — со
// своим правом чтения. Права ролей — docs/05-ролевая-модель.md: поддержка видит
// все пять (geo.imports.read у неё allow), модератор — без «Импорта».

const DIRECTORIES = [paths.cities, paths.countries, paths.imports, paths.versions, paths.flags];

const menuPaths = (me: Me) => navFor(me).flatMap((g) => g.items.map((i) => i.path));
const groupOf = (me: Me, path: string) =>
  navFor(me).find((g) => g.items.some((i) => i.path === path))?.labelKey;

const meSupport: Me = {
  ...meOwner,
  role: 'support',
  permissions: {
    'audit.log.read': 'allow',
    'staff.roles.read': 'allow',
    'geo.cities.read': 'allow',
    'geo.countries.read': 'allow',
    'geo.imports.read': 'allow',
    'platform.versions.read': 'allow',
    'platform.flags.read': 'allow',
  },
};

const meModeratorGeo: Me = {
  ...meModerator,
  permissions: {
    ...meModerator.permissions,
    'geo.countries.read': 'allow',
    'platform.versions.read': 'allow',
    'platform.flags.read': 'allow',
  },
};

describe('«Справочники»', () => {
  test('поддержка видит все пять разделов в группе «Справочники»', () => {
    for (const p of DIRECTORIES) {
      expect(menuPaths(meSupport)).toContain(p);
      expect(groupOf(meSupport, p)).toBe('nav.groups.directories');
    }
  });

  test('модератор — «Города и запуск», «Страны», «Версии», «Флаги», без «Импорта»', () => {
    const menu = menuPaths(meModeratorGeo);
    expect(menu).toEqual(
      expect.arrayContaining([paths.cities, paths.countries, paths.versions, paths.flags]),
    );
    expect(menu).not.toContain(paths.imports);
  });

  test('каждый пункт — с правом чтения своего раздела', () => {
    const items = NAV.find((g) => g.labelKey === 'nav.groups.directories')?.items ?? [];
    expect(items.map((i) => [i.path, i.permission])).toEqual([
      [paths.cities, 'geo.cities.read'],
      [paths.countries, 'geo.countries.read'],
      [paths.imports, 'geo.imports.read'],
      [paths.versions, 'platform.versions.read'],
      [paths.flags, 'platform.flags.read'],
    ]);
    for (const i of items) expect(i.icon).toBeDefined();
  });

  test('группа — после «Безопасности»', () => {
    expect(NAV.map((g) => g.labelKey)).toEqual(['nav.groups.security', 'nav.groups.directories']);
  });

  test('без прав на справочники — группы нет', () => {
    expect(navFor({ ...meOwner, permissions: { 'audit.log.read': 'allow' } })).toHaveLength(1);
  });

  test('стартовая страница не изменилась — журнал (первый раздел «Безопасности»)', () => {
    expect(startPath(meSupport)).toBe(paths.audit);
    expect(startPath(meModeratorGeo)).toBe(paths.audit);
  });

  test('пути разделов', () => {
    expect(paths).toMatchObject({
      cities: '/geo/cities',
      city: '/geo/cities/$cityId',
      imports: '/geo/imports',
      countries: '/geo/countries',
      versions: '/app/versions',
      flags: '/flags',
    });
  });
});

// V1 п. 7: у каждого вложенного экрана каркаса (адрес внутри раздела меню, `/geo/cities/$cityId`) —
// возврат к родителю: AppShell рисует BackLink на раздел, найденный findNavItem. Новый вложенный
// путь без раздела-родителя в NAV остался бы тупиком — страж ловит это сразу.
test('каждый вложенный путь каркаса — внутри раздела меню (родитель для «Назад»)', () => {
  const sections = NAV.flatMap((g) => g.items.map((i) => i.path));
  const nested = Object.values(paths).filter(
    (p) => !sections.includes(p) && p.split('/').length > 2 && !AUTH_PATHS.includes(p),
  );
  expect(nested).toContain(paths.city);
  for (const p of nested) {
    const parent = findNavItem(NAV, p);
    expect(parent, p).toBeDefined();
    expect(parent?.path, p).not.toBe(p);
  }
});

// Все разделы открыты на любой ширине и устройстве (спека UI-фундамента §3.1): «только с компьютера» —
// действия (ActionGate), а не разделы. Флаг остаётся в типе на будущее; каркас его сейчас не читает —
// вернуть закрытие раздела можно только вместе с поддержкой в AppShell и тестом.
test('ни один пункт меню не desktopOnly', () => {
  const flagged = NAV.flatMap((g) => g.items).filter((i) => 'desktopOnly' in i);
  expect(flagged.map((i) => i.path)).toEqual([]);
});
