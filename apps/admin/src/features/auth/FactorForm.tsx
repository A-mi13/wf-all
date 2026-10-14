import { type ReactNode, useId, useRef, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { ApiError } from '../../api/client';
import { useAdminMutation } from '../../api/mutation';
import {
  Button,
  Form,
  OtpInput,
  type OtpInputHandle,
  TextField,
  useCountdown,
  useErrorText,
} from '../../ui';
import styles from './Auth.module.css';

// Второй фактор — ровно одно поле (StaffFactorRequest).
export type Factor = { code: string } | { recovery_code: string };

export type FactorFormProps = {
  // Запрос с ключом идемпотентности действия: staffVerifyMfa (вход) или staffUnlock (разблокировка).
  send: (factor: Factor, idempotencyKey: string) => Promise<unknown>;
  submitLabel: string;
  onSuccess: () => unknown; // 204: промис (refresh) держит форму занятой, пока он не кончится
  onLocked: () => void; // «Ко входу» после блокировки (attempts_left = 0)
  onExpired?: () => void; // 401 auth.unauthenticated — сессии больше нет
  onWrongStage?: () => void; // 409 staff.wrong_stage — сессия уже не ждёт этот шаг
  extra?: ReactNode; // ссылка рядом с «Использовать резервный код» (не при блокировке)
};

// Ввод второго фактора — шаг 2 входа и окно разблокировки (спека §9.4 п. 1, §4.6–4.7):
// - 6 ячеек OtpInput с автоотправкой или «Использовать резервный код» — обычное поле;
// - staff.mfa_invalid — «Осталось N попыток…» по attempts_left; 0 — доступ закрыт на час: ввод
//   недоступен, вместо отправки — «Ко входу» (сервер уже погасил все сессии сотрудника);
// - 429 — отсчёт по Retry-After, отправка недоступна до конца;
// - код и резервный код никуда не сохраняются (черновиков у этой формы нет).
export function FactorForm({
  send,
  submitLabel,
  onSuccess,
  onLocked,
  onExpired,
  onWrongStage,
  extra,
}: FactorFormProps) {
  const t = useTranslations('auth.mfa');
  const errorText = useErrorText();
  const errorId = useId();
  const otp = useRef<OtpInputHandle>(null);
  const [mode, setMode] = useState<'app' | 'recovery'>('app');
  const [code, setCode] = useState('');
  const [failure, setFailure] = useState<ApiError | null>(null);
  const wait = useCountdown();
  const recovery = useForm<{ recovery_code: string }>({ defaultValues: { recovery_code: '' } });

  const m = useAdminMutation(send, {
    onSuccess: () => onSuccess(),
    onError: (e) => {
      if (e.code === 'auth.unauthenticated' && onExpired) return onExpired();
      if (e.code === 'staff.wrong_stage' && onWrongStage) return onWrongStage();
      if (e.code === 'ratelimit.exceeded') wait.start(e.retryAfter ?? 60);
      setFailure(e);
      otp.current?.reset();
    },
  });

  const locked = failure?.code === 'staff.mfa_invalid' && failure.attemptsLeft === 0;
  const limited = wait.left > 0;
  const blocked = locked || limited;

  // Код из ячеек: ошибку разбирает onError мутации. Резервный код — через Form (mutateAsync).
  const sendCode = (c: string) => {
    if (blocked || m.isPending) return;
    setFailure(null);
    m.mutate({ code: c });
  };

  const message = !failure
    ? null
    : failure.code === 'staff.mfa_invalid' && failure.attemptsLeft !== undefined
      ? failure.attemptsLeft === 0
        ? t('locked')
        : t('invalid', { count: failure.attemptsLeft })
      : errorText(failure);

  const submitButton = locked ? (
    <Button size="lg" block onClick={onLocked}>
      {t('to_login')}
    </Button>
  ) : (
    <Button
      size="lg"
      block
      type={mode === 'recovery' ? 'submit' : 'button'}
      loading={m.isPending}
      disabled={limited || (mode === 'app' && code.length < 6)}
      onClick={mode === 'app' ? () => sendCode(code) : undefined}
    >
      {limited ? t('wait', { seconds: wait.left }) : submitLabel}
    </Button>
  );
  const alert = message && (
    <p role="alert" id={errorId} className={styles.error}>
      {message}
    </p>
  );

  return (
    <>
      {mode === 'app' ? (
        <>
          <div className={styles.factor}>
            {/* подпись видна; читалке группу называет aria-label OtpInput */}
            <p aria-hidden="true" className={styles.fieldLabel}>
              {t('code')}
            </p>
            <OtpInput
              ref={otp}
              label={t('code')}
              onComplete={sendCode}
              onChange={setCode}
              invalid={Boolean(failure)}
              disabled={m.isPending || blocked}
              autoFocus
              describedBy={message ? errorId : undefined}
            />
            {alert}
          </div>
          {submitButton}
        </>
      ) : (
        <Form
          form={recovery}
          showError={false}
          onSubmit={(v) => {
            if (blocked) return;
            setFailure(null);
            return m.mutateAsync({ recovery_code: v.recovery_code });
          }}
          // все ошибки показывает форма фактора сама (попытки, блокировка, 429)
          onError={() => true}
        >
          <TextField
            name="recovery_code"
            label={t('recovery')}
            hint={t('recovery_hint')}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            autoFocus
            disabled={blocked}
            rules={{ required: true }}
          />
          {alert}
          {submitButton}
        </Form>
      )}
      <div className={styles.actions}>
        <Button
          variant="link"
          disabled={locked || m.isPending}
          onClick={() => {
            setMode(mode === 'app' ? 'recovery' : 'app');
            setCode('');
            if (!locked) setFailure(null);
          }}
        >
          {mode === 'app' ? t('use_recovery') : t('use_app')}
        </Button>
        {!locked && extra}
      </div>
    </>
  );
}
