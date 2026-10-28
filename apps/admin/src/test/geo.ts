import type { components } from '../api/gen/admin';
import { CITY_OTHER, CITY_OWN } from './me';
import { problem } from './problem';
import { http, server } from './server';
import { city } from './staff';

// «Справочники» (план B) для моков — по схемам контракта admin.d.ts (урок 12b: мок не отвечает тем,
// чего сервер не скажет): города, карточка города, районы, страны, регионы, места GeoNames, журнал
// импорта, версии приложения, флаги функций. geoServer() — ответы чтений по умолчанию (тест маршрутов
// приводит в раздел — неописанный запрос роняет тест, src/test/setup.ts); тест экрана ставит свои
// ответы и мутации (server.use позже — важнее).

type GeoCity = components['schemas']['GeoCity'];
type GeoCityDetails = components['schemas']['GeoCityDetails'];
type GeoCountry = components['schemas']['GeoCountry'];
type GeoRegion = components['schemas']['GeoRegion'];
type GeoPlace = components['schemas']['GeoPlace'];
type GeoDistrict = components['schemas']['GeoDistrict'];
type GeoImport = components['schemas']['GeoImport'];
type GeoImportReport = components['schemas']['GeoImportReport'];
type GeoImportJob = components['schemas']['GeoImportJob'];
type AppVersions = components['schemas']['AppVersions'];
type FeatureFlag = components['schemas']['FeatureFlag'];

export const REGION_STAV = 'b7e4d2c9-1a3f-4e8b-9c6d-5f0a2e7b1c38';
export const REGION_SVERD = 'c2a7e9d1-4b3f-4a6c-8d2e-7f1b0c9a5e63';

export function country(over: Partial<GeoCountry> = {}): GeoCountry {
  return {
    code: 'RU',
    name: 'Россия',
    names: { ru: 'Россия', en: 'Russia' },
    currency: 'RUB',
    default_locale: 'ru',
    phone_prefix: '+7',
    week_starts_on: 1,
    min_signup_age: 14,
    age_of_majority: 18,
    is_enabled: true,
    open_cities: 1,
    version: 3,
    ...over,
  };
}

export const COUNTRIES: GeoCountry[] = [
  country(),
  country({
    code: 'KZ',
    name: 'Казахстан',
    names: { ru: 'Казахстан' },
    currency: 'KZT',
    is_enabled: false,
    open_cities: 0,
    version: 1,
  }),
];

export function region(over: Partial<GeoRegion> = {}): GeoRegion {
  return {
    id: REGION_STAV,
    name: 'Ставропольский край',
    names: { ru: 'Ставропольский край', en: 'Stavropol Krai' },
    geoname_admin1_code: '70',
    ...over,
  };
}

export const REGIONS: GeoRegion[] = [
  region({
    id: REGION_SVERD,
    name: 'Свердловская область',
    names: { ru: 'Свердловская область' },
    geoname_admin1_code: '71',
  }),
  region(),
];

export function place(over: Partial<GeoPlace> = {}): GeoPlace {
  return {
    geoname_id: 526815,
    name: 'Михайловск',
    ascii_name: 'Mikhaylovsk',
    feature_code: 'PPL',
    population: 56000,
    timezone: 'Europe/Moscow',
    region_name: 'Ставропольский край',
    country: { code: 'RU', name: 'Россия' },
    city_id: null,
    ...over,
  };
}

export const PLACES: GeoPlace[] = [
  place({
    geoname_id: 487846,
    name: 'Ставрополь',
    ascii_name: 'Stavropol',
    feature_code: 'PPLA',
    population: 450680,
    city_id: CITY_OWN,
  }),
  place(),
];

export const GEO_CITIES: GeoCity[] = [
  city({ districts_count: 4 }),
  city({
    id: CITY_OTHER,
    slug: 'pyatigorsk',
    name: 'Пятигорск',
    status: 'pilot',
    geoname_id: 511565,
    population: 145448,
  }),
];

export function cityDetails(over: Partial<GeoCityDetails> = {}): GeoCityDetails {
  return {
    id: CITY_OWN,
    slug: 'stavropol',
    slug_history: [],
    name: 'Ставрополь',
    names: { ru: 'Ставрополь', en: 'Stavropol' },
    canonical_locale: 'ru',
    status: 'live',
    geoname_id: 487846,
    population: 450680,
    timezone: 'Europe/Moscow',
    location: { lat: 45.04484, lon: 41.9691 },
    country: { code: 'RU', name: 'Россия', is_enabled: true },
    region: { id: REGION_STAV, name: 'Ставропольский край' },
    districts_count: 1,
    version: 3,
    ...over,
  };
}

export function district(over: Partial<GeoDistrict> = {}): GeoDistrict {
  return {
    id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e15',
    name: 'Промышленный',
    archived_at: null,
    version: 1,
    ...over,
  };
}

export function geoImport(over: Partial<GeoImport> = {}): GeoImport {
  return {
    id: '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e01',
    country_code: 'RU',
    status: 'succeeded',
    started_at: '2026-10-20T08:00:00Z',
    finished_at: '2026-10-20T08:04:00Z',
    started_by: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
    started_by_label: 'Ольга В.',
    river_job_id: 1842,
    places_upserted: 1520,
    places_removed: 0,
    places_missing: 0,
    names_upserted: 3010,
    error: null,
    ...over,
  };
}

