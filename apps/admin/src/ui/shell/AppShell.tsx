import { Link, useRouterState } from '@tanstack/react-router';
import { type ReactNode, useId, useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { usePermissionCheck } from '../../app/session';
import { Avatar } from '../Avatar';
import { Button } from '../Button';
import { CountBadge } from '../CountBadge';
import { cx } from '../cx';
import { useIsMobile } from '../media';
import { Sheet } from '../overlay';
import { DesktopOnlyState, ForbiddenState, OfflineBanner } from '../states';
import styles from './AppShell.module.css';
import { NAV_ICONS, type NavIcon } from './icons';
import { filterNav, findNavItem, type NavGroup, type NavItem } from './types';

export type AppShellProps = {
  groups: NavGroup[]; // всё меню; пункты без права AppShell убирает сам (usePermissionCheck)
  account: { name: string; details: string }; // «Мария К.», «модератор · Ставрополь»
  counts?: Partial<Record<string, number>>; // счётчики пунктов по path («Подтверждения» — Task 15)
  onLogout: () => void;
  loggingOut?: boolean;
  profileMenu?: ReactNode; // меню профиля: переключатель языка
  children: ReactNode; // страница раздела
};

// Таббар на телефоне: не больше стольких разделов, остальное — в «Ещё».
const TABBAR_ITEMS = 4;

// Каркас админки (спека §9.2–9.4, макеты 07 и 08).
// - Десктоп: сайдбар — бренд, группы меню со счётчиками, карточка сотрудника («имя, роль · города»,
//   меню профиля, «Выйти»).
// - Уже breakpoint-mobile (useIsMobile): нижний таббар — первые разделы, открытые на телефоне, и
//   «Ещё» (шторка: карточка, все разрешённые разделы, меню профиля, «Выйти»).
// - Меню — по правам (D23): пункта без права нет; прямой адрес такого раздела — ForbiddenState.
//   desktopOnly на телефоне — DesktopOnlyState (D22). Решает всегда сервер.
// - Над страницей — баннер «нет сети».
export function AppShell(props: AppShellProps) {
  const { groups, children } = props;
  const can = usePermissionCheck();
  const mobile = useIsMobile();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const menu = useMemo(() => filterNav(groups, can), [groups, can]);
  const current = findNavItem(groups, pathname);

  let content = children;
  if (current && current.permission !== null && !can(current.permission)) {
    content = <ForbiddenState />;
  } else if (mobile && current?.desktopOnly) {
    content = <DesktopOnlyState />;
  }

  const page = (
    <div className={styles.page}>
      <OfflineBanner />
      <main className={styles.main}>{content}</main>
    </div>
  );

  return mobile ? (
    <div className={cx(styles.shell, styles.mobile)}>
      {page}
      <TabBar {...props} menu={menu} current={current} />
    </div>
  ) : (
    <div className={styles.shell}>
      <Sidebar {...props} menu={menu} current={current} />
      {page}
    </div>
  );
}

type Layout = AppShellProps & { menu: NavGroup[]; current: NavItem | undefined };

function Sidebar({ menu, current, counts, account, profileMenu, onLogout, loggingOut }: Layout) {
  const t = useTranslations();
  return (
    <aside className={styles.sidebar}>
      <Brand />
      <nav aria-label={t('nav.label')} className={styles.nav}>
        <Groups menu={menu} current={current} counts={counts} />
      </nav>
      <div className={styles.account}>
        <p className={styles.accountName}>{account.name}</p>
        <p className={styles.accountDetails}>{account.details}</p>
        {profileMenu && <div className={styles.profileMenu}>{profileMenu}</div>}
        <Button variant="link" className={styles.logout} loading={loggingOut} onClick={onLogout}>
          {t('nav.logout')}
        </Button>
      </div>
    </aside>
  );
}

function Brand() {
  const t = useTranslations('app');
  return (
    <div className={styles.brand}>
      <p className={styles.brandName}>{t('brand')}</p>
      <p className={styles.brandTitle}>{t('title')}</p>
    </div>
  );
}

function Groups({
  menu,
  current,
  counts,
  onNavigate,
}: {
  menu: NavGroup[];
  current: NavItem | undefined;
  counts?: Partial<Record<string, number>>;
  onNavigate?: () => void;
}) {
  return menu.map((g) => (
    <Group key={g.labelKey} group={g} current={current} counts={counts} onNavigate={onNavigate} />
  ));
}

function Group({
  group,
  current,
  counts,
  onNavigate,
}: {
  group: NavGroup;
  current: NavItem | undefined;
  counts?: Partial<Record<string, number>>;
  onNavigate?: () => void;
}) {
  const t = useTranslations();
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className={styles.group}>
      <p id={id} className={styles.groupLabel}>
        {t(group.labelKey)}
      </p>
      <ul className={styles.items}>
        {group.items.map((item) => (
          <li key={item.path}>
            <NavLink
              item={item}
              active={item === current}
              count={counts?.[item.path]}
              onNavigate={onNavigate}
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

function NavLink({
  item,
  active,
  count,
  onNavigate,
  tab,
}: {
  item: NavItem;
  active: boolean;
  count?: number;
  onNavigate?: () => void;
  tab?: boolean; // кнопка таббара: иконка над подписью
}) {
  const t = useTranslations();
  const label = count && item.countLabelKey ? t(item.countLabelKey, { count }) : undefined;
  return (
    <Link
      to={item.path}
      activeOptions={{ includeSearch: false }}
      className={cx(tab ? styles.tab : styles.link, active && styles.active)}
      onClick={onNavigate}
    >
      {tab ? (
        <>
          <span className={styles.tabIcon}>
            <Icon name={item.icon ?? 'list'} />
          </span>
          <span className={styles.tabLabel}>{t(item.labelKey)}</span>
          {count ? ' ' : null}
          {/* счётчик — после подписи (имя «Подтверждения 2 …»), на иконке — стилем */}
          <span className={styles.tabBadge}>
            <CountBadge count={count ?? 0} active label={label} />
          </span>
        </>
      ) : (
        <>
          <span className={styles.mark} aria-hidden="true" />
          <span className={styles.linkLabel}>{t(item.labelKey)}</span>
          {count ? ' ' : null}
          <CountBadge count={count ?? 0} active={active} label={label} />
        </>
      )}
    </Link>
  );
}

function Icon({ name }: { name: NavIcon }) {
  return (
    <svg viewBox="0 0 24 24" width="19" height="19" aria-hidden="true" className={styles.icon}>
      <path d={NAV_ICONS[name]} />
    </svg>
  );
}

function TabBar({ menu, current, counts, account, profileMenu, onLogout, loggingOut }: Layout) {
  const t = useTranslations();
  const [open, setOpen] = useState(false);
  const tabs = menu
    .flatMap((g) => g.items)
    .filter((i) => !i.desktopOnly)
    .slice(0, TABBAR_ITEMS);
  const inMore = current !== undefined && !tabs.includes(current);

  return (
    <>
      <nav aria-label={t('nav.label')} className={styles.tabbar}>
        {tabs.map((item) => (
          <NavLink
            key={item.path}
            item={item}
            active={item === current}
            count={counts?.[item.path]}
            tab
          />
        ))}
        <button
          type="button"
          className={cx(styles.tab, inMore && styles.active)}
          aria-haspopup="dialog"
          aria-expanded={open}
          data-current={inMore ? 'true' : undefined}
          onClick={() => setOpen(true)}
        >
          <span className={styles.tabIcon}>
            <Icon name="more" />
          </span>
          <span className={styles.tabLabel}>{t('nav.more')}</span>
        </button>
      </nav>
      <Sheet open={open} onClose={() => setOpen(false)} title={t('nav.more_title')}>
        <div className={styles.moreAccount}>
          <Avatar name={account.name} size="md" />
          <div>
            <p className={styles.accountName}>{account.name}</p>
            <p className={styles.accountDetails}>{account.details}</p>
          </div>
        </div>
        <nav aria-label={t('nav.label')} className={styles.nav}>
          <Groups menu={menu} current={current} counts={counts} onNavigate={() => setOpen(false)} />
        </nav>
        {profileMenu && <div className={styles.profileMenu}>{profileMenu}</div>}
        <Button variant="secondary" block loading={loggingOut} onClick={onLogout}>
          {t('nav.logout')}
        </Button>
      </Sheet>
    </>
  );
}
