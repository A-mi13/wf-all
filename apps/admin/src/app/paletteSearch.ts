import { useQuery } from '@tanstack/react-query';
import { unwrap } from '../api/client';
import { useApi } from '../api/context';
import { useGeoCountries } from '../features/geo/queries';
import { useLocale } from '../i18n/locale';
import { fuzzyRank } from '../ui/palette/fuzzy';
import { paths } from './paths';
import { usePermissionCheck } from './session';

// Сущности палитры (спека UI-фундамента §4.1, план UF4.5): города (listGeoCities?q=&limit=5, geo.cities.read),
// сотрудники (listStaffAccounts?q=&limit=5, staff.accounts.read), страны — локально из кэша listGeoCountries
// (geo.countries.read). term — уже после паузы ввода (SEARCH_DEBOUNCE_MS) и без пробелов по краям; короче 2 символов
// и без права запрос не уходит вовсе. Ключ запроса — с term: показывается только ответ последнего запроса, прежний
// отменяется (signal TanStack Query → fetch), когда у него не осталось наблюдателя. Каждая группа — свои «ищем…» и
// ошибка, палитру они не роняют. У сотрудника — только display_name: почта в палитру не идёт (и в недавнее).

export const ENTITY_MIN_CHARS = 2;
export const ENTITY_LIMIT = 5;
export const PALETTE_KEY = ['palette'] as const;

export type EntityKind = 'city' | 'staff' | 'country';
export type EntityHit = {
  kind: EntityKind;
  id: string;
  label: string;
  detail?: string;
  path: string; // адрес для недавнего
  to: { to: string; search?: Record<string, unknown> };
};
export type EntityGroup = {
  kind: EntityKind;
  status: 'loading' | 'error' | 'ready';
  error: unknown;
  items: EntityHit[];
};

type Q = { isPending: boolean; isError: boolean; error: unknown };
const statusOf = (q: Q): EntityGroup['status'] =>
  q.isError ? 'error' : q.isPending ? 'loading' : 'ready';

export function useEntitySearch(term: string): EntityGroup[] {
  const api = useApi();
  const locale = useLocale();
  const can = usePermissionCheck();
  const long = term.length >= ENTITY_MIN_CHARS;
  const canCities = can('geo.cities.read');
  const canStaff = can('staff.accounts.read');
  const canCountries = can('geo.countries.read');

  const cities = useQuery({
    queryKey: [...PALETTE_KEY, 'cities', locale, term],
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: {
            query: { q: term, limit: ENTITY_LIMIT },
            header: { 'Accept-Language': locale },
          },
          signal,
        }),
      ),
    enabled: canCities && long,
  });
  const staff = useQuery({
    queryKey: [...PALETTE_KEY, 'staff', term],
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/v1/staff/accounts', {
          params: { query: { q: term, limit: ENTITY_LIMIT } },
          signal,
        }),
      ),
    enabled: canStaff && long,
  });
  const countries = useGeoCountries(canCountries && long);

  const groups: EntityGroup[] = [];
  if (!long) return groups;
  if (canCities) {
    groups.push({
      kind: 'city',
      status: statusOf(cities),
      error: cities.error,
      items: (cities.data?.items ?? []).map((c) => ({
        kind: 'city',
        id: c.id,
        label: c.name,
        detail: c.region_name ?? c.country_code,
        path: paths.city.replace('$cityId', c.id),
        to: { to: paths.city.replace('$cityId', c.id) },
      })),
    });
  }
  if (canStaff) {
    groups.push({
      kind: 'staff',
      status: statusOf(staff),
      error: staff.error,
      items: (staff.data?.items ?? []).map((a) => ({
        kind: 'staff',
        id: a.id,
        label: a.display_name,
        path: `${paths.staff}?account=${a.id}`,
        to: { to: paths.staff, search: { account: a.id } },
      })),
    });
  }
  if (canCountries) {
    const all = countries.data?.items ?? [];
    groups.push({
      kind: 'country',
      status: statusOf(countries),
      error: countries.error,
      items: fuzzyRank(term, all, { fields: (c) => [c.name, c.code] })
        .slice(0, ENTITY_LIMIT)
        .map(({ item: c }) => ({
          kind: 'country',
          id: c.code,
          label: c.name,
          detail: c.code,
          path: `${paths.countries}?edit=${c.code}`,
          to: { to: paths.countries, search: { edit: c.code } },
        })),
    });
  }
  return groups;
}
