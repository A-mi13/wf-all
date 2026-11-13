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

// Тело обратного («Отменить», §4.10): версия и список — из базы обратного (ответ прямого или последний ответ своей
// цепочки правок флага — ownBase; не кэш экрана), город — ∓.
export function inverseBody(v: FlagToggle, base: FeatureFlag): FeatureFlagPatch {
  return v.change.kind === 'global'
    ? { version: base.version, enabled_globally: !v.change.on }
    : {
        version: base.version,
        enabled_city_ids: withCity(base.enabled_city_ids, v.change.cityId, !v.change.on),
      };
}

// Своя цепочка правок флага во вкладке (финальное ревью m3): версии, которые дали ответы PATCH этой вкладки, последний
// такой ответ и PATCH в полёте. По QueryClient: смена сотрудника стирает кэш, но не объект — цепочка прошлого
// сотрудника своей не станет: его версии старше ответа прямого нового (обратное ищет свои версии ПОСЛЕ него).
type Chain = { own: Set<number>; last: FeatureFlag | null; inflight: Set<Promise<unknown>> };
const chains = new WeakMap<QueryClient, Map<string, Chain>>();

function chainOf(queryClient: QueryClient, key: string): Chain {
  let flags = chains.get(queryClient);
  if (!flags) chains.set(queryClient, (flags = new Map()));
  let c = flags.get(key);
  if (!c) flags.set(key, (c = { own: new Set(), last: null, inflight: new Set() }));
  return c;
}

// База обратного: ждёт PATCH этого флага в полёте (своя правка Y, начатая до нажатия «Отменить» X), затем — последний
// свой ответ, если все версии после ответа прямого — свои (обратное города не зависит от других городов, «Везде» — от
// списка); есть чужая версия или своих после прямого нет — ответ прямого (чужое изменение → 409 «уже изменили»).
async function ownBase(queryClient: QueryClient, key: string, result: FeatureFlag) {
  const c = chainOf(queryClient, key);
  await Promise.allSettled([...c.inflight]);
  const last = c.last;
  if (!last || last.version <= result.version) return result;
  for (let n = result.version + 1; n <= last.version; n++) if (!c.own.has(n)) return result;
  return last;
}

// Ответ правки — флаг целиком: кладётся в список флагов (без второго запроса). Перечитывание списка в полёте
// (фокус вкладки, неизвестный исход другого флага) прочитано до записи и затёрло бы ответ (клетка назад, следующий
// щелчок — 409 от своей правки; G9-F m-1): оно отменяется до записи, а свежий список читается заново после неё.
async function putFlag(queryClient: QueryClient, flag: FeatureFlag) {
  const reading = queryClient.isFetching({ queryKey: FLAGS_KEY }) > 0;
  await queryClient.cancelQueries({ queryKey: FLAGS_KEY });
  queryClient.setQueryData<FeatureFlagList>(FLAGS_KEY, (list) =>
    list ? { items: list.items.map((f) => (f.key === flag.key ? flag : f)) } : list,
  );
  // отменённое перечитывание (за чужими правками других флагов) — заново, уже после записи; cancelRefetch: false —
  // запрос в полёте здесь может быть только начатым после записи, его не прерывать
  if (reading)
    void queryClient.invalidateQueries({ queryKey: FLAGS_KEY }, { cancelRefetch: false });
}

// PATCH флага с учётом своей цепочки: ответ — своя версия, запрос — в полёте до ответа.
function usePatchFlag() {
  const api = useApi();
  const queryClient = useQueryClient();
  return (key: string, body: FeatureFlagPatch, idempotencyKey: string) => {
    const c = chainOf(queryClient, key);
    const done = unwrap(
      api.PATCH('/v1/flags/{key}', {
        params: { path: { key }, header: { 'Idempotency-Key': idempotencyKey } },
        body,
      }),
    ).then((flag) => {
      c.own.add(flag.version);
      if (!c.last || flag.version > c.last.version) c.last = flag;
      return flag;
    });
    c.inflight.add(done);
    void done.then(
      () => c.inflight.delete(done),
      () => c.inflight.delete(done),
    );
    return done;
  };
}

// Переключить флаг (updateFeatureFlag). Не оптимистично: клетка меняется только по ответу (putFlag).
export function useToggleFlag() {
  const patch = usePatchFlag();
  const queryClient = useQueryClient();
  return useAdminMutation((v: FlagToggle, key: string) => patch(v.key, toggleBody(v), key), {
    onSuccess: (flag) => putFlag(queryClient, flag),
  });
}

// Обратное переключение для useUndoable: тот же PATCH новым ключом с версией и списком из ответа прямого или своей
// цепочки после него (ownBase); исход неизвестен — флаги перечитываются, «отменено» — если состояние города (или
// «Везде») снова прежнее.
export function useFlagUndo(): UndoInverse<FlagToggle, FeatureFlag> {
  const api = useApi();
  const patch = usePatchFlag();
  const queryClient = useQueryClient();
  return {
    run: async (v, result, key) => {
      const base = await ownBase(queryClient, v.key, result);
      await putFlag(queryClient, await patch(v.key, inverseBody(v, base), key));
    },
    reread: async (v) => {
      const list = await unwrap(api.GET('/v1/flags'));
      await queryClient.cancelQueries({ queryKey: FLAGS_KEY });
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
