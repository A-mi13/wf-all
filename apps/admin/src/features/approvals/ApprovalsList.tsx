import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { type ApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { can, type Me } from '../../app/permissions';
import { useDateTime } from '../../i18n/format';
import {
  Button,
  type Column,
  DataTable,
  EmptyState,
  infiniteTableProps,
  StatusChip,
  useErrorText,
  useIsMobile,
  useToast,
} from '../../ui';
import { cityLabel, useCityNames } from '../staff/cities';
import { ACCOUNTS_KEY, accountKey, useRoleLabel } from '../staff/queries';
import { useApprovalAction, useApprovalFailureText, useApprovalStatus } from './labels';
import { approvalsListKey, useInvalidateApprovals } from './queries';
import { RejectDialog } from './RejectDialog';
import type { ApprovalBox } from './search';
import { summarizePayload } from './summary';
import styles from './approvals.module.css';

type Approval = components['schemas']['Approval'];

// Решения подтверждений — под одним ключом: пока любое идёт, кнопки других строк недоступны.
const DECIDE_KEY = ['approvals', 'decide'] as const;

// Список вкладки (DataTable, курсор «Показать ещё»): «Входящие» — box=incoming&status=pending,
// «Мои» — box=mine, все состояния. Строка — запрос (действие, кто и когда), «кому и что» из payload,
// срок или итог (кто решил, комментарий, причина неудачи), решение.
// - «Подтвердить» — сразу, без окна (контракт e2e Task 16); итог — по status ответа: executed —
//   успех (и перечитывание учёток), failed — ошибка по error, иное — справка по статусу.
// - «Отклонить» — окно с обязательным комментарием; «Отменить» — свои ожидающие.
// - Ошибки (platform.approval_*, staff.* исполнителя, прочие) — тост по коду; после любого решения
//   перечитываются оба списка и счётчик меню.
// - На телефоне решения по staff.* скрыты (D22, ограничение интерфейса — решает сервер).
export function ApprovalsList({ box, me }: { box: ApprovalBox; me: Me }) {
  const t = useTranslations('approvals');
  const api = useApi();
  const mobile = useIsMobile();
  const list = useInfiniteQuery({
    queryKey: approvalsListKey(box),
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/approvals', {
          params: {
            query:
              box === 'incoming'
                ? { box, status: 'pending', cursor: pageParam }
                : { box, cursor: pageParam },
          },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.next_cursor ?? undefined,
  });
  // Решённый запрос уходит из «Входящих» вместе с кнопкой, на которой стоял фокус (или куда окно
  // «Отклонить» вернуло бы его): после решения, когда список перечитан, фокус с body — на список.
  const root = useRef<HTMLDivElement>(null);
  const refocus = useRef(false);
  const [decided, setDecided] = useState(0);
  const onDone = () => {
    refocus.current = true;
    setDecided((n) => n + 1);
  };
  const { data, isFetching } = list;
  useEffect(() => {
    if (!refocus.current) return;
    const active = document.activeElement;
    if (!active || active === document.body) {
      root.current?.focus();
      refocus.current = false;
    } else if (!isFetching && !document.querySelector('[role="dialog"]')) refocus.current = false;
  }, [decided, data, isFetching]);
  const decisions = useDecisions(onDone);
  const [rejecting, setRejecting] = useState<Approval | null>(null);

  const decision = (a: Approval) => {
    if (a.status !== 'pending') return null;
    if (box === 'mine')
      return (
        <Button
          variant="secondary"
          loading={decisions.pendingId === a.id}
          disabled={decisions.busy && decisions.pendingId !== a.id}
          onClick={() => decisions.cancel(a)}
        >
          {t('cancel')}
        </Button>
      );
    if (mobile && a.action.startsWith('staff.'))
      return <p className={styles.note}>{t('desktop_only')}</p>;
    const other = decisions.busy && decisions.pendingId !== a.id;
    return (
      <div className={styles.actions}>
        <Button
          loading={decisions.pendingId === a.id}
          disabled={other}
          onClick={() => decisions.approve(a)}
        >
          {t('approve')}
        </Button>
        <Button variant="danger-outline" disabled={decisions.busy} onClick={() => setRejecting(a)}>
          {t('reject')}
        </Button>
      </div>
    );
  };

  const columns: Column<Approval>[] = [
    {
      id: 'request',
      header: t('table.columns.request'),
      cell: (a) => <RequestCell approval={a} />,
    },
    {
      id: 'subject',
      header: t('table.columns.subject'),
      cell: (a) => <Subject approval={a} me={me} />,
    },
    {
      id: 'state',
      header: t('table.columns.state'),
      width: 'lg',
      cell: (a) => <StateCell approval={a} />,
    },
    {
      id: 'decision',
      header: t('table.columns.decision'),
      width: 'md',
      align: 'end',
      cell: decision,
    },
  ];

  return (
    <div ref={root} tabIndex={-1} className={styles.list}>
      <DataTable
        label={t(`table.${box}`)}
        columns={columns}
        rowKey={(a) => a.id}
        {...infiniteTableProps(list)}
        empty={
          <EmptyState
            tone={box === 'incoming' ? 'accent' : 'neutral'}
            title={t(`empty.${box}_title`)}
            text={t(`empty.${box}_text`)}
          />
        }
      />
      <RejectDialog approval={rejecting} onClose={() => setRejecting(null)} onDone={onDone} />
    </div>
  );
}

// Подтвердить и отменить. Повторный клик, пока решение идёт, не уходит: нажатая кнопка — loading
// (клик гасится), остальные — disabled; pendingId ставится в обработчике клика, и React перерисует
// до следующего (дискретное событие). Второй запрос с новым ключом был бы вторым действием.
function useDecisions(onDone: () => void) {
  const t = useTranslations('approvals');
  const api = useApi();
  const toast = useToast();
  const errorText = useErrorText();
  const failureText = useApprovalFailureText();
  const statusOf = useApprovalStatus();
  const queryClient = useQueryClient();
  const invalidate = useInvalidateApprovals();
  const [pendingId, setPendingId] = useState<string | null>(null);

  const onError = (e: ApiError) => toast.error(errorText(e));
  const onSettled = async () => {
    setPendingId(null);
    await invalidate();
    onDone();
  };
  const approveM = useAdminMutation(
    (a: Approval, key: string) =>
      unwrap(
        api.POST('/v1/approvals/{approvalId}/approve', {
          params: { path: { approvalId: a.id }, header: { 'Idempotency-Key': key } },
          body: {},
        }),
      ),
    {
      mutationKey: DECIDE_KEY,
      // 200 — запрос в итоговом состоянии: исполнение могло провалиться (failed) — итог по status
      onSuccess: (r) => {
        if (r.status === 'executed') {
          toast.success(t('done.executed'));
          // учётки изменились: «Роли и доступ» (список, карточки) перечитаются
          void queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
        } else if (r.status === 'failed') {
          toast.error(t('failed_toast', { reason: failureText(r.error ?? '') }));
        } else toast.info(t('done.other', { status: statusOf(r.status).text }));
      },
      onError,
      onSettled,
    },
  );
  const cancelM = useAdminMutation(
    (a: Approval, key: string) =>
      unwrap(
        api.POST('/v1/approvals/{approvalId}/cancel', {
          params: { path: { approvalId: a.id }, header: { 'Idempotency-Key': key } },
        }),
      ),
    {
      mutationKey: DECIDE_KEY,
      onSuccess: (r) =>
        r.status === 'cancelled'
          ? toast.success(t('done.cancelled'))
          : toast.info(t('done.other', { status: statusOf(r.status).text })),
      onError,
      onSettled,
    },
  );
  const start = (a: Approval, run: (a: Approval) => void) => {
    setPendingId(a.id);
    run(a);
  };
  return {
    pendingId,
    busy: pendingId !== null,
    approve: (a: Approval) => start(a, approveM.mutate),
    cancel: (a: Approval) => start(a, cancelM.mutate),
  };
}

function RequestCell({ approval: a }: { approval: Approval }) {
  const t = useTranslations('approvals');
  const actionLabel = useApprovalAction();
  const dateTime = useDateTime();
  return (
    <div className={styles.stack}>
      <span className={styles.action}>{actionLabel(a.action)}</span>
      <span className={styles.muted}>
        {t('requested', { name: a.requested_by.label, time: dateTime(a.requested_at) })}
      </span>
    </div>
  );
}

// Срок (ожидающий) или итог: состояние, кто решил (система — у просрочки и гашения), комментарий,
// причина неудачи (failed, системная отмена).
function StateCell({ approval: a }: { approval: Approval }) {
  const t = useTranslations('approvals');
  const dateTime = useDateTime();
  const statusOf = useApprovalStatus();
  const failureText = useApprovalFailureText();
  const status = statusOf(a.status);
  return (
    <div className={styles.stack}>
      <span>
        <StatusChip tone={status.tone}>{status.text}</StatusChip>
      </span>
      {a.status === 'pending' ? (
        <span className={styles.muted}>{t('expires', { time: dateTime(a.expires_at) })}</span>
      ) : (
        a.decided_at && (
          <span className={styles.muted}>
            {t('decided', {
              name: a.decided_by?.label ?? t('system'),
              time: dateTime(a.decided_at),
            })}
          </span>
        )
      )}
      {a.comment && <span>{t('comment', { text: a.comment })}</span>}
      {a.error && (
        <span className={styles.failure}>
          {t('failure_reason', { text: failureText(a.error) })}
        </span>
      )}
    </div>
  );
}

// «Кому и что» — из payload (summary.ts). Цель без имени в payload (смена роли, сброс, выключение
// владельца) — по target_id из карточки сотрудника (staff.accounts.read), иначе «сотрудник <id>».
function Subject({ approval: a, me }: { approval: Approval; me: Me }) {
  const t = useTranslations('approvals.summary');
  const tr = useTranslations('reasons');
  const roleLabel = useRoleLabel();
  const s = summarizePayload(a.payload);
  const reason = s.reason && (tr.has(s.reason) ? tr(s.reason) : s.reason);
  return (
    <dl className={styles.facts} aria-label={t('label')}>
      <dt>{t('to')}</dt>
      <dd>
        {s.name || s.email ? (
          <span className={styles.stack}>
            {s.name && <span className={styles.name}>{s.name}</span>}
            {/* почта как хранится: IDN-домен — A-форма xn--…, не декодируется */}
            {s.email && <span className={styles.muted}>{s.email}</span>}
          </span>
        ) : a.target_id ? (
          <TargetName id={a.target_id} me={me} />
        ) : (
          '—'
        )}
      </dd>
      {s.role && (
        <>
          <dt>{t('role')}</dt>
          <dd>
            {s.fromRole && s.fromRole !== s.role
              ? t('role_change', { from: roleLabel(s.fromRole), to: roleLabel(s.role) })
              : roleLabel(s.role)}
          </dd>
        </>
      )}
      {s.cityIds && s.cityIds.length > 0 && (
        <>
          <dt>{t('cities')}</dt>
          <dd>
            <CityNames ids={s.cityIds} me={me} />
          </dd>
        </>
      )}
      {reason && (
        <>
          <dt>{t('reason')}</dt>
          <dd>{reason}</dd>
        </>
      )}
      {s.comment && (
        <>
          <dt>{t('comment')}</dt>
          <dd>{s.comment}</dd>
        </>
      )}
    </dl>
  );
}

function TargetName({ id, me }: { id: string; me: Me }) {
  const t = useTranslations('approvals.summary');
  const api = useApi();
  const card = useQuery({
    queryKey: accountKey(id),
    queryFn: () =>
      unwrap(api.GET('/v1/staff/accounts/{staffId}', { params: { path: { staffId: id } } })),
    enabled: can(me, 'staff.accounts.read'),
  });
  if (!card.data) return <>{t('target', { id: id.slice(0, 8) })}</>;
  return (
    <span className={styles.stack}>
      <span className={styles.name}>{card.data.display_name}</span>
      <span className={styles.muted}>{card.data.email}</span>
    </span>
  );
}

// Подписи городов — GET /v1/geo/cities?ids= (с правом geo.cities.read), иначе число.
function CityNames({ ids, me }: { ids: string[]; me: Me }) {
  const t = useTranslations('approvals.summary');
  const names = useCityNames(ids, can(me, 'geo.cities.read'));
  const labels = ids
    .map((id) => names.data?.items.find((c) => c.id === id))
    .filter((c) => c !== undefined)
    .map(cityLabel);
  return (
    <>
      {labels.length === ids.length ? labels.join('; ') : t('cities_count', { count: ids.length })}
    </>
  );
}
