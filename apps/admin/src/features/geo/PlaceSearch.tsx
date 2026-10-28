import { useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useFormatter, useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import {
  Button,
  type Column,
  DataTable,
  EmptyState,
  Form,
  infiniteTableProps,
  SelectField,
} from '../../ui';
import styles from './geo.module.css';
import { withCurrent } from './options';
import { useGeoCountries, useGeoPlaces } from './queries';

type GeoPlace = components['schemas']['GeoPlace'];

// Поиск места GeoNames — шаг 1 активации города и ручной привязки (Task 18, 19). q — от 1 символа
// (пустой сервер отверг бы; без него — подсказка, запроса нет); страна — фильтр поиска (defaultCountry —
// страна города у привязки). Место с городом (city_id) не выбирается — «уже заведён» и ссылка на город.
export function PlaceSearch({
  onChoose,
  onClose,
  defaultCountry = '',
}: {
  onChoose: (p: GeoPlace) => void;
  onClose: () => void; // закрыть окно перед переходом на заведённый город
  defaultCountry?: string;
}) {
  const t = useTranslations('geo.activate');
  const navigate = useNavigate();
  const format = useFormatter();
  const [q, setQ] = useState('');
  const filter = useForm<{ country: string }>({ defaultValues: { country: defaultCountry } });
  const country = useWatch({ control: filter.control, name: 'country' });
  const countries = useGeoCountries();
  const text = q.trim();
  const places = useGeoPlaces({ q: text, ...(country ? { country } : {}) }, text.length > 0);

  const columns: Column<GeoPlace>[] = [
    {
      id: 'place',
      header: t('columns.place'),
      cell: (p) => (
        <span className={styles.place}>
          <span className={styles.name}>{p.name}</span>
          <span className={styles.muted}>{p.ascii_name}</span>
        </span>
      ),
    },
    { id: 'region', header: t('columns.region'), cell: (p) => p.region_name ?? '—' },
    { id: 'country', header: t('columns.country'), cell: (p) => p.country.name },
    {
      id: 'population',
      header: t('columns.population'),
      align: 'end',
      cell: (p) => format.number(p.population),
    },
    {
      id: 'action',
      header: t('columns.action'),
      align: 'end',
      width: 'md',
      cell: (p) => {
        const cityId = p.city_id;
        if (cityId)
          return (
            <span className={styles.place}>
              <span className={styles.muted}>{t('already')}</span>
              <Button
                variant="link"
                aria-label={t('open_city_label', { name: p.name })}
                onClick={() => {
                  onClose();
                  void navigate({ to: paths.city, params: { cityId } });
                }}
              >
                {t('open_city')}
              </Button>
            </span>
          );
        return (
          <Button
            variant="secondary"
            aria-label={t('choose_label', { name: p.name })}
            onClick={() => onChoose(p)}
          >
            {t('choose')}
          </Button>
        );
      },
    },
  ];

  return (
    <>
      <Form form={filter} onSubmit={() => {}}>
        <SelectField
          name="country"
          label={t('place_country')}
          options={[
            { value: '', label: t('place_country_all') },
            ...withCurrent(
              (countries.data?.items ?? []).map((c) => ({ value: c.code, label: c.name })),
              defaultCountry,
            ),
          ]}
        />
      </Form>
      <DataTable
        label={t('places_label')}
        columns={columns}
        rowKey={(p) => String(p.geoname_id)}
        {...(text ? infiniteTableProps(places) : { rows: [], onRetry: () => {} })}
        search={{ value: q, onChange: setQ, placeholder: t('search') }}
        summary={text.length > 0}
        empty={text ? undefined : <EmptyState title={t('prompt')} text={t('prompt_text')} />}
      />
    </>
  );
}
