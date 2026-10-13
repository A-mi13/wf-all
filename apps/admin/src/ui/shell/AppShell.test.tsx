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
import { meModerator, meOwner } from '../../test/me';
import { renderWithProviders } from '../../test/render';
import { AppShell } from './AppShell';
import type { NavGroup } from './types';

// Меню-образец: как в app/nav.ts, плюс раздел, закрытый на телефоне, со своим правом.
const NAV: NavGroup[] = [
  {
    labelKey: 'nav.groups.security',
    items: [
      { path: '/audit', labelKey: 'nav.audit', permission: 'audit.log.read', icon: 'log' },
      {
        path: '/staff',
        labelKey: 'nav.staff',
        permission: 'staff.roles.read',
        desktopOnly: true,
        icon: 'key',
      },
      {
        path: '/approvals',
        labelKey: 'nav.approvals',
        permission: null,
        icon: 'shield',
        countLabelKey: 'nav.counts.approvals',
      },
      { path: '/me', labelKey: 'nav.me', permission: null, desktopOnly: true, icon: 'user' },
    ],
  },
];

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
        account={{ name: 'Ольга В.', details: 'модератор · Ставрополь' }}
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
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  renderWithProviders(<RouterProvider router={router} />, { me });
  return { onLogout, router };
}

// Узкий экран: matchMedia отвечает «да» на запрос ширины меньше breakpoint-mobile.
function phone() {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: query.includes('max-width'),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
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

  test('счётчик пункта: число и подпись для читалки; 0 не рисуется', async () => {
    mount('/audit', meOwner, { counts: { '/approvals': 3, '/audit': 0 } });
    await screen.findByRole('heading', { name: 'Страница журнала' });
    expect(screen.getByRole('link', { name: /Подтверждения/ })).toHaveAccessibleName(
      'Подтверждения 3 запроса ждут решения',
    );
    expect(screen.getByRole('link', { name: 'Журнал действий' })).toBeInTheDocument();
  });

  test('desktopOnly на компьютере открывается', async () => {
    mount('/me', meOwner);
    expect(await screen.findByRole('heading', { name: 'Страница доступа' })).toBeInTheDocument();
  });
});

describe('телефон (< breakpoint-mobile): таббар и «Ещё»', () => {
  test('таббар — разделы, открытые на телефоне, и «Ещё»; сайдбара нет', async () => {
    phone();
    mount('/audit', meOwner, { counts: { '/approvals': 2 } });
    await screen.findByRole('heading', { name: 'Страница журнала' });
    const tabbar = screen.getByRole('navigation', { name: 'Разделы' });
    const links = within(tabbar).getAllByRole('link');
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAccessibleName('Журнал действий');
    expect(links[1]).toHaveAccessibleName('Подтверждения 2 запроса ждут решения');
    expect(within(tabbar).getByRole('button', { name: 'Ещё' })).toBeInTheDocument();
    expect(screen.queryByText('Ольга В.')).not.toBeInTheDocument();
  });

  test('«Ещё»: все разрешённые разделы, карточка, язык и «Выйти»; desktopOnly → DesktopOnlyState', async () => {
    phone();
    const user = userEvent.setup();
    const { onLogout } = mount('/audit', meOwner);
    await screen.findByRole('heading', { name: 'Страница журнала' });
    await user.click(screen.getByRole('button', { name: 'Ещё' }));
    const sheet = screen.getByRole('dialog', { name: 'Разделы и доступ' });
    expect(within(sheet).getByText('Ольга В.')).toBeInTheDocument();
    expect(within(sheet).getByText('Переключатель языка')).toBeInTheDocument();
    expect(within(sheet).getByRole('link', { name: 'Мой доступ' })).toBeInTheDocument();
    await user.click(within(sheet).getByRole('link', { name: 'Роли и доступ' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Только с компьютера' }),
    ).toBeInTheDocument();
    expect(screen.queryByText('Страница ролей')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Ещё' }));
    await user.click(screen.getByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  test('прямой адрес desktopOnly на телефоне — DesktopOnlyState, «Ещё» отмечена текущей', async () => {
    phone();
    mount('/me', meOwner);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Только с компьютера' }),
    ).toBeInTheDocument();
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
