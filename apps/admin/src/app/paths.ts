// Пути админки — только отсюда: маршруты, ссылки меню, редиректы (Поправки плана A, «Фронт»).
export const paths = {
  root: '/',
  login: '/login',
  accept: '/accept',
  setup2fa: '/setup-2fa',
  me: '/me',
  staff: '/staff',
  approvals: '/approvals',
  audit: '/audit',
  // «Справочники» (план B): города и страница города, страны, импорт GeoNames, версии, флаги
  cities: '/geo/cities',
  city: '/geo/cities/$cityId',
  countries: '/geo/countries',
  imports: '/geo/imports',
  versions: '/app/versions',
  flags: '/flags',
} as const;

export type AppPath = (typeof paths)[keyof typeof paths];

// Экраны входа: без активной сессии; активному сотруднику — на стартовую или ?return=.
export const AUTH_PATHS: readonly string[] = [paths.login, paths.accept, paths.setup2fa];
