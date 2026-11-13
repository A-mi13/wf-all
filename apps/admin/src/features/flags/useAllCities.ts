import { useQuery } from '@tanstack/react-query';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { isMeaningfulQuery } from '../../app/search';
import { useLocale } from '../../i18n/locale';
import { CITIES_KEY, type CitiesQuery } from '../geo/queries';

type GeoCity = components['schemas']['GeoCity'];

// Потолок городов-столбцов матрицы: дальше — предупреждение «уточните поиском».
export const CITIES_CAP = 2000;
// Страница listGeoCities (максимум контракта) и пачка ids (предел параметра ids контракта).
const PAGE = 100;
export const IDS_BATCH = 100;

// Ключи — под CITIES_KEY: правки и активация городов (invalidateCityLists) делают их устаревшими.
export const ALL_CITIES_KEY = [...CITIES_KEY, 'all'] as const;
export const allCitiesKeys = {
  list: (locale: string, q: CitiesQuery) => [...ALL_CITIES_KEY, 'list', locale, q] as const,
  ids: (locale: string, ids: readonly string[]) => [...ALL_CITIES_KEY, 'ids', locale, ids] as const,
};

// Все города для столбцов матрицы флагов (план B, Task 22; ревью планов minor 6): listGeoCities отдаёт до 100 за
// раз — курсором до конца, не больше CITIES_CAP (capped — показаны не все). Город, который включён во флаге, но не
// попал в загруженные (потолок), догружается по ids пачками по IDS_BATCH: столбец флага не пропадает. Полный список
// городов флага при переключении берётся из флага, а не отсюда — столбцы только показывают. С поиском или статусом
// — только города фильтра (фильтр сужает столбцы), без догрузки.
export function useAllCities(q: CitiesQuery, flagCityIds: readonly string[]) {
  const api = useApi();
  const locale = useLocale();
  const list = useQuery({
    queryKey: allCitiesKeys.list(locale, q),
    queryFn: async ({ signal }) => {
      // q без буквы и цифры сервер отверг бы — «по фильтру городов нет» без запроса (isMeaningfulQuery)
      if (q.q !== undefined && !isMeaningfulQuery(q.q)) return { items: [], capped: false };
      const items: GeoCity[] = [];
      let cursor: string | undefined;
      do {
        const page = await unwrap(
          api.GET('/v1/geo/cities', {
            params: { query: { ...q, limit: PAGE, cursor }, header: { 'Accept-Language': locale } },
            signal,
          }),
        );
        items.push(...page.items);
        cursor = page.next_cursor ?? undefined;
      } while (cursor !== undefined && items.length < CITIES_CAP);
      return { items: items.slice(0, CITIES_CAP), capped: cursor !== undefined };
    },
  });

  const filtered = Object.keys(q).length > 0;
  const loaded = new Set(list.data?.items.map((c) => c.id));
  const missing =
    list.data && !filtered
      ? [...new Set(flagCityIds)].filter((cid) => !loaded.has(cid)).sort()
      : [];
  const extra = useQuery({
    queryKey: allCitiesKeys.ids(locale, missing),
    enabled: missing.length > 0,
    queryFn: async ({ signal }) => {
      const out: GeoCity[] = [];
      for (let i = 0; i < missing.length; i += IDS_BATCH) {
        const ids = missing.slice(i, i + IDS_BATCH);
        const page = await unwrap(
          api.GET('/v1/geo/cities', {
            params: {
              query: { ids, limit: IDS_BATCH },
              header: { 'Accept-Language': locale },
            },
            signal,
          }),
        );
        out.push(...page.items);
      }
      return out;
    },
  });

  const extraItems = missing.length > 0 ? (extra.data ?? []) : [];
  return {
    cities: [...(list.data?.items ?? []), ...extraItems.filter((c) => !loaded.has(c.id))],
    capped: list.data?.capped ?? false,
    // список загружен (догрузка по ids не держит матрицу: её столбцы приходят следом); без него — скелетон
    // или ошибка списка
    loaded: list.data !== undefined,
    error: list.error ?? extra.error,
    isFetching: list.isFetching || extra.isFetching,
    refetch: () => {
      void list.refetch();
      if (missing.length > 0) void extra.refetch();
    },
  };
}
