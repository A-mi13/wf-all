import type { IconName } from '@wf/icons';

// Пункт меню админки (спека §9.3–9.4, D22–D23).
// - permission — право, без которого пункта нет в меню, а прямой адрес даёт ForbiddenState;
//   null — своё (`self`): доступно любому активному сотруднику;
// - desktopOnly — задел (спека UI-фундамента §3.1: модерация может снова закрыть раздел целиком);
//   сейчас его нет ни у одного пункта (страж app/nav.test.ts), и каркас его не читает — «только с
//   компьютера» решают действия (ActionGate). Вернуть закрытие раздела — вместе с каркасом и тестом;
// - countLabelKey — подпись счётчика для читалки экрана ({count}), число даёт AppShell.counts.
export type NavItem = {
  path: string;
  labelKey: string;
  permission: string | null;
  desktopOnly?: boolean;
  icon?: IconName;
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
