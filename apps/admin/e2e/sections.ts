import type { Key } from './keys';

// Разделы меню (src/app/nav.ts), которые прод-проект живого e2e обходит под сотрудником-владельцем в поисках
// нарушений CSP (план UF1b.7, сценарий 2) и отказов API (I-4, итоговое ревью I1). Список свой, а не импорт NAV: e2e
// не тянет код приложения (иконки, права, сборку Vite); сверка с NAV — e2e/env.test.ts (новый раздел без строки здесь
// — падение). titleKey — заголовок h1 страницы раздела (PageHeader): у состояний вместо страницы («Слишком много
// запросов», «Нет доступа к разделу») тоже есть h1, поэтому обход сверяет имя, а не наличие заголовка.
export const PROD_SECTIONS: readonly { path: string; titleKey: Key }[] = [
  { path: '/', titleKey: 'nav.home' },
  { path: '/captains', titleKey: 'captains.title' },
  { path: '/players', titleKey: 'players.title' },
  { path: '/audit', titleKey: 'nav.audit' },
  { path: '/staff', titleKey: 'nav.staff' },
  { path: '/approvals', titleKey: 'nav.approvals' },
  { path: '/me', titleKey: 'nav.me' },
  { path: '/geo/cities', titleKey: 'nav.cities' },
  { path: '/geo/countries', titleKey: 'nav.countries' },
  { path: '/geo/imports', titleKey: 'nav.imports' },
  { path: '/app/versions', titleKey: 'nav.versions' },
  { path: '/flags', titleKey: 'nav.flags' },
];

// Заголовки страниц разделов, которые не совпадают с подписью пункта меню (у остальных h1 = labelKey пункта NAV).
export const TITLE_NOT_NAV_LABEL: Readonly<Record<string, Key>> = {
  '/captains': 'captains.title',
  '/players': 'players.title',
};
