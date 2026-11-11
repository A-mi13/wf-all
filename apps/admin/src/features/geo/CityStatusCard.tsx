import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Button, Form, SelectField, useToast } from '../../ui';
import styles from './geo.module.css';
import { useUpdateCity } from './mutations';

type GeoCityDetails = components['schemas']['GeoCityDetails'];
type CityStatus = components['schemas']['CityStatus'];

const STATUSES: readonly CityStatus[] = ['waitlist', 'pilot', 'live'];

// Статус города (*; geo.cities.manage — карточку показывает страница): выбор и «Сохранить» →
// updateGeoCity {status}; значение формы — из карточки (после ответа — новое). Тот же статус —
// запроса нет. Отказ сервера (403, 404) — текстом формы. Незнакомый статус (список в контракте открытый) —
// опция «—», выбрать её нельзя; отправка с ним — ошибка поля, а не молчание (ревью G8, m12). readOnly —
// карточка перечитывается на новом языке: отправка недоступна (m18).
export function CityStatusCard({ city, readOnly }: { city: GeoCityDetails; readOnly?: boolean }) {
  const t = useTranslations('geo.city.status');
  const ts = useTranslations('geo.status');
  const ta = useTranslations('actions');
  const toast = useToast();
  const form = useForm<{ status: string }>({
    values: { status: city.status },
    resetOptions: { keepDirtyValues: true },
  });
  const update = useUpdateCity(city.id, ['geo', 'city', city.id, 'status']);

  const submit = async ({ status }: { status: string }) => {
    if (readOnly) return;
    const next = STATUSES.find((s) => s === status);
    if (!next) {
      form.setError(
        'status',
        { type: 'server', message: t('unknown_error') },
        { shouldFocus: true },
      );
      return;
    }
    if (next === city.status) return;
    const updated = await update.mutateAsync({ status: next });
    // форма — к ответу: сохранённое поле не «залипает» поверх будущих данных (раунд 1, I1)
    form.reset({ status: updated.status }, { keepDirtyValues: false });
    toast.success(t('saved'));
  };

  return (
    <Form form={form} onSubmit={submit} className={styles.panel}>
      <SelectField
        name="status"
        label={t('label')}
        options={[
          ...(STATUSES.some((s) => s === city.status)
            ? []
            : [{ value: city.status, label: ts('unknown'), disabled: true }]),
          ...STATUSES.map((s) => ({ value: s, label: ts(s) })),
        ]}
      />
      <div className={styles.row}>
        <Button type="submit" loading={update.isPending} disabled={readOnly}>
          {ta('save')}
        </Button>
      </div>
    </Form>
  );
}
