import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  RouterProvider,
  type RouterHistory,
} from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { AcceptInvitePage } from '../features/auth/AcceptInvitePage';
import { LoginPage } from '../features/auth/LoginPage';
import { ReauthModal } from '../features/auth/ReauthModal';
import { SetupMfaPage } from '../features/auth/SetupMfaPage';
import { LocaleSwitcher } from '../features/me/LocaleSwitcher';
import { AppShell, ConnectionErrorState } from '../ui';
import { useAccountCard } from './account';
import { resolveRoute } from './guard';
import { useLogout } from './logout';
import { NAV } from './nav';
import type { Me } from './permissions';
import { paths } from './paths';
import { type Session, useSession } from './session';
import { LoadingStub, NotFoundStub, SectionStub } from './stubs';

// Роутер админки (спека §9.1, §9.3–9.4): TanStack Router, маршруты кодом, пути — paths.ts.
// Стадия сессии решает, куда пускать (guard.ts, beforeLoad корня); разделы — внутри AppShell: он
// строит меню по правам и сам показывает ForbiddenState на прямой адрес без права (D23) и
// DesktopOnlyState на телефоне (D22). Экраны входа — features/auth (Task 13), разделы пока —
// заглушки (Task 14–15). Окно разблокировки (ReauthModal) стоит поверх любой страницы.

export interface RouterContext {
  session: Session;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  beforeLoad: ({ context, location }) => {
    const to = resolveRoute(context.session, location);
    if (to) throw redirect(to);
  },
  component: Outlet,
  notFoundComponent: NotFoundStub,
});

const searchReturn = (s: Record<string, unknown>): { return?: string } =>
  typeof s.return === 'string' ? { return: s.return } : {};

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.login,
  validateSearch: searchReturn,
  component: LoginPage,
});

const acceptRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.accept,
  component: AcceptInvitePage,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.setup2fa,
  component: SetupMfaPage,
});

// Корень всегда уводит (guard): на стартовую или ко входу.
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.root,
  component: () => null,
});

// Каркас активной стадии: AppShell с меню из NAV, карточкой сотрудника, языком и выходом.
function Shell() {
  const s = useSession();
  if (s.status !== 'active') return null;
  return <ActiveShell me={s.me} />;
}

function ActiveShell({ me }: { me: Me }) {
  const account = useAccountCard(me);
  // Выход — вход без return (logout.ts)
  const logout = useLogout();
  return (
    <AppShell
      groups={NAV}
      account={account}
      onLogout={() => logout.mutate()}
      loggingOut={logout.isPending}
      profileMenu={<LocaleSwitcher />}
    >
      <Outlet />
    </AppShell>
  );
}

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'shell',
  component: Shell,
});

const section = (path: string, titleKey: string) =>
  createRoute({
    getParentRoute: () => shellRoute,
    path,
    component: () => <SectionStub titleKey={titleKey} />,
  });

const routeTree = rootRoute.addChildren([
  loginRoute,
  acceptRoute,
  setupRoute,
  indexRoute,
  shellRoute.addChildren([
    section(paths.audit, 'nav.audit'),
    section(paths.staff, 'nav.staff'),
    section(paths.approvals, 'nav.approvals'),
    section(paths.me, 'nav.me'),
  ]),
]);

function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    // настоящая сессия приходит из RouterProvider (AppRouter)
    context: {
      session: { status: 'loading', refresh: async () => {}, afterLogout: async () => {} },
    },
  });
}

// Роутер поверх сессии: пока me не ответил — загрузка; admin-api недоступен — «нет связи» с
// повтором. Смена сессии (вход, выход, настройка 2FA) перезапускает beforeLoad.
export function AppRouter({ history }: { history?: RouterHistory }) {
  const session = useSession();
  const [router] = useState(() => createAppRouter(history));
  useEffect(() => {
    void router.invalidate();
  }, [router, session]);

  if (session.status === 'loading') return <LoadingStub />;
  if (session.status === 'error')
    return (
      <main>
        <ConnectionErrorState onRetry={() => void session.refresh()} />
      </main>
    );
  return (
    <>
      <RouterProvider router={router} context={{ session }} />
      {/* 401 auth.reauth_required любого запроса — окно поверх текущей страницы (§9.3) */}
      <ReauthModal />
    </>
  );
}
