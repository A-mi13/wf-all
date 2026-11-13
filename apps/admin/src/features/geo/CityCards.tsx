import { useNavigate, useRouter } from '@tanstack/react-router';
import type { ReactNode } from 'react';
import { useForm } from 'react-hook-form';
import { useFormatter, useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import {
  Card,
  DataTable,
  EmptyState,
  Form,
  infiniteTableProps,
  SelectField,
  StatusChip,
  useReducedMotion,
} from '../../ui';
import styles from './geo.module.css';
import { withCurrent } from './options';
import { useGeoCities, useGeoCountries, useGeoRegions } from './queries';
import { CITY_STATUSES, type CitiesSearch, toCitiesQuery } from './search';
import { useCityStatus } from './status';

type GeoCity = components['schemas']['GeoCity'];

// Сколько ждать кусок страницы города по клику, прежде чем перейти без View Transition (§4.15): прогретый
// наведением готов сразу, холодный не задерживает переход заметно.
const VT_WAIT_MS = 150;

// Карточки городов (макет 07): название на языке интерфейса, чип статуса, население и районы,
// регион, «страна выключена»; «Открыть» — страница города. Метрики макета (игроки, капитаны, матчи)
// появятся с модулями. Фильтры — search-параметры /geo/cities (replace: «Назад» не листает каждую
// букву поиска): поиск (q), чипы статуса (один статус; массив из адреса — как есть), страна и регион
// в «Ещё фильтры». Модератор видит только свои города — сужает сервер. «Открыть: <город>» — ссылка
// (ревью G8, m11); пустой список — иллюстрация «поле» и следующий шаг emptyAction (экран: «Активировать
// город» при праве).
export function CityCards({
  search,
  emptyAction,
}: {
  search: CitiesSearch;
  emptyAction?: ReactNode;
}) {
  const t = useTranslations('geo.cities');
  const navigate = useNavigate();
  const router = useRouter();
  const format = useFormatter();
  const statusOf = useCityStatus();
  // переход «список → карточка» — View Transition; «уменьшить движение» — без него (§4.15)
  const reduced = useReducedMotion();
  const list = useGeoCities(toCitiesQuery(search));
  const countries = useGeoCountries();
  const regions = useGeoRegions(search.country);

  // Переход к странице города — View Transition (§4.15). TanStack Router 1.170 начинает переход, только если кусок
  // маршрута назначения уже загружен, иначе показывает страницу без него и оставляет флаг viewTransition
  // СЛЕДУЮЩЕЙ навигации. Поэтому кусок греется по намерению (наведение, фокус на ссылке карточки), а по клику ждём
  // его не дольше VT_WAIT_MS: успел — переход с View Transition; не успел или не загрузился — сразу без него (пока
  // кусок грузится — скелетон раздела, ошибка — errorComponent). Сотрудник за это время ушёл (меню, «Назад») —
  // переход отменён (ревью UF2b/II-B, I2).
  const toCity = (cityId: string) => ({ to: paths.city, params: { cityId } }) as const;
  const warm = (cityId: string) => void router.preloadRoute(toCity(cityId)).catch(() => undefined);
  const openCity = (cityId: string) => {
    const to = toCity(cityId);
    if (reduced) {
      void navigate(to);
      return;
    }
    const entry = () => router.latestLocation.state.__TSR_key ?? router.latestLocation.href;
    const from = entry();
    let ready = false;
    const loaded = router.preloadRoute(to).then(
      () => (ready = true),
      () => undefined,
    );
    void Promise.race([loaded, new Promise((r) => setTimeout(r, VT_WAIT_MS))]).then(() => {
      if (entry() !== from) return;
      void navigate({ ...to, viewTransition: ready });
    });
  };
  const setSearch = (next: CitiesSearch) =>
    void navigate({ to: paths.cities, search: next, replace: true });
  const update = (patch: Partial<CitiesSearch>) => {
    const next: CitiesSearch = { ...search, ...patch };
    // пустой массив статусов — не значение: «Все» убирает status из адреса (ревью G8, m9)
    for (const k of ['q', 'status', 'country', 'region_id'] as const) {
      const v = next[k];
      if (!v || (Array.isArray(v) && v.length === 0)) delete next[k];
    }
    setSearch(next);
  };

  // Страна и регион — поля «Ещё фильтры»: значения — из адреса, изменение сразу уходит в адрес.
  const filterForm = useForm<{ country: string; region_id: string }>({
    values: { country: search.country ?? '', region_id: search.region_id ?? '' },
  });

  const chip = search.status?.length === 1 ? (search.status[0] ?? '') : search.status ? '' : 'all';
  const active = (search.country ? 1 : 0) + (search.region_id ? 1 : 0);
  const dirty = Boolean(search.q || search.status || search.country || search.region_id);

  const card = (c: GeoCity) => {
    const status = statusOf(c.status);
    return (
      <Card
        title={c.name}
        chip={<StatusChip tone={status.tone}>{status.text}</StatusChip>}
        selected={c.status === 'live'}
        stats={[
          { value: format.number(c.population), label: t('population') },
          { value: c.districts_count, label: t('districts', { count: c.districts_count }) },
        ]}
        note={
          <span className={styles.note}>
            <span>{c.region_name ?? t('no_region')}</span>
            {!c.country_enabled && <StatusChip tone="danger">{t('country_disabled')}</StatusChip>}
          </span>
        }
        action={{
          label: t('open'),
          name: t('open_label', { name: c.name }),
          href: router.buildLocation({ to: paths.city, params: { cityId: c.id } }).href,
          onNavigate: () => openCity(c.id),
          onIntent: () => warm(c.id),
        }}
      />
    );
  };

  return (
    <DataTable
      label={t('label')}
      columns={[]}
      rowKey={(c) => c.id}
      card={card}
      {...infiniteTableProps(list)}
      search={{
        value: search.q ?? '',
        onChange: (v) => update({ q: v.trim() ? v : undefined }),
        placeholder: t('search'),
      }}
      chips={{
        label: t('status_filter'),
        value: chip,
        options: [
          { value: 'all', label: t('status_all') },
          ...CITY_STATUSES.map((s) => ({ value: s, label: t(`statuses.${s}`) })),
        ],
        onChange: (v) => update({ status: CITY_STATUSES.filter((s) => s === v) }),
      }}
      filters={{
        active,
        content: (
          <Form form={filterForm} onSubmit={() => {}}>
            <SelectField
              name="country"
              label={t('country_filter')}
              options={[
                { value: '', label: t('country_all') },
                ...withCurrent(
                  (countries.data?.items ?? []).map((c) => ({ value: c.code, label: c.name })),
                  search.country,
                ),
              ]}
              rules={{
                // другая страна — регион прежней к ней не относится
                onChange: (e: { target: { value: string } }) =>
                  update({ country: e.target.value || undefined, region_id: undefined }),
              }}
            />
            {search.country && (
              <SelectField
                name="region_id"
                label={t('region_filter')}
                options={[
                  { value: '', label: t('region_all') },
                  ...withCurrent(
                    (regions.data?.items ?? []).map((r) => ({ value: r.id, label: r.name })),
                    search.region_id,
                  ),
                ]}
                rules={{
                  onChange: (e: { target: { value: string } }) =>
                    update({ region_id: e.target.value || undefined }),
                }}
              />
            )}
          </Form>
        ),
      }}
      dirty={dirty}
      onReset={() => setSearch({})}
      empty={
        <EmptyState
          illustration="pitch"
          title={t('empty_title')}
          text={t('empty_text')}
          action={emptyAction}
        />
      }
    />
  );
}
