import { useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useFormatter, useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import {
  Button,
  DataTable,
  EmptyState,
  Form,
  infiniteTableProps,
  SelectField,
  StatusChip,
} from '../../ui';
import { cx } from '../../ui/cx';
import styles from './geo.module.css';
import { withCurrent } from './options';
import { useGeoCountries, useGeoPlaces } from './queries';

type GeoPlace = components['schemas']['GeoPlace'];

// Поиск места GeoNames — шаг 1 активации города и ручной привязки (Task 18, 19; V1 п. 9). q — от 1
// символа (пустой сервер отверг бы; без него — подсказка, запроса нет); страна — фильтр поиска
// (defaultCountry — страна города у привязки). Места — компактный список выбора (DataTable select):
// клик и стрелки выделяют строку, Enter и двойной клик — «Выбрать». Выделение держит окно, кнопка
// действия — PlaceAction в закреплённом подвале окна; прокручивается только список.
export function PlaceSearch({
  selected,
  onSelect,
  onChoose,
  defaultCountry = '',
  bounded,
}: {
  selected: GeoPlace | null;
  onSelect: (p: GeoPlace) => void;
  onChoose: (p: GeoPlace) => void; // Enter / двойной клик по свободному месту
  defaultCountry?: string;
  // окно по центру (Modal) высоты не задаёт — шаг ограничен сам, иначе прокручивалось бы всё окно
  bounded?: boolean;
}) {
  const t = useTranslations('geo.activate');
  const format = useFormatter();
  const [q, setQ] = useState('');
  const filter = useForm<{ country: string }>({ defaultValues: { country: defaultCountry } });
  const country = useWatch({ control: filter.control, name: 'country' });
  const countries = useGeoCountries();
  const text = q.trim();
  const places = useGeoPlaces({ q: text, ...(country ? { country } : {}) }, text.length > 0);

  return (
    <div className={cx(styles.searchStep, bounded && styles.searchStepBounded)}>
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
        columns={[]}
        rowKey={(p) => String(p.geoname_id)}
        {...(text ? infiniteTableProps(places) : { rows: [], onRetry: () => {} })}
        search={{ value: q, onChange: setQ, placeholder: t('search') }}
        summary={text.length > 0}
        empty={text ? undefined : <EmptyState title={t('prompt')} text={t('prompt_text')} />}
        select={{
          value: selected ? String(selected.geoname_id) : null,
          onChange: onSelect,
          onActivate: (p) => {
            onSelect(p);
            if (!p.city_id) onChoose(p);
          },
          option: (p) => (
            <span className={styles.option}>
              <span className={styles.optionHead}>
                <span className={styles.name}>{p.name}</span>
                {p.city_id && <StatusChip tone="neutral">{t('already')}</StatusChip>}
              </span>
              <span className={styles.muted}>
                {[p.ascii_name, p.region_name, p.country.name, format.number(p.population)]
                  .filter(Boolean)
                  .join(' · ')}
              </span>
            </span>
          ),
        }}
      />
    </div>
  );
}

// Действие выбранного места — для подвала окна: свободное — «Выбрать», заведённое — «Открыть
// город» (окно закрывается, переход на город), ничего не выделено — «Выбрать» недоступна.
export function PlaceAction({
  selected,
  onChoose,
  onClose,
}: {
  selected: GeoPlace | null;
  onChoose: (p: GeoPlace) => void;
  onClose: () => void;
}) {
  const t = useTranslations('geo.activate');
  const navigate = useNavigate();
  if (!selected) return <Button disabled>{t('choose')}</Button>;
  const cityId = selected.city_id;
  if (cityId)
    return (
      <Button
        variant="secondary"
        aria-label={t('open_city_label', { name: selected.name })}
        onClick={() => {
          onClose();
          void navigate({ to: paths.city, params: { cityId } });
        }}
      >
        {t('open_city')}
      </Button>
    );
  return (
    <Button
      aria-label={t('choose_label', { name: selected.name })}
      onClick={() => onChoose(selected)}
    >
      {t('choose')}
    </Button>
  );
}
