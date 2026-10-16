import { useIsMutating, useQuery, useQueryClient } from '@tanstack/react-query';
import { type ReactNode, useEffect, useId, useRef, useState } from 'react';
import { FormProvider, useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import type { components } from '../../api/gen/admin';
import { can, type Decision, decide, type Me } from '../../app/permissions';
import { useDateTime } from '../../i18n/format';
import {
  Button,
  ConfirmDialog,
  Drawer,
  ErrorState,
  Form,
  LoadingRows,
  PendingApprovalNotice,
  SelectField,
  StatusChip,
  TextField,
  useCountdown,
  useErrorText,
  useToast,
} from '../../ui';
import { useSessionLabel } from '../me/sessionLabel';
import { cityLabel, useCityNames } from './cities';
import { CityField } from './CityField';
import { ACCOUNTS_KEY, accountKey, accountSessionsKey, useRoleLabel } from './queries';
import { ROLES, roleChangePermission, type StaffAccount, staffActions } from './roles';
import { useStaffStatus } from './status';
import styles from './staff.module.css';

type Reason = components['schemas']['StaffActionReason'];
const REASONS: readonly Reason[] = ['left_team', 'security', 'role_change', 'other'];
// Предел комментария к причине — StaffConfirmedAction.comment (maxLength: 500).
const COMMENT_MAX = 500;

// Все действия панели — под одним префиксом: пока любое идёт, панель не закрывается.
const ACTION_KEY = ['staff', 'account-action'] as const;

// Строка сотрудника (*; спека §9.4 п. 5, §2.5–2.6, §4.9): карточка (getStaffAccount) и действия,
// разрешённые по me.permissions с учётом роли цели (roles.ts): переотправить приглашение (204; 429 —
// отсчёт), роль и города (200 — сразу, 202 — на подтверждение), сеансы цели и «Завершить все»,
// сбросить доступ (202), выключить (200/202). Разрушающие — ConfirmDialog с вводом почты цели как
// она показана (IDN — A-форма xn--…) и причиной из списка с комментарием. После любой мутации
// перечитываются список, карточка и сеансы (ACCOUNTS_KEY).
export function StaffDrawer({
  me,
  target,
  onClose,
}: {
  me: Me;
  target: StaffAccount | null;
  onClose: () => void;
}) {
  const t = useTranslations('staff.drawer');
  const busy = useIsMutating({ mutationKey: ACTION_KEY }) > 0;
  return (
    <Drawer
      open={target !== null}
      onClose={onClose}
      title={target?.display_name ?? t('title')}
      dismissible={!busy}
    >
      {target && <StaffPanel key={target.id} me={me} initial={target} />}
    </Drawer>
  );
}

function StaffPanel({ me, initial }: { me: Me; initial: StaffAccount }) {
  const t = useTranslations('staff');
  const ta = useTranslations('account');
  const api = useApi();
  const roleLabel = useRoleLabel();
  const statusOf = useStaffStatus();
  const dateTime = useDateTime();
  const card = useQuery({
    queryKey: accountKey(initial.id),
    queryFn: () =>
      unwrap(
        api.GET('/v1/staff/accounts/{staffId}', { params: { path: { staffId: initial.id } } }),
      ),
    placeholderData: initial,
  });
  const a = card.data ?? initial;
  const names = useCityNames(a.city_ids, can(me, 'geo.cities.read'));
  const [approval, setApproval] = useState(false);
  // «сейчас» — на момент открытия панели: закрыт ли вход (locked_until в прошлом — уже нет)
  const [now] = useState(() => Date.now());
  // Действие исполнено — его блок исчезает (выключенной учётке нечего выключать), а с ним и кнопка,
  // на которую диалог вернул фокус. Фокус не должен падать на body при открытой панели: он уходит
  // на саму панель. Только если панель — верхний оверлей: под открытым диалогом (подтверждение,
  // разблокировка) фокус не трогаем — читалка объявила бы панель вместо диалога (ревью M2).
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const active = document.activeElement;
    if (active && active !== document.body) return;
    const own = panel.current?.closest('[role="dialog"]');
    const dialogs = document.querySelectorAll('[role="dialog"]');
    if (own && dialogs[dialogs.length - 1] === own) panel.current?.focus();
  });

  if (card.isError && !card.isPlaceholderData)
    return (
      <ErrorState
        error={card.error}
        onRetry={() => void card.refetch()}
        retrying={card.isFetching}
      />
    );

  const actions = staffActions(me, a);
  const status = statusOf(a);
  const cities = a.city_ids
    .map((id) => {
      const c = names.data?.items.find((x) => x.id === id);
      return c ? cityLabel(c) : null;
    })
    .filter((x) => x !== null);
  const onApproval = () => setApproval(true);

  return (
    <div ref={panel} tabIndex={-1} className={styles.panel}>
      {approval && <PendingApprovalNotice />}
      <dl className={styles.facts}>
        <dt>{t('drawer.email')}</dt>
        {/* почта как хранится (A-форма xn--…) — её же вводят в подтверждении */}
        <dd>{a.email}</dd>
        <dt>{t('drawer.status')}</dt>
        <dd>
          <StatusChip tone={status.tone}>{status.text}</StatusChip>
        </dd>
        <dt>{t('drawer.role')}</dt>
        <dd>{roleLabel(a.role)}</dd>
        {a.city_ids.length > 0 && (
          <>
            <dt>{t('drawer.cities')}</dt>
            <dd>
              {/* пока подписи не пришли (или нет права на города) — число городов */}
              {cities.length === a.city_ids.length
                ? cities.join('; ')
                : ta('cities', { count: a.city_ids.length })}
            </dd>
          </>
        )}
        <dt>{t('drawer.last_login')}</dt>
        <dd>{a.last_login_at ? dateTime(a.last_login_at) : t('list.never')}</dd>
        <dt>{t('drawer.created')}</dt>
        <dd>{dateTime(a.created_at)}</dd>
      </dl>
      {a.locked_until && Date.parse(a.locked_until) > now && (
        <p className={styles.text}>
          {t('drawer.locked_until', { time: dateTime(a.locked_until) })}
        </p>
      )}
      {a.invite && (
        <p className={styles.text}>
          {a.invite.expired
            ? t('status.expired')
            : t('drawer.invite_until', { time: dateTime(a.invite.expires_at) })}
        </p>
      )}

      {actions.resend && <ResendBlock target={a} />}
      {actions.roleOptions.length > 0 && (
        <RoleBlock
          key={a.version}
          me={me}
          target={a}
          options={actions.roleOptions}
          onApproval={onApproval}
        />
      )}
      {actions.sessions && <SessionsBlock target={a} />}
      {actions.reset !== 'deny' && (
        <DestructiveBlock
          kind="reset"
          target={a}
          decision={actions.reset}
          onApproval={onApproval}
        />
      )}
      {actions.disable !== 'deny' && (
        <DestructiveBlock
          kind="disable"
          target={a}
          decision={actions.disable}
          onApproval={onApproval}
        />
      )}
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={styles.block}>
      <h3 id={id} className={styles.blockTitle}>
        {title}
      </h3>
      {children}
    </section>
  );
}

