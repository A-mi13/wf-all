import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { operations } from '../../api/gen/admin';
import { useLocale } from '../../i18n/locale';

// Чтения «Справочников» (план B, Task 17): города, карточка города, районы, страны, регионы, места
// GeoNames, журнал импорта и задача очереди — под ['geo', …]; версии приложения и флаги — модуль
// platform, под ['platform', …]. Названия в ответах — на языке интерфейса (Accept-Language): язык —
// часть ключа, смена языка не показывает прежние подписи. Мутация города — обновляет карточку из
// ответа и инвалидирует CITIES_KEY (списки, подписи городов модератора, поиск — staff/cities.ts).

export type CitiesQuery = Omit<
  NonNullable<operations['listGeoCities']['parameters']['query']>,
  'cursor' | 'limit' | 'ids'
>;
export type PlacesQuery = Omit<
  NonNullable<operations['listGeoPlaces']['parameters']['query']>,
  'cursor' | 'limit'
>;
export type ImportsQuery = Omit<
  NonNullable<operations['listGeoImports']['parameters']['query']>,
  'cursor' | 'limit'
>;

export const GEO_KEY = ['geo'] as const;
export const CITIES_KEY = ['geo', 'cities'] as const;
export const COUNTRIES_KEY = ['geo', 'countries'] as const;
export const IMPORTS_KEY = ['geo', 'imports'] as const;
export const GEO_PLACES_KEY = ['geo', 'places'] as const;
export const VERSIONS_KEY = ['platform', 'versions'] as const;
export const FLAGS_KEY = ['platform', 'flags'] as const;

export const geoKeys = {
  cities: (locale: string, q: CitiesQuery) => [...CITIES_KEY, 'list', locale, q] as const,
  city: (cityId: string, locale: string) => [...CITIES_KEY, 'card', cityId, locale] as const,
  districts: (cityId: string) => [...CITIES_KEY, 'districts', cityId] as const,
  countries: (locale: string) => [...COUNTRIES_KEY, 'list', locale] as const,
  regions: (code: string, locale: string) => [...COUNTRIES_KEY, 'regions', code, locale] as const,
  places: (locale: string, q: PlacesQuery) => [...GEO_PLACES_KEY, locale, q] as const,
  imports: (q: ImportsQuery) => [...IMPORTS_KEY, 'list', q] as const,
  importReport: (id: string, locale: string) => [...IMPORTS_KEY, 'report', id, locale] as const,
  importJob: (jobId: number) => [...IMPORTS_KEY, 'job', jobId] as const,
};

const nextCursor = (p: { next_cursor: string | null }) => p.next_cursor ?? undefined;

// Города (listGeoCities) — курсор «Показать ещё». Модератору сервер отдаёт только его города.
export function useGeoCities(q: CitiesQuery) {
  const api = useApi();
  const locale = useLocale();
  return useInfiniteQuery({
    queryKey: geoKeys.cities(locale, q),
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: { query: { ...q, cursor: pageParam }, header: { 'Accept-Language': locale } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
  });
}

// Карточка города (getGeoCity): названия по всем языкам, история адресов, страна, регион. Язык — в
// ключе: пока карточка перечитывается на новом языке, видна прежняя ТОГО ЖЕ города (placeholderData —
// функция со сверкой cityId прежнего ключа): формы не размонтируются и ввод не стирается (ревью G8, m18);
// другой город — без подмены, скелетон. enabled — экран не спрашивает заведомо негодный id.
export function useGeoCity(cityId: string, enabled = true) {
  const api = useApi();
  const locale = useLocale();
  return useQuery({
    queryKey: geoKeys.city(cityId, locale),
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[3] === cityId ? prev : undefined),
    enabled,
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/cities/{cityId}', {
          params: { path: { cityId }, header: { 'Accept-Language': locale } },
        }),
      ),
  });
}

// Районы города — действующие, затем архивные (без пагинации).
export function useGeoDistricts(cityId: string) {
  const api = useApi();
  return useQuery({
    queryKey: geoKeys.districts(cityId),
    queryFn: () =>
      unwrap(api.GET('/v1/geo/cities/{cityId}/districts', { params: { path: { cityId } } })),
  });
}

// Все страны справочника (включённые и выключенные). enabled — палитра (src/app/paletteSearch.ts) спрашивает их
// только с правом и от 2 символов запроса; кэш общий с экранами.
export function useGeoCountries(enabled = true) {
  const api = useApi();
  const locale = useLocale();
  return useQuery({
    queryKey: geoKeys.countries(locale),
    queryFn: () =>
      unwrap(api.GET('/v1/geo/countries', { params: { header: { 'Accept-Language': locale } } })),
    staleTime: 60_000,
    enabled,
  });
}

// Регионы страны; без кода страны — не запрашиваются.
export function useGeoRegions(code: string | undefined) {
  const api = useApi();
  const locale = useLocale();
  return useQuery({
    queryKey: geoKeys.regions(code ?? '', locale),
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/countries/{code}/regions', {
          params: { path: { code: code ?? '' }, header: { 'Accept-Language': locale } },
        }),
      ),
    enabled: Boolean(code),
    staleTime: 60_000,
  });
}

// Места GeoNames (поиск для активации и привязки); enabled — экран решает, когда искать.
export function useGeoPlaces(q: PlacesQuery, enabled = true) {
  const api = useApi();
  const locale = useLocale();
  return useInfiniteQuery({
    queryKey: geoKeys.places(locale, q),
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/geo/places', {
          params: { query: { ...q, cursor: pageParam }, header: { 'Accept-Language': locale } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    enabled,
  });
}

// Журнал импортов GeoNames (новые сверху).
export function useGeoImports(q: ImportsQuery = {}) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: geoKeys.imports(q),
    queryFn: ({ pageParam }) =>
      unwrap(api.GET('/v1/geo/imports', { params: { query: { ...q, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
  });
}

// Отчёт импорта со сверкой городов (кандидаты — с названиями на языке ответа).
export function useGeoImport(importId: string) {
  const api = useApi();
  const locale = useLocale();
  return useQuery({
    queryKey: geoKeys.importReport(importId, locale),
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/imports/{importId}', {
          params: { path: { importId }, header: { 'Accept-Language': locale } },
        }),
      ),
  });
}

// Задача импорта в очереди (R7); опрос состояния и фоновый заголовок задаёт экран импорта (Task 20).
export function useGeoImportJob(jobId: number | undefined) {
  const api = useApi();
  return useQuery({
    queryKey: geoKeys.importJob(jobId ?? 0),
    queryFn: () =>
      unwrap(api.GET('/v1/geo/imports/jobs/{jobId}', { params: { path: { jobId: jobId ?? 0 } } })),
    enabled: jobId !== undefined,
  });
}

// Версии приложения — обе платформы.
export function useAppVersions() {
  const api = useApi();
  return useQuery({ queryKey: VERSIONS_KEY, queryFn: () => unwrap(api.GET('/v1/app/versions')) });
}

// Флаги функций.
export function useFeatureFlags() {
  const api = useApi();
  return useQuery({ queryKey: FLAGS_KEY, queryFn: () => unwrap(api.GET('/v1/flags')) });
}
