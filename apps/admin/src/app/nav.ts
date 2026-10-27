import { filterNav, type NavGroup, type NavItem } from '../ui/shell/types';
import { paths } from './paths';
import { can, type Me } from './permissions';

// Меню админки (спека §9.4, макет 07): разделы и права, по которым они видны (D23). Раздела без
// права в меню нет, прямой адрес — ForbiddenState (AppShell). permission: null — своё (`self`):
// доступно любому активному сотруднику. desktopOnly — на телефоне закрыт («только с компьютера»,
// D22). Разделы будущих модулей и «Справочники» (план B) появятся вместе с модулями.
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
        desktopOnly: true,
        icon: 'key',
      },
      {
        path: paths.approvals,
        labelKey: 'nav.approvals',
        permission: null,
        icon: 'shield',
        countLabelKey: 'nav.counts.approvals',
      },
      { path: paths.me, labelKey: 'nav.me', permission: null, desktopOnly: true, icon: 'user' },
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
