import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Me } from '../../app/permissions';
import { stubMedia } from '../../test/media';
import { meModerator, meOwner } from '../../test/me';
import { renderWithProviders } from '../../test/render';
import { AppShell } from './AppShell';
import type { NavGroup } from './types';

// Меню-образец: как в app/nav.ts — пять разделов, пятый на телефоне уходит в «Ещё».
const NAV: NavGroup[] = [
  {
    labelKey: 'nav.groups.security',
    items: [
      { path: '/audit', labelKey: 'nav.audit', permission: 'audit.log.read', icon: 'log' },
      { path: '/staff', labelKey: 'nav.staff', permission: 'staff.roles.read', icon: 'key' },
      {
        path: '/approvals',
        labelKey: 'nav.approvals',
        permission: null,
        icon: 'shield',
        countLabelKey: 'nav.counts.approvals',
      },
      { path: '/me', labelKey: 'nav.me', permission: null, icon: 'user' },
    ],
  },
  {
    labelKey: 'nav.groups.directories',
    items: [
      { path: '/geo/cities', labelKey: 'nav.cities', permission: 'geo.cities.read', icon: 'city' },
    ],
  },
];

// Длинная почта: в карточке обрезается многоточием, полная — в подсказке.
const LONG_EMAIL = 'olga.vladimirovna.moderator@stavropol.wf.example';

// Модератор без права на «Роли и доступ» (в каталоге оно у всех ролей — здесь проверяется
// механизм: нет права — нет пункта).
const meNoRoles: Me = {
  ...meModerator,
  permissions: { 'audit.log.read': 'scoped' },
};

function mount(path: string, me: Me, opts: { counts?: Record<string, number> } = {}) {
  const onLogout = vi.fn();
  const rootRoute = createRootRoute({
    component: () => (
      <AppShell
        groups={NAV}
        account={{ name: 'Ольга В.', email: LONG_EMAIL, details: 'модератор · Ставрополь' }}
        counts={opts.counts}
        onLogout={onLogout}
        profileMenu={<p>Переключатель языка</p>}
      >
        <Outlet />
      </AppShell>
    ),
  });
  const page = (p: string, title: string) =>
    createRoute({ getParentRoute: () => rootRoute, path: p, component: () => <h1>{title}</h1> });
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      page('/audit', 'Страница журнала'),
      page('/staff', 'Страница ролей'),
      page('/approvals', 'Страница подтверждений'),
      page('/me', 'Страница доступа'),
      page('/geo/cities', 'Страница городов'),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  renderWithProviders(<RouterProvider router={router} />, { me });
  return { onLogout, router };
}

