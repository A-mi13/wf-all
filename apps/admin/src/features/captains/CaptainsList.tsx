import { useInfiniteQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { can, type Me } from '../../app/permissions';
import { paths } from '../../app/paths';
import {
  Button,
  type Column,
  DataTable,
  EmptyState,
  Form,
  infiniteTableProps,
  type Option,
  RelativeTime,
  SelectField,
  StatusChip,
} from '../../ui';
import { useAllCities } from '../flags/useAllCities';
import { cityLabel, useCityNames } from '../staff/cities';
import { useApplicationResult } from './labels';
import { captainsListKey, READ_PERMISSION } from './queries';
import { type CaptainsSearch, parseCaptainsSearch, STATUSES } from './search';
import styles from './captains.module.css';

type Application = components['schemas']['IdentityCaptainApplication'];

// Очередь заявок (спека identity §7.4, макет 07 «captains»; план I-4, Task 8): DataTable по
// listCaptainApplications — ник, город, возраст, стаж, когда подана; у решённых — итог, кто решил, когда. Чипы
// статуса и город — search-параметры маршрута (экран их читает и меняет навигацией, replace). Город: модератору —
// только свои города (scoped: чужой город сервер всё равно отдал бы пустой страницей), остальным — все города
// справочника. «Открыть» в строке — карточка, панель ?application=<id>. Возраст и стаж — вместо «Матчей» и
// «Надёжности» макета (данных players/stats нет, Приложение 4).
export function CaptainsList({
  me,
  search,
  activeId,
  onOpen,
}: {
  me: Me;
  search: CaptainsSearch;
  activeId?: string;
  onOpen: (row: Application) => void;
}) {
  const t = useTranslations('captains');
  const api = useApi();
  const navigate = useNavigate();
  const resultOf = useApplicationResult();
  const status = search.status ?? 'pending';
  const { city } = search;

  const list = useInfiniteQuery({
    queryKey: captainsListKey(status, city),
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/identity/captain-applications', {
          params: { query: { status, city_id: city, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.next_cursor ?? undefined,
  });

  const canCities = can(me, 'geo.cities.read');
  const ids = [...new Set(list.data?.pages.flatMap((p) => p.items.map((a) => a.city_id)) ?? [])];
  const names = useCityNames(ids.slice(0, 100), canCities);
  const cityName = (id: string) => {
    const c = names.data?.items.find((x) => x.id === id);
    return c ? cityLabel(c) : null;
  };

  // Фильтры — в адрес: replace, чтобы «Назад» не листал каждый щелчок.
  const update = (patch: Partial<CaptainsSearch>) => {
    const next: CaptainsSearch = { ...search, ...patch };
    if (!next.status) delete next.status;
    if (!next.city) delete next.city;
    void navigate({ to: paths.captains, search: next, replace: true });
  };

  const decided = status !== 'pending';
  const columns: Column<Application>[] = [
    {
      id: 'nickname',
      header: t('table.nickname'),
      cell: (a) => <span className={styles.name}>{a.nickname}</span>,
    },
    {
      id: 'city',
      header: t('table.city'),
      width: 'lg',
      cell: (a) => cityName(a.city_id) ?? <span className={styles.muted}>—</span>,
    },
    {
      id: 'age',
      header: t('table.age'),
      width: 'sm',
      align: 'end',
      cell: (a) => t('table.age_value', { count: a.age_years }),
    },
    {
      id: 'account_age',
      header: t('table.account_age'),
      width: 'sm',
      align: 'end',
      cell: (a) => t('table.account_age_value', { count: a.account_age_days }),
    },
    {
      id: 'submitted',
      header: t('table.submitted'),
      width: 'md',
      priority: decided ? 'low' : 'high',
      cell: (a) => <RelativeTime value={a.created_at} />,
    },
    ...(decided
      ? ([
          {
            id: 'result',
            header: t('table.result'),
            width: 'md',
            cell: (a) => {
              const r = resultOf(a.status);
              return <StatusChip tone={r.tone}>{r.text}</StatusChip>;
            },
          },
          {
            id: 'decided_by',
            header: t('table.decided_by'),
            width: 'md',
            cell: (a) =>
              a.decided_by_label ?? <span className={styles.muted}>{t('table.system')}</span>,
          },
          {
            id: 'decided',
            header: t('table.decided'),
            width: 'md',
            cell: (a) => (a.decided_at ? <RelativeTime value={a.decided_at} /> : '—'),
          },
        ] satisfies Column<Application>[])
      : []),
    {
      id: 'open',
      header: t('table.actions'),
      width: 'sm',
      align: 'end',
      cell: (a) => (
        <Button
          variant="secondary"
          aria-label={t('table.open_label', { name: a.nickname })}
          onClick={() => onOpen(a)}
        >
          {t('table.open')}
        </Button>
      ),
    },
  ];

  return (
    <DataTable
      label={t('table.label')}
      tableId="captains"
      columns={columns}
      rowKey={(a) => a.id}
      activeRowKey={activeId}
      {...infiniteTableProps(list)}
      chips={{
        label: t('tabs.label'),
        value: status,
        options: STATUSES.map((s) => ({ value: s, label: t(`tabs.${s}`) })),
        // pending — по умолчанию, в адрес не пишется; незнакомое отбрасывает парсер адреса
        onChange: (v) => update({ status: parseCaptainsSearch({ status: v }).status }),
      }}
      filters={
        canCities
          ? {
              active: city ? 1 : 0,
              content: <CityFilter me={me} value={city} onChange={(c) => update({ city: c })} />,
            }
          : undefined
      }
      empty={
        !decided && !city ? (
          <EmptyState illustration="captain" title={t('empty')} text={t('empty_text')} />
        ) : undefined
      }
      dirty={Boolean(city || search.status)}
      onReset={() => void navigate({ to: paths.captains, search: {}, replace: true })}
    />
  );
}

// Город очереди: модератору (право чтения заявок scoped) — только его города, остальным — все города справочника.
function CityFilter({
  me,
  value,
  onChange,
}: {
  me: Me;
  value: string | undefined;
  onChange: (city: string | undefined) => void;
}) {
  return me.permissions[READ_PERMISSION] === 'scoped' ? (
    <ScopedCities me={me} value={value} onChange={onChange} />
  ) : (
    <AllCities value={value} onChange={onChange} />
  );
}

function ScopedCities(props: {
  me: Me;
  value: string | undefined;
  onChange: (city: string | undefined) => void;
}) {
  const names = useCityNames(props.me.city_ids);
  const options = props.me.city_ids.map((id) => {
    const c = names.data?.items.find((x) => x.id === id);
    return { value: id, label: c ? cityLabel(c) : id };
  });
  return <CitySelect options={options} value={props.value} onChange={props.onChange} />;
}

function AllCities(props: {
  value: string | undefined;
  onChange: (city: string | undefined) => void;
}) {
  const all = useAllCities({}, props.value ? [props.value] : []);
  const options = all.cities.map((c) => ({ value: c.id, label: cityLabel(c) }));
  return <CitySelect options={options} value={props.value} onChange={props.onChange} />;
}

function CitySelect({
  options,
  value,
  onChange,
}: {
  options: Option[];
  value: string | undefined;
  onChange: (city: string | undefined) => void;
}) {
  const t = useTranslations('captains.table');
  // значение формы — из адреса; изменение сразу уходит в адрес
  const form = useForm<{ city: string }>({ values: { city: value ?? '' } });
  return (
    <Form form={form} onSubmit={() => {}}>
      <SelectField
        name="city"
        label={t('city_filter')}
        placeholder={t('city_all')}
        options={options}
        rules={{
          onChange: (e: { target: { value: string } }) => onChange(e.target.value || undefined),
        }}
      />
    </Form>
  );
}
