import { filterNav, type NavGroup, type NavItem } from '../ui/shell/types';
import { paths } from './paths';
import { can, type Me } from './permissions';

// Меню админки (спека §9.4, макет 07): разделы и права, по которым они видны (D23). Раздела без
// права в меню нет, прямой адрес — ForbiddenState (AppShell). permission: null — своё (`self`):
// доступно любому активному сотруднику. Все разделы открыты на любой ширине и устройстве (спека
// UI-фундамента §3.1): «только с компьютера» — действия (ActionGate, src/app/desktopOnly.ts), а не
// разделы; desktopOnly ни у одного пункта нет (страж nav.test.ts). Разделы будущих модулей появятся
// вместе с модулями. «Справочники» — план B, спека §9.4 п. 8–12; страница города — вложенный адрес
// «Городов» (их право).
// Новый раздел: путь — paths.ts, маршрут — router.tsx, пункт с правом — здесь.
export type { NavGroup, NavItem };

export const NAV: NavGroup[] = [
  {
    labelKey: 'nav.groups.security',
    items: [
      { path: paths.audit, labelKey: 'nav.audit', permission: 'audit.log.read', icon: 'log' },
      {
        path: paths.staff,
        labelKey: 'nav.staff',
        permission: 'staff.roles.read',
        icon: 'key',
      },
      {
        path: paths.approvals,
        labelKey: 'nav.approvals',
        permission: null,
        icon: 'shield',
        countLabelKey: 'nav.counts.approvals',
      },
      { path: paths.me, labelKey: 'nav.me', permission: null, icon: 'user' },
    ],
  },
  {
    labelKey: 'nav.groups.directories',
    items: [
      {
        path: paths.cities,
        labelKey: 'nav.cities',
        permission: 'geo.cities.read',
        icon: 'city',
      },
      {
        path: paths.countries,
        labelKey: 'nav.countries',
        permission: 'geo.countries.read',
        icon: 'globe',
      },
      {
        path: paths.imports,
        labelKey: 'nav.imports',
        permission: 'geo.imports.read',
        icon: 'download',
      },
      {
        path: paths.versions,
        labelKey: 'nav.versions',
        permission: 'platform.versions.read',
        icon: 'phone',
      },
      {
        path: paths.flags,
        labelKey: 'nav.flags',
        permission: 'platform.flags.read',
        icon: 'flag',
      },
    ],
  },
];

// Меню сотрудника: только разрешённые разделы, пустые группы убраны.
export function navFor(me: Me): NavGroup[] {
  return filterNav(NAV, (p) => can(me, p));
}

// Стартовая страница — первый разрешённый раздел меню (P19). «Мой доступ» есть у всех.
export function startPath(me: Me): string {
  return navFor(me)[0]?.items[0]?.path ?? paths.me;
}
