import type { components } from '../../api/gen/admin';
import { parseKeys } from '../../app/search';
import type { CitiesQuery } from '../geo/queries';
import { CITY_STATUSES } from '../geo/search';

type CityStatus = components['schemas']['CityStatus'];

// Фильтры «Флагов функций» — search-параметры /flags (принцип 10): поиск и статус города сужают столбцы матрицы
// (на телефоне — города раскрытого флага), flag — раскрытый флаг на телефоне. Ключи пути — FILTER_SEARCH_KEYS
// (src/app/filterKeys.ts). Незнакомое и негодное отбрасывается: статус — один из CityStatus, поиск — 1–100
// символов, ключ флага — шаблон пути updateFeatureFlag.
export const KEYS = ['q', 'status', 'flag'] as const;

export type FlagsSearch = { q?: string; status?: CityStatus; flag?: string };

const Q_MAX = 100;
const FLAG_KEY = /^[a-z][a-z0-9_]{0,63}$/;

const isStatus = (v: unknown): v is CityStatus =>
  typeof v === 'string' && (CITY_STATUSES as readonly string[]).includes(v);

export function parseFlagsSearch(s: Record<string, unknown>): FlagsSearch {
  return parseKeys<(typeof KEYS)[number], FlagsSearch>(
    KEYS,
    {
      // роутер разбирает значения как JSON: «123» в поиске приходит числом
      q: (v) => {
        const q = typeof v === 'string' || typeof v === 'number' ? String(v).slice(0, Q_MAX) : '';
        return q.trim() ? q : undefined;
      },
      status: (v) => (isStatus(v) ? v : undefined),
      flag: (v) => (typeof v === 'string' && FLAG_KEY.test(v) ? v : undefined),
    },
    s,
  );
}

// Параметры listGeoCities: поиск без пробелов по краям (пустой сервер отверг бы), статус — массивом контракта.
export function toCitiesQuery(s: FlagsSearch): CitiesQuery {
  const q: CitiesQuery = {};
  const text = s.q?.trim();
  if (text) q.q = text;
  if (s.status) q.status = [s.status];
  return q;
}