// Цель изменилась в другой вкладке или у другого сотрудника (уже выключена, удалена): после
// перечитывания блок действия с его ошибкой может исчезнуть — текст уходит общим тостом
// useStaffAction (ревью M3), а блок свой текст не дублирует: одна ошибка — одно сообщение (уборка №6,
// R1). Диалог действия с такой ошибкой закрывается.
const TOASTED = new Set(['staff.wrong_stage', 'staff.not_found']);
const toasted = (e: ApiError): boolean => TOASTED.has(e.code);

// Мутация действия над сотрудником: общий ключ (панель не закрывается) и перечитывание всего про
// учётки после ответа — и после ошибки (409 wrong_stage, last_owner: данные могли устареть).
function useStaffAction<V, R>(
  fn: (vars: V, key: string) => Promise<R>,
  opts: { onSuccess?: (r: R) => void; onError?: (e: ApiError) => void } = {},
) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const errorText = useErrorText();
  return useAdminMutation(fn, {
    mutationKey: ACTION_KEY,
    onSuccess: opts.onSuccess,
    onError: (e) => {
      if (toasted(e)) toast.error(errorText(e));
      opts.onError?.(e);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY }),
  });
}

// 200 — исполнено сразу, 202 — создан запрос на подтверждение (правило двух рук).
type Outcome = 'done' | 'approval';
const outcomeOf = (r: { response: Response }): Outcome =>
  r.response.status === 202 ? 'approval' : 'done';

