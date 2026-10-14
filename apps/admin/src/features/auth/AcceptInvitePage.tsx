import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useDraft } from '../../app/draft';
import { paths } from '../../app/paths';
import { useSession } from '../../app/session';
import { Button, Form, FormError, PasswordField, TextField, useCountdown } from '../../ui';
import styles from './Auth.module.css';
import { AuthLayout } from './AuthLayout';

// Пауза между повторными отправками кода приглашения (спека §4.3): сервер всегда отвечает 202 и
// сам не шлёт чаще; отсчёт на клиенте — чтобы не жать впустую.
const RESEND_PAUSE_S = 60;

type Values = { email: string; password: string; passwordRepeat: string; code: string };
// Черновик — только почта: пароль и код из письма в sessionStorage не кладутся.
type AcceptDraft = { email: string };

// Принятие приглашения (спека §9.4 п. 2, §4.3–4.4; дорисовано — в макетах экрана нет). Почта
// вводится руками: в ссылке из письма её нет, и из адреса она не читается. Пароль дважды
// (несовпадение — ошибка поля на клиенте), требования — подсказкой; правила пароля проверяет
// сервер (validation.failed по body.password раскладывает Form). Успех — стадия setup: me
// перечитывается, роутер уводит на /setup-2fa.
export function AcceptInvitePage() {
  const t = useTranslations('auth.accept');
  const tp = useTranslations('pages');
  const api = useApi();
  const session = useSession();
  const [draft, saveDraft] = useDraft<AcceptDraft>(paths.accept);
  const form = useForm<Values>({
    defaultValues: { email: draft?.email ?? '', password: '', passwordRepeat: '', code: '' },
  });

  // Анонимные мутации (security: []): без Idempotency-Key.
  const submitWait = useCountdown();
  const [limit, setLimit] = useState<ApiError | null>(null);
  const accept = useAdminMutation(
    (v: { email: string; code: string; password: string }) =>
      unwrap(api.POST('/v1/staff/auth/invite/accept', { body: v })),
    { onSuccess: () => session.refresh() },
  );

  const resendWait = useCountdown();
  const [resent, setResent] = useState(false);
  const resend = useAdminMutation(
    (v: { email: string }) => api.POST('/v1/staff/auth/invite/resend', { body: v }),
    {
      onSuccess: () => {
        setResent(true);
        resendWait.start(RESEND_PAUSE_S);
      },
      onError: (e) => {
        setResent(false);
        if (e.code === 'ratelimit.exceeded') resendWait.start(e.retryAfter ?? RESEND_PAUSE_S);
      },
    },
  );
  const askResend = async () => {
    if (resendWait.left > 0 || !(await form.trigger('email'))) return;
    const email = form.getValues('email');
    saveDraft({ email });
    resend.mutate({ email });
  };

  const limited = submitWait.left > 0;
  return (
    <AuthLayout title={tp('accept.title')} lead={t('lead')}>
      <Form
        form={form}
        onSubmit={(v) => {
          if (limited) return;
          saveDraft({ email: v.email });
          return accept.mutateAsync({ email: v.email, code: v.code, password: v.password });
        }}
        onError={(e) => {
          if (e.code !== 'ratelimit.exceeded') return false;
          setLimit(e);
          submitWait.start(e.retryAfter ?? 60);
          return true;
        }}
      >
        <TextField
          name="email"
          type="email"
          label={t('email')}
          autoComplete="username"
          autoFocus
          rules={{ required: true }}
        />
        <PasswordField
          name="password"
          label={t('password')}
          hint={t('password_hint')}
          autoComplete="new-password"
          rules={{ required: true }}
        />
        <PasswordField
          name="passwordRepeat"
          label={t('passwordRepeat')}
          autoComplete="new-password"
          rules={{
            required: true,
            validate: { mismatch: (v) => v === form.getValues('password') },
          }}
        />
        <TextField
          name="code"
          label={t('code')}
          hint={t('code_hint')}
          inputMode="numeric"
          autoComplete="one-time-code"
          rules={{ required: true }}
        />
        <div className={styles.actions}>
          <Button
            variant="link"
            loading={resend.isPending}
            disabled={resendWait.left > 0}
            onClick={() => void askResend()}
          >
            {resendWait.left > 0 ? t('resend_wait', { seconds: resendWait.left }) : t('resend')}
          </Button>
        </div>
        {resent && (
          <p role="status" className={styles.note}>
            {t('resent')}
          </p>
        )}
        <FormError error={resend.error} />
        {limited && <FormError error={limit} />}
        <Button type="submit" size="lg" block loading={accept.isPending} disabled={limited}>
          {limited ? t('wait', { seconds: submitWait.left }) : t('submit')}
        </Button>
      </Form>
    </AuthLayout>
  );
}
