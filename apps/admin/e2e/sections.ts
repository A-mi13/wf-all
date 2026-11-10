// Разделы меню (src/app/nav.ts), которые прод-проект живого e2e обходит под сотрудником-владельцем в поисках
// нарушений CSP (план UF1b.7, сценарий 2). Список свой, а не импорт NAV: e2e не тянет код приложения
// (иконки, права, сборку Vite); сверка с NAV — e2e/env.test.ts (новый раздел без строки здесь — падение).
export const PROD_SECTIONS = [
  '/audit',
  '/staff',
  '/approvals',
  '/me',
  '/geo/cities',
  '/geo/countries',
  '/geo/imports',
  '/app/versions',
  '/flags',
] as const;
