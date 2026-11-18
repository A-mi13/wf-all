import type { components } from '../../api/gen/admin';
import { parseKeys } from '../../app/search';

type Status = components['schemas']['IdentityCaptainApplicationStatus'];

// Фильтры «Капитанов» — search-параметры маршрута /captains (план I-4, Task 8): статус заявок и город —
// переживают перезагрузку и «Назад». Без статуса — ожидающие (pending — значение по умолчанию, в адрес не
// пишется); незнакомое значение из адреса отбрасывается — в запрос уходят только значения контракта.
// Ключи фильтров пути (FILTER_SEARCH_KEYS, страж searchKeys.test.ts): парсер разбирает только их.
export const KEYS = ['status', 'city'] as const;

export const STATUSES: readonly Status[] = ['pending', 'approved', 'rejected', 'withdrawn'];
export type DecidedStatus = Exclude<Status, 'pending'>;
export type CaptainsSearch = { status?: DecidedStatus; city?: string };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const isDecided = (v: unknown): v is DecidedStatus =>
  typeof v === 'string' && v !== 'pending' && (STATUSES as readonly string[]).includes(v);

export function parseCaptainsSearch(s: Record<string, unknown>): CaptainsSearch {
  return parseKeys<(typeof KEYS)[number], CaptainsSearch>(
    KEYS,
    {
      status: (v) => (isDecided(v) ? v : undefined),
      city: (v) => (typeof v === 'string' && UUID.test(v) ? v : undefined),
    },
    s,
  );
}
