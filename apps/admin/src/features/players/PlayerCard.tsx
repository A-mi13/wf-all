import { useEffect, useId, useRef, useState } from 'react';
import { isApiError } from '../../api/client';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { can } from '../../app/permissions';
import { useSession } from '../../app/session';
import { useDateTime } from '../../i18n/format';
import {
  Button,
  ConfirmDialog,
  CopyButton,
  PendingApprovalNotice,
  StatusChip,
  useErrorText,
  useToast,
} from '../../ui';
import { cityLabel, useCityNames } from '../staff/cities';
import { useCancelMfaReset } from './mutations';
import { RESET_PERMISSION } from './queries';
import { RequestResetDialog } from './RequestResetDialog';
import styles from './players.module.css';

type Player = components['schemas']['IdentityPlayerLookup'];

const STATUS_TONE = {
  active: 'success',
  suspended: 'warning',
  banned: 'danger',
  pending_deletion: 'neutral',
} as const;
const isKnownStatus = (s: string): s is keyof typeof STATUS_TONE => Object.hasOwn(STATUS_TONE, s);

// Карточка найденного игрока (план I-4, Task 9): ник, город, статус (незнакомый — общим текстом), создан, 2FA,
// назначенный сброс. Действия — право identity.mfa.reset («вдвоём» — тоже разрешение, §7.2): «Запросить сброс 2FA»
// (есть 2FA и нет назначенного сброса; 202 → пометка «на подтверждение», кнопка уходит) и «Отменить сброс»
// (ConfirmDialog с ником → 200 → onCancelled: экран обновляет карточку по ответу, без повторного поиска).
export function PlayerCard({
  player: p,
  onCancelled,
}: {
  player: Player;
  onCancelled: () => void;
}) {
  const t = useTranslations('players');
  const toast = useToast();
  const errorText = useErrorText();
  const dateTime = useDateTime();
  const session = useSession();
  const me = session.status === 'active' ? session.me : null;
  const canReset = me !== null && can(me, RESET_PERMISSION, p.city_id);
  const names = useCityNames([p.city_id], me !== null && can(me, 'geo.cities.read'));
  const city = names.data?.items.find((c) => c.id === p.city_id);
  const titleId = useId();
  const [requesting, setRequesting] = useState(false);
  const [requested, setRequested] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const cancel = useCancelMfaReset(p.mfa_reset?.id ?? '');
  const status = isKnownStatus(p.status)
    ? { tone: STATUS_TONE[p.status], text: t(`status.${p.status}`) }
    : { tone: 'neutral' as const, text: t('status.unknown') };
  // Действие исполнено — его кнопка исчезает, а с ней и открыватель, которому окно вернуло фокус: фокус не должен
  // падать на body — он уходит на карточку (только если поверх нет окна: читалка объявила бы карточку вместо него).
  const self = useRef<HTMLElement>(null);
  useEffect(() => {
    const active = document.activeElement;
    if (active && active !== document.body) return;
    if (document.querySelector('[role="dialog"]')) return;
    self.current?.focus();
  });

  return (
    <section ref={self} tabIndex={-1} aria-labelledby={titleId} className={styles.card}>
      <h2 id={titleId} className={styles.cardTitle}>
        {p.nickname}
      </h2>
      {requested && <PendingApprovalNotice />}
      <dl className={styles.facts}>
        <dt>{t('card.id')}</dt>
        <dd className={styles.copyable}>
          <span className={styles.mono}>{p.id}</span>
          <CopyButton value={p.id} label={t('card.copy_id')} />
        </dd>
        <dt>{t('card.city')}</dt>
        <dd>{city ? cityLabel(city) : t('card.city_unknown')}</dd>
        <dt>{t('card.status')}</dt>
        <dd>
          <StatusChip tone={status.tone}>{status.text}</StatusChip>
        </dd>
        <dt>{t('card.created')}</dt>
        <dd>{dateTime(p.created_at)}</dd>
        <dt>{t('card.mfa')}</dt>
        <dd>
          {p.mfa_enabled ? t('card.mfa_on') : t('card.mfa_off')}
          {p.mfa_reset && (
            <span className={styles.reset}>
              {t('card.reset_at', { time: dateTime(p.mfa_reset.execute_at) })}
            </span>
          )}
        </dd>
      </dl>
      {canReset && (
        <div className={styles.actions}>
          {p.mfa_reset ? (
            <Button
              variant="danger-outline"
              onClick={() => {
                cancel.reset();
                setCancelling(true);
              }}
            >
              {t('reset.cancel')}
            </Button>
          ) : (
            p.mfa_enabled &&
            !requested && (
              <Button variant="secondary" onClick={() => setRequesting(true)}>
                {t('reset.request')}
              </Button>
            )
          )}
        </div>
      )}
      {requesting && (
        <RequestResetDialog
          player={p}
          onClose={() => setRequesting(false)}
          onRequested={() => {
            setRequesting(false);
            setRequested(true);
          }}
        />
      )}
      <ConfirmDialog
        open={cancelling}
        onClose={() => setCancelling(false)}
        onConfirm={() =>
          cancel.mutate(undefined, {
            onSuccess: () => {
              setCancelling(false);
              toast.success(t('reset.cancelled'));
              onCancelled();
            },
            onError: (e) => {
              // сброса уже нет (выполнен, отменён владельцем или другим сотрудником) — карточка его не показывает
              if (!isApiError(e, 'identity.mfa_reset_not_found')) return;
              setCancelling(false);
              toast.error(errorText(e));
              onCancelled();
            },
          })
        }
        title={t('reset.cancel_title', { name: p.nickname })}
        text={t('reset.cancel_text')}
        confirmWord={p.nickname}
        confirmLabel={t('reset.cancel')}
        note={t('reset.journal')}
        loading={cancel.isPending}
        error={cancel.error}
      />
    </section>
  );
}
