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
import { FactorForm } from './FactorForm';

type Credentials = { email: string; password: string };
// Черновик экрана входа — только почта (пароль и коды в sessionStorage не кладутся).
type LoginDraft = { email: string };

// Вход (спека §9.3–9.4 п. 1, §4.6; макеты 07 и 08). Шаг выбирает стадия сессии: без сессии — почта
// и пароль; mfa_pending (и после перезагрузки) — второй фактор. Успех шага 2 перечитывает me —
// стадия active, и роутер уводит на ?return= или стартовую (guard.ts). Шаг 2 истёк (5 минут) —
// 401 auth.unauthenticated: me перечитан, сессия anonymous, снова шаг 1 с пояснением.
export function LoginPage() {
  const t = useTranslations('auth');
  const tp = useTranslations('pages');
  const session = useSession();
  const [draft, saveDraft] = useDraft<LoginDraft>(paths.login);
  const [expired, setExpired] = useState(false);
  // «Ко входу» после блокировки и «Другая почта или пароль»: брошенная сессия mfa_pending
  // запоминается по id, и её шаг 2 больше не показывается — шаг 1, пока не придёт другая
  // сессия (успешный вход гасит прежнюю на сервере). Неудачный вход оставляет прежнюю сессию — и
  // экран остаётся на шаге 1 с ошибкой.
  const [abandoned, setAbandoned] = useState<{ id: string | null } | null>(null);
  const step2 =
    session.status === 'mfa_pending' && !(abandoned && abandoned.id === session.sessionId);
  const restart = () => {
    setAbandoned({ id: session.status === 'mfa_pending' ? session.sessionId : null });
    void session.refresh();
  };

  return (
    <AuthLayout
      title={tp('login.title')}
      lead={step2 ? t('mfa.lead') : t('login.lead')}
      footnote={t('common.footnote')}
    >
      {step2 ? (
        <FactorStep email={draft?.email} onExpired={() => setExpired(true)} onRestart={restart} />
      ) : (
        <PasswordStep
          defaultEmail={draft?.email}
          expired={expired}
          onSubmit={(email) => {
            saveDraft({ email });
            setExpired(false);
          }}
        />
      )}
    </AuthLayout>
  );
}

function PasswordStep({
  defaultEmail,
  expired,
  onSubmit,
}: {
  defaultEmail?: string;
  expired: boolean;
  onSubmit: (email: string) => void;
}) {
  const t = useTranslations('auth.login');
  const api = useApi();
  const session = useSession();
  const form = useForm<Credentials>({
    defaultValues: { email: defaultEmail ?? '', password: '' },
  });
  // 429: отсчёт по Retry-After; отправка (кнопкой и Enter) недоступна до конца.
  const wait = useCountdown();
  const [limit, setLimit] = useState<ApiError | null>(null);
  const limited = wait.left > 0;
  // Анонимная мутация (security: []): без Idempotency-Key, ключ хука не используется.
  const login = useAdminMutation(
    (v: Credentials) => unwrap(api.POST('/v1/staff/auth/login', { body: v })),
    // 200 {stage: mfa_pending} — me перечитывается, и экран переходит к шагу 2. gcTime 0 — пароль
    // в переменных мутации не остаётся в кэше после шага.
    { gcTime: 0, onSuccess: () => session.refresh() },
  );

  return (
    <Form
      form={form}
      onSubmit={(v) => {
        if (limited) return;
        onSubmit(v.email);
        return login.mutateAsync(v);
      }}
      onError={(e) => {
        if (e.code !== 'ratelimit.exceeded') return false;
        setLimit(e);
        wait.start(e.retryAfter ?? 60);
        return true;
      }}
    >
      {expired && (
        <p role="status" className={styles.note}>
          {t('expired')}
        </p>
      )}
      <TextField
        name="email"
        type="email"
        label={t('email')}
        autoComplete="username"
        autoFocus
        rules={{ required: true }}
      />
      <PasswordField name="password" label={t('password')} rules={{ required: true }} />
      {limited && <FormError error={limit} />}
      <Button type="submit" size="lg" block loading={login.isPending} disabled={limited}>
        {limited ? t('wait', { seconds: wait.left }) : t('submit')}
      </Button>
    </Form>
  );
}

function FactorStep({
  email,
  onExpired,
  onRestart,
}: {
  email?: string;
  onExpired: () => void;
  onRestart: () => void;
}) {
  const t = useTranslations('auth.mfa');
  const api = useApi();
  const session = useSession();
  return (
    <>
      {email && <p className={styles.note}>{t('as', { email })}</p>}
      <FactorForm
        send={(factor, key) =>
          api.POST('/v1/staff/auth/mfa', {
            params: { header: { 'Idempotency-Key': key } },
            body: factor,
          })
        }
        submitLabel={t('submit')}
        // 204 — новый токен уже в cookie (D9): me перечитывается, стадия active
        onSuccess={() => session.refresh()}
        onExpired={onExpired}
        onWrongStage={() => void session.refresh()}
        onLocked={onRestart}
        extra={
          <Button variant="link" onClick={onRestart}>
            {t('restart')}
          </Button>
        }
      />
    </>
  );
}
