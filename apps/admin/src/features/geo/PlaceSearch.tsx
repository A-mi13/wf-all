import { useNavigate } from '@tanstack/react-router';
import { useEffect, useMemo, useRef } from 'react';
import { useForm } from 'react-hook-form';
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

// Поиск и страна мест — у окна (PlaceFilter): «Другое место» возвращает к прежним результатам.
export type PlaceFilter = { q: string; country: string };

// Контракт listGeoPlaces: q — 1–100 символов (ревью G8, m5).
const Q_MAX = 100;

// Поиск места GeoNames — шаг 1 активации города и ручной привязки (Task 18, 19; V1 п. 9). q — от 1
// символа (пустой сервер отверг бы; без него — подсказка, запроса нет), не длиннее 100; страна — фильтр
// поиска (у привязки по умолчанию — страна города). Места — компактный список выбора (DataTable select):
// клик и стрелки выделяют строку, Enter и двойной клик — «Выбрать». Выделение держит окно, кнопка
// действия — PlaceAction в закреплённом подвале окна; прокручивается только список. Выбор держится, пока
// его строка в результатах: смена страны или поиска без неё снимает выбор (ревью V1, m1). focusList —
// возврат со шага 2 («Другое место»): фокус на список (или на поиск, если списка нет), не на body (V1, m2).
export function PlaceSearch({
  selected,
  onSelect,
  onChoose,
  filter,
  onFilterChange,
  focusList,
  bounded,
}: {
  selected: GeoPlace | null;
  onSelect: (p: GeoPlace | null) => void;
  onChoose: (p: GeoPlace) => void; // Enter / двойной клик по свободному месту
  filter: PlaceFilter;
  onFilterChange: (f: PlaceFilter) => void;
  focusList?: boolean;
  // окно по центру (Modal) высоты не задаёт — шаг ограничен сам, иначе прокручивалось бы всё окно
  bounded?: boolean;
}) {
  const t = useTranslations('geo.activate');
  const format = useFormatter();
  const form = useForm<{ country: string }>({ defaultValues: { country: filter.country } });
  const countries = useGeoCountries();
  const text = filter.q.trim().slice(0, Q_MAX);
  const country = filter.country;
  const places = useGeoPlaces({ q: text, ...(country ? { country } : {}) }, text.length > 0);
  const rows = useMemo(
    () => (text ? places.data?.pages.flatMap((p) => p.items) : []),
    [text, places.data],
  );

  // строки выбранного места в результатах нет (другая страна, другой поиск, ещё грузятся) — выбор снят
  const shown = selected !== null && (rows ?? []).some((p) => p.geoname_id === selected.geoname_id);
  useEffect(() => {
    if (selected && !shown) onSelect(null);
  }, [selected, shown, onSelect]);

  const root = useRef<HTMLDivElement>(null);
  const pendingFocus = useRef(Boolean(focusList));
  useEffect(() => {
    if (!pendingFocus.current) return;
    const list = root.current?.querySelector<HTMLElement>('[role="listbox"]');
    if (list) list.focus();
    else if (!text) root.current?.querySelector<HTMLElement>('input[type="search"]')?.focus();
    else return; // результаты ещё грузятся — ждём список
    pendingFocus.current = false;
  }, [rows, text]);

  return (
    <div ref={root} className={cx(styles.searchStep, bounded && styles.searchStepBounded)}>
      <Form form={form} onSubmit={() => {}}>
        <SelectField
          name="country"
          label={t('place_country')}
          options={[
            { value: '', label: t('place_country_all') },
            ...withCurrent(
              (countries.data?.items ?? []).map((c) => ({ value: c.code, label: c.name })),
              filter.country,
            ),
          ]}
          rules={{
            onChange: (e: { target: { value: string } }) =>
              onFilterChange({ ...filter, country: e.target.value }),
          }}
        />
      </Form>
      <DataTable
        label={t('places_label')}
        columns={[]}
        rowKey={(p) => String(p.geoname_id)}
        {...(text ? infiniteTableProps(places) : { rows: [], onRetry: () => {} })}
        search={{
          value: filter.q,
          onChange: (q) => onFilterChange({ ...filter, q }),
          placeholder: t('search'),
        }}
        summary={text.length > 0}
        empty={
          text ? undefined : (
            <EmptyState illustration="ball" title={t('prompt')} text={t('prompt_text')} />
          )
        }
        select={{
          value: shown && selected ? String(selected.geoname_id) : null,
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
