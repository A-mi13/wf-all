import type { components } from '../api/gen/admin';
import type { Me } from '../app/permissions';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from './me';
import { emptyQueryProblem, problem } from './problem';
import { http, server } from './server';
import { approval, city } from './staff';

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

// Флаги по умолчанию (G9-F): выключенный, включённый в городе, включённый везде — матрица и снимки.
export const FLAGS: FeatureFlag[] = [
  flag(),
  flag({ key: 'economy', description: 'Очки, кредиты, косметика', enabled_city_ids: [CITY_OWN] }),
  flag({ key: 'predictions', description: 'Прогнозы на матчи', enabled_globally: true }),
  flag({ key: 'challenges', description: 'Вызовы', enabled_city_ids: [CITY_OWN, CITY_OTHER] }),
];

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
      const bad = emptyQueryProblem(q);
      if (bad) return response.untyped(bad);
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
      const bad = emptyQueryProblem(q);
      if (bad) return response.untyped(bad);
      const rows = state.places.filter(
        (p) =>
          (!q.get('country') || p.country.code === q.get('country')) &&
          starts(q.get('q') ?? '', p.name, p.ascii_name),
      );
      return response(200).json(page(rows, q));
    }),
    http.get('/v1/geo/imports', ({ request, response }) => {
      const country = new URL(request.url).searchParams.get('country');
      const items = [geoImport()].filter((i) => !country || i.country_code === country);
      return response(200).json({ items, next_cursor: null });
    }),
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
    http.get('/v1/flags', ({ response }) => response(200).json({ items: FLAGS })),
  );
  return { state, seen };
}

// «Импорт GeoNames» (Task 20) — как на сервере: журнал с фильтром страны (новые сверху), отчёт по id, задача
// очереди по id, постановка (202 {job_id}; страна уже в очереди или идёт — 409 geo.import_in_progress с job_id её
// задачи, у импорта оператора — без job_id), отмена (queued → cancelled; cancelled — снова 200, K6; running и
// конечные — 409 geo.import_job_not_cancellable; нет задачи — 404). answer(op) — свой ответ (обрыв, 5xx). sent —
// запросы: операция, путь, ключ идемпотентности, фоновый заголовок, тело.
type ImportOp = 'list' | 'report' | 'job' | 'start' | 'cancel';

