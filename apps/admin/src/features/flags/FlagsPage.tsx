import { useNavigate, useSearch } from '@tanstack/react-router';
import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import {
  Button,
  DataState,
  EmptyState,
  ErrorState,
  PageHeader,
  SearchBox,
  Segmented,
  Skeleton,
  useIsMobile,
} from '../../ui';
import { useFeatureFlags } from '../geo/queries';
import { CITY_STATUSES } from '../geo/search';
import styles from './flags.module.css';
import { FlagsList } from './FlagsList';
import { FlagsMatrix } from './FlagsMatrix';
import { type FlagsSearch, parseFlagsSearch, toCitiesQuery } from './search';
import { useFlagToggle } from './toggle';
import { useAllCities } from './useAllCities';

type FeatureFlag = components['schemas']['FeatureFlag'];
type StatusOption = 'all' | (typeof CITY_STATUSES)[number];

// «Флаги функций» (экран 12, макет 07; план B, Task 22; спека UI-фундамента §6.2): раздел открыт на любой ширине
// (право platform.flags.read проверяет каркас). Десктоп — матрица «функция × город» (закреплённый первый столбец,
// города — горизонтальная виртуализация, до 2000), телефон — список по флагам. Поиск и статус города — в адресе
// (/flags?q=&status=), сужают столбцы; полный список городов флага при переключении — из флага (useFlagToggle).
export function FlagsPage() {
  const t = useTranslations();
  const tp = useTranslations('flagsPage');
  const search = parseFlagsSearch(useSearch({ strict: false }));
  const navigate = useNavigate();
  const set = (next: FlagsSearch) =>
    void navigate({ to: paths.flags, search: next, replace: true });
  const flags = useFeatureFlags();
  const flagCities = useMemo(
    () => [...new Set(flags.data?.items.flatMap((f) => f.enabled_city_ids) ?? [])],
    [flags.data],
  );
  const cities = useAllCities(toCitiesQuery(search), flagCities);
  const toggler = useFlagToggle();
  const mobile = useIsMobile();
  const filtered = search.q !== undefined || search.status !== undefined;

  const body = (items: FeatureFlag[]) => {
    if (!cities.loaded)
      return cities.error ? (
        <ErrorState error={cities.error} onRetry={cities.refetch} retrying={cities.isFetching} />
      ) : (
        <Skeleton.Table columns={5} rows={Math.min(items.length, 8)} />
      );
    return (
      <>
        <div className={styles.summary}>
          {cities.capped ? (
            <span className={styles.warning}>{tp('capped', { count: cities.cities.length })}</span>
          ) : filtered && cities.cities.length === 0 ? (
            <span>{tp('no_cities')}</span>
          ) : (
            <span>{tp('cities_count', { count: cities.cities.length })}</span>
          )}
          {filtered && (
            <Button variant="link" onClick={() => set({ flag: search.flag })}>
              {t('actions.resetFilters')}
            </Button>
          )}
        </div>
        {mobile ? (
          <FlagsList
            flags={items}
            cities={cities.cities}
            toggler={toggler}
            open={search.flag}
            onOpen={(flag) => set({ ...search, flag })}
          />
        ) : (
          <FlagsMatrix flags={items} cities={cities.cities} toggler={toggler} />
        )}
      </>
    );
  };

  const statuses: StatusOption[] = ['all', ...CITY_STATUSES];
  return (
    <div className={styles.page}>
      <PageHeader title={t('nav.flags')} subtitle={tp('subtitle')} />
      <div className={styles.toolbar}>
        <SearchBox
          value={search.q ?? ''}
          onChange={(q) => set({ ...search, q: q.trim() ? q : undefined })}
          placeholder={tp('search')}
        />
        <Segmented<StatusOption>
          wide
          label={tp('status')}
          options={statuses.map((s) => ({ value: s, text: tp(`statuses.${s}`) }))}
          value={search.status ?? 'all'}
          onChange={(s) => set({ ...search, status: s === 'all' ? undefined : s })}
        />
      </div>
      <DataState
        query={flags}
        skeleton={<Skeleton.Table columns={5} rows={6} />}
        isEmpty={(d) => d.items.length === 0}
        empty={
          <EmptyState illustration="corner" title={tp('empty_title')} text={tp('empty_text')} />
        }
      >
        {(d) => body(d.items)}
      </DataState>
      <p className={styles.note}>{tp('note')}</p>
      {toggler.sheet}
    </div>
  );
}
