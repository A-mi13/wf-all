import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { useLocale } from '../../i18n/locale';
import { CITIES_KEY, geoKeys } from './queries';

type GeoCityDetails = components['schemas']['GeoCityDetails'];
type GeoCityPatch = components['schemas']['GeoCityPatch'];
type GeoDistrictPatch = components['schemas']['GeoDistrictPatch'];

// Правки города и районов (план B, Task 19). Ответ правки города — актуальная карточка целиком: она
// ложится в кэш getGeoCity (без второго запроса), списки и подписи городов (CITIES_KEY, кроме
// карточек и районов) помечаются устаревшими. Районы: ответ — один район, порядок и число районов
// в карточке считает сервер — перечитываются районы, карточка и списки.

// Списки и подписи городов — устарели (карточки и районы обновляет вызывающий).
export function invalidateCityLists(queryClient: QueryClient) {
  return queryClient.invalidateQueries({
    queryKey: CITIES_KEY,
    predicate: (q) => q.queryKey[2] !== 'card' && q.queryKey[2] !== 'districts',
  });
}

function useCityResult() {
  const queryClient = useQueryClient();
  const locale = useLocale();
  return async (city: GeoCityDetails) => {
    queryClient.setQueryData(geoKeys.city(city.id, locale), city);
    await invalidateCityLists(queryClient);
  };
}

// updateGeoCity: status и slug — geo.cities.manage, names — geo.cities.rename (сервер решает по
// каждому полю; поле без права — 403 на весь запрос).
export function useUpdateCity(cityId: string, mutationKey: readonly unknown[]) {
  const api = useApi();
  const locale = useLocale();
  const onResult = useCityResult();
  return useAdminMutation(
    (body: GeoCityPatch, key: string) =>
      unwrap(
        api.PATCH('/v1/geo/cities/{cityId}', {
          params: {
            path: { cityId },
            header: { 'Idempotency-Key': key, 'Accept-Language': locale },
          },
          body,
        }),
      ),
    { mutationKey, onSuccess: onResult },
  );
}

// linkGeoCity: ручная привязка к месту GeoNames.
export function useLinkCity(cityId: string) {
  const api = useApi();
  const locale = useLocale();
  const onResult = useCityResult();
  return useAdminMutation(
    (geonameId: number, key: string) =>
      unwrap(
        api.POST('/v1/geo/cities/{cityId}/link', {
          params: {
            path: { cityId },
            header: { 'Idempotency-Key': key, 'Accept-Language': locale },
          },
          body: { geoname_id: geonameId },
        }),
      ),
    { mutationKey: ['geo', 'link', cityId], onSuccess: onResult },
  );
}

function useDistrictsChanged(cityId: string) {
  const queryClient = useQueryClient();
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: geoKeys.districts(cityId) }),
      queryClient.invalidateQueries({ queryKey: [...CITIES_KEY, 'card', cityId] }),
      invalidateCityLists(queryClient),
    ]);
}

export function useCreateDistrict(cityId: string) {
  const api = useApi();
  const changed = useDistrictsChanged(cityId);
  return useAdminMutation(
    (name: string, key: string) =>
      unwrap(
        api.POST('/v1/geo/cities/{cityId}/districts', {
          params: { path: { cityId }, header: { 'Idempotency-Key': key } },
          body: { name },
        }),
      ),
    { mutationKey: ['geo', 'districts', cityId, 'create'], onSuccess: changed },
  );
}

export function useUpdateDistrict(cityId: string) {
  const api = useApi();
  const changed = useDistrictsChanged(cityId);
  return useAdminMutation(
    ({ id, patch }: { id: string; patch: GeoDistrictPatch }, key: string) =>
      unwrap(
        api.PATCH('/v1/geo/cities/{cityId}/districts/{districtId}', {
          params: { path: { cityId, districtId: id }, header: { 'Idempotency-Key': key } },
          body: patch,
        }),
      ),
    { mutationKey: ['geo', 'districts', cityId, 'update'], onSuccess: changed },
  );
}
