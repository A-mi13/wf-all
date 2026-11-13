import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import type { UndoInverse } from '../../api/undo';
import { FLAGS_KEY } from '../geo/queries';

type FeatureFlag = components['schemas']['FeatureFlag'];
type FeatureFlagList = components['schemas']['FeatureFlagList'];
type FeatureFlagPatch = components['schemas']['FeatureFlagPatch'];

// Предел контракта updateFeatureFlag: больше 1000 городов во флаге — 400 validation.failed («включите везде»).
export const FLAG_CITIES_MAX = 1000;

// Одно переключение (план B, Task 22): флаг, версия, которую видел экран, и список городов ФЛАГА на момент нажатия
// (не видимые столбцы: поиск сузил столбцы — невидимые города флага остаются включёнными). Переменные сравниваются
// по содержимому: повтор того же переключения после обрыва — тот же ключ идемпотентности (useAdminMutation).
export type FlagToggle = {
  key: string;
  version: number;
  cities: readonly string[];
  change: { kind: 'global'; on: boolean } | { kind: 'city'; cityId: string; on: boolean };
};

const withCity = (list: readonly string[], cityId: string, on: boolean): string[] =>
  on ? [...new Set([...list, cityId])] : list.filter((c) => c !== cityId);

// Тело прямого: «Везде» — enabled_globally; город — полный новый список флага ± город.
export function toggleBody(v: FlagToggle): FeatureFlagPatch {
  return v.change.kind === 'global'
    ? { version: v.version, enabled_globally: v.change.on }
    : { version: v.version, enabled_city_ids: withCity(v.cities, v.change.cityId, v.change.on) };
}

// Тело обратного («Отменить», §4.10): версия и список — из ответа прямого (не из кэша экрана), город — ∓.
export function inverseBody(v: FlagToggle, result: FeatureFlag): FeatureFlagPatch {
  return v.change.kind === 'global'
    ? { version: result.version, enabled_globally: !v.change.on }
    : {
        version: result.version,
        enabled_city_ids: withCity(result.enabled_city_ids, v.change.cityId, !v.change.on),
      };
}

// Ответ правки — флаг целиком: кладётся в список флагов (без второго запроса).
function putFlag(queryClient: QueryClient, flag: FeatureFlag) {
  queryClient.setQueryData<FeatureFlagList>(FLAGS_KEY, (list) =>
    list ? { items: list.items.map((f) => (f.key === flag.key ? flag : f)) } : list,
  );
}

function usePatchFlag() {
  const api = useApi();
  return (key: string, body: FeatureFlagPatch, idempotencyKey: string) =>
    unwrap(
      api.PATCH('/v1/flags/{key}', {
        params: { path: { key }, header: { 'Idempotency-Key': idempotencyKey } },
        body,
      }),
    );
}

// Переключить флаг (updateFeatureFlag). Не оптимистично: клетка меняется только по ответу (putFlag).
export function useToggleFlag() {
  const patch = usePatchFlag();
  const queryClient = useQueryClient();
  return useAdminMutation((v: FlagToggle, key: string) => patch(v.key, toggleBody(v), key), {
    onSuccess: (flag) => putFlag(queryClient, flag),
  });
}

// Обратное переключение для useUndoable: тот же PATCH новым ключом с версией и списком из ответа прямого; исход
// неизвестен — флаги перечитываются, «отменено» — если состояние города (или «Везде») снова прежнее.
export function useFlagUndo(): UndoInverse<FlagToggle, FeatureFlag> {
  const api = useApi();
  const patch = usePatchFlag();
  const queryClient = useQueryClient();
  return {
    run: async (v, result, key) => {
      putFlag(queryClient, await patch(v.key, inverseBody(v, result), key));
    },
    reread: async (v) => {
      const list = await unwrap(api.GET('/v1/flags'));
      queryClient.setQueryData(FLAGS_KEY, list);
      const f = list.items.find((i) => i.key === v.key);
      if (!f) return 'not_undone';
      const now =
        v.change.kind === 'global'
          ? f.enabled_globally
          : f.enabled_city_ids.includes(v.change.cityId);
      return now === !v.change.on ? 'undone' : 'not_undone';
    },
  };
}
