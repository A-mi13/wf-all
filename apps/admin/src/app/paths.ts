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

// Ключи search открытых панелей (спека UI-фундамента §4.6, план UF2b.8): карточка сотрудника, приглашение,
// активация города, запись журнала, страна (G9 — ?edit=<код>, не ?country=: country — фильтр списков, I1). Ключ
// панели не совпадает ни с одним ключом фильтра ни на одном пути и ни с одним query-параметром контракта (страж
// searchKeys.test.ts; ключи фильтров путей — filterKeys.ts): память «Назад» (BackMemory) и ключ позиции таблицы
// (restore.ts) их вырезают.
export const PANEL_SEARCH_KEYS = ['account', 'invite', 'activate', 'entry', 'edit'] as const;
export type PanelKey = (typeof PANEL_SEARCH_KEYS)[number];

export function isPanelKey(key: string): key is PanelKey {
  return (PANEL_SEARCH_KEYS as readonly string[]).includes(key);
}
