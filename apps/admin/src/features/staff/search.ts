import type { components } from '../../api/gen/admin';
import { parseKeys } from '../../app/search';
import { isKnownRole, type StaffRole } from './roles';

type StaffStatus = components['schemas']['StaffStatus'];

// Фильтры «Кто имеет доступ» — search-параметры маршрута /staff (спека §9.1): переживают
// перезагрузку и «Назад». Незнакомое значение из адреса отбрасывается — в запрос уходят только
// значения контракта.
// Ключи фильтров пути (FILTER_SEARCH_KEYS, страж searchKeys.test.ts): парсер разбирает только их.
export const KEYS = ['q', 'status', 'role'] as const;

export type StaffSearch = { q?: string; status?: StaffStatus; role?: StaffRole };

export const STATUSES: readonly StaffStatus[] = ['active', 'invited', 'disabled'];
const isStatus = (v: unknown): v is StaffStatus =>
  typeof v === 'string' && (STATUSES as readonly string[]).includes(v);

export function parseStaffSearch(s: Record<string, unknown>): StaffSearch {
  return parseKeys<(typeof KEYS)[number], StaffSearch>(
    KEYS,
    {
      // Роутер разбирает значения как JSON: «123» в поиске приходит числом.
      q: (v) =>
        typeof v === 'string' || typeof v === 'number' ? String(v) || undefined : undefined,
      status: (v) => (isStatus(v) ? v : undefined),
      role: (v) => (typeof v === 'string' && isKnownRole(v) ? v : undefined),
    },
    s,
  );
}
