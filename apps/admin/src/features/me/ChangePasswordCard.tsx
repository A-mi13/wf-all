import { useQueryClient } from '@tanstack/react-query';
import { useId, useRef, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, noReplay } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import {
  Button,
  FieldError,
  Form,
  OtpInput,
  type OtpInputHandle,
  PasswordField,
  TextField,
  useErrorText,
  useToast,
} from '../../ui';
import { isLock, useLockout } from './lockout';
import styles from './me.module.css';
import { MY_SESSIONS_KEY } from './queries';

type PasswordChange = components['schemas']['StaffPasswordChange'];

type Values = {
  current_password: string;
  new_password: string;
  new_password_repeat: string;
  code: string;
  recovery_code: string;
};

const EMPTY: Values = {
  current_password: '',
  new_password: '',
  new_password_repeat: '',
  code: '',
  recovery_code: '',
};

// Смена своего пароля (*; спека §4.7, D7): текущий пароль, новый дважды и второй фактор — код из
// приложения или резервный код, ровно одно. Успех — 204, остальные свои сеансы погашены (список
// перечитывается). Неверный текущий пароль (staff.password_invalid) и неверный код
// (staff.mfa_invalid) считаются в блокировку вместе: «осталось N попыток», 0 — useLockout.
// Пароли и коды — только в памяти формы: без черновика, мутация — gcTime 0.
export function ChangePasswordCard() {
  const t = useTranslations('me.password');
  const tme = useTranslations('me');
  const errorText = useErrorText();
  const titleId = useId();
  const codeId = useId();
  const errorId = useId();
  const api = useApi();
  const queryClient = useQueryClient();
  const toast = useToast();
  const lockout = useLockout();
  const form = useForm<Values>({ defaultValues: EMPTY });
  const otp = useRef<OtpInputHandle>(null);
  // Ячейки OtpInput держат цифры сами: новый ключ — пустые ячейки без фокуса (после успеха).
  const [round, setRound] = useState(0);
  const [mode, setMode] = useState<'app' | 'recovery'>('app');
  const [failure, setFailure] = useState<ApiError | null>(null);

  const change = useAdminMutation(
    (body: PasswordChange, key: string) =>
      api.POST('/v1/staff/me/password', {
        params: { header: { 'Idempotency-Key': key } },
        body,
        // код второго фактора одноразовый: после разблокировки не повторять (ревью I1)
        ...noReplay(),
      }),
    {
      // пароли и код — не в кэше мутаций: gcTime 0 и reset() после ответа (карточка остаётся на
      // экране, и без reset мутация с переменными жила бы в кэше, пока её наблюдают)
      gcTime: 0,
      onSuccess: () => {
        toast.success(t('done'));
        form.reset(EMPTY);
        setRound((r) => r + 1);
        void queryClient.invalidateQueries({ queryKey: MY_SESSIONS_KEY });
      },
    },
  );

  // Неудачный фактор стирается: код из приложения устаревает, резервный мог сгореть.
  const clearFactor = (focus: boolean) => {
    form.setValue('code', '');
    form.setValue('recovery_code', '');
    if (mode === 'app') {
      if (focus) otp.current?.reset();
      else setRound((r) => r + 1);
    } else if (focus) form.setFocus('recovery_code');
  };

  const onError = (e: ApiError) => {
    if (isLock(e.code, e.attemptsLeft)) {
      lockout();
      return true;
    }
    if (e.code === 'staff.password_invalid') {
      setFailure(e);
      clearFactor(false);
      form.setValue('current_password', '');
      form.setFocus('current_password');
      return true;
    }
    // разблокировку отклонили (выход, блокировка): страница уходит на вход — ни «Вход
    // подтверждён», ни FormError (уборка №6, R3)
    if (e.code === 'auth.reauth_required' && !e.replaySkipped) return true;
    // неверный код или разблокировка посреди запроса (код уже использован, запрос не повторён)
    if (e.code === 'staff.mfa_invalid' || e.code === 'auth.reauth_required') {
      setFailure(e);
      clearFactor(true);
      return true;
    }
    return false; // validation.failed — по полям, остальное — FormError
  };

  const message = !failure
    ? null
    : failure.code === 'auth.reauth_required'
      ? tme('reauth_retry')
      : failure.attemptsLeft === undefined
        ? errorText(failure) // без остатка попыток — общий текст по коду, не немая ошибка
        : failure.code === 'staff.password_invalid'
          ? t('password_invalid', { count: failure.attemptsLeft })
          : t('mfa_invalid', { count: failure.attemptsLeft });

  return (
    <section aria-labelledby={titleId} className={styles.card}>
      <h2 id={titleId} className={styles.cardTitle}>
        {t('title')}
      </h2>
      <p className={styles.text}>{t('text')}</p>
      <Form
        form={form}
        onSubmit={async (v) => {
          setFailure(null);
          try {
            await change.mutateAsync({
              current_password: v.current_password,
              new_password: v.new_password,
              // ровно одно из двух (StaffPasswordChange)
              ...(mode === 'app' ? { code: v.code } : { recovery_code: v.recovery_code }),
            });
          } finally {
            change.reset();
          }
        }}
        onError={onError}
      >
        <PasswordField
          name="current_password"
          label={t('current')}
          autoComplete="current-password"
          rules={{ required: true }}
        />
        <PasswordField
          name="new_password"
          label={t('new')}
          hint={t('new_hint')}
          autoComplete="new-password"
          rules={{ required: true }}
        />
        <PasswordField
          name="new_password_repeat"
          label={t('repeat')}
          autoComplete="new-password"
          rules={{
            required: true,
            validate: { mismatch: (v) => v === form.getValues('new_password') },
          }}
        />
        {mode === 'app' ? (
          <div className={styles.factor}>
            {/* подпись видна; читалке группу называет aria-label OtpInput */}
            <p aria-hidden="true" className={styles.fieldLabel}>
              {t('code')}
            </p>
            <Controller
              name="code"
              control={form.control}
              rules={{ validate: { required: (v: string) => v.length === 6 } }}
              render={({ field, fieldState }) => (
                <OtpInput
                  key={round}
                  ref={otp}
                  label={t('code')}
                  onChange={field.onChange}
                  // полный код не отправляет форму сам: нужны ещё пароли
                  onComplete={() => {}}
                  invalid={Boolean(fieldState.error) || failure?.code === 'staff.mfa_invalid'}
                  busy={change.isPending}
                  describedBy={
                    [fieldState.error ? codeId : '', message ? errorId : '']
                      .filter(Boolean)
                      .join(' ') || undefined
                  }
                />
              )}
            />
            <FieldError name="code" id={codeId} />
          </div>
        ) : (
          <TextField
            name="recovery_code"
            label={t('recovery')}
            hint={t('recovery_hint')}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            rules={{ required: true }}
          />
        )}
        {message && (
          <p role="alert" id={errorId} className={styles.error}>
            {message}
          </p>
        )}
        <div className={styles.spread}>
          <Button
            variant="link"
            disabled={change.isPending}
            onClick={() => {
              setMode(mode === 'app' ? 'recovery' : 'app');
              form.setValue('code', '');
              form.setValue('recovery_code', '');
              form.clearErrors(['code', 'recovery_code']);
            }}
          >
            {mode === 'app' ? t('use_recovery') : t('use_app')}
          </Button>
          <Button type="submit" loading={change.isPending}>
            {t('submit')}
          </Button>
        </div>
      </Form>
    </section>
  );
}
