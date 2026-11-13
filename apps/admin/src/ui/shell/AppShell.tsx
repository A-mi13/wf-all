import { Link, useRouterState } from '@tanstack/react-router';
import { type ReactNode, useId, useMemo, useState } from 'react';
import { flushSync } from 'react-dom';
import { useTranslations } from 'use-intl';
import { usePermissionCheck } from '../../app/session';
import { Avatar } from '../Avatar';
import { Button } from '../Button';
import { CountBadge } from '../CountBadge';
import { cx } from '../cx';
import { Icon } from '../icon';
import { Tooltip } from '../hint/Tooltip';
import { useIsCompact, useIsMobile } from '../media';
import { Sheet } from '../overlay';
import { usePalette } from '../palette/context';
import { ForbiddenState, OfflineBanner } from '../states';
import { ThemeSwitch } from '../theme/ThemeSwitch';
import styles from './AppShell.module.css';
import { BackLink, BackMemoryProvider } from './BackLink';
import { filterNav, findNavItem, type NavGroup, type NavItem } from './types';

export type AppShellProps = {
  groups: NavGroup[]; // всё меню; пункты без права AppShell убирает сам (usePermissionCheck)
  // «Мария К.», почта-логин (нет — не показывается), «модератор · Ставрополь»
  account: { name: string; email?: string; details: string };
  counts?: Partial<Record<string, number>>; // счётчики пунктов по path («Подтверждения» — Task 15)
  onLogout: () => void;
  loggingOut?: boolean;
  profileMenu?: ReactNode; // меню профиля: переключатель языка
  children: ReactNode; // страница раздела
};

// Таббар на телефоне: не больше стольких разделов, остальное — в «Ещё».
const TABBAR_ITEMS = 4;

// Каркас админки (спека §9.2–9.4, макеты 07 и 08).
// - Десктоп: сайдбар — бренд, группы меню со счётчиками, карточка сотрудника (имя, почта, «роль ·
//   города»; ниже строками — меню профиля (язык), тема и «Выйти» с иконкой во всю ширину).
// - breakpoint-mobile…breakpoint-compact (useIsCompact, 768–1023): узкая полоса size.rail — значки разделов
//   (имя — aria-label, название — подсказкой Tooltip), счётчик — точкой (число слышно в имени ссылки);
//   карточка сотрудника — аватар, по нажатию шторка (карточка, язык, тема, «Выйти»). Спека UI-фундамента §3.4.
// - Уже breakpoint-mobile (useIsMobile): нижний таббар — первые 4 разрешённых раздела (U10) и
//   «Ещё» (шторка: карточка, все разрешённые разделы, меню профиля, тема, «Выйти»).
// - Меню — по правам (D23): пункта без права нет; прямой адрес такого раздела — ForbiddenState.
//   Разделы открыты на любой ширине и устройстве (спека UI-фундамента §3.1): «только с компьютера» —
//   действия (ActionGate). Решает всегда сервер.
// - Палитра (usePalette, есть провайдер): «Искать везде ⌘K» над меню сайдбара, значок поиска первым в полосе,
//   первым пунктом шторки «Ещё» на телефоне (спека UI-фундамента §4.1).
// - Над страницей — баннер «нет сети».
// - Вложенный экран раздела (адрес внутри раздела меню: /geo/cities/$cityId) — над страницей
//   BackLink «← раздел» на последний адрес раздела с его фильтрами (BackMemoryProvider). Один на все
//   вложенные экраны: экран свою кнопку «назад» не заводит.
export function AppShell(props: AppShellProps) {
  const { groups, children } = props;
  const t = useTranslations();
  const can = usePermissionCheck();
  const mobile = useIsMobile();
  const compact = useIsCompact();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const menu = useMemo(() => filterNav(groups, can), [groups, can]);
  const current = findNavItem(groups, pathname);

  let content = children;
  if (current && current.permission !== null && !can(current.permission)) {
    content = <ForbiddenState />;
  } else if (current && pathname !== current.path) {
    content = (
      <>
        <BackLink to={current.path} label={t(current.labelKey)} />
        {children}
      </>
    );
  }

  // Страница — ребёнок с ключом: при смене раскладки (поворот телефона, масштаб 200 % через breakpoint-mobile)
  // React сопоставляет её по ключу, а не по позиции, и не пересоздаёт — несохранённый ввод, прокрутка, выбор и
  // открытые панели остаются (финальное ревью I1). Порядок детей не выравниваем: на телефоне страница идёт до
  // таббара и в DOM — порядок чтения и Tab (WCAG 1.3.2, 2.4.3); CSS order его бы не поменял. Обёртка — тот же
  // div во всех раскладках (меняется только класс), иначе пересоздалось бы всё поддерево.
  const page = (
    <div key="page" className={styles.page}>
      <OfflineBanner />
      <main className={styles.main}>{content}</main>
    </div>
  );
  const layout = { ...props, menu, current };

  return (
    <BackMemoryProvider>
      <div className={cx(styles.shell, mobile && styles.mobile)}>
        {mobile
          ? [page, <TabBar key="tabbar" {...layout} />]
          : compact
            ? [<Rail key="rail" {...layout} />, page]
            : [<Sidebar key="sidebar" {...layout} />, page]}
      </div>
    </BackMemoryProvider>
  );
}

