import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useLocale } from '../../i18n/locale';
import { SEARCH_DEBOUNCE_MS } from '../../ui';

type GeoCity = components['schemas']['GeoCity'];

// Города модератора — через listGeoCities (спека §9.4 п. 5, D24): поиск по началу названия на
// сервере и подписи уже выбранных (ids). Названия — на языке интерфейса. Ключ подписей тот же, что
// у карточки сотрудника в каркасе (app/account.ts): один запрос на одни и те же id.

// «Ставрополь, Ставропольский край» — регион различает одноимённые города.
export const cityLabel = (c: GeoCity) => (c.region_name ? `${c.name}, ${c.region_name}` : c.name);

// Подписи выбранных городов; enabled — есть право geo.cities.read (иначе экран показывает число).
export function useCityNames(ids: readonly string[], enabled = true) {
  const api = useApi();
  const locale = useLocale();
  return useQuery({
    queryKey: ['geo', 'cities', 'names', locale, ids],
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: {
            query: { ids: [...ids], limit: Math.min(ids.length, 100) },
            header: { 'Accept-Language': locale },
          },
        }),
      ),
    enabled: enabled && ids.length > 0,
    staleTime: 10 * 60_000,
  });
}

// Поиск городов: запрос уходит после паузы набора; пустой (после обрезки) — первая страница без q
// (сервер отверг бы q из одних пробелов). Пока идёт новый поиск, показан прежний список.
export function useCitySearch(text: string) {
  const api = useApi();
  const locale = useLocale();
  const [q, setQ] = useState(text.trim());
  useEffect(() => {
    const timer = setTimeout(() => setQ(text.trim()), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text]);
  return useQuery({
    queryKey: ['geo', 'cities', 'search', locale, q],
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: { query: q ? { q } : {}, header: { 'Accept-Language': locale } },
        }),
      ),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });
}
