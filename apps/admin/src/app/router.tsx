import { useQueryClient } from '@tanstack/react-query';
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
import { useApi } from '../api/context';
import { useAdminMutation } from '../api/mutation';
import { LocaleSwitcher } from '../features/me/LocaleSwitcher';
import { AppShell, ConnectionErrorState, useErrorText, useToast } from '../ui';
import { useAccountCard } from './account';
import { resolveRoute } from './guard';
import { NAV } from './nav';
import type { Me } from './permissions';
import { paths } from './paths';
import { isMeKey } from './query';
import { type Session, useSession } from './session';
import { LoadingStub, NotFoundStub, PageStub, SectionStub } from './stubs';

// Роутер админки (спека §9.1, §9.3–9.4): TanStack Router, маршруты кодом, пути — paths.ts.
// Стадия сессии решает, куда пускать (guard.ts, beforeLoad корня); разделы — внутри AppShell: он
// строит меню по правам и сам показывает ForbiddenState на прямой адрес без права (D23) и
// DesktopOnlyState на телефоне (D22). Экраны пока — заглушки (Task 13–15).

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
  component: () => <PageStub titleKey="pages.login.title" />,
});

const acceptRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.accept,
  component: () => <PageStub titleKey="pages.accept.title" />,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: paths.setup2fa,
  component: () => <PageStub titleKey="pages.setup2fa.title" />,
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
  return <ActiveShell me={s.me} afterLogout={s.afterLogout} />;
}

function ActiveShell({ me, afterLogout }: { me: Me; afterLogout: () => Promise<void> }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const toast = useToast();
  const errorText = useErrorText();
  const account = useAccountCard(me);
  // Выход: сессия гасится на сервере, данные прежнего сотрудника — из кэша, me перечитывается
  // (401 → anonymous → /login без return). 401 auth.unauthenticated — сессии уже нет: итог тот
  // же, что у выхода (вход без return), тост не нужен.
  const signedOut = async () => {
    queryClient.removeQueries({ predicate: (q) => !isMeKey(q.queryKey) });
    await afterLogout();
  };
  const logout = useAdminMutation(
    (_: void, key: string) =>
      api.POST('/v1/staff/auth/logout', { params: { header: { 'Idempotency-Key': key } } }),
    {
      onSuccess: signedOut,
      onError: (e) => {
        if (e.code === 'auth.unauthenticated') return signedOut();
        toast.error(errorText(e));
      },
    },
  );
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
  return <RouterProvider router={router} context={{ session }} />;
}
