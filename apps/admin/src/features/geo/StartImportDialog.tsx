import { useIsMutating } from '@tanstack/react-query';
import { useId } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, isApiError } from '../../api/client';
import { Button, Form, Modal, SelectField } from '../../ui';
import styles from './imports.module.css';
import { useStartImport } from './importMutations';
import { useGeoCountries } from './queries';

type Values = { country: string };

// «Запустить импорт» (план B, Task 20; geo.imports.run; панель в адресе ?start=1): страна — из всех стран
// справочника (включённые и выключенные: контракт ставит импорт и выключенной) → startGeoImport → 202 {job_id} —
// экран показывает строку задачи. 409 geo.import_in_progress с job_id — строка той задачи (её можно отменить), без
// памяти клиента (I-2); без job_id (импорт оператора) — экран говорит об этом и подсвечивает строку журнала.
// Выбранная страна — ввод: Overlay.dirty спрашивает «Уйти без сохранения?». Неизвестный исход — окно открыто с
// ошибкой, повтор той же страны — тем же ключом (useAdminMutation).
export function StartImportDialog({
  open,
  onClose,
  onStarted,
  onBusy,
}: {
  open: boolean;
  onClose: () => void;
  onStarted: (jobId: number, country: string, already: boolean) => void;
  onBusy: (country: string) => void;
}) {
  return open ? <StartFlow onClose={onClose} onStarted={onStarted} onBusy={onBusy} /> : null;
}

function StartFlow({
  onClose,
  onStarted,
  onBusy,
}: {
  onClose: () => void;
  onStarted: (jobId: number, country: string, already: boolean) => void;
  onBusy: (country: string) => void;
}) {
  const t = useTranslations('geo.imports.dialog');
  const formId = useId();
  const start = useStartImport();
  const busy = useIsMutating({ mutationKey: ['geo', 'imports', 'start'] }) > 0;
  const countries = useGeoCountries();
  const form = useForm<Values>({ defaultValues: { country: '' } });

  const options = (countries.data?.items ?? []).map((c) => ({
    value: c.code,
    label: c.is_enabled
      ? `${c.name} (${c.code})`
      : t('country_off', { name: `${c.name} (${c.code})` }),
  }));

  const submit = async ({ country }: Values) => {
    const res = await start.mutateAsync(country);
    form.reset({ country });
    onStarted(res.job_id, country, false);
  };
  // 409 geo.import_in_progress — не отказ формы: задача страны уже есть, экран показывает её
  const onError = (e: ApiError) => {
    if (!isApiError(e, 'geo.import_in_progress')) return false;
    const { country } = form.getValues();
    form.reset({ country });
    if (e.jobId !== undefined) onStarted(e.jobId, country, true);
    else onBusy(country);
    return true;
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t('title')}
      description={t('text')}
      dismissible={!busy}
      dirty={form.formState.isDirty}
      footer={
        <Button type="submit" form={formId} loading={busy}>
          {t('submit')}
        </Button>
      }
    >
      <div className={styles.jobBody}>
        <Form id={formId} form={form} onSubmit={submit} onError={onError}>
          <SelectField
            name="country"
            label={t('country')}
            placeholder={t('country_placeholder')}
            options={options}
            rules={{ required: true }}
          />
        </Form>
      </div>
    </Modal>
  );
}