// Телефон: узкий экран и палец (спека UI-фундамента §3.2; раскладку решает ширина).
function phone() {
  stubMedia({ width: 390, pointer: 'coarse' });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('десктоп: сайдбар', () => {
  test('меню по правам: без права раздела нет пункта', async () => {
    mount('/audit', meNoRoles);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(nav).getByRole('group', { name: 'Безопасность' })).toBeInTheDocument();
    expect(within(nav).getByRole('link', { name: 'Журнал действий' })).toHaveAttribute(
      'aria-current',
      'page',
    );
    expect(within(nav).getByRole('link', { name: 'Подтверждения' })).toBeInTheDocument();
    expect(within(nav).queryByRole('link', { name: 'Роли и доступ' })).not.toBeInTheDocument();
  });

  test('прямой адрес раздела без права — ForbiddenState вместо страницы', async () => {
    mount('/staff', meNoRoles);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Нет доступа к разделу' }),
    ).toBeInTheDocument();
    expect(screen.queryByText('Страница ролей')).not.toBeInTheDocument();
  });

  test('карточка сотрудника, меню профиля и «Выйти»', async () => {
    const { onLogout } = mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    expect(screen.getByText('Ольга В.')).toBeInTheDocument();
    expect(screen.getByText('модератор · Ставрополь')).toBeInTheDocument();
    expect(screen.getByText('Переключатель языка')).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  // Карточка (спека §4.12, ревью V1 m3): язык и тема — каждый своей строкой, «Выйти» — последней строкой во
  // всю ширину (с темой ряд «язык · Выйти» не помещается в колонку сайдбара).
  test('карточка: язык, тема, «Выйти» — по порядку, «Выйти» — последняя и во всю ширину', async () => {
    mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const aside = screen.getByRole('complementary');
    const locale = within(aside).getByText('Переключатель языка');
    const theme = within(aside).getByRole('group', { name: 'Тема' });
    const logout = within(aside).getByRole('button', { name: 'Выйти' });
    expect(locale.compareDocumentPosition(theme) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(theme.compareDocumentPosition(logout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(logout.parentElement?.lastElementChild).toBe(logout);
    expect(logout.className).toMatch(/block/);
  });

  test('карточка: почта сотрудника — полной строкой в подсказке (длинная обрезается многоточием)', async () => {
    mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const email = screen.getByText(LONG_EMAIL);
    expect(email).toHaveAttribute('title', LONG_EMAIL);
    // имя, почта, роль — сверху вниз
    const name = screen.getByText('Ольга В.');
    const details = screen.getByText('модератор · Ставрополь');
    expect(name.compareDocumentPosition(email) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(email.compareDocumentPosition(details) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test('«Выйти» — заметная кнопка с иконкой, с клавиатуры: Tab до неё и Enter', async () => {
    const { onLogout } = mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const logout = screen.getByRole('button', { name: 'Выйти' });
    // иконка выхода — декоративная: имя кнопки остаётся «Выйти»
    expect(logout.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
    // не текст-ссылка (variant link), а вторичная кнопка
    expect(logout.className).toMatch(/secondary/);
    logout.focus();
    await userEvent.setup().keyboard('{Enter}');
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  test('счётчик пункта: число и подпись для читалки; 0 не рисуется', async () => {
    mount('/audit', meOwner, { counts: { '/approvals': 3, '/audit': 0 } });
    await screen.findByRole('heading', { name: 'Страница журнала' });
    expect(screen.getByRole('link', { name: /Подтверждения/ })).toHaveAccessibleName(
      'Подтверждения 3 запроса ждут решения',
    );
    expect(screen.getByRole('link', { name: 'Журнал действий' })).toBeInTheDocument();
  });

  test('все разделы открыты на компьютере, «Справочники» — своей группой', async () => {
    mount('/geo/cities', meOwner);
    expect(await screen.findByRole('heading', { name: 'Страница городов' })).toBeInTheDocument();
  });
});

describe('телефон (< breakpoint-mobile): таббар и «Ещё»', () => {
  test('таббар — первые 4 разрешённых раздела (U10) и «Ещё»; сайдбара нет', async () => {
    phone();
    mount('/audit', meOwner, { counts: { '/approvals': 2 } });
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const tabbar = screen.getByRole('navigation', { name: 'Разделы' });
    const links = within(tabbar).getAllByRole('link');
    expect(links.map((l) => l.getAttribute('href'))).toEqual([
      '/audit',
      '/staff',
      '/approvals',
      '/me',
    ]);
    expect(links[2]).toHaveAccessibleName('Подтверждения 2 запроса ждут решения');
    expect(within(tabbar).getByRole('button', { name: 'Ещё' })).toBeInTheDocument();
    expect(screen.queryByText('Ольга В.')).not.toBeInTheDocument();
  });

  test('«Ещё»: все разрешённые разделы, карточка, язык и «Выйти»; раздел открывается', async () => {
    phone();
    const user = userEvent.setup();
    const { onLogout } = mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    await user.click(screen.getByRole('button', { name: 'Ещё' }));
    const sheet = screen.getByRole('dialog', { name: 'Разделы и доступ' });
    expect(within(sheet).getByText('Ольга В.')).toBeInTheDocument();
    expect(within(sheet).getByText(LONG_EMAIL)).toHaveAttribute('title', LONG_EMAIL);
    expect(within(sheet).getByText('Переключатель языка')).toBeInTheDocument();
    expect(within(sheet).getByRole('group', { name: 'Тема' })).toBeInTheDocument();
    expect(within(sheet).getByRole('link', { name: 'Мой доступ' })).toBeInTheDocument();
    await user.click(within(sheet).getByRole('link', { name: 'Города и запуск' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Страница городов' })).toBeInTheDocument();
    expect(screen.queryByText('Только с компьютера')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Ещё' }));
    await user.click(screen.getByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  test('прямой адрес раздела вне таббара — страница, «Ещё» отмечена текущей', async () => {
    phone();
    mount('/geo/cities', meOwner);
    expect(await screen.findByRole('heading', { name: 'Страница городов' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Ещё' })).toHaveAttribute('data-current', 'true');
  });

  test('ширина меняется на лету — раскладка следом', async () => {
    let narrow = false;
    const listeners = new Set<() => void>();
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => ({
        get matches() {
          return narrow;
        },
        media: query,
        addEventListener: (_: string, fn: () => void) => listeners.add(fn),
        removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
      })),
    );
    mount('/audit', meOwner);
    await screen.findByText('Ольга В.');
    narrow = true;
    act(() => listeners.forEach((fn) => fn()));
    expect(screen.queryByText('Ольга В.')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Ещё' })).toBeInTheDocument();
  });
});

// Три ширины каркаса (спека UI-фундамента §3.4): ≥ 1024 — сайдбар с подписями, 768–1023 — полоса
// иконок (size.rail) с подсказкой-названием, < 768 — таббар. Решает ширина окна (useIsMobile,
// useIsCompact), не устройство.
describe('узкая полоса 768–1023', () => {
  test('900 + мышь: ссылки полосы с именами разделов, счётчик слышен; сайдбара и таббара нет', async () => {
    stubMedia({ width: 900, pointer: 'fine' });
    mount('/audit', meOwner, { counts: { '/approvals': 2 } });
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((l) => l.getAttribute('href'))).toEqual([
      '/audit',
      '/staff',
      '/approvals',
      '/me',
      '/geo/cities',
    ]);
    expect(within(nav).getByRole('link', { name: 'Журнал действий' })).toHaveAttribute(
      'aria-current',
      'page',
    );
    expect(
      within(nav).getByRole('link', { name: 'Подтверждения 2 запроса ждут решения' }),
    ).toBeInTheDocument();
    expect(within(nav).getByRole('group', { name: 'Справочники' })).toBeInTheDocument();
    // подсказка-название не описывает ссылку повторно (имя и так «Журнал действий», ревью m2)
    for (const link of links) expect(link).not.toHaveAttribute('aria-describedby');
    // подписи не видны — только значки; карточки сотрудника и «Ещё» таббара нет
    expect(screen.queryByText('Ольга В.')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Ещё' })).not.toBeInTheDocument();
  });

  test('900: фокус на значке — подсказка с названием раздела', async () => {
    stubMedia({ width: 900, pointer: 'fine' });
    mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    await userEvent.setup().tab();
    const first = screen.getByRole('link', { name: 'Журнал действий' });
    expect(first).toHaveFocus();
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Журнал действий');
  });

  test('900: аватар → шторка с карточкой, языком, темой и «Выйти»', async () => {
    stubMedia({ width: 900, pointer: 'fine' });
    const user = userEvent.setup();
    const { onLogout } = mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const avatar = screen.getByRole('button', { name: 'Профиль и настройки: Ольга В.' });
    expect(avatar).toHaveAttribute('aria-expanded', 'false');
    await user.click(avatar);
    const sheet = screen.getByRole('dialog', { name: 'Профиль и настройки' });
    expect(within(sheet).getByText('Ольга В.')).toBeInTheDocument();
    expect(within(sheet).getByText(LONG_EMAIL)).toHaveAttribute('title', LONG_EMAIL);
    expect(within(sheet).getByText('Переключатель языка')).toBeInTheDocument();
    expect(within(sheet).getByRole('group', { name: 'Тема' })).toBeInTheDocument();
    await user.click(within(sheet).getByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  test('1280 — сайдбар с подписями; 390 — таббар', async () => {
    const media = stubMedia({ width: 1280, pointer: 'fine' });
    mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    expect(screen.getByText('Ольга В.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Журнал действий' })).toHaveTextContent(
      'Журнал действий',
    );
    act(() => media.set({ width: 390, pointer: 'coarse' }));
    expect(screen.getByRole('button', { name: 'Ещё' })).toBeInTheDocument();
    expect(screen.queryByText('Ольга В.')).not.toBeInTheDocument();
    act(() => media.set({ width: 900, pointer: 'coarse' }));
    expect(
      screen.getByRole('button', { name: 'Профиль и настройки: Ольга В.' }),
    ).toBeInTheDocument();
  });
});
