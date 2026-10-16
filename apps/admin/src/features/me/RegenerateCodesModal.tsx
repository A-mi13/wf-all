import { useIsMutating, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { type ApiError, noReplay, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { Button, Modal, OtpInput, type OtpInputHandle, useCountdown, useErrorText } from '../../ui';
import { RecoveryCodesPanel } from '../auth/RecoveryCodesPanel';
import { isLock, useLockout } from './lockout';
import styles from './me.module.css';
import { MY_MFA_KEY } from './queries';

// Перевыпуск резервных кодов (спека §4.7, M9): подтверждается ТОЛЬКО кодом из приложения —
// резервного кода здесь нет ни полем, ни переключателем. Успех — 10 новых кодов один раз
// (RecoveryCodesPanel: скопировать, .txt), «Готово» закрывает окно: коды жили только в его
// состоянии (закрытый оверлей не рендерится), мутация — gcTime 0 (ни кодов, ни TOTP в кэше
// мутаций после окна). Неудачи считаются в блокировку: attempts_left = 0 — useLockout. Пока запрос
// идёт, окно не закрывается: сервер уже мог выпустить новые коды (старые сгорели), и закрытое окно
// потеряло бы их. Пока коды на экране, окно закрывает только «Готово» (Esc, фон и «×» — нет): старые
// коды уже сгорели, и случайное закрытие оставило бы сотрудника без кодов при «10 из 10».
// После разблокировки сессии запрос не повторяется (noReplay, ревью I1): код из приложения
// одноразовый — экран стирает его и просит новый.
const REGENERATE_KEY = ['staff', 'my', 'recovery-codes'] as const;

export function RegenerateCodesModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslations('me.regenerate');
  const [codes, setCodes] = useState<string[] | null>(null);
  const busy = useIsMutating({ mutationKey: REGENERATE_KEY }) > 0;
  return (
    <Modal
      open={open}
      dismissible={!busy && !codes}
      onClose={() => {
        setCodes(null);
        onClose();
      }}
      title={t('title')}
      description={codes ? undefined : t('text')}
      footer={
        codes && (
          <Button
            onClick={() => {
              setCodes(null);
              onClose();
            }}
          >
            {t('done')}
          </Button>
        )
      }
    >
      {codes ? <RecoveryCodesPanel codes={codes} /> : <CodeStep onIssued={setCodes} />}
    </Modal>
  );
}

function CodeStep({ onIssued }: { onIssued: (codes: string[]) => void }) {
  const t = useTranslations('me.regenerate');
  const tm = useTranslations('auth.mfa');
  const tme = useTranslations('me');
  const api = useApi();
  const queryClient = useQueryClient();
  const errorText = useErrorText();
  const lockout = useLockout();
  const errorId = useId();
  const otp = useRef<OtpInputHandle>(null);
  const [code, setCode] = useState('');
  const [failure, setFailure] = useState<ApiError | null>(null);
  const wait = useCountdown();
  const limited = wait.left > 0;

  const m = useAdminMutation(
    (v: { code: string }, key: string) =>
      unwrap(
        api.POST('/v1/staff/me/mfa/recovery-codes', {
          params: { header: { 'Idempotency-Key': key } },
          body: v,
          ...noReplay(),
        }),
      ),
    {
      mutationKey: REGENERATE_KEY,
      gcTime: 0, // коды и TOTP — не в кэше мутаций после окна
      onSuccess: (d) => {
        onIssued(d.recovery_codes);
        void queryClient.invalidateQueries({ queryKey: MY_MFA_KEY });
      },
      onError: (e) => {
        if (isLock(e.code, e.attemptsLeft)) return lockout();
        if (e.code === 'ratelimit.exceeded') wait.start(e.retryAfter ?? 60);
        setFailure(e);
        otp.current?.reset();
      },
    },
  );

  // Конец отсчёта 429: код, набранный за время ожидания, стирается (тот же урок, что N1 входа).
  const wasLimited = useRef(false);
  useEffect(() => {
    if (wasLimited.current && !limited) otp.current?.reset();
    wasLimited.current = limited;
  }, [limited]);

  const send = (c: string) => {
    if (limited || m.isPending) return;
    setFailure(null);
    m.mutate({ code: c });
  };

  // auth.reauth_required: разблокировка прошла, а запрос не повторён — «введите новый код»;
  // разблокировку отклонили (выход, блокировка) — молчим: страница уходит на вход (уборка №6, R3).
  const message =
    !failure ||
    (failure.code === 'ratelimit.exceeded' && !limited) ||
    (failure.code === 'auth.reauth_required' && !failure.replaySkipped)
      ? null
      : failure.code === 'auth.reauth_required'
        ? tme('reauth_retry')
        : failure.code === 'staff.mfa_invalid' && failure.attemptsLeft !== undefined
          ? tm('invalid', { count: failure.attemptsLeft })
          : errorText(failure);

  return (
    <div className={styles.factor}>
      <p aria-hidden="true" className={styles.fieldLabel}>
        {t('code')}
      </p>
      <OtpInput
        ref={otp}
        label={t('code')}
        onComplete={send}
        onChange={setCode}
        invalid={Boolean(message)}
        busy={m.isPending}
        autoFocus
        describedBy={message ? errorId : undefined}
      />
      {message && (
        <p role="alert" id={errorId} className={styles.error}>
          {message}
        </p>
      )}
      <Button
        loading={m.isPending}
        disabled={limited || code.length < 6}
        onClick={() => send(code)}
      >
        {limited ? t('wait', { seconds: wait.left }) : t('submit')}
      </Button>
    </div>
  );
}