export function importsServer(
  opts: {
    imports?: GeoImport[];
    reports?: Record<string, GeoImportReport['reconciled']>;
    jobs?: GeoImportJob[];
    busy?: Record<string, number | null>; // страна → задача в очереди или идёт (null — импорт оператора)
    answer?: (op: ImportOp, req: Request) => Response | undefined;
  } = {},
) {
  const state = {
    imports: opts.imports ?? [geoImport()],
    reports: opts.reports ?? {},
    jobs: new Map((opts.jobs ?? []).map((j) => [j.job_id, j])),
    busy: { ...opts.busy },
    nextJob: 2001,
  };
  const sent: {
    op: ImportOp;
    path: string;
    key: string | null;
    background: string | null;
    body: unknown;
  }[] = [];
  const record = async (op: ImportOp, request: Request) => {
    const body: unknown =
      request.method === 'POST'
        ? await request
            .clone()
            .json()
            .catch(() => null)
        : null;
    sent.push({
      op,
      path: new URL(request.url).pathname + new URL(request.url).search,
      key: request.headers.get('Idempotency-Key'),
      background: request.headers.get('X-WF-Background'),
      body,
    });
    return { own: opts.answer?.(op, request), body };
  };
  server.use(
    http.get('/v1/geo/imports', async ({ request, response }) => {
      const { own } = await record('list', request);
      if (own) return response.untyped(own);
      const country = new URL(request.url).searchParams.get('country');
      const items = state.imports.filter((i) => !country || i.country_code === country);
      return response(200).json({ items, next_cursor: null });
    }),
    http.get('/v1/geo/imports/{importId}', async ({ params, request, response }) => {
      const { own } = await record('report', request);
      if (own) return response.untyped(own);
      const row = state.imports.find((i) => i.id === params.importId);
      if (!row) return response.untyped(problem(404, 'geo.import_not_found'));
      return response(200).json({ ...row, reconciled: state.reports[row.id] ?? null });
    }),
    http.get('/v1/geo/imports/jobs/{jobId}', async ({ params, request, response }) => {
      const { own } = await record('job', request);
      if (own) return response.untyped(own);
      const job = state.jobs.get(Number(params.jobId));
      return job
        ? response(200).json(job)
        : response.untyped(problem(404, 'geo.import_job_not_found'));
    }),
    http.post('/v1/geo/imports', async ({ request, response }) => {
      const { own, body } = await record('start', request);
      if (own) return response.untyped(own);
      const { country } = body as { country: string };
      if (Object.hasOwn(state.busy, country)) {
        const id = state.busy[country];
        return response.untyped(
          problem(
            409,
            'geo.import_in_progress',
            id === null || id === undefined ? {} : { job_id: id },
          ),
        );
      }
      const job = importJob({
        job_id: state.nextJob++,
        state: 'queued',
        import_id: null,
        attempt: 0,
        created_at: '2026-10-28T09:00:00Z',
      });
      state.jobs.set(job.job_id, job);
      state.busy[country] = job.job_id;
      return response(202).json({ job_id: job.job_id });
    }),
    http.post('/v1/geo/imports/jobs/{jobId}/cancel', async ({ params, request, response }) => {
      const { own } = await record('cancel', request);
      if (own) return response.untyped(own);
      const job = state.jobs.get(Number(params.jobId));
      if (!job) return response.untyped(problem(404, 'geo.import_job_not_found'));
      if (job.state !== 'queued' && job.state !== 'cancelled')
        return response.untyped(problem(409, 'geo.import_job_not_cancellable'));
      const next = { ...job, state: 'cancelled' as const };
      state.jobs.set(job.job_id, next);
      for (const [c, id] of Object.entries(state.busy)) if (id === job.job_id) delete state.busy[c];
      return response(200).json(next);
    }),
  );
  return { state, sent };
}

// Снимки «Импорта GeoNames» (src/shots/main.tsx, адрес /geo/imports): три строки журнала (сотрудник, оператор сервера,
// «сотрудник» без имени), задача 1842 в очереди (?job=1842), отчёт первой строки со всеми группами (?import=).
export const IMPORT_SHOTS_ID = '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e01';
export function importsShotsServer() {
  return importsServer({
    imports: [
      geoImport({ id: IMPORT_SHOTS_ID }),
      geoImport({
        id: '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e02',
        country_code: 'KZ',
        status: 'running',
        started_at: '2026-10-28T08:55:00Z',
        finished_at: null,
        started_by: null,
        started_by_label: null,
        river_job_id: null,
        places_upserted: null,
        places_removed: null,
        places_missing: null,
        names_upserted: null,
      }),
      geoImport({
        id: '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e03',
        status: 'failed',
        started_at: '2026-10-12T08:00:00Z',
        finished_at: '2026-10-12T08:01:10Z',
        started_by_label: null,
        places_upserted: null,
        places_removed: null,
        places_missing: null,
        names_upserted: null,
        error: 'download timeout',
      }),
    ],
    reports: {
      [IMPORT_SHOTS_ID]: {
        linked: [
          {
            city_id: '3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47',
            slug: 'stavropol',
            geoname_id: 487846,
          },
        ],
        ambiguous: [
          {
            city_id: '7a1d5e9c-2b4f-4c8a-b3e6-1f0d9c8b7a65',
            slug: 'mihaylovsk',
            candidates: [
              {
                geoname_id: 526815,
                name: 'Михайловск',
                region_name: 'Ставропольский край',
                population: 56000,
              },
              {
                geoname_id: 526816,
                name: 'Михайловск',
                region_name: 'Свердловская область',
                population: 9000,
              },
            ],
          },
        ],
        not_found: [{ city_id: '9c3b1a7e-4d2f-4e8a-b5c6-7d8e9f0a1b2c', slug: 'zarechny-3' }],
        conflict: [],
        missing: [524901],
        skipped_timezone: 3,
      },
    },
    jobs: [importJob({ job_id: 1842, state: 'queued', import_id: null, attempt: 0 })],
  });
}

