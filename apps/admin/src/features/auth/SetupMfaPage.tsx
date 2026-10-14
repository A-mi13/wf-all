import { useQuery } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useSession } from '../../app/session';
import {
  Button,
  CheckboxField,
  ErrorState,
  Form,
  FormError,
  LoadingRows,
  OtpInput,
  type OtpInputHandle,
  QrCode,
  useErrorText,
  useToast,
} from '../../ui';
import styles from './Auth.module.css';
import { AuthLayout } from './AuthLayout';

// Выпуск секрета: POST, но читается как запрос — один раз на экран. Ключ кэша свой; gcTime 0 —
// секрет уходит из кэша, как только шаг сканирования закрыт (коды или уход с экрана).
const SETUP_KEY = ['staff', 'mfa', 'setup'] as const;

type View = { kind: 'scan' } | { kind: 'codes'; codes: string[] } | { kind: 'confirmed' };

// Настройка 2FA (спека §9.4 п. 3, §4.5; макет 07 «Мой доступ» — второй фактор; работает и на
// телефоне — раскладка резиновая). Шаги: QR и секрет → код из приложения (staffMfaConfirm) → 10
// резервных кодов, «Я сохранил коды», «Готово» (staffRecoveryAck: сервер ставит новую cookie, me
// перечитывается — стадия active, роутер уводит на стартовую). Секрет, QR и коды живут только в
// памяти экрана: ни черновиков, ни storage, ни кэша Query после шага.
export function SetupMfaPage() {
  const t = useTranslations('auth.setup');
  const tp = useTranslations('pages');
  const [view, setView] = useState<View>({ kind: 'scan' });
  return (
    <AuthLayout
      title={tp('setup2fa.title')}
      lead={view.kind === 'scan' ? t('lead') : undefined}
      wide
    >
      {view.kind === 'scan' && (
        <ScanStep
          onConfirmed={(codes) => setView({ kind: 'codes', codes })}
          onAlreadyConfirmed={() => setView({ kind: 'confirmed' })}
        />
      )}
      {view.kind === 'codes' && <CodesStep codes={view.codes} />}
      {view.kind === 'confirmed' && <ConfirmedStep />}
    </AuthLayout>
  );
}

// Секрет base32 группами по 4 — легче вводить руками; копируется без пробелов.
const grouped = (secret: string) => secret.replace(/(.{4})(?=.)/g, '$1 ');

function useCopy() {
  const t = useTranslations('auth.setup');
  const toast = useToast();
  return async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t('copied'));
    } catch {
      toast.error(t('copy_failed'));
    }
  };
}

// recovery-codes/ack: 204 — новая cookie стадии active (D9), клиент только перечитывает me.
// staff.wrong_stage — настройка уже завершена (другая вкладка): тоже перечитать me.
function useAck() {
  const api = useApi();
  const session = useSession();
  return useAdminMutation(
    (_: void, key: string) =>
      api.POST('/v1/staff/me/mfa/recovery-codes/ack', {
        params: { header: { 'Idempotency-Key': key } },
      }),
    {
      onSuccess: () => session.refresh(),
      onError: (e) => {
        if (e.code === 'staff.wrong_stage') return session.refresh();
      },
    },
  );
}

