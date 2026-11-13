import {
  createRootRouteWithContext,
  createRoute,
  lazyRouteComponent,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { parseApprovalsSearch } from '../features/approvals/search';
import { parseAuditSearch } from '../features/audit/search';
import { parseImportsSearch } from '../features/geo/importsSearch';
import { parseFlagsSearch } from '../features/flags/search';
import { parseCitiesSearch } from '../features/geo/search';
import { AcceptInvitePage } from '../features/auth/AcceptInvitePage';
import { LoginPage } from '../features/auth/LoginPage';
import { SetupMfaPage } from '../features/auth/SetupMfaPage';
import { parseStaffSearch } from '../features/staff/search';
import { ChunkErrorState, SectionSkeleton } from '../ui';
import { resolveRoute } from './guard';
import { panelSearch } from './panels';
import { paths } from './paths';
import type { Session } from './session';
import { Shell } from './Shell';
import { NotFoundStub } from './stubs';

// Роутер админки (спека §9.1, §9.3–9.4): TanStack Router, маршруты кодом, пути — paths.ts.
// Стадия сессии решает, куда пускать (guard.ts, beforeLoad корня); разделы — внутри AppShell: он
// строит меню по правам и сам показывает ForbiddenState на прямой адрес без права (D23); разделы
// открыты на любом устройстве — «только с компьютера» решают действия (ActionGate). Экраны входа — features/auth (Task 13), «Мой доступ» и «Роли
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

// Ленивые разделы (спека UI-фундамента §4.15, план UF2b.9): экран раздела — свой кусок (lazyRouteComponent,
// TanStack Router 1.170: lazyRouteComponent.tsx:16 — importer и имя экспорта; .preload вызывает загрузка маршрута,
// в том числе предзагрузка по намерению — defaultPreload ниже). Экраны входа и каркас — в основном куске.
// Пока кусок грузится дольше defaultPendingMs — скелетон раздела; кусок не загрузился (вышла новая сборка) — роутер
// один раз перезагружает страницу сам (lazyRouteComponent.tsx:57–67, метка в sessionStorage), повторная ошибка —
// ErrorState с «Обновить страницу». Каркас (меню) остаётся: можно уйти в другой раздел.
const section = {
  pendingComponent: SectionSkeleton,
  errorComponent: ChunkErrorState,
};

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'shell',
  component: Shell,
});

// Дерево маршрутов: роутер — router.tsx (AppRouter); здесь — отдельно, чтобы его видели стражи (searchKeys.test.ts:
// каждый маршрут с validateSearch — в FILTER_SEARCH_KEYS) без экспорта не-компонента из файла AppRouter (Fast Refresh).
export const routeTree = rootRoute.addChildren([
  loginRoute,
  acceptRoute,
  setupRoute,
  shellRoute.addChildren([
    // Главная «Что требует внимания» (спека UI-фундамента §4.13): стартовая после входа; без сессии guard уводит
    // корень ко входу без return.
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.root,
      ...section,
      component: lazyRouteComponent(() => import('../features/home/HomePage'), 'HomePage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.audit,
      // вкладка, фильтры, период и раскрытая запись журнала — search-параметры (перезагрузка, «Назад»)
      validateSearch: (s: Record<string, unknown>) => ({
        ...parseAuditSearch(s),
        ...panelSearch(['entry'])(s),
      }),
      ...section,
      component: lazyRouteComponent(() => import('../features/audit/AuditPage'), 'AuditPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.staff,
      // фильтры «Кто имеет доступ» и панели (карточка сотрудника, приглашение) — search-параметры
      // (перезагрузка, «Назад»; спека UI-фундамента §4.6)
      validateSearch: (s: Record<string, unknown>) => ({
        ...parseStaffSearch(s),
        ...panelSearch(['account', 'invite'])(s),
      }),
      ...section,
      component: lazyRouteComponent(() => import('../features/staff/StaffPage'), 'StaffPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.approvals,
      // вкладка «Входящие» / «Мои» — search-параметр box
      validateSearch: parseApprovalsSearch,
      ...section,
      component: lazyRouteComponent(
        () => import('../features/approvals/ApprovalsPage'),
        'ApprovalsPage',
      ),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.me,
      ...section,
      component: lazyRouteComponent(() => import('../features/me/MyAccessPage'), 'MyAccessPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.cities,
      // фильтры карточек и панель активации — search-параметры (перезагрузка, «Назад»)
      validateSearch: (s: Record<string, unknown>) => ({
        ...parseCitiesSearch(s),
        ...panelSearch(['activate'])(s),
      }),
      ...section,
      component: lazyRouteComponent(() => import('../features/geo/CitiesPage'), 'CitiesPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.city,
      ...section,
      component: lazyRouteComponent(() => import('../features/geo/CityPage'), 'CityPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.countries,
      // панель страны — ?edit=<код> | new (G9, Task 21; country — фильтр списков, I1)
      validateSearch: panelSearch(['edit']),
      ...section,
      component: lazyRouteComponent(() => import('../features/geo/CountriesPage'), 'CountriesPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.imports,
      // страна журнала и строка задачи (?job=) — фильтры; запуск (?start=1) и раскрытый отчёт (?import=) — панели
      validateSearch: (s: Record<string, unknown>) => ({
        ...parseImportsSearch(s),
        ...panelSearch(['start', 'import'])(s),
      }),
      ...section,
      component: lazyRouteComponent(() => import('../features/geo/ImportsPage'), 'ImportsPage'),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.versions,
      ...section,
      component: lazyRouteComponent(
        () => import('../features/versions/VersionsPage'),
        'VersionsPage',
      ),
    }),
    createRoute({
      getParentRoute: () => shellRoute,
      path: paths.flags,
      // поиск и статус города, раскрытый флаг телефона — search-параметры (перезагрузка, «Назад»)
      validateSearch: parseFlagsSearch,
      ...section,
      component: lazyRouteComponent(() => import('../features/flags/FlagsPage'), 'FlagsPage'),
    }),
  ]),
]);
