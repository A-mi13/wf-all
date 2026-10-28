import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import type { ChipTone } from '../../ui';
import type { ApprovalSummary } from './summary';

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

// Строка «что будет сделано» действий geo (summary.ts): правила страны — «RU: валюта RUB → KZT»
// (все поля patch через «; », незнакомые — «и ещё N полей: ключи»), новая страна — «новая страна KZ: KZT, ru, +7, неделя с пн, 14/18,
// выключена» (неизвестные части пропущены). Не geo — undefined.
export function useCountrySummary(): (s: ApprovalSummary) => string | undefined {
  const t = useTranslations('approvals.summary.geo');
  const tc = useTranslations('geo.countries');
  return useCallback(
    (s) => {
      const enabled = (on: boolean) => tc(on ? 'enabled' : 'disabled');
      if (s.country) {
        const changes = s.country.changes.map((c) => {
          if (c.field === 'is_enabled')
            return t('is_enabled', { from: enabled(c.from), to: enabled(c.to) });
          if (c.field === 'week_starts_on')
            return t('week_starts_on', {
              from: t(`weekday.${c.from}`),
              to: t(`weekday.${c.to}`),
            });
          if (c.field === 'names')
            return t('names', {
              locale: c.locale,
              from: c.from ?? t('none'),
              to: c.to ?? t('none'),
            });
          return t(c.field, { from: String(c.from), to: String(c.to) });
        });
        // незнакомые поля patch — не молча: подтверждающий должен знать, что исполнится ещё
        const others = s.country.others;
        if (others.length > 0)
          changes.push(t('others', { count: others.length, keys: others.join(', ') }));
        return t('rules', { code: s.country.code, changes: changes.join('; ') });
      }
      if (s.newCountry) {
        const n = s.newCountry;
        const details = [
          n.currency,
          n.defaultLocale,
          n.phonePrefix,
          n.weekStartsOn !== undefined ? t(`week.${n.weekStartsOn}`) : undefined,
          n.minSignupAge !== undefined && n.ageOfMajority !== undefined
            ? t('ages', { min: String(n.minSignupAge), majority: String(n.ageOfMajority) })
            : undefined,
          n.isEnabled !== undefined ? enabled(n.isEnabled) : undefined,
        ].filter((x) => x !== undefined);
        return t('create', { code: n.code, details: details.join(', ') });
      }
      return undefined;
    },
    [t, tc],
  );
}
