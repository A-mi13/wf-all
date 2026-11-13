import { useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { isApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useAdminMutation } from '../../api/mutation';
import { useLeaveGuard } from '../../app/leaveGuard';
import { useCan } from '../../app/session';
import {
  applyFieldErrors,
  Button,
  ConfirmDialog,
  DataState,
  Form,
  PageHeader,
  RelativeTime,
  Skeleton,
  StatusChip,
  TextField,
  useToast,
} from '../../ui';
import { useAppVersions, VERSIONS_KEY } from '../geo/queries';
import { compareVersions, raisesMin, SEMVER } from './semver';
import styles from './versions.module.css';

type AppVersions = components['schemas']['AppVersions'];
type AppVersionsList = components['schemas']['AppVersionsList'];
type AppVersionsPut = components['schemas']['AppVersionsPut'];

// https:// в нижнем регистре, без пробелов и управляющих символов; хост магазина — подсказка, проверяет сервер.
const STORE_URL = /^https:\/\/[^\s\p{Cc}]+$/u;

const PLATFORM_KEYS = ['ios', 'android'] as const;
type KnownPlatform = (typeof PLATFORM_KEYS)[number];
const known = (p: string): p is KnownPlatform => (PLATFORM_KEYS as readonly string[]).includes(p);

// «Версии приложения» (план B, Task 21; спека UI-фундамента §6.2; право platform.versions.read — каркас): две
// карточки (iOS, Android) — минимальная, рекомендуемая версии и ссылка на магазин. Сохранение — PUT платформы
// целиком (200), без «Отменить» (§4.10: внешнее последствие, version в теле нет — последняя запись побеждает).
// Без platform.versions.manage — только чтение; незаведённая платформа — пустая форма и «не задано».
export function VersionsPage() {
  const t = useTranslations('versions');
  const tn = useTranslations('nav');
  const q = useAppVersions();
  const canManage = useCan('platform.versions.manage');
  return (
    <div className={styles.page}>
      <PageHeader title={tn('versions')} subtitle={t('subtitle')} />
      <DataState
        query={q}
        skeleton={
          <div className={styles.grid}>
            <Skeleton.Form />
            <Skeleton.Form />
          </div>
        }
      >
        {(data) => (
          <div className={styles.grid}>
            {data.items.map((v) => (
              <PlatformCard key={v.platform} versions={v} readOnly={!canManage} />
            ))}
          </div>
        )}
      </DataState>
    </div>
  );
}

type Values = { min_version: string; recommended_version: string; store_url: string };

const toValues = (v: AppVersions): Values => ({
  min_version: v.min_version ?? '',
  recommended_version: v.recommended_version ?? '',
  store_url: v.store_url ?? '',
});

function PlatformCard({ versions, readOnly }: { versions: AppVersions; readOnly: boolean }) {
  const t = useTranslations('versions');
  const ta = useTranslations('actions');
  const api = useApi();
  const toast = useToast();
  const queryClient = useQueryClient();
  const titleId = useId();
  const platform = versions.platform;
  const name = known(platform) ? t(`platforms.${platform}`) : platform;
  // ответ сервера — новые значения формы (values): сохранённое не «грязное»; ввод при фоновом перечитывании цел
  const form = useForm<Values>({
    values: toValues(versions),
    resetOptions: { keepDirtyValues: true },
  });
  // несохранённый ввод — уход со страницы спрашивает (спека UI-фундамента §4.11, I-13)
  useLeaveGuard(form.formState.isDirty);
  const [confirm, setConfirm] = useState<Values | null>(null);

  const put = useAdminMutation(
    (body: AppVersionsPut, key: string) =>
      unwrap(
        api.PUT('/v1/app/versions/{platform}', {
          params: { path: { platform }, header: { 'Idempotency-Key': key } },
          body,
        }),
      ),
    {
      mutationKey: ['platform', 'versions', platform],
      onSuccess: (next) =>
        queryClient.setQueryData<AppVersionsList>(VERSIONS_KEY, (old) =>
          old ? { items: old.items.map((i) => (i.platform === next.platform ? next : i)) } : old,
        ),
    },
  );

  const save = async (v: Values) => {
    const next = await put.mutateAsync(v);
    form.reset(toValues(next), { keepDirtyValues: false });
    toast.success(t('saved', { platform: name }));
  };

  const submit = async (v: Values) => {
    if (readOnly) return;
    // без изменений — PUT нет: версия записи и журнал не растут пустой правкой (ревью G9-C, m-4)
    const cur = toValues(versions);
    if (
      v.min_version === cur.min_version &&
      v.recommended_version === cur.recommended_version &&
      v.store_url === cur.store_url
    ) {
      toast.info(t('no_changes'));
      return;
    }
    if (raisesMin(versions.min_version, v.min_version)) {
      // ошибка прошлого обычного сохранения — не в только что открытом окне (ревью G9-C, m-3)
      put.reset();
      setConfirm(v);
      return;
    }
    await save(v);
  };

  const unset = versions.version === null;
  const host = known(platform) ? t(`store_hint.${platform}`) : undefined;

  return (
    <section aria-labelledby={titleId} className={styles.card}>
      <div className={styles.head}>
        <h2 id={titleId} className={styles.title}>
          {name}
        </h2>
        {unset ? (
          <StatusChip tone="neutral">{t('not_set')}</StatusChip>
        ) : (
          versions.updated_at && (
            <span className={styles.muted}>
              {t.rich('updated', {
                time: () => <RelativeTime value={versions.updated_at ?? ''} />,
              })}
            </span>
          )
        )}
      </div>
      <Form form={form} onSubmit={submit} className={styles.form}>
        <TextField
          name="min_version"
          label={t('min')}
          hint={t('min_hint')}
          inputMode="decimal"
          autoComplete="off"
          spellCheck={false}
          readOnly={readOnly}
          rules={{ required: true, pattern: SEMVER }}
        />
        <TextField
          name="recommended_version"
          label={t('recommended')}
          hint={t('recommended_hint')}
          inputMode="decimal"
          autoComplete="off"
          spellCheck={false}
          readOnly={readOnly}
          rules={{
            required: true,
            pattern: SEMVER,
            validate: {
              // не ниже минимальной (сервер ответил бы minimum по body.recommended_version)
              minimum: (v: unknown) => {
                const min = form.getValues('min_version');
                return (
                  typeof v !== 'string' ||
                  !SEMVER.test(v) ||
                  !SEMVER.test(min) ||
                  compareVersions(v, min) >= 0
                );
              },
            },
          }}
        />
        <TextField
          name="store_url"
          type="url"
          label={t('store')}
          hint={host}
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="none"
          readOnly={readOnly}
          rules={{ required: true, pattern: STORE_URL }}
        />
        {!readOnly && (
          <div className={styles.row}>
            <Button type="submit" loading={put.isPending}>
              {ta('save')}
            </Button>
          </div>
        )}
      </Form>
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => {
          setConfirm(null);
          put.reset();
        }}
        onConfirm={() => {
          const v = confirm;
          if (!v) return;
          // ошибки полей (400 validation.failed) — у полей формы, окно закрыто; прочие — в окне (повтор после
          // обрыва — тот же ключ: useAdminMutation, те же значения)
          save(v).then(
            () => setConfirm(null),
            (e: unknown) => {
              if (isApiError(e) && applyFieldErrors(form, e)) {
                setConfirm(null);
                put.reset();
              }
            },
          );
        }}
        title={t('raise.title', { platform: name, version: confirm?.min_version ?? '' })}
        text={t('raise.text', { version: confirm?.min_version ?? '' })}
        confirmWord={confirm?.min_version ?? ''}
        confirmLabel={t('raise.confirm')}
        loading={put.isPending}
        error={put.error}
      />
    </section>
  );
}
