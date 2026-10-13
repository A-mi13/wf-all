import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Link,
  Outlet,
  redirect,
  RouterProvider,
  type RouterHistory,
} from '@tanstack/react-router';
import { type ReactNode, useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { LocaleSwitcher } from '../features/me/LocaleSwitcher';
import { resolveRoute } from './guard';
import { canOpen, navFor, navItem } from './nav';
import { paths } from './paths';
import { type Session, useSession } from './session';
import { ConnectionErrorStub, ForbiddenStub, LoadingStub, NotFoundStub, PageStub } from './stubs';

// Роутер админки (спека §9.1, §9.3–9.4): TanStack Router, маршруты кодом, пути — paths.ts.
// Стадия сессии решает, куда пускать (guard.ts, beforeLoad корня); право раздела — сам маршрут:
// без права — ForbiddenState (D23). Экраны пока — заглушки (Task 13–15).

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

// Каркас разделов до AppShell (Task 12b): меню по правам и переключатель языка.
function Shell() {
  const t = useTranslations();
  const s = useSession();
  if (s.status !== 'active') return null;
  return (
    <>
      <nav>
        {navFor(s.me).map((g) => (
          <section key={g.labelKey} aria-label={t(g.labelKey)}>
            {g.items.map((i) => (
              <Link key={i.path} to={i.path}>
                {t(i.labelKey)}
              </Link>
            ))}
          </section>
        ))}
      </nav>
      <LocaleSwitcher />
      <Outlet />
    </>
  );
}

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'shell',
  component: Shell,
});

// Раздел меню: без права на прямой адрес — ForbiddenState (в меню такого раздела нет).
function Section({ path, children }: { path: string; children: ReactNode }) {
  const s = useSession();
  const item = navItem(path);
  if (s.status !== 'active' || (item && !canOpen(s.me, item))) return <ForbiddenStub />;
  return children;
}

const section = (path: string, titleKey: string) =>
  createRoute({
    getParentRoute: () => shellRoute,
    path,
    component: () => (
      <Section path={path}>
        <PageStub titleKey={titleKey} />
      </Section>
    ),
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
    context: { session: { status: 'loading', refresh: async () => {} } },
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
    return <ConnectionErrorStub onRetry={() => void session.refresh()} />;
  return <RouterProvider router={router} context={{ session }} />;
}