function ResendBlock({ target }: { target: StaffAccount }) {
  const t = useTranslations('staff.drawer.resend');
  const api = useApi();
  const toast = useToast();
  const errorText = useErrorText();
  const wait = useCountdown();
  const resend = useStaffAction(
    (_: void, key: string) =>
      api.POST('/v1/staff/accounts/{staffId}/invite/resend', {
        params: { path: { staffId: target.id }, header: { 'Idempotency-Key': key } },
      }),
    {
      onSuccess: () => toast.success(t('done')),
      onError: (e) => {
        // пауза 60 с от выпуска приглашения — отсчёт в кнопке, не ошибка
        if (e.code === 'ratelimit.exceeded') wait.start(e.retryAfter ?? 60);
        else if (!toasted(e)) toast.error(errorText(e));
      },
    },
  );
  const limited = wait.left > 0;
  return (
    <Block title={t('title')}>
      <p className={styles.text}>{t('text')}</p>
      <div className={styles.row}>
        <Button
          variant="secondary"
          loading={resend.isPending}
          disabled={limited}
          onClick={() => resend.mutate()}
        >
          {limited ? t('wait', { seconds: wait.left }) : t('button')}
        </Button>
      </div>
    </Block>
  );
}

type RoleValues = { role: string; city_ids: string[] };

function RoleBlock({
  me,
  target,
  options,
  onApproval,
}: {
  me: Me;
  target: StaffAccount;
  options: readonly string[];
  onApproval: () => void;
}) {
  const t = useTranslations('staff.drawer.role_change');
  const api = useApi();
  const toast = useToast();
  const errorText = useErrorText();
  const roleLabel = useRoleLabel();
  const form = useForm<RoleValues>({
    defaultValues: { role: target.role, city_ids: target.city_ids },
  });
  const role = useWatch({ control: form.control, name: 'role' });
  const cityIds = useWatch({ control: form.control, name: 'city_ids' });
  const next = { role, city_ids: role === 'moderator' ? cityIds : [] };
  // Право — по самому изменению (§2.6): elevated — grant_elevated, роль или новый город — grant,
  // только снятие городов — revoke (сразу).
  const permission = roleChangePermission(target, next);
  const decision: Decision = permission ? decide(me, permission) : 'deny';

  const change = useStaffAction(
    async (v: RoleValues, key: string) => {
      const r = ROLES.find((x) => x === v.role);
      if (!r) throw new Error('RoleBlock: незнакомая роль');
      return outcomeOf(
        await api.PUT('/v1/staff/accounts/{staffId}/role', {
          params: { path: { staffId: target.id }, header: { 'Idempotency-Key': key } },
          body: { role: r, city_ids: r === 'moderator' ? v.city_ids : [] },
        }),
      );
    },
    {
      onSuccess: (o) => {
        if (o === 'done') return void toast.success(t('saved'));
        // запрос ушёл на подтверждение: роль пока прежняя — форма тоже
        form.reset({ role: target.role, city_ids: target.city_ids });
        onApproval();
      },
    },
  );

  return (
    <Block title={t('title')}>
      <Form
        form={form}
        onSubmit={(v) => (permission && decision !== 'deny' ? change.mutateAsync(v) : undefined)}
        onError={(e) => {
          if (toasted(e)) return true; // текст — общим тостом
          if (e.code !== 'staff.city_not_found') return false;
          form.setError(
            'city_ids',
            { type: 'server', message: errorText(e) },
            { shouldFocus: true },
          );
          return true;
        }}
      >
        <SelectField
          name="role"
          label={t('role')}
          options={options.map((r) => ({ value: r, label: roleLabel(r) }))}
        />
        {role === 'moderator' && (
          <CityField
            name="city_ids"
            label={t('cities')}
            rules={{
              validate: {
                min_items: (v: unknown) =>
                  form.getValues('role') !== 'moderator' || (Array.isArray(v) && v.length > 0),
              },
            }}
          />
        )}
        {permission && decision === 'deny' && <p className={styles.text}>{t('denied')}</p>}
        <div className={styles.row}>
          <Button
            type="submit"
            variant={decision === 'needs_approval' ? 'secondary' : 'primary'}
            loading={change.isPending}
            disabled={!permission || decision === 'deny'}
          >
            {decision === 'needs_approval' ? t('request') : t('save')}
          </Button>
        </div>
      </Form>
    </Block>
  );
}

