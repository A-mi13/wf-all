import { useQuery } from '@tanstack/react-query';
import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { ApiError, isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { ONE_SHOT } from '../../app/query';
import { Button, CopyButton, Form, FormError, Modal, Skeleton, TextField } from '../../ui';
import { contactKey } from './queries';
import { cleanAccessReason, encodeAccessReason, reasonProblem } from './reason';
import styles from './captains.module.css';

// «Показать контакты» (спека identity §7.4, план I-4 R10, R19, R41): почта и телефон заявителя — только по причине
// (3–200 символов, однострочная, подсказка «не указывайте ПД игрока» — причина в журнале неизменяема и переживает
// анонимизацию). Причина идёт заголовком X-WF-Access-Reason в percent-encoding; каждое чтение сервер пишет в
// журнал, поэтому чтение — только по явному «Показать»: запрос без повторов, без перезапросов по фокусу окна и
// сети (staleTime ∞, retry false, refetchOnWindowFocus/Reconnect false), ключ — без причины, кэш живёт, пока
// открыто окно (gcTime 0: закрыли — данные стёрты, повторное открытие начинает с причины), смена сотрудника его
// удаляет (ONE_SHOT). Причина и контакты — только в памяти окна: ни адреса, ни storage, ни черновиков, ни
// «Недавнего». Ошибка чтения — текст в окне, «Показать» читает снова (новая запись в журнале — явное действие).
// Двойное «Показать» (щелчок, Enter в поле), пока чтение идёт, второго запроса не шлёт (ревью T8 I1): кнопка
// занята, отправка гасится, перечитывание после ошибки не отменяет идущее (cancelRefetch: false). Введённая
// причина — dirty окна (закрытие спрашивает); после показа фокус — на контакты, не на body (ревью T8 I2).
type BodyState = { shown: boolean; busy: boolean; dirty: boolean };
const IDLE: BodyState = { shown: false, busy: false, dirty: false };

export function ContactDialog({
  id,
  open,
  onClose,
}: {
  id: string;
  open: boolean;
  onClose: () => void;
}) {
  const t = useTranslations('captains.contact');
  const formId = useId();
  const [state, setState] = useState<BodyState>(IDLE);
  const report = useCallback(
    (next: BodyState) =>
      setState((prev) =>
        prev.shown === next.shown && prev.busy === next.busy && prev.dirty === next.dirty
          ? prev
          : next,
      ),
    [],
  );
  return (
    <Modal
      open={open}
      onClose={() => {
        setState(IDLE);
        onClose();
      }}
      title={t('title')}
      description={t('text')}
      dirty={state.dirty && !state.shown}
      footer={
        state.shown ? undefined : (
          <Button type="submit" form={formId} loading={state.busy}>
            {t('submit')}
          </Button>
        )
      }
    >
      <ContactBody id={id} formId={formId} onState={report} />
    </Modal>
  );
}

function ContactBody({
  id,
  formId,
  onState,
}: {
  id: string;
  formId: string;
  onState: (s: BodyState) => void;
}) {
  const t = useTranslations('captains.contact');
  const api = useApi();
  const form = useForm<{ reason: string }>({ defaultValues: { reason: '' } });
  // причина — в памяти окна (ref): в ключ кэша и состояние запроса она не попадает
  const reason = useRef('');
  const [asked, setAsked] = useState(false);
  const contact = useQuery({
    queryKey: contactKey(id),
    queryFn: () =>
      unwrap(
        api.GET('/v1/identity/captain-applications/{applicationId}/contact', {
          params: {
            path: { applicationId: id },
            header: { 'X-WF-Access-Reason': encodeAccessReason(reason.current) },
          },
        }),
      ),
    enabled: asked,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: ONE_SHOT,
  });
  // первое чтение ещё не завершено (asked + pending) или идёт перечитывание
  const busy = contact.isFetching || (asked && contact.isPending);

  const submit = (v: { reason: string }) => {
    if (busy) return;
    reason.current = cleanAccessReason(v.reason);
    if (!asked) setAsked(true);
    else void contact.refetch({ cancelRefetch: false });
  };

  // данные — только этого открытия окна (asked): кэш прежнего открытия не показывается
  const data = asked ? contact.data : undefined;
  const shown = data !== undefined;
  const { isDirty } = form.formState;
  useEffect(() => {
    onState({ shown, busy, dirty: isDirty });
  }, [shown, busy, isDirty, onState]);
  // контакты показаны — форма и «Показать» подвала уходят вместе с фокусом: фокус на контакты (иначе на body)
  const facts = useRef<HTMLDListElement>(null);
  useEffect(() => {
    if (shown) facts.current?.focus();
  }, [shown]);

  if (data) {
    return (
      <dl ref={facts} tabIndex={-1} className={`${styles.facts} ${styles.focusTarget}`}>
        <dt>{t('email')}</dt>
        <dd className={styles.copyable}>
          {data.email}
          <CopyButton value={data.email} label={t('copy_email')} />
        </dd>
        <dt>{t('phone')}</dt>
        <dd className={styles.copyable}>
          {data.phone ?? <span className={styles.muted}>{t('phone_none')}</span>}
          {data.phone && <CopyButton value={data.phone} label={t('copy_phone')} />}
        </dd>
      </dl>
    );
  }

  return (
    <Form id={formId} form={form} onSubmit={submit}>
      <TextField
        name="reason"
        label={t('reason')}
        hint={t('reason_hint')}
        autoComplete="off"
        spellCheck={false}
        autoFocus
        readOnly={busy}
        rules={{
          validate: {
            format: (v: string) =>
              reasonProblem(cleanAccessReason(v)) !== 'format' || t('reason_invalid'),
            minLength: (v: string) => reasonProblem(cleanAccessReason(v)) !== 'minLength',
            maxLength: (v: string) => reasonProblem(cleanAccessReason(v)) !== 'maxLength',
          },
        }}
      />
      {busy && <Skeleton.Text lines={2} />}
      {asked && contact.isError && !busy && (
        <FormError
          error={
            isApiError(contact.error)
              ? contact.error
              : new ApiError({ status: 0, code: 'internal' })
          }
        />
      )}
    </Form>
  );
}
