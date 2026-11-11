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
// запроса нет. Отказ сервера (403, 404) — текстом формы.
export function CityStatusCard({ city }: { city: GeoCityDetails }) {
  const t = useTranslations('geo.city.status');
  const ts = useTranslations('geo.status');
  const ta = useTranslations('actions');
  const toast = useToast();
  const form = useForm<{ status: string }>({ values: { status: city.status } });
  const update = useUpdateCity(city.id, ['geo', 'city', city.id, 'status']);

  const submit = async ({ status }: { status: string }) => {
    const next = STATUSES.find((s) => s === status);
    if (!next || next === city.status) return;
    await update.mutateAsync({ status: next });
    toast.success(t('saved'));
  };

  return (
    <Form form={form} onSubmit={submit} className={styles.panel}>
      <SelectField
        name="status"
        label={t('label')}
        options={STATUSES.map((s) => ({ value: s, label: ts(s) }))}
      />
      <div className={styles.row}>
        <Button type="submit" loading={update.isPending}>
          {ta('save')}
        </Button>
      </div>
    </Form>
  );
}
