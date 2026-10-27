import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import type { ChipTone } from '../../ui';

type ApprovalStatus = components['schemas']['ApprovalStatus'];

// Подписи «Подтверждений»: действие, состояние, причина неудачи. Список кодов в контракте открытый
// (сервер новее сборки): незнакомое — общий текст, не сырой код и не пусто.

const TONE: Record<ApprovalStatus, ChipTone> = {
  pending: 'warning',
  executed: 'success',
  rejected: 'danger',
  cancelled: 'neutral',
  expired: 'neutral',
  failed: 'danger',
};
const isStatus = (s: string): s is ApprovalStatus => s in TONE;

// Состояние запроса для чипа.
export function useApprovalStatus(): (status: string) => { tone: ChipTone; text: string } {
  const t = useTranslations('approvals.statuses');
  return useCallback(
    (status) =>
      isStatus(status)
        ? { tone: TONE[status], text: t(status) }
        : { tone: 'neutral', text: t('unknown') },
    [t],
  );
}

// Подпись действия — approvals.actions.<действие> (D21, страж labels.test.ts); незнакомое — сам код.
export function useApprovalAction(): (action: string) => string {
  const t = useTranslations('approvals.actions');
  return useCallback(
    (action) => (t.has(action) && typeof t.raw(action) === 'string' ? t(action) : action),
    [t],
  );
}

// Почему запрос не исполнился или погашен системой (Approval.error, platform/approvals): свои коды
// платформы (initiator_changed, execution_failed) — approvals.failure.*; код ответа исполнителя
// (staff.*, platform.approval_stale) — тот же текст, что у ошибки API (errors.<код>); незнакомое —
// approvals.failure.unknown. Свой откат, а не errors.internal («попробуйте ещё раз»): решённый
// запрос повторить нельзя.
export function useApprovalFailureText(): (code: string) => string {
  const tf = useTranslations('approvals.failure');
  const te = useTranslations('errors');
  return useCallback(
    (code) => {
      if (code !== 'unknown' && tf.has(code) && typeof tf.raw(code) === 'string') return tf(code);
      if (te.has(code) && typeof te.raw(code) === 'string') return te(code);
      return tf('unknown');
    },
    [tf, te],
  );
}