function ScanStep({
  onConfirmed,
  onAlreadyConfirmed,
}: {
  onConfirmed: (codes: string[]) => void;
  onAlreadyConfirmed: () => void;
}) {
  const t = useTranslations('auth.setup');
  const errorText = useErrorText();
  const api = useApi();
  const copy = useCopy();
  const errorId = useId();
  const otp = useRef<OtpInputHandle>(null);
  const [code, setCode] = useState('');

  // Повтор выпуска выдаёт НОВЫЙ секрет — прежний QR перестаёт работать. Поэтому: один запрос на
  // экран (staleTime ∞, без повторов и перезапросов по фокусу; StrictMode — тот же запрос в
  // полёте), новый ключ идемпотентности — только на явное «Начать заново» или «Повторить».
  const secret = useQuery({
    queryKey: SETUP_KEY,
    queryFn: () =>
      unwrap(
        api.POST('/v1/staff/me/mfa/setup', {
          params: { header: { 'Idempotency-Key': crypto.randomUUID() } },
        }),
      ),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const confirm = useAdminMutation(
    (v: { code: string }, key: string) =>
      unwrap(
        api.POST('/v1/staff/me/mfa/confirm', {
          params: { header: { 'Idempotency-Key': key } },
          body: v,
        }),
      ),
    {
      gcTime: 0, // коды — только в состоянии экрана, не в кэше мутаций
      onSuccess: (d) => onConfirmed(d.recovery_codes),
      onError: (e) => {
        if (e.code === 'staff.wrong_stage') onAlreadyConfirmed();
        else otp.current?.reset();
      },
    },
  );

  // Перезагрузка после confirm до ack: секрет больше не выдаётся (409) — экран «уже подтверждён».
  const alreadyConfirmed = isApiError(secret.error, 'staff.wrong_stage');
  useEffect(() => {
    if (alreadyConfirmed) onAlreadyConfirmed();
  }, [alreadyConfirmed, onAlreadyConfirmed]);

  // Ответы с секретами не кэшируются на сервере: повтор тем же ключом — replay_unavailable.
  const restart = () => {
    confirm.reset();
    otp.current?.reset();
    void secret.refetch();
  };
  const replay =
    isApiError(secret.error, 'idempotency.replay_unavailable') ||
    isApiError(confirm.error, 'idempotency.replay_unavailable');
  if (replay)
    return (
      <>
        <p role="alert" className={styles.error}>
          {t('replay')}
        </p>
        <Button size="lg" block onClick={restart}>
          {t('restart')}
        </Button>
      </>
    );
  if (alreadyConfirmed || secret.isPending || secret.isFetching) return <LoadingRows rows={3} />;
  if (secret.isError)
    return <ErrorState error={secret.error} onRetry={() => void secret.refetch()} />;

  const sendCode = (c: string) => {
    if (confirm.isPending) return;
    confirm.mutate({ code: c });
  };
  const failure = confirm.error;
  const message = !failure
    ? null
    : failure.code === 'staff.mfa_invalid'
      ? t('invalid')
      : errorText(failure);

  return (
    <>
      <div className={styles.scan}>
        <div className={styles.qr}>
          <QrCode value={secret.data.otpauth_uri} label={t('qr')} />
        </div>
        <div className={styles.secretBlock}>
          <p className={styles.fieldLabel}>{t('secret')}</p>
          <p className={styles.secret}>{grouped(secret.data.secret)}</p>
          <div className={styles.row}>
            <Button
              variant="secondary"
              aria-label={t('copy_secret')}
              onClick={() => void copy(secret.data.secret)}
            >
              {t('copy')}
            </Button>
          </div>
        </div>
      </div>
      <div className={styles.factor}>
        <p aria-hidden="true" className={styles.fieldLabel}>
          {t('code')}
        </p>
        <OtpInput
          ref={otp}
          label={t('code')}
          onComplete={sendCode}
          onChange={setCode}
          invalid={Boolean(failure)}
          disabled={confirm.isPending}
          describedBy={message ? errorId : undefined}
        />
        {message && (
          <p role="alert" id={errorId} className={styles.error}>
            {message}
          </p>
        )}
      </div>
      <Button
        size="lg"
        block
        loading={confirm.isPending}
        disabled={code.length < 6}
        onClick={() => sendCode(code)}
      >
        {t('confirm')}
      </Button>
    </>
  );
}

function CodesStep({ codes }: { codes: string[] }) {
  const t = useTranslations('auth.setup');
  const copy = useCopy();
  const ack = useAck();
  const form = useForm<{ saved: boolean }>({ defaultValues: { saved: false } });
  const saved = useWatch({ control: form.control, name: 'saved' });

  // Файл собирается на клиенте (Blob) — коды не уходят в сеть.
  const download = () => {
    const text = [t('file_header'), '', ...codes, ''].join('\n');
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = t('file_name');
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <>
      <h2 className={styles.sectionTitle}>{t('codes_title')}</h2>
      <p className={styles.note}>{t('codes_text')}</p>
      {/* role="list": Safari снимает семантику списка с list-style: none */}
      <ul role="list" aria-label={t('codes_title')} className={styles.codes}>
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className={styles.row}>
        <Button variant="secondary" onClick={() => void copy(codes.join('\n'))}>
          {t('copy_codes')}
        </Button>
        <Button variant="secondary" onClick={download}>
          {t('download')}
        </Button>
      </div>
      <Form form={form} onSubmit={(v) => (v.saved ? ack.mutateAsync() : undefined)}>
        <CheckboxField name="saved" label={t('saved')} />
        <Button type="submit" size="lg" block disabled={!saved} loading={ack.isPending}>
          {t('finish')}
        </Button>
      </Form>
    </>
  );
}

// Второй фактор подтверждён, но коды не подтверждены (перезагрузка после confirm): коды не
// вернуть — их перевыпуск в «Моём доступе»; «Продолжить» завершает настройку (ack).
function ConfirmedStep() {
  const t = useTranslations('auth.setup');
  const ack = useAck();
  return (
    <>
      <h2 className={styles.stepTitle}>{t('confirmed_title')}</h2>
      <p className={styles.note}>{t('confirmed_text')}</p>
      <FormError error={ack.error} />
      <Button size="lg" block loading={ack.isPending} onClick={() => ack.mutate()}>
        {t('continue')}
      </Button>
    </>
  );
}
