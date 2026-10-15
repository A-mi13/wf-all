import { useQueryClient } from '@tanstack/react-query';
import { type ReactNode, useId } from 'react';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import {
  Button,
  EmptyState,
  ErrorState,
  LoadingRows,
  StatusChip,
  useErrorText,
  useToast,
} from '../../ui';
import styles from './me.module.css';
import { MY_SESSIONS_KEY, useMySessions } from './queries';
import { useSessionLabel } from './sessionLabel';

// «Активные сеансы» (макет 07, спека §4.7): браузер и ОС из User-Agent, сеть (подсеть /24 или /48 —
// маскирует сервер), вход и последняя активность. Текущий — «Текущий сеанс» без кнопки; остальные —
// «Завершить». Сеанс уже завершён (другая вкладка, истёк) — 404 staff.session_not_found: тост и
// перезапрос списка. После любого завершения список перечитывается.
export function MySessionsCard() {
  const t = useTranslations('me.sessions');
  const titleId = useId();
  const api = useApi();
  const queryClient = useQueryClient();
  const toast = useToast();
  const errorText = useErrorText();
  const sessions = useMySessions();
  const describe = useSessionLabel();

  const refresh = () => queryClient.invalidateQueries({ queryKey: MY_SESSIONS_KEY });
  const revoke = useAdminMutation(
    (sessionId: string, key: string) =>
      api.DELETE('/v1/staff/me/sessions/{sessionId}', {
        params: { path: { sessionId }, header: { 'Idempotency-Key': key } },
      }),
    {
      onSuccess: () => toast.success(t('revoked')),
      onError: (e) => toast.error(errorText(e)),
      onSettled: refresh,
    },
  );
  const revokeOthers = useAdminMutation(
    (_: void, key: string) =>
      unwrap(
        api.POST('/v1/staff/me/sessions/revoke-others', {
          params: { header: { 'Idempotency-Key': key } },
        }),
      ),
    {
      onSuccess: (d) => toast.success(t('revoked_others', { count: d.revoked })),
      onError: (e) => toast.error(errorText(e)),
      onSettled: refresh,
    },
  );

  let body: ReactNode;
  if (sessions.isPending) body = <LoadingRows rows={3} />;
  else if (sessions.isError)
    body = (
      <ErrorState
        error={sessions.error}
        onRetry={() => void sessions.refetch()}
        retrying={sessions.isFetching}
      />
    );
  else if (sessions.data.items.length === 0) body = <EmptyState title={t('empty')} text="" />;
  else
    body = (
      // role="list": Safari снимает семантику списка с list-style: none
      <ul role="list" aria-label={t('label')} className={styles.sessions}>
        {sessions.data.items.map((s) => {
          const d = describe(s);
          return (
            <li
              key={s.id}
              className={s.current ? `${styles.session} ${styles.current}` : styles.session}
            >
              <p className={styles.device}>{d.device}</p>
              <p className={styles.details}>{d.details}</p>
              <div className={styles.sessionAction}>
                {s.current ? (
                  <StatusChip tone="success">{t('current')}</StatusChip>
                ) : (
                  <Button
                    variant="danger-outline"
                    aria-label={t('revoke_label', { device: d.device, created: d.created })}
                    loading={revoke.isPending && revoke.variables === s.id}
                    disabled={revoke.isPending}
                    onClick={() => revoke.mutate(s.id)}
                  >
                    {t('revoke')}
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    );

  const others = sessions.data?.items.some((s) => !s.current) ?? false;
  return (
    <section aria-labelledby={titleId} className={styles.card}>
      <h2 id={titleId} className={styles.cardTitle}>
        {t('title')}
      </h2>
      {body}
      <Button
        variant="danger-outline"
        block
        loading={revokeOthers.isPending}
        disabled={!others}
        onClick={() => revokeOthers.mutate()}
      >
        {t('revoke_others')}
      </Button>
      <p className={styles.note}>{t('note')}</p>
    </section>
  );
}
