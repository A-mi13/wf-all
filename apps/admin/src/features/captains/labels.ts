import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import type { ChipTone } from '../../ui';

type Status = components['schemas']['IdentityCaptainApplicationStatus'];
export type RejectionCode = components['schemas']['IdentityCaptainRejectionCode'];

// Подписи «Капитанов»: итог заявки, причина отказа, причина «нельзя одобрить» (ApiError.reasons). Коды открытые
// (сервер новее сборки): незнакомое — общий текст или сам код у поддержки, не пусто.

export const REJECTION_CODES: readonly RejectionCode[] = [
  'profile_incomplete',
  'insufficient_info',
  'contact_unreachable',
  'suspicious_activity',
  'duplicate_account',
  'other',
];

const TONE: Record<Status, ChipTone> = {
  pending: 'warning',
  approved: 'success',
  rejected: 'danger',
  withdrawn: 'neutral',
};
const isStatus = (s: string): s is Status => s in TONE;
const isRejection = (s: string): s is RejectionCode =>
  (REJECTION_CODES as readonly string[]).includes(s);

const INELIGIBILITY = [
  'too_young',
  'restricted',
  'profile_incomplete',
  'captain_elsewhere_in_city',
  'already_permitted',
  'checks_unavailable',
] as const;
const isIneligibility = (s: string): s is (typeof INELIGIBILITY)[number] =>
  (INELIGIBILITY as readonly string[]).includes(s);

export function useApplicationResult(): (status: string) => { tone: ChipTone; text: string } {
  const t = useTranslations('captains.result');
  return useCallback(
    (status) => {
      if (!isStatus(status)) return { tone: 'neutral', text: status };
      const text: Record<Status, string> = {
        pending: t('pending'),
        approved: t('approved'),
        rejected: t('rejected'),
        withdrawn: t('withdrawn'),
      };
      return { tone: TONE[status], text: text[status] };
    },
    [t],
  );
}

export function useRejectionText(): (code: string) => string {
  const t = useTranslations('captains.rejection');
  return useCallback((code) => (isRejection(code) ? t(code) : code), [t]);
}

// Причины 409 identity.captain_ineligible (ApiError.reasons): знакомые — текстом, незнакомые — общим текстом кода
// (errors.identity.captain_ineligible), один раз.
export function useIneligibilityTexts(): (reasons: readonly string[]) => string[] {
  const t = useTranslations('captains.reasons');
  const te = useTranslations('errors.identity');
  return useCallback(
    (reasons) => {
      const out = reasons.filter(isIneligibility).map((r) => t(r));
      if (reasons.length === 0 || reasons.some((r) => !isIneligibility(r)))
        out.push(te('captain_ineligible'));
      return out;
    },
    [t, te],
  );
}