type GeoCityPatch = components['schemas']['GeoCityPatch'];
type GeoDistrictPatch = components['schemas']['GeoDistrictPatch'];
type Op = 'patch' | 'link' | 'createDistrict' | 'patchDistrict';

// Правки города и районов (updateGeoCity, linkGeoCity, createGeoDistrict, updateGeoDistrict) — как на
// сервере: правка меняет состояние, ответ — актуальная карточка; slug из taken — 409 geo.slug_taken
// (прежний уходит в историю); названия: null — удалить перевод, язык страны удалить нельзя (400
// required); имя района занято действующим районом — 409 geo.district_name_taken. answer(op) — свой
// ответ вместо обычного (403, коды, обрыв). sent — тела, ключи идемпотентности и путь.
export function cityEditServer(
  opts: {
    details?: GeoCityDetails;
    districts?: GeoDistrict[];
    taken?: string[];
    answer?: (op: Op, body: unknown) => Response | undefined;
  } = {},
) {
  const state = {
    details: opts.details ?? cityDetails(),
    districts: opts.districts ?? [district()],
  };
  const sent: { op: Op; body: unknown; key: string | null; path: string }[] = [];
  const record = async (op: Op, request: Request) => {
    const body: unknown = await request.json();
    sent.push({
      op,
      body,
      key: request.headers.get('Idempotency-Key'),
      path: new URL(request.url).pathname,
    });
    return { body, own: opts.answer?.(op, body) };
  };
  const nameTaken = (name: string, except?: string) =>
    state.districts.some(
      (d) =>
        d.id !== except && d.archived_at === null && d.name.toLowerCase() === name.toLowerCase(),
    );
  geoServer({ details: [state.details], districts: { [state.details.id]: state.districts } });
  server.use(
    http.get('/v1/geo/cities/{cityId}', ({ params, response }) =>
      params.cityId === state.details.id
        ? response(200).json(state.details)
        : response.untyped(problem(404, 'geo.city_not_found')),
    ),
    // порядок сервера: действующие по имени, затем архивные по имени
    http.get('/v1/geo/cities/{cityId}/districts', ({ response }) =>
      response(200).json({
        items: [...state.districts].sort(
          (a, b) =>
            Number(a.archived_at !== null) - Number(b.archived_at !== null) ||
            a.name.localeCompare(b.name, 'ru'),
        ),
      }),
    ),
    http.patch('/v1/geo/cities/{cityId}', async ({ request, response }) => {
      const { body, own } = await record('patch', request);
      if (own) return response.untyped(own);
      const p = body as GeoCityPatch;
      const next = { ...state.details, names: { ...state.details.names } };
      if (p.slug !== undefined && p.slug !== next.slug) {
        if (opts.taken?.includes(p.slug)) return response.untyped(problem(409, 'geo.slug_taken'));
        next.slug_history = [
          { slug: next.slug, replaced_at: '2026-10-28T10:00:00Z' },
          ...next.slug_history,
        ];
        next.slug = p.slug;
      }
      if (p.status) next.status = p.status;
      for (const [locale, name] of Object.entries(p.names ?? {})) {
        if (name === null) {
          if (locale === next.canonical_locale)
            return response.untyped(
              problem(400, 'validation.failed', {
                errors: [{ field: `body.names.${locale}`, code: 'required' }],
              }),
            );
          delete next.names[locale];
        } else next.names[locale] = name;
      }
      next.version += 1;
      state.details = next;
      return response(200).json(next);
    }),
    http.post('/v1/geo/cities/{cityId}/link', async ({ request, response }) => {
      const { body, own } = await record('link', request);
      if (own) return response.untyped(own);
      const { geoname_id } = body as { geoname_id: number };
      state.details = { ...state.details, geoname_id, version: state.details.version + 1 };
      return response(200).json(state.details);
    }),
    http.post('/v1/geo/cities/{cityId}/districts', async ({ request, response }) => {
      const { body, own } = await record('createDistrict', request);
      if (own) return response.untyped(own);
      const { name } = body as { name: string };
      if (nameTaken(name)) return response.untyped(problem(409, 'geo.district_name_taken'));
      const d = district({
        id: `0192f1c4-4444-7b21-9c5d-${String(state.districts.length + 1).padStart(12, '0')}`,
        name,
      });
      state.districts = [...state.districts, d];
      return response(200).json(d);
    }),
    http.patch(
      '/v1/geo/cities/{cityId}/districts/{districtId}',
      async ({ params, request, response }) => {
        const { body, own } = await record('patchDistrict', request);
        if (own) return response.untyped(own);
        const p = body as GeoDistrictPatch;
        const cur = state.districts.find((d) => d.id === params.districtId);
        if (!cur) return response.untyped(problem(404, 'geo.district_not_found'));
        const name = p.name ?? cur.name;
        const archived = p.archived ?? cur.archived_at !== null;
        if (!archived && nameTaken(name, cur.id))
          return response.untyped(problem(409, 'geo.district_name_taken'));
        const next: GeoDistrict = {
          ...cur,
          name,
          archived_at: archived ? (cur.archived_at ?? '2026-10-28T10:00:00Z') : null,
          version: cur.version + 1,
        };
        state.districts = state.districts.map((d) => (d.id === cur.id ? next : d));
        return response(200).json(next);
      },
    ),
  );
  return { state, sent };
}

