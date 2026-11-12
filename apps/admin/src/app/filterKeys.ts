import { KEYS as APPROVALS_KEYS } from '../features/approvals/search';
import { KEYS as AUDIT_KEYS } from '../features/audit/search';
import { KEYS as CITIES_KEYS } from '../features/geo/search';
import { KEYS as STAFF_KEYS } from '../features/staff/search';
import { paths } from './paths';

// Отдельно от paths.ts: paths.ts берут каркас, src/ui и живой e2e — им парсеры экранов не нужны (e2e их и не
// соберёт: tsconfig.e2e без jsx).
// Ключи фильтров каждого пути — из KEYS парсеров рядом с экраном (features/*/search.ts, n7). Новый путь с
// фильтрами — строка здесь (страж пересечения с панелями видит только перечисленное).
export const FILTER_SEARCH_KEYS: Readonly<Record<string, readonly string[]>> = {
  [paths.audit]: AUDIT_KEYS,
  [paths.staff]: STAFF_KEYS,
  [paths.approvals]: APPROVALS_KEYS,
  [paths.cities]: CITIES_KEYS,
};