function SessionsBlock({ target }: { target: StaffAccount }) {
  const t = useTranslations('staff.drawer.sessions');
  const tc = useTranslations('staff.drawer');
  const api = useApi();
  const toast = useToast();
  const label = useSessionLabel();
  const [confirming, setConfirming] = useState(false);
  const sessions = useQuery({
    queryKey: accountSessionsKey(target.id),
    queryFn: () =>
      unwrap(
        api.GET('/v1/staff/accounts/{staffId}/sessions', {
          params: { path: { staffId: target.id } },
        }),
      ),
  });
  const revokeAll = useStaffAction(
    (_: void, key: string) =>
      unwrap(
        api.POST('/v1/staff/accounts/{staffId}/sessions/revoke-all', {
          params: { path: { staffId: target.id }, header: { 'Idempotency-Key': key } },
        }),
      ),
    {
      onSuccess: (d) => {
        setConfirming(false);
        toast.success(t('done', { count: d.revoked }));
      },
      onError: (e) => {
        if (toasted(e)) setConfirming(false);
      },
    },
  );

  let body: ReactNode;
  if (sessions.isPending) body = <LoadingRows rows={2} />;
  else if (sessions.isError)
    body = (
      <ErrorState
        error={sessions.error}
        onRetry={() => void sessions.refetch()}
        retrying={sessions.isFetching}
      />
    );
  else if (sessions.data.items.length === 0) body = <p className={styles.text}>{t('empty')}</p>;
  else
    body = (
      // role="list": Safari снимает семантику списка с list-style: none
      <ul role="list" aria-label={t('label')} className={styles.sessions}>
        {sessions.data.items.map((s) => {
          const l = label(s);
          return (
            <li key={s.id} className={styles.session}>
              <strong className={styles.sessionDevice}>{l.device}</strong>
              {l.details}
            </li>
          );
        })}
      </ul>
    );

  return (
    <Block title={t('title')}>
      {body}
      <div className={styles.row}>
        <Button
          variant="danger-outline"
          disabled={!sessions.data || sessions.data.items.length === 0}
          onClick={() => {
            revokeAll.reset();
            setConfirming(true);
          }}
        >
          {t('revoke_all')}
        </Button>
      </div>
      <ConfirmDialog
        open={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={() => revokeAll.mutate()}
        title={t('confirm_title', { name: target.display_name })}
        text={t('confirm_text')}
        confirmWord={target.email}
        confirmLabel={t('confirm')}
        note={tc('journal')}
        loading={revokeAll.isPending}
        error={revokeAll.error}
      />
    </Block>
  );
}

type DestructiveValues = { reason: string; comment: string };

// Сброс доступа и выключение: причина из списка и комментарий (для «Другое» — обязателен) — поля в
// самом диалоге, над вводом почты цели; почта уходит в confirm как показана. Ошибка — внутри
// диалога, он не закрывается и введённое не теряет. Диалог монтируется только открытым: при новом
// открытии поля пусты.
function DestructiveBlock({
  kind,
  target,
  decision,
  onApproval,
}: {
  kind: 'reset' | 'disable';
  target: StaffAccount;
  decision: Decision;
  onApproval: () => void;
}) {
  const t = useTranslations(`staff.drawer.${kind}`);
  const [open, setOpen] = useState(false);
  // сброс — всегда вдвоём; выключение — сразу или запросом (владелец — revoke_owner, dual)
  const label = kind === 'disable' && decision !== 'allow' ? t('request') : t('button');
  return (
    <Block title={t('title')}>
      <p className={styles.text}>{t('text')}</p>
      <div className={styles.row}>
        <Button variant="danger-outline" onClick={() => setOpen(true)}>
          {label}
        </Button>
      </div>
      {open && (
        <DestructiveDialog
          kind={kind}
          target={target}
          decision={decision}
          onClose={() => setOpen(false)}
          onApproval={onApproval}
        />
      )}
    </Block>
  );
}

function DestructiveDialog({
  kind,
  target,
  decision,
  onClose,
  onApproval,
}: {
  kind: 'reset' | 'disable';
  target: StaffAccount;
  decision: Decision;
  onClose: () => void;
  onApproval: () => void;
}) {
  const t = useTranslations(`staff.drawer.${kind}`);
  const tc = useTranslations('staff.drawer');
  const tr = useTranslations('reasons');
  const api = useApi();
  const toast = useToast();
  const form = useForm<DestructiveValues>({ defaultValues: { reason: '', comment: '' } });
  const action = useStaffAction(
    async (v: DestructiveValues, key: string) => {
      const reason = REASONS.find((r) => r === v.reason);
      if (!reason) throw new Error('DestructiveDialog: причина не выбрана');
      const body = {
        reason,
        ...(v.comment.trim() ? { comment: v.comment.trim() } : {}),
        // почта цели — как показана и введена (A-форма xn--…); сервер сравнит нормализованные
        confirm: target.email,
      };
      const params = { path: { staffId: target.id }, header: { 'Idempotency-Key': key } };
      return outcomeOf(
        kind === 'reset'
          ? await api.POST('/v1/staff/accounts/{staffId}/access-reset', { params, body })
          : await api.POST('/v1/staff/accounts/{staffId}/disable', { params, body }),
      );
    },
    {
      onSuccess: (o) => {
        onClose();
        if (o === 'approval') onApproval();
        else toast.success(t('done'));
      },
      onError: (e) => {
        if (toasted(e)) onClose();
      },
    },
  );

  const consequences =
    kind === 'reset'
      ? [t('consequences.secrets'), t('consequences.sessions'), t('consequences.invite')]
      : [t('consequences.role'), t('consequences.sessions'), t('consequences.history')];
  const confirmLabel =
    kind === 'reset'
      ? t('confirm')
      : decision === 'allow'
        ? t('confirm_now')
        : t('confirm_request');

  return (
    <ConfirmDialog
      open
      onClose={onClose}
      onConfirm={() => {
        if (action.isPending) return;
        void form.handleSubmit((v) => action.mutate(v))();
      }}
      title={t('confirm_title', { name: target.display_name })}
      consequences={consequences}
      confirmWord={target.email}
      confirmLabel={confirmLabel}
      note={tc('journal')}
      loading={action.isPending}
      error={action.error}
    >
      {/* поля причины — в диалоге (ConfirmDialog рисует свою форму): контекст react-hook-form */}
      <FormProvider {...form}>
        <SelectField
          name="reason"
          label={tc('reason')}
          placeholder={tc('reason_placeholder')}
          options={REASONS.map((r) => ({ value: r, label: tr(r) }))}
          rules={{ required: true }}
        />
        <TextField
          name="comment"
          label={tc('comment')}
          hint={tc('comment_hint')}
          autoComplete="off"
          maxLength={COMMENT_MAX}
          rules={{
            // как в контракте (StaffConfirmedAction.comment): длиннее — ошибка поля до запроса
            maxLength: COMMENT_MAX,
            validate: {
              required: (v: string) => form.getValues('reason') !== 'other' || v.trim() !== '',
            },
          }}
        />
      </FormProvider>
    </ConfirmDialog>
  );
}
