import type { IconName } from '@wf/icons';
import { expect, expectTypeOf, test } from 'vitest';
import { filterNav, findNavItem, type NavGroup, type NavItem } from './types';

const NAV: NavGroup[] = [
  {
    labelKey: 'g',
    items: [
      { path: '/staff', labelKey: 'staff', permission: 'staff.roles.read' },
      { path: '/me', labelKey: 'me', permission: null },
    ],
  },
];

test('findNavItem: сам путь и вложенный адрес раздела — пункт; общий префикс без «/» — нет', () => {
  expect(findNavItem(NAV, '/staff')?.labelKey).toBe('staff');
  expect(findNavItem(NAV, '/staff/0192f1c4')?.labelKey).toBe('staff');
  expect(findNavItem(NAV, '/staffx')).toBeUndefined();
  expect(findNavItem(NAV, '/st')).toBeUndefined();
  expect(findNavItem(NAV, '/')).toBeUndefined();
});

test('filterNav: без права — без пункта, пустая группа убрана; null — своё, всегда', () => {
  expect(filterNav(NAV, () => false)).toEqual([{ labelKey: 'g', items: [NAV[0]!.items[1]] }]);
  expect(filterNav([{ labelKey: 'x', items: [NAV[0]!.items[0]!] }], () => false)).toEqual([]);
});

// Иконка пункта меню — имя из @wf/icons (план UF1a.3): NAV_ICONS переехали в пакет (группа nav).
test('иконка пункта меню — IconName из @wf/icons', () => {
  expectTypeOf<NavItem['icon']>().toEqualTypeOf<IconName | undefined>();
  const item: NavItem = { path: '/x', labelKey: 'x', permission: null, icon: 'log' };
  expect(item.icon).toBe('log');
});