type Layout = AppShellProps & { menu: NavGroup[]; current: NavItem | undefined };

// Клавиша палитры по платформе: ⌘K на Apple, Ctrl K на остальных (спека UI-фундамента §4.1).
const isApple = () =>
  typeof navigator !== 'undefined' &&
  /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent);

// «Искать везде ⌘K» над меню (макет 07): открывает палитру (usePalette — провайдер в каркасе активной стадии; вне
// него кнопки нет). Подпись клавиши — по платформе, для читалки — aria-keyshortcuts.
function SearchButton() {
  const t = useTranslations('palette');
  const palette = usePalette();
  if (!palette) return null;
  const apple = isApple();
  return (
    <Button
      variant="secondary"
      block
      className={styles.search}
      aria-keyshortcuts={apple ? 'Meta+K' : 'Control+K'}
      onClick={palette.open}
    >
      <Icon name="search" size={16} />
      <span className={styles.searchLabel}>{t('open')}</span>
      <kbd className={styles.searchKey} aria-hidden="true">
        {t(apple ? 'key_mac' : 'key_other')}
      </kbd>
    </Button>
  );
}

function Sidebar({ menu, current, counts, account, profileMenu, onLogout, loggingOut }: Layout) {
  const t = useTranslations();
  return (
    <aside className={styles.sidebar}>
      <Brand />
      <SearchButton />
      <nav aria-label={t('nav.label')} className={styles.nav}>
        <Groups menu={menu} current={current} counts={counts} />
      </nav>
      <div className={styles.account}>
        <AccountText account={account} />
        <div className={styles.accountActions}>
          {profileMenu && <div className={styles.profileMenu}>{profileMenu}</div>}
          <ThemeSwitch />
          <LogoutButton block loading={loggingOut} onClick={onLogout} />
        </div>
      </div>
    </aside>
  );
}

// Имя, почта (длинная — многоточием, полная — в подсказке) и «роль · города».
function AccountText({ account }: { account: AppShellProps['account'] }) {
  return (
    <div className={styles.accountText}>
      <p className={styles.accountName}>{account.name}</p>
      {account.email && (
        <p className={styles.accountEmail} title={account.email}>
          {account.email}
        </p>
      )}
      <p className={styles.accountDetails}>{account.details}</p>
    </div>
  );
}

