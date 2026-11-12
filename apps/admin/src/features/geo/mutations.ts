import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import type { UndoInverse } from '../../api/undo';
import { useLocale } from '../../i18n/locale';
import { CITIES_KEY, GEO_PLACES_KEY, geoKeys } from './queries';

type GeoCityDetails = components['schemas']['GeoCityDetails'];
type GeoCityPatch = components['schemas']['GeoCityPatch'];
type GeoDistrictPatch = components['schemas']['GeoDistrictPatch'];
type GeoDistrict = components['schemas']['GeoDistrict'];

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

// linkGeoCity: ручная привязка к месту GeoNames. 409 geo.city_already_linked — город уже привязан
// (например, сверкой импорта, пока окно было открыто): карточка перечитывается, иначе страница так и
// показывала бы «не привязан» и «Привязать» до перезагрузки (ревью G8, m2). Успех и 409
// geo.city_already_active — места GeoNames устарели (метка «уже заведён», ревью G8, m3).
export function useLinkCity(cityId: string) {
  const api = useApi();
  const locale = useLocale();
  const queryClient = useQueryClient();
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
    {
      mutationKey: ['geo', 'link', cityId],
      onSuccess: async (city) => {
        await Promise.all([
          onResult(city),
          queryClient.invalidateQueries({ queryKey: GEO_PLACES_KEY }),
        ]);
      },
      onError: (e) => {
        if (isApiError(e, 'geo.city_already_linked'))
          void queryClient.invalidateQueries({ queryKey: [...CITIES_KEY, 'card', cityId] });
        if (isApiError(e, 'geo.city_already_active'))
          void queryClient.invalidateQueries({ queryKey: GEO_PLACES_KEY });
      },
    },
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

// Правка района с «Отменить» (спека UI-фундамента §4.10, m11): previous — прежние значения тех же полей (экран
// кладёт их из строки до правки: имя, archived), обратное — PATCH с ними новым ключом. version в GeoDistrictPatch
// нет — «последняя запись побеждает», кода *_stale у района нет. Исход обратного неизвестен — районы
// перечитываются, факт — по полям previous у района на сервере.
export type DistrictEdit = { id: string; patch: GeoDistrictPatch; previous: GeoDistrictPatch };

export function useDistrictUndo(cityId: string): UndoInverse<DistrictEdit, GeoDistrict> {
  const api = useApi();
  const changed = useDistrictsChanged(cityId);
  return {
    run: async (v, _result, key) => {
      try {
        await unwrap(
          api.PATCH('/v1/geo/cities/{cityId}/districts/{districtId}', {
            params: { path: { cityId, districtId: v.id }, header: { 'Idempotency-Key': key } },
            body: v.previous,
          }),
        );
      } finally {
        void changed();
      }
    },
    reread: async (v) => {
      const list = await unwrap(
        api.GET('/v1/geo/cities/{cityId}/districts', { params: { path: { cityId } } }),
      );
      void changed();
      const d = list.items.find((i) => i.id === v.id);
      if (!d) return 'not_undone';
      const nameOk = v.previous.name === undefined || d.name === v.previous.name;
      const archivedOk =
        v.previous.archived === undefined || (d.archived_at !== null) === v.previous.archived;
      return nameOk && archivedOk ? 'undone' : 'not_undone';
    },
  };
}

export function useUpdateDistrict(cityId: string) {
  const api = useApi();
  const changed = useDistrictsChanged(cityId);
  return useAdminMutation(
    ({ id, patch }: DistrictEdit, key: string) =>
      unwrap(
        api.PATCH('/v1/geo/cities/{cityId}/districts/{districtId}', {
          params: { path: { cityId, districtId: id }, header: { 'Idempotency-Key': key } },
          body: patch,
        }),
      ),
    { mutationKey: ['geo', 'districts', cityId, 'update'], onSuccess: changed },
  );
}
