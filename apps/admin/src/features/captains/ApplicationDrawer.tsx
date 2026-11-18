import { useIsMutating, useQuery } from '@tanstack/react-query';
import { type ReactNode, useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { type ApiError, isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { can, type Me } from '../../app/permissions';
import { useNumber } from '../../i18n/format';
import {
  Button,
  Drawer,
  EmptyState,
  ErrorState,
  RelativeTime,
  Skeleton,
  StatusChip,
  useErrorText,
  useToast,
} from '../../ui';
import { cityLabel, useCityNames } from '../staff/cities';
import { ApproveDialog } from './ApproveDialog';
import { allChecksOk, hasUnavailable, useCheckView } from './checks';
import { ContactDialog } from './ContactDialog';
import { useApplicationResult, useIneligibilityTexts, useRejectionText } from './labels';
import { DECIDE_KEY } from './mutations';
import { applicationKey, DECIDE_PERMISSION, PII_PERMISSION } from './queries';
import { RejectDialog } from './RejectDialog';
import styles from './captains.module.css';

type Card = components['schemas']['IdentityCaptainApplicationCard'];
type Application = components['schemas']['IdentityCaptainApplication'];
type Dialog = 'approve' | 'reject' | 'contact' | null;

// Карточка заявки (спека identity §7.4, макет 07 «Решение по заявке»; план I-4, R21, R22): панель ?application=<id>
// — заявитель, поля формы (текст игрока — с его переводами строк, без разметки), «телефон указан / нет», условия
// сейчас, прошлые заявки, число связанных аккаунтов. Подвал — «Отклонить» и «Одобрить» (недоступна с пояснением,
// пока хоть одно условие не ok; сервер перепроверяет в момент решения); у решённой — итог вместо кнопок. Ответы
// решения: 409 application_closed — тост и перечитанная карточка (итог другого сотрудника); 409
// captain_ineligible — причины текстом в карточке; 404 — «Заявка не найдена», панель закрыта; self_action и прочее
// — текст в окне решения. Карточки нет (404, модератору — и чужой город) — «Заявка не найдена» в панели.
export function ApplicationDrawer({
  me,
  id,
  onClose,
}: {
  me: Me;
  id: string | undefined;
  onClose: () => void;
}) {
  const t = useTranslations('captains.card');
  const api = useApi();
  const busy = useIsMutating({ mutationKey: DECIDE_KEY }) > 0;
  const card = useQuery({
    queryKey: applicationKey(id ?? ''),
    queryFn: () =>
      unwrap(
        api.GET('/v1/identity/captain-applications/{applicationId}', {
          params: { path: { applicationId: id ?? '' } },
        }),
      ),
    enabled: id !== undefined,
  });
  const missing =
    card.isError &&
    isApiError(card.error) &&
    (card.error.status === 403 || card.error.status === 404);
  const data = missing ? undefined : card.data;
  // окна и причины отказа — одной заявки: другая заявка (или закрытие) начинает с чистого
  const [state, setState] = useState<{ id?: string; dialog: Dialog; ineligible: string[] }>({
    dialog: null,
    ineligible: [],
  });
  const own = state.id === id ? state : { id, dialog: null, ineligible: [] };
  const setDialog = (dialog: Dialog) => setState({ ...own, dialog });
  const toast = useToast();
  const errorText = useErrorText();
  const ineligibleTexts = useIneligibilityTexts();
  const tc = useTranslations('captains');
  const hintId = useId();

  const onDecisionError = (e: ApiError): boolean => {
    switch (e.code) {
      case 'identity.application_closed':
        toast.info(tc('toasts.closed'));
        setState({ ...own, dialog: null, ineligible: [] });
        return true;
      case 'identity.captain_ineligible':
        setState({ ...own, dialog: null, ineligible: ineligibleTexts(e.reasons ?? []) });
        return true;
      case 'identity.application_not_found':
        toast.error(errorText(e));
        setState({ ...own, dialog: null });
        onClose();
        return true;
      default:
        return false;
    }
  };

  const app = data?.application;
  const pending = app?.status === 'pending';
  const canDecide = app !== undefined && pending && can(me, DECIDE_PERMISSION, app.city_id);
  const ok = data !== undefined && allChecksOk(data.checks);

  return (
    <Drawer
      open={id !== undefined}
      onClose={onClose}
      title={app?.nickname ?? t('title')}
      dismissible={!busy}
      footer={
        canDecide ? (
          <div className={styles.footer}>
            {!ok && (
              <p id={hintId} className={styles.note}>
                {tc('approve.disabled_hint')}
              </p>
            )}
            <Button variant="danger-outline" onClick={() => setDialog('reject')}>
              {tc('reject.submit')}
            </Button>
            <Button
              disabled={!ok}
              aria-describedby={ok ? undefined : hintId}
              onClick={() => setDialog('approve')}
            >
              {tc('approve.submit')}
            </Button>
          </div>
        ) : undefined
      }
    >
      {missing ? (
        <EmptyState
          illustration="captain"
          title={t('not_found_title')}
          text={t('not_found_text')}
        />
      ) : card.isError && !data ? (
        <ErrorState
          error={card.error}
          onRetry={() => void card.refetch()}
          retrying={card.isFetching}
        />
      ) : data ? (
        <CardBody
          me={me}
          card={data}
          ineligible={own.ineligible}
          onContacts={() => setDialog('contact')}
        />
      ) : (
        <Skeleton.Form fields={6} />
      )}
      {app && (
        <>
          <ApproveDialog
            id={app.id}
            open={own.dialog === 'approve'}
            onClose={() => setDialog(null)}
            onError={onDecisionError}
          />
          <RejectDialog
            id={app.id}
            open={own.dialog === 'reject'}
            onClose={() => setDialog(null)}
            onError={onDecisionError}
          />
          <ContactDialog
            id={app.id}
            open={own.dialog === 'contact'}
            onClose={() => setDialog(null)}
          />
        </>
      )}
    </Drawer>
  );
}

function CardBody({
  me,
  card,
  ineligible,
  onContacts,
}: {
  me: Me;
  card: Card;
  ineligible: string[];
  onContacts: () => void;
}) {
  const t = useTranslations('captains');
  const number = useNumber();
  const checkView = useCheckView();
  const resultOf = useApplicationResult();
  const rejection = useRejectionText();
  const app = card.application;
  const names = useCityNames([app.city_id], can(me, 'geo.cities.read'));
  const city = names.data?.items.find((c) => c.id === app.city_id);
  const headingId = useId();
  // Кнопки решения или «Показать» исчезли вместе с фокусом (перечитанная карточка, закрытое окно) — фокус на
  // панель, а не на body (ревью T8 I2, как StaffPanel). Только если панель — верхний диалог: под открытым окном
  // фокус не трогаем.
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const active = document.activeElement;
    if (active && active !== document.body) return;
    const own = panel.current?.closest('[role="dialog"]');
    const dialogs = document.querySelectorAll('[role="dialog"]');
    if (own && dialogs[dialogs.length - 1] === own) panel.current?.focus();
  });

  return (
    <div ref={panel} tabIndex={-1} className={styles.panel}>
      {ineligible.length > 0 && (
        <section className={styles.block} role="alert">
          <p className={styles.text}>{t('ineligible')}</p>
          <ul className={styles.list}>
            {ineligible.map((r) => (
              <li key={r}>{r}</li>
            ))}
          </ul>
        </section>
      )}
      {app.status !== 'pending' && <Decision app={app} />}
      <section className={styles.block} aria-labelledby={`${headingId}-a`}>
        <h3 id={`${headingId}-a`} className={styles.heading}>
          {t('card.applicant')}
        </h3>
        <dl className={styles.facts}>
          <dt>{t('table.city')}</dt>
          <dd>{city ? cityLabel(city) : '—'}</dd>
          <dt>{t('table.age')}</dt>
          <dd>{t('table.age_value', { count: card.applicant.age_years })}</dd>
          <dt>{t('table.account_age')}</dt>
          <dd>{t('table.account_age_value', { count: card.applicant.account_age_days })}</dd>
          <dt>{t('card.submitted')}</dt>
          <dd>
            <RelativeTime value={app.created_at} />
          </dd>
          <dt>{t('card.contacts')}</dt>
          <dd>{app.has_phone ? t('card.phone_given') : t('card.phone_missing')}</dd>
          <dt>{t('card.linked_accounts')}</dt>
          <dd>{number(card.linked_accounts)}</dd>
        </dl>
        {can(me, PII_PERMISSION, app.city_id) && (
          <div className={styles.row}>
            <Button variant="secondary" onClick={onContacts}>
              {t('contact.show')}
            </Button>
          </div>
        )}
      </section>
      <section className={styles.block} aria-labelledby={`${headingId}-f`}>
        <h3 id={`${headingId}-f`} className={styles.heading}>
          {t('card.form')}
        </h3>
        <dl className={styles.facts}>
          <Fact label={t('card.format')}>{app.proposed_format}</Fact>
          <Fact label={t('card.pitch')}>{app.proposed_pitch}</Fact>
          <Fact label={t('card.frequency')}>{app.play_frequency}</Fact>
          <Fact label={t('card.contact_link')}>{app.contact_link}</Fact>
          <Fact label={t('card.note')}>{app.note}</Fact>
        </dl>
      </section>
      <section className={styles.block} aria-labelledby={`${headingId}-c`}>
        <h3 id={`${headingId}-c`} className={styles.heading}>
          {t('card.checks')}
        </h3>
        <ul className={styles.checks}>
          {card.checks.map((c, i) => {
            const v = checkView(c);
            return (
              <li key={`${c.check}-${i}`} className={styles.check}>
                <span>{v.label}</span>
                <StatusChip tone={v.tone}>{v.text}</StatusChip>
              </li>
            );
          })}
        </ul>
        {hasUnavailable(card.checks) && <p className={styles.note}>{t('unavailable_hint')}</p>}
      </section>
      <section className={styles.block} aria-labelledby={`${headingId}-h`}>
        <h3 id={`${headingId}-h`} className={styles.heading}>
          {t('card.history')}
        </h3>
        {card.history.length === 0 ? (
          <p className={styles.note}>{t('card.history_empty')}</p>
        ) : (
          <ul className={styles.history}>
            {card.history.map((h) => {
              const r = resultOf(h.status);
              return (
                <li key={h.id} className={styles.historyItem}>
                  <span className={styles.row}>
                    <StatusChip tone={r.tone}>{r.text}</StatusChip>
                    <RelativeTime value={h.created_at} />
                  </span>
                  {h.rejection_code && (
                    <span className={styles.muted}>
                      {t('card.rejection', { reason: rejection(h.rejection_code) })}
                    </span>
                  )}
                  {h.comment && (
                    <span className={`${styles.muted} ${styles.pre}`}>
                      {t('card.comment', { comment: h.comment })}
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}

// Итог решённой (или отозванной) заявки — вместо кнопок решения.
function Decision({ app }: { app: Application }) {
  const t = useTranslations('captains');
  const resultOf = useApplicationResult();
  const rejection = useRejectionText();
  const r = resultOf(app.status);
  const who = app.decided_by_label ?? t('table.system');
  return (
    <section className={styles.block}>
      <h3 className={styles.heading}>{t('card.decision')}</h3>
      {app.decided_at ? (
        <p className={styles.text}>
          {t.rich('card.decided', {
            result: r.text,
            who,
            time: () => <RelativeTime value={app.decided_at ?? app.created_at} />,
          })}
        </p>
      ) : (
        <StatusChip tone={r.tone}>{r.text}</StatusChip>
      )}
      {app.rejection_code && (
        <p className={styles.text}>
          {t('card.rejection', { reason: rejection(app.rejection_code) })}
        </p>
      )}
      {app.comment && (
        <p className={`${styles.text} ${styles.pre}`}>
          {t('card.comment', { comment: app.comment })}
        </p>
      )}
    </section>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd className={styles.pre}>{children ?? '—'}</dd>
    </>
  );
}