// Права «Справочников» для тестов: админ (всё про города, поиск мест) и модератор своего города
// (названия — rename из meModerator, районы — districts.manage), оба — по docs/05.
export const meGeoAdmin: Me = {
  ...meOwner,
  permissions: {
    ...meOwner.permissions,
    'geo.cities.manage': 'allow',
    'geo.cities.rename': 'allow',
    'geo.districts.manage': 'allow',
    'geo.places.search': 'allow',
  },
};

// Админ «Справочников» целиком (G9: страны, импорт, версии, флаги — по docs/05, столбец admin): роль shots
// geo-admin; тесты разделов G9 могут брать её же.
export const meDirectoriesAdmin: Me = {
  ...meGeoAdmin,
  permissions: {
    ...meGeoAdmin.permissions,
    'geo.cities.activate': 'allow',
    'geo.countries.read': 'allow',
    'geo.countries.manage': 'allow',
    'geo.countries.rules': 'dual',
    'geo.imports.read': 'allow',
    'geo.imports.run': 'allow',
    'platform.versions.read': 'allow',
    'platform.versions.manage': 'allow',
    'platform.flags.read': 'allow',
    'platform.flags.manage': 'allow',
  },
};

export const meGeoModerator: Me = {
  ...meModerator,
  permissions: { ...meModerator.permissions, 'geo.districts.manage': 'scoped' },
};

// G9-C: поддержка — «Справочники» только читает (docs/05, столбец support): страны, импорт, версии, флаги.
export const meDirectoriesSupport: Me = {
  ...meOwner,
  role: 'support',
  permissions: {
    'staff.roles.read': 'allow',
    'geo.cities.read': 'allow',
    'geo.countries.read': 'allow',
    'geo.imports.read': 'allow',
    'platform.versions.read': 'allow',
    'platform.flags.read': 'allow',
  },
};

type GeoCountryCreate = components['schemas']['GeoCountryCreate'];
type GeoCountryPatch = components['schemas']['GeoCountryPatch'];
type CountryOp = 'create' | 'patch';

// Правила страны — вдвоём (контракт updateGeoCountry): смена валюты и возрастов, выключение включённой. Поле с
// текущим значением правилом не считается.
const countryRuleChanged = (c: GeoCountry, p: GeoCountryPatch) =>
  (p.currency !== undefined && p.currency !== c.currency) ||
  (p.min_signup_age !== undefined && p.min_signup_age !== c.min_signup_age) ||
  (p.age_of_majority !== undefined && p.age_of_majority !== c.age_of_majority) ||
  (p.is_enabled === false && c.is_enabled);

