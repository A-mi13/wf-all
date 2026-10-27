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
import { ApprovalsPage } from '../features/approvals/ApprovalsPage';
import { useIncomingApprovalsCount } from '../features/approvals/queries';
import { parseApprovalsSearch } from '../features/approvals/search';
import { AuditPage } from '../features/audit/AuditPage';
import { parseAuditSearch } from '../features/audit/search';
import { AcceptInvitePage } from '../features/auth/AcceptInvitePage';
import { LoginPage } from '../features/auth/LoginPage';
import { ReauthModal } from '../features/auth/ReauthModal';
import { SetupMfaPage } from '../features/auth/SetupMfaPage';
import { LocaleSwitcher } from '../features/me/LocaleSwitcher';
import { MyAccessPage } from '../features/me/MyAccessPage';
import { parseStaffSearch } from '../features/staff/search';
import { StaffPage } from '../features/staff/StaffPage';
import { isApiError } from '../api/client';
import { AppShell, ConnectionErrorState, RateLimitedState } from '../ui';
import { useAccountCard } from './account';
import { resolveRoute } from './guard';
import { useLogout } from './logout';
import { NAV } from './nav';
import type { Me } from './permissions';
import { paths } from './paths';
import { ME_KEY } from './query';
import { type Session, useSession } from './session';
import { LoadingStub, NotFoundStub } from './stubs';

// Роутер админки (спека §9.1, §9.3–9.4): TanStack Router, маршруты кодом, пути — paths.ts.
// Стадия сессии решает, куда пускать (guard.ts, beforeLoad корня); разделы — внутри AppShell: он
// строит меню по правам и сам показывает ForbiddenState на прямой адрес без права (D23) и
// DesktopOnlyState на телефоне (D22). Экраны входа — features/auth (Task 13), «Мой доступ» и «Роли
// и доступ» — features/me и features/staff (Task 14), «Подтверждения» и «Журнал действий» —
// features/approvals и features/audit (Task 15). Окно разблокировки (ReauthModal) стоит поверх
// любой страницы.

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

// Каркас активной стадии: AppShell с меню из NAV, карточкой сотрудника, языком и выходом. key по
// me.id: сменился сотрудник без выхода (другая вкладка, ревью ветки I1) — экраны прежнего
// размонтируются вместе с состоянием (открытые окна, коды, введённое), кэш стирает SessionProvider.
function Shell() {
  const s = useSession();
  if (s.status !== 'active') return null;
  return <ActiveShell key={s.me.id} me={s.me} />;
}

function ActiveShell({ me }: { me: Me }) {
  const account = useAccountCard(me);
  // Выход — вход без return (logout.ts)
  const logout = useLogout();
  // число у «Подтверждений» — фоновый опрос (features/approvals/queries.ts)
  const incoming = useIncomingApprovalsCount();
  return (
    <AppShell
      groups={NAV}
      account={account}
      counts={{ [paths.approvals]: incoming }}
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

const routeTree = rootRoute.addChildren([
  loginRoute,
  acceptRoute,
  setupRoute,
  indexRoute,
  shellRoute.addChildren([
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.audit,
      // вкладка, фильтры и период журнала — search-параметры (перезагрузка, «Назад»)
      validateSearch: parseAuditSearch,
      component: AuditPage,
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.staff,
      // фильтры «Кто имеет доступ» — search-параметры (перезагрузка, «Назад»)
      validateSearch: parseStaffSearch,
      component: StaffPage,
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.approvals,
      // вкладка «Входящие» / «Мои» — search-параметр box
      validateSearch: parseApprovalsSearch,
      component: ApprovalsPage,
    }),
    createRoute({ getParentRoute: () => shellRoute, path: paths.me, component: MyAccessPage }),
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
// повтором, отказал (ревью ветки M1) — 429 с отсчётом (лимит staff_auth по общему NAT) или текст по
// коду (403 auth.ip_not_allowed). Смена сессии (вход, выход, настройка 2FA) перезапускает beforeLoad.
export function AppRouter({ history }: { history?: RouterHistory }) {
  const session = useSession();
  const queryClient = useQueryClient();
  const [router] = useState(() => createAppRouter(history));
  useEffect(() => {
    void router.invalidate();
  }, [router, session]);

  if (session.status === 'loading') return <LoadingStub />;
  if (session.status === 'error') {
    const retry = () => void session.refresh();
    return (
      <main>
        {isApiError(session.error) && session.error.status === 429 ? (
          // повторный 429 с тем же сроком — отсчёт заново (новый ответ — новый key)
          <RateLimitedState
            key={queryClient.getQueryState(ME_KEY)?.errorUpdatedAt}
            page
            retryAfter={session.error.retryAfter}
            onRetry={retry}
          />
        ) : (
          <ConnectionErrorState error={session.error} onRetry={retry} />
        )}
      </main>
    );
  }
  return (
    <>
      <RouterProvider router={router} context={{ session }} />
      {/* 401 auth.reauth_required любого запроса — окно поверх текущей страницы (§9.3) */}
      <ReauthModal />
    </>
  );
}
