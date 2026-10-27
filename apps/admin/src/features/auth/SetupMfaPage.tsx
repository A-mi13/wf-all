import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useLogout } from '../../app/logout';
import type { Me } from '../../app/permissions';
import { ME_KEY, ONE_SHOT } from '../../app/query';
import { useSession, useStaffSwitched } from '../../app/session';
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
} from '../../ui';
import styles from './Auth.module.css';
import { AuthLayout } from './AuthLayout';
import { useCopy } from './copy';
import { RecoveryCodesPanel } from './RecoveryCodesPanel';

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
  // Чья настройка на экране (повторное ревью 27.10, m2): в другой вкладке прежний вышел, а следующий
  // принял приглашение и сканирует свой секрет там (cookie общая). Здесь секрет и коды прежнего
  // снимаются, а свой секрет не выпускается сам — новый выпуск сжёг бы QR в той вкладке; «Начать
  // заново» — явное решение. Выпуск прежнего SessionProvider удалил из кэша (meta ONE_SHOT).
  const session = useSession();
  const queryClient = useQueryClient();
  const staffId =
    session.status === 'setup' || session.status === 'active' ? session.me.id : undefined;
  // пришёл сюда сменой сотрудника без 401 (каркас прежнего → стадия setup) — тоже не выпускать самому
  const { switched, acknowledge } = useStaffSwitched(staffId);
  const [owner, setOwner] = useState<string | null | undefined>(() => (switched ? null : staffId));
  // шаги — только своей настройки; без сессии (вышли, 401) — ни шагов, ни выпуска, пока роутер уводит на вход
  // метка смены без 401 — и для вернувшегося прежнего (A → B → A): его настройка тоже могла уйти в другую
  // вкладку; «Начать заново» метку снимает (acknowledge)
  const mine = staffId !== undefined && staffId === owner && !switched;
  const other = staffId !== undefined && !mine;
  // «Начать заново» (здесь и в ScanStep) исчезает с экрана — фокус на заголовок страницы, не на body
  const heading = useRef<HTMLHeadingElement>(null);
  const focusTitle = () => heading.current?.focus();
  // настройка стала чужой (смена сотрудника), а фокус был в шаге (код) — шаг снят, фокус на заголовок;
  // текст состояния читалка прочтёт следом (без role=status — одно объявление)
  useEffect(() => {
    if (other) heading.current?.focus();
  }, [other]);
  const startHere = () => {
    focusTitle();
    queryClient.removeQueries({ queryKey: SETUP_KEY, exact: true });
    acknowledge();
    setOwner(staffId);
    setRescanned(false);
    setView({ kind: 'scan' });
  };
  // ack ответил wrong_stage, а стадия всё ещё setup: второй фактор не подтверждён (секрет сброшен
  // повторным принятием приглашения в другой вкладке) — снова сканирование с новым секретом.
  // Один раз: если и новый выпуск, и ack снова отвечают wrong_stage, — ошибка, а не петля.
  const [rescanned, setRescanned] = useState(false);
  const rescan = () => {
    if (rescanned) return false;
    setRescanned(true);
    setView({ kind: 'scan' });
    return true;
  };
  // общий компьютер: брошенную настройку можно закрыть — сервер разрешает выход в стадии setup
  const logout = useLogout();
  return (
    <AuthLayout
      title={tp('setup2fa.title')}
      lead={view.kind === 'scan' && mine ? t('lead') : undefined}
      wide
      titleRef={heading}
      below={
        <Button variant="link" loading={logout.isPending} onClick={() => logout.mutate()}>
          {t('logout')}
        </Button>
      }
    >
      {other && (
        <>
          <p className={styles.note}>{t('elsewhere')}</p>
          <Button size="lg" block onClick={startHere}>
            {t('restart')}
          </Button>
        </>
      )}
      {mine && view.kind === 'scan' && (
        <ScanStep
          onRestart={focusTitle}
          onConfirmed={(codes) => setView({ kind: 'codes', codes })}
          onAlreadyConfirmed={() => setView({ kind: 'confirmed' })}
        />
      )}
      {mine && view.kind === 'codes' && <CodesStep codes={view.codes} onRescan={rescan} />}
      {mine && view.kind === 'confirmed' && <ConfirmedStep onRescan={rescan} />}
    </AuthLayout>
  );
}

// Секрет base32 группами по 4 — легче вводить руками; копируется без пробелов.
const grouped = (secret: string) => secret.replace(/(.{4})(?=.)/g, '$1 ');

// recovery-codes/ack: 204 — новая cookie стадии active (D9), клиент только перечитывает me.
// staff.wrong_stage: me перечитывается — настройка завершена (другая вкладка) → роутер уводит на
// стартовую; стадия всё ещё setup (2FA не подтверждена) → onRescan; false — ошибка остаётся.
function useAck(onRescan: () => boolean) {
  const api = useApi();
  const session = useSession();
  const queryClient = useQueryClient();
  return useAdminMutation(
    (_: void, key: string) =>
      api.POST('/v1/staff/me/mfa/recovery-codes/ack', {
        params: { header: { 'Idempotency-Key': key } },
      }),
    {
      onSuccess: () => session.refresh(),
      onError: async (e) => {
        if (e.code !== 'staff.wrong_stage') return;
        await session.refresh();
        if (queryClient.getQueryData<Me>(ME_KEY)?.stage === 'setup') onRescan();
      },
    },
  );
}

function ScanStep({
  onRestart,
  onConfirmed,
  onAlreadyConfirmed,
}: {
  onRestart: () => void;
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
    // смена сотрудника (другая вкладка) удаляет, а не перезапускает выпуск (session.tsx)
    meta: ONE_SHOT,
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
    onRestart();
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
          busy={confirm.isPending}
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

function CodesStep({ codes, onRescan }: { codes: string[]; onRescan: () => boolean }) {
  const t = useTranslations('auth.setup');
  const ack = useAck(onRescan);
  const form = useForm<{ saved: boolean }>({ defaultValues: { saved: false } });
  const saved = useWatch({ control: form.control, name: 'saved' });

  return (
    <>
      <RecoveryCodesPanel codes={codes} />
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
function ConfirmedStep({ onRescan }: { onRescan: () => boolean }) {
  const t = useTranslations('auth.setup');
  const ack = useAck(onRescan);
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