// «Выйти» — вторичная кнопка с иконкой (иконка декоративная, имя — текст); пока выход идёт — спиннер.
function LogoutButton({
  loading,
  onClick,
  block,
}: {
  loading?: boolean;
  onClick: () => void;
  block?: boolean;
}) {
  const t = useTranslations();
  return (
    <Button
      variant="secondary"
      block={block}
      className={styles.logout}
      loading={loading}
      onClick={onClick}
    >
      {!loading && <Icon name="logout" size={16} />}
      {t('nav.logout')}
    </Button>
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
            <Icon name={item.icon ?? 'list'} size={20} />
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

// Полоса 768–1023: значки разделов по группам, внизу — аватар сотрудника (шторка с настройками).
function Rail({ menu, current, counts, account, profileMenu, onLogout, loggingOut }: Layout) {
  const t = useTranslations();
  const [open, setOpen] = useState(false);
  const palette = usePalette();
  return (
    <>
      <aside className={styles.rail}>
        {palette && (
          <Tooltip content={t('palette.open')} placement="right" describe={false}>
            <button
              type="button"
              className={styles.railSearch}
              aria-label={t('palette.open')}
              aria-keyshortcuts={isApple() ? 'Meta+K' : 'Control+K'}
              onClick={palette.open}
            >
              <Icon name="search" size={20} />
            </button>
          </Tooltip>
        )}
        <nav aria-label={t('nav.label')} className={styles.railNav}>
          {menu.map((g) => (
            <div
              key={g.labelKey}
              role="group"
              aria-label={t(g.labelKey)}
              className={styles.railGroup}
            >
              <ul className={styles.railItems}>
                {g.items.map((item) => (
                  <li key={item.path}>
                    <RailLink item={item} active={item === current} count={counts?.[item.path]} />
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <button
          type="button"
          className={styles.railAccount}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={t('nav.account_open', { name: account.name })}
          onClick={() => setOpen(true)}
        >
          <Avatar name={account.name} size="sm" />
        </button>
      </aside>
      <Sheet open={open} onClose={() => setOpen(false)} title={t('nav.account')}>
        <div className={styles.moreAccount}>
          <Avatar name={account.name} size="md" />
          <AccountText account={account} />
        </div>
        <div className={styles.sheetSettings}>
          {profileMenu}
          <ThemeSwitch />
        </div>
        <LogoutButton block loading={loggingOut} onClick={onLogout} />
      </Sheet>
    </>
  );
}

// Значок раздела: имя ссылки — название (и число счётчика для читалки), подсказка — название; счётчик
// виден точкой на значке.
function RailLink({ item, active, count }: { item: NavItem; active: boolean; count?: number }) {
  const t = useTranslations();
  const name = t(item.labelKey);
  const label = count && item.countLabelKey ? `${name} ${t(item.countLabelKey, { count })}` : name;
  return (
    <Tooltip content={name} placement="right" describe={false}>
      <Link
        to={item.path}
        activeOptions={{ includeSearch: false }}
        aria-label={label}
        className={cx(styles.railLink, active && styles.active)}
      >
        <Icon name={item.icon ?? 'list'} size={20} />
        {count ? <span className={styles.railDot} aria-hidden="true" /> : null}
      </Link>
    </Tooltip>
  );
}

function TabBar({ menu, current, counts, account, profileMenu, onLogout, loggingOut }: Layout) {
  const t = useTranslations();
  const [open, setOpen] = useState(false);
  const palette = usePalette();
  const tabs = menu.flatMap((g) => g.items).slice(0, TABBAR_ITEMS);
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
            <Icon name="more" size={20} />
          </span>
          <span className={styles.tabLabel}>{t('nav.more')}</span>
        </button>
      </nav>
      <Sheet open={open} onClose={() => setOpen(false)} title={t('nav.more_title')}>
        {palette && (
          <Button
            variant="secondary"
            block
            className={styles.search}
            onClick={() => {
              // шторка снимается синхронно (с записью стека оверлеев): open() поверх модальной ничего не делает
              flushSync(() => setOpen(false));
              palette.open();
            }}
          >
            <Icon name="search" size={16} />
            <span className={styles.searchLabel}>{t('palette.open')}</span>
          </Button>
        )}
        <div className={styles.moreAccount}>
          <Avatar name={account.name} size="md" />
          <AccountText account={account} />
        </div>
        <nav aria-label={t('nav.label')} className={styles.nav}>
          <Groups menu={menu} current={current} counts={counts} onNavigate={() => setOpen(false)} />
        </nav>
        <div className={styles.sheetSettings}>
          {profileMenu}
          <ThemeSwitch />
        </div>
        <LogoutButton block loading={loggingOut} onClick={onLogout} />
      </Sheet>
    </>
  );
}
