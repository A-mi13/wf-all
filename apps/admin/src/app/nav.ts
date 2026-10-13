import { paths } from './paths';
import { can, type Me } from './permissions';

// Меню админки (спека §9.4, макет 07): разделы и права, по которым они видны (D23). Раздела без
// права в меню нет, прямой адрес — ForbiddenState. permission: null — своё (`self`): доступно
// любому активному сотруднику. desktopOnly — на телефоне закрыт («только с компьютера», D22).
// Разделы будущих модулей и «Справочники» (план B) появятся вместе с модулями.
export type NavItem = {
  path: string;
  labelKey: string;
  permission: string | null;
  desktopOnly?: boolean;
};
export type NavGroup = { labelKey: string; items: NavItem[] };

export const NAV: NavGroup[] = [
  {
    labelKey: 'nav.groups.security',
    items: [
      { path: paths.audit, labelKey: 'nav.audit', permission: 'audit.log.read' },
      {
        path: paths.staff,
        labelKey: 'nav.staff',
        permission: 'staff.roles.read',
        desktopOnly: true,
      },
      { path: paths.approvals, labelKey: 'nav.approvals', permission: null },
      { path: paths.me, labelKey: 'nav.me', permission: null, desktopOnly: true },
    ],
  },
];

export const canOpen = (me: Me, item: NavItem): boolean =>
  item.permission === null || can(me, item.permission);

// Меню сотрудника: только разрешённые разделы, пустые группы убраны.
export function navFor(me: Me): NavGroup[] {
  return NAV.map((g) => ({ ...g, items: g.items.filter((i) => canOpen(me, i)) })).filter(
    (g) => g.items.length > 0,
  );
}

// Раздел по пути (для проверки права на прямой адрес).
export const navItem = (path: string): NavItem | undefined =>
  NAV.flatMap((g) => g.items).find((i) => i.path === path);

// Стартовая страница — первый разрешённый раздел меню (P19). «Мой доступ» есть у всех.
export function startPath(me: Me): string {
  return navFor(me)[0]?.items[0]?.path ?? paths.me;
}
