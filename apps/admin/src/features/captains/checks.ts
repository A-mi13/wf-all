import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import type { ChipTone } from '../../ui';

type Card = components['schemas']['IdentityCaptainApplicationCard'];
export type CaptainCheck = Card['checks'][number];

// Условия капитанства в карточке (спека identity §7.4, R22): check × state → подпись условия и чип состояния.
// Список открытый (модуль новее сборки): незнакомое условие или состояние — «—», а не сырой код.
const CHECKS = ['age', 'restrictions', 'profile', 'captaincy'] as const;
const STATES = ['ok', 'failed', 'unavailable'] as const;
const TONE: Record<(typeof STATES)[number], ChipTone> = {
  ok: 'success',
  failed: 'danger',
  unavailable: 'warning',
};

const isOneOf = <T extends string>(list: readonly T[], v: string): v is T =>
  (list as readonly string[]).includes(v);

// «Одобрить» доступна, только если условия есть и все ok (R22; сервер перепроверяет в момент решения).
export const allChecksOk = (checks: readonly CaptainCheck[]) =>
  checks.length > 0 && checks.every((c) => c.state === 'ok');

export const hasUnavailable = (checks: readonly CaptainCheck[]) =>
  checks.some((c) => c.state === 'unavailable');

export function useCheckView() {
  const t = useTranslations('captains');
  return useCallback(
    (c: CaptainCheck): { label: string; text: string; tone: ChipTone } => ({
      label: isOneOf(CHECKS, c.check) ? t(`checks.${c.check}`) : '—',
      text: isOneOf(STATES, c.state) ? t(`check_state.${c.state}`) : '—',
      tone: isOneOf(STATES, c.state) ? TONE[c.state] : 'neutral',
    }),
    [t],
  );
}
