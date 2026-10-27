import type { NavIcon } from './icons';

// Пункт меню админки (спека §9.3–9.4, D22–D23).
// - permission — право, без которого пункта нет в меню, а прямой адрес даёт ForbiddenState;
//   null — своё (`self`): доступно любому активному сотруднику;
// - desktopOnly — на телефоне раздел закрыт: в таббар не попадает, в «Ещё» есть, открывается
//   DesktopOnlyState;
// - countLabelKey — подпись счётчика для читалки экрана ({count}), число даёт AppShell.counts.
export type NavItem = {
  path: string;
  labelKey: string;
  permission: string | null;
  desktopOnly?: boolean;
  icon?: NavIcon;
  countLabelKey?: string;
};

export type NavGroup = { labelKey: string; items: NavItem[] };

// Меню сотрудника: только разрешённые разделы, пустые группы убраны.
export function filterNav(groups: NavGroup[], can: (permission: string) => boolean): NavGroup[] {
  return groups
    .map((g) => ({
      ...g,
      items: g.items.filter((i) => i.permission === null || can(i.permission)),
    }))
    .filter((g) => g.items.length > 0);
}

// Раздел по пути: сам путь или вложенный адрес раздела (/staff/…).
export function findNavItem(groups: NavGroup[], pathname: string): NavItem | undefined {
  return groups
    .flatMap((g) => g.items)
    .find((i) => pathname === i.path || pathname.startsWith(`${i.path}/`));
}
