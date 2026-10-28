import { describe, expect, test } from 'vitest';
import { meModerator, meOwner } from '../test/me';
import { NAV, navFor, startPath } from './nav';
import { paths } from './paths';
import type { Me } from './permissions';

// Меню «Справочников» (спека §9.4, план B Task 17): пять разделов после «Безопасности», каждый — со
// своим правом чтения и только с компьютера. Права ролей — docs/05-ролевая-модель.md: поддержка видит
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

  test('каждый пункт — с правом чтения своего раздела и только с компьютера', () => {
    const items = NAV.find((g) => g.labelKey === 'nav.groups.directories')?.items ?? [];
    expect(items.map((i) => [i.path, i.permission, i.desktopOnly])).toEqual([
      [paths.cities, 'geo.cities.read', true],
      [paths.countries, 'geo.countries.read', true],
      [paths.imports, 'geo.imports.read', true],
      [paths.versions, 'platform.versions.read', true],
      [paths.flags, 'platform.flags.read', true],
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
