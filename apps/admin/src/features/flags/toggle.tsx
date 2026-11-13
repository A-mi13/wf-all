import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { isApiError, isUnknownOutcome } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { useUndoable } from '../../api/undo';
import { useCan } from '../../app/session';
import { Button, Sheet, useErrorText, useIsCoarse, useToast } from '../../ui';
import { FLAGS_KEY } from '../geo/queries';
import { FLAG_CITIES_MAX, type FlagToggle, useFlagUndo, useToggleFlag } from './mutations';
import { ALL_CITIES_KEY } from './useAllCities';

type FeatureFlag = components['schemas']['FeatureFlag'];

// Подпись флага: flags.<ключ>.title и .note из локалей; незнакомый ключ (флаг новее админки) — сам ключ.
export function useFlagLabel() {
  const t = useTranslations('flags');
  return useCallback(
    (key: string): { title: string; note: string | null; known: boolean } => {
      const title = `${key}.title`;
      if (!t.has(title)) return { title: key, note: null, known: false };
      const note = `${key}.note`;
      return { title: t(title), note: t.has(note) ? t(note) : null, known: true };
    },
    [t],
  );
}

// Что переключается: «Везде» или город (имя — для тоста и шторки).
export type FlagTarget = { kind: 'global' } | { kind: 'city'; cityId: string; cityName: string };

// Клетка флага: значение, занятость и доступность (одна версия на флаг — пока переключение флага в полёте,
// остальные его клетки недоступны; «Везде» включён — городские неактивны; предел контракта — включить город нельзя).
export type CellState = { checked: boolean; busy: boolean; disabled: boolean };

const BUSY_GLOBAL = '*';

// Переключатель флагов экрана (матрица и список телефона): useUndoable над updateFeatureFlag (§4.10, обратное —
// по ответу прямого), не оптимистично (клетка — по ответу), подтверждение шторкой на грубом указателе (§3.5,
// m-5: промах пальцем включил бы функцию городу; на компьютере — без окна). Ошибки: 409 flag_stale — тост
// «изменили», флаги перечитаны, повтора нет; 422 flag_city_not_found — тост, города перечитаны; 400 при включении
// города — предел 1000 городов; неизвестный исход — флаги перечитаны (повтор того же — тем же ключом).
export function useFlagToggle() {
  const t = useTranslations('flagsPage');
  const ta = useTranslations('actions');
  const label = useFlagLabel();
  const toast = useToast();
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const canManage = useCan('platform.flags.manage');
  const coarse = useIsCoarse();
  const undoable = useUndoable(useToggleFlag(), useFlagUndo());
  const [busy, setBusy] = useState<Readonly<Record<string, string>>>({});
  const [ask, setAsk] = useState<{ v: FlagToggle; title: string; success: string } | null>(null);
  // ответ пришёл после ухода со страницы — ошибку снятый экран не показывает (тост «Отменить» живёт сам)
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const run = (v: FlagToggle, success: string) => {
    const target = v.change.kind === 'global' ? BUSY_GLOBAL : v.change.cityId;
    setBusy((b) => ({ ...b, [v.key]: target }));
    undoable
      .mutateAsync(v, { successText: success })
      .catch((e: unknown) => {
        if (!alive.current) return;
        if (isApiError(e, 'platform.flag_stale')) {
          toast.info(t('stale'));
          void queryClient.invalidateQueries({ queryKey: FLAGS_KEY });
          return;
        }
        if (isApiError(e, 'platform.flag_city_not_found')) {
          void queryClient.invalidateQueries({ queryKey: FLAGS_KEY });
          void queryClient.invalidateQueries({ queryKey: ALL_CITIES_KEY });
        }
        if (isApiError(e, 'validation.failed') && v.change.kind === 'city' && v.change.on) {
          toast.error(t('limit'));
          return;
        }
        if (isUnknownOutcome(e)) void queryClient.invalidateQueries({ queryKey: FLAGS_KEY });
        toast.error(errorText(isApiError(e) ? e : 'internal'));
      })
      .finally(() => {
        if (!alive.current) return;
        setBusy((b) => {
          if (b[v.key] !== target) return b;
          const next = { ...b };
          delete next[v.key];
          return next;
        });
      });
  };

  const cell = (flag: FeatureFlag, target: FlagTarget): CellState => {
    const inFlight = busy[flag.key];
    if (target.kind === 'global') {
      return {
        checked: flag.enabled_globally,
        busy: inFlight === BUSY_GLOBAL,
        disabled: !canManage || (inFlight !== undefined && inFlight !== BUSY_GLOBAL),
      };
    }
    const included = flag.enabled_city_ids.includes(target.cityId);
    return {
      checked: flag.enabled_globally || included,
      busy: inFlight === target.cityId,
      disabled:
        !canManage ||
        flag.enabled_globally ||
        (inFlight !== undefined && inFlight !== target.cityId) ||
        (!included && atLimit(flag)),
    };
  };

  const toggle = (flag: FeatureFlag, target: FlagTarget) => {
    const state = cell(flag, target);
    if (state.disabled || state.busy) return;
    const on = !state.checked;
    const name = label(flag.key).title;
    const v: FlagToggle = {
      key: flag.key,
      version: flag.version,
      cities: flag.enabled_city_ids,
      change:
        target.kind === 'global'
          ? { kind: 'global', on }
          : { kind: 'city', cityId: target.cityId, on },
    };
    const values: Record<string, string> =
      target.kind === 'global' ? { flag: name } : { flag: name, city: target.cityName };
    const which = target.kind === 'global' ? 'all' : 'city';
    const success = t(`toast.${which}_${on ? 'on' : 'off'}`, values);
    if (!coarse) return run(v, success);
    setAsk({ v, title: t(`confirm.${which}_${on ? 'on' : 'off'}`, values), success });
  };

  const on = ask?.v.change.on ?? true;
  const sheet = (
    <Sheet
      open={ask !== null}
      onClose={() => setAsk(null)}
      title={ask?.title ?? ''}
      description={t('confirm.text')}
      footer={
        <>
          <Button variant="secondary" onClick={() => setAsk(null)}>
            {ta('cancel')}
          </Button>
          <Button
            onClick={() => {
              if (ask) run(ask.v, ask.success);
              setAsk(null);
            }}
          >
            {on ? t('confirm.on') : t('confirm.off')}
          </Button>
        </>
      }
    />
  );

  return { canManage, cell, toggle, sheet };
}

// Предел контракта: во флаге уже FLAG_CITIES_MAX городов — следующий включить нельзя (только «Везде»).
export const atLimit = (flag: FeatureFlag) =>
  !flag.enabled_globally && flag.enabled_city_ids.length >= FLAG_CITIES_MAX;
