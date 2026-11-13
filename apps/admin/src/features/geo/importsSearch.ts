import { parseKeys } from '../../app/search';
import type { ImportsQuery } from './queries';

// Фильтры «Импорта GeoNames» — search-параметры маршрута /geo/imports (план B, Task 20; спека UI-фундамента §6.2):
// country — страна журнала (ссылки главной ведут сюда с ней, U18), job — строка задачи очереди над журналом
// (?job=<id>: F5 и пересланная ссылка возобновляют её опрос с начала окна, принцип 10). Ключи пути — в
// FILTER_SEARCH_KEYS (src/app/filterKeys.ts, страж пересечения с панелями searchKeys.test.ts). Негодное
// отбрасывается: в запрос уходят только значения контракта.
export const KEYS = ['country', 'job'] as const;

export type ImportsSearch = { country?: string; job?: number };

const COUNTRY = /^[A-Z]{2}$/;
const DIGITS = /^\d{1,16}$/;

// id задачи очереди (int64 сервера): положительное безопасное целое; роутер разбирает «1842» числом, строку из
// цифр (ссылка из другого места) — тоже принимаем.
function jobOf(v: unknown): number | undefined {
  const n = typeof v === 'number' ? v : typeof v === 'string' && DIGITS.test(v) ? Number(v) : NaN;
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
}

export function parseImportsSearch(s: Record<string, unknown>): ImportsSearch {
  return parseKeys<(typeof KEYS)[number], ImportsSearch>(
    KEYS,
    {
      country: (v) => (typeof v === 'string' && COUNTRY.test(v) ? v : undefined),
      job: jobOf,
    },
    s,
  );
}

// Параметры listGeoImports: задача — строка над журналом, список ею не фильтруется.
export function toImportsQuery(s: ImportsSearch): ImportsQuery {
  return s.country ? { country: s.country } : {};
}
