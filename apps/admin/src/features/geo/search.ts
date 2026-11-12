import type { components } from '../../api/gen/admin';
import { parseKeys } from '../../app/search';
import type { CitiesQuery } from './queries';

type CityStatus = components['schemas']['CityStatus'];

// Фильтры «Городов и запуска» — search-параметры маршрута /geo/cities (спека §9.1): переживают
// перезагрузку и «Назад». Незнакомое и негодное из адреса отбрасывается — в запрос уходят только
// значения контракта (status — из CityStatus, country — ISO 3166-1 alpha-2, region_id — uuid, q —
// 1–100 символов). status — массив (контракт: повторённый параметр); роутер держит его JSON-ом.
// Ключи фильтров пути (FILTER_SEARCH_KEYS, страж searchKeys.test.ts): парсер разбирает только их, по порядку
// (регион — после страны).
export const KEYS = ['q', 'status', 'country', 'region_id'] as const;

export type CitiesSearch = {
  q?: string;
  status?: CityStatus[];
  country?: string;
  region_id?: string;
};

export const CITY_STATUSES: readonly CityStatus[] = ['live', 'pilot', 'waitlist'];

export const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const COUNTRY = /^[A-Z]{2}$/;
const Q_MAX = 100;

const isStatus = (v: unknown): v is CityStatus =>
  typeof v === 'string' && (CITY_STATUSES as readonly string[]).includes(v);

export function parseCitiesSearch(s: Record<string, unknown>): CitiesSearch {
  return parseKeys<(typeof KEYS)[number], CitiesSearch>(
    KEYS,
    {
      // Роутер разбирает значения как JSON: «123» в поиске приходит числом.
      q: (v) => {
        const q = typeof v === 'string' || typeof v === 'number' ? String(v).slice(0, Q_MAX) : '';
        return q.trim() ? q : undefined;
      },
      status: (v) => {
        const raw = Array.isArray(v) ? v : [v];
        const status = [...new Set(raw.filter(isStatus))];
        return status.length > 0 ? status : undefined;
      },
      country: (v) => (typeof v === 'string' && COUNTRY.test(v) ? v : undefined),
      // регион — только при стране: без неё поле «Регион» не показано, и сбросить фильтр было бы нечем (G8 m10)
      region_id: (v, out) => (out.country && typeof v === 'string' && UUID.test(v) ? v : undefined),
    },
    s,
  );
}

// Параметры listGeoCities: поиск без пробелов по краям (пустой сервер отверг бы — minLength).
export function toCitiesQuery(s: CitiesSearch): CitiesQuery {
  const q: CitiesQuery = {};
  const text = s.q?.trim();
  if (text) q.q = text;
  if (s.status && s.status.length > 0) q.status = s.status;
  if (s.country) q.country = s.country;
  if (s.region_id) q.region_id = s.region_id;
  return q;
}