// Создание и правка страны (createGeoCountry, updateGeoCountry) — как на сервере: создание — всегда 202 (запрос
// geo.country_create), код уже есть — 409 geo.country_exists; правка правил — 202 (geo.country_rules, страна не
// меняется), прочее — 200 и страна после правки (версия растёт); страны нет — 404. answer — свой ответ (ошибки,
// обрыв). sent — тела, ключи, путь.
export function countryEditServer(
  opts: {
    countries?: GeoCountry[];
    answer?: (op: CountryOp, body: unknown) => Response | undefined;
  } = {},
) {
  const { state } = geoServer({ countries: opts.countries ?? COUNTRIES });
  const sent: { op: CountryOp; body: unknown; key: string | null; path: string }[] = [];
  const record = async (op: CountryOp, request: Request) => {
    const body: unknown = await request.json();
    sent.push({
      op,
      body,
      key: request.headers.get('Idempotency-Key'),
      path: new URL(request.url).pathname,
    });
    return { body, own: opts.answer?.(op, body) };
  };
  server.use(
    http.post('/v1/geo/countries', async ({ request, response }) => {
      const { body, own } = await record('create', request);
      if (own) return response.untyped(own);
      const b = body as GeoCountryCreate;
      if (state.countries.some((c) => c.code === b.code))
        return response.untyped(problem(409, 'geo.country_exists'));
      return response(202).json({
        approval: approval({
          action: 'geo.country_create',
          permission: 'geo.countries.rules',
          payload: { v: 1, ...b },
        }),
      });
    }),
    http.patch('/v1/geo/countries/{code}', async ({ params, request, response }) => {
      const { body, own } = await record('patch', request);
      if (own) return response.untyped(own);
      const cur = state.countries.find((c) => c.code === params.code);
      if (!cur) return response.untyped(problem(404, 'geo.country_not_found'));
      const p = body as GeoCountryPatch;
      if (countryRuleChanged(cur, p))
        return response(202).json({
          approval: approval({
            action: 'geo.country_rules',
            permission: 'geo.countries.rules',
            target_id: null,
            payload: { v: 1, code: cur.code, version: cur.version, patch: p },
          }),
        });
      const names = { ...cur.names };
      for (const [l, n] of Object.entries(p.names ?? {})) {
        if (n === null) delete names[l];
        else names[l] = n;
      }
      const next: GeoCountry = {
        ...cur,
        phone_prefix: p.phone_prefix ?? cur.phone_prefix,
        week_starts_on: p.week_starts_on ?? cur.week_starts_on,
        is_enabled: p.is_enabled ?? cur.is_enabled,
        names,
        name: names.ru ?? cur.name,
        version: cur.version + 1,
      };
      state.countries = state.countries.map((c) => (c.code === cur.code ? next : c));
      return response(200).json(next);
    }),
  );
  return { state, sent };
}

type AppVersionsPut = components['schemas']['AppVersionsPut'];

// Версии приложения (listAppVersions, putAppVersions): PUT заменяет версии платформы целиком, версия записи растёт.
// answer — свой ответ (400 validation.failed по полям, обрыв). sent — тела, ключи, путь.
export function versionsServer(
  opts: { items?: AppVersions[]; answer?: (body: AppVersionsPut) => Response | undefined } = {},
) {
  const state = {
    items: opts.items ?? [appVersions(), appVersions({ platform: 'android' })],
  };
  const sent: { body: AppVersionsPut; key: string | null; path: string }[] = [];
  server.use(
    http.get('/v1/app/versions', ({ response }) => response(200).json({ items: state.items })),
    http.put('/v1/app/versions/{platform}', async ({ params, request, response }) => {
      const body = (await request.json()) as AppVersionsPut;
      sent.push({
        body,
        key: request.headers.get('Idempotency-Key'),
        path: new URL(request.url).pathname,
      });
      const own = opts.answer?.(body);
      if (own) return response.untyped(own);
      const cur = state.items.find((i) => i.platform === params.platform);
      if (!cur) return response.untyped(problem(400, 'validation.failed'));
      const next: AppVersions = {
        ...cur,
        ...body,
        version: (cur.version ?? 0) + 1,
        updated_at: '2026-10-28T10:00:00Z',
      };
      state.items = state.items.map((i) => (i.platform === cur.platform ? next : i));
      return response(200).json(next);
    }),
  );
  return { state, sent };
}
