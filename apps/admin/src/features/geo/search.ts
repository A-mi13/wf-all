import type { components } from '../../api/gen/admin';
import type { CitiesQuery } from './queries';

type CityStatus = components['schemas']['CityStatus'];

// Фильтры «Городов и запуска» — search-параметры маршрута /geo/cities (спека §9.1): переживают
// перезагрузку и «Назад». Незнакомое и негодное из адреса отбрасывается — в запрос уходят только
// значения контракта (status — из CityStatus, country — ISO 3166-1 alpha-2, region_id — uuid, q —
// 1–100 символов). status — массив (контракт: повторённый параметр); роутер держит его JSON-ом.
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
  const out: CitiesSearch = {};
  // Роутер разбирает значения как JSON: «123» в поиске приходит числом.
  const q = typeof s.q === 'string' || typeof s.q === 'number' ? String(s.q).slice(0, Q_MAX) : '';
  if (q.trim()) out.q = q;
  const raw = Array.isArray(s.status) ? s.status : [s.status];
  const status = [...new Set(raw.filter(isStatus))];
  if (status.length > 0) out.status = status;
  if (typeof s.country === 'string' && COUNTRY.test(s.country)) out.country = s.country;
  // регион — только при стране: без неё поле «Регион» не показано, и сбросить фильтр было бы нечем (G8 m10)
  if (out.country && typeof s.region_id === 'string' && UUID.test(s.region_id))
    out.region_id = s.region_id;
  return out;
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