export function importJob(over: Partial<GeoImportJob> = {}): GeoImportJob {
  return {
    job_id: 1842,
    state: 'completed',
    import_id: '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e01',
    attempt: 1,
    max_attempts: 3,
    created_at: '2026-10-20T07:59:58Z',
    ...over,
  };
}

export function appVersions(over: Partial<AppVersions> = {}): AppVersions {
  return {
    platform: 'ios',
    min_version: null,
    recommended_version: null,
    store_url: null,
    version: null,
    updated_at: null,
    ...over,
  };
}

export function flag(over: Partial<FeatureFlag> = {}): FeatureFlag {
  return {
    key: 'subscriptions',
    description: 'Платные тарифы',
    enabled_globally: false,
    enabled_city_ids: [],
    version: 1,
    updated_at: '2026-10-20T08:00:00Z',
    ...over,
  };
}

// Сервер «Справочников» по умолчанию: чтения с фильтрами и курсором (позиция строки), как на сервере.
// seen — что спрашивал экран (параметры запросов и заголовки языка).
export function geoServer(
  opts: {
    cities?: GeoCity[];
    details?: GeoCityDetails[];
    districts?: Record<string, GeoDistrict[]>;
    countries?: GeoCountry[];
    regions?: Record<string, GeoRegion[]>;
    places?: GeoPlace[];
    pageSize?: number;
  } = {},
) {
  const state = {
    cities: opts.cities ?? GEO_CITIES,
    details: opts.details ?? [cityDetails()],
    districts: opts.districts ?? { [CITY_OWN]: [district()] },
    countries: opts.countries ?? COUNTRIES,
    regions: opts.regions ?? { RU: REGIONS },
    places: opts.places ?? PLACES,
  };
  const seen = {
    cities: [] as URLSearchParams[],
    places: [] as URLSearchParams[],
    regions: [] as string[],
    languages: [] as (string | null)[],
  };
  const size = opts.pageSize ?? 20;
  const page = <T>(rows: T[], q: URLSearchParams) => {
    const from = Number(q.get('cursor') ?? 0);
    const items = rows.slice(from, from + size);
    return { items, next_cursor: from + size < rows.length ? String(from + size) : null };
  };
  const starts = (text: string, ...names: string[]) =>
    !text || names.some((n) => n.toLocaleLowerCase().startsWith(text.toLocaleLowerCase()));

  server.use(
    http.get('/v1/geo/cities', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.cities.push(q);
      seen.languages.push(request.headers.get('Accept-Language'));
      const ids = q.getAll('ids');
      const statuses = q.getAll('status');
      const rows = state.cities.filter(
        (c) =>
          (ids.length === 0 || ids.includes(c.id)) &&
          (statuses.length === 0 || statuses.includes(c.status)) &&
          (!q.get('country') || c.country_code === q.get('country')) &&
          (!q.get('region_id') || c.region_id === q.get('region_id')) &&
          starts(q.get('q') ?? '', c.name),
      );
      return response(200).json(page(rows, q));
    }),
    http.get('/v1/geo/cities/{cityId}', ({ params, response }) => {
      const c = state.details.find((x) => x.id === params.cityId);
      return c ? response(200).json(c) : response.untyped(problem(404, 'geo.city_not_found'));
    }),
    http.get('/v1/geo/cities/{cityId}/districts', ({ params, response }) => {
      const items = state.districts[params.cityId];
      return items
        ? response(200).json({ items })
        : response.untyped(problem(404, 'geo.city_not_found'));
    }),
    http.get('/v1/geo/countries', ({ response }) => response(200).json({ items: state.countries })),
    http.get('/v1/geo/countries/{code}/regions', ({ params, response }) => {
      seen.regions.push(params.code);
      if (!state.countries.some((c) => c.code === params.code))
        return response.untyped(problem(404, 'geo.country_not_found'));
      return response(200).json({ items: state.regions[params.code] ?? [] });
    }),
    http.get('/v1/geo/places', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.places.push(q);
      const rows = state.places.filter(
        (p) =>
          (!q.get('country') || p.country.code === q.get('country')) &&
          starts(q.get('q') ?? '', p.name, p.ascii_name),
      );
      return response(200).json(page(rows, q));
    }),
    http.get('/v1/geo/imports', ({ response }) =>
      response(200).json({ items: [geoImport()], next_cursor: null }),
    ),
    http.get('/v1/geo/imports/{importId}', ({ params, response }) => {
      const report: GeoImportReport = { ...geoImport({ id: params.importId }), reconciled: null };
      return response(200).json(report);
    }),
    http.get('/v1/geo/imports/jobs/{jobId}', ({ params, response }) =>
      response(200).json(importJob({ job_id: Number(params.jobId) })),
    ),
    http.get('/v1/app/versions', ({ response }) =>
      response(200).json({
        items: [appVersions(), appVersions({ platform: 'android' })],
      }),
    ),
    http.get('/v1/flags', ({ response }) => response(200).json({ items: [flag()] })),
  );
  return { state, seen };
}
