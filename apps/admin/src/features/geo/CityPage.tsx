import { useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { type ReactNode, type Ref, useEffect, useId, useRef, useState } from 'react';
import { useFormatter, useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { useCan } from '../../app/session';
import { useLocale } from '../../i18n/locale';
import { Button, CopyButton, DataState, PageHeader, StatusChip } from '../../ui';
import { CityNamesCard } from './CityNamesCard';
import { CityPageSkeleton } from './CityPageSkeleton';
import { CitySlugCard } from './CitySlugCard';
import { CityStatusCard } from './CityStatusCard';
import { DistrictsCard } from './DistrictsCard';
import styles from './geo.module.css';
import { LinkPlaceDialog } from './LinkPlaceDialog';
import { geoKeys, useGeoCity } from './queries';
import { UUID } from './search';
import { useCityStatus } from './status';

type GeoCityDetails = components['schemas']['GeoCityDetails'];

// Страница города (*; спека §9.4 п. 8; план B Task 19; право раздела geo.cities.read — каркас).
// Шапка: название, статус, страна (пометка «выключена»), регион, население, geoname_id или «не
// привязан». Карточки — по правам в этом городе (useCan с cityId; решает сервер, R10): статус и
// адрес — geo.cities.manage, названия — geo.cities.rename (модератор своего), районы —
// geo.districts.manage; без права — только чтение. Привязка к GeoNames — у непривязанного города
// с geo.cities.manage. Чужой город модератора: сервер отвечает 403 — «нет доступа», 404 — «не найден».
//
// Загрузка — DataState со скелетоном по форме страницы (UF2a.10); cityId не uuid — «Город не найден» без
// запроса (сервер ответил бы 400 по path.cityId; ревью G8, m1). Пока карточка перечитывается на новом
// языке, видна прежняя карточка этого города, а отправка форм недоступна (stale; ревью G8, m18).
export function CityPage() {
  const params = useParams({ strict: false });
  const cityId = typeof params.cityId === 'string' ? params.cityId : '';
  const valid = UUID.test(cityId);
  const city = useGeoCity(cityId, valid);
  if (!valid) return <NotFound />;
  if (city.data === undefined && isApiError(city.error, 'geo.city_not_found')) return <NotFound />;
  return (
    <DataState query={city} skeleton={<CityPageSkeleton />}>
      {(c) => (
        <CityView key={c.id} city={c} stale={city.isPlaceholderData} fetching={city.isFetching} />
      )}
    </DataState>
  );
}

function NotFound() {
  const t = useTranslations('geo.city');
  return <PageHeader title={t('not_found_title')} subtitle={t('not_found_text')} />;
}

function CityView({
  city,
  stale,
  fetching,
}: {
  city: GeoCityDetails;
  stale: boolean;
  fetching: boolean;
}) {
  const t = useTranslations('geo.city');
  const td = useTranslations('geo.districts');
  const statusOf = useCityStatus();
  const canManage = useCan('geo.cities.manage', city.id);
  const canRename = useCan('geo.cities.rename', city.id);
  const canDistricts = useCan('geo.districts.manage', city.id);
  // привязка начинается с поиска места: без geo.places.search окно упёрлось бы в отказ (ревью G8, m13)
  const canSearch = useCan('geo.places.search');
  const canLink = canManage && canSearch;
  const [linking, setLinking] = useState(false);
  // Привязка удалась — кнопки «Привязать» (открывателя окна) больше нет, и фокус упал бы на body
  // (ревью G8, m4): ставим его на сведения о городе, где видна привязка. Окно закрывается и кнопка
  // исчезает в разных отрисовках (ответ в кэше — отдельным уведомлением), порядок любой: эффект ждёт
  // и закрытия (linked), и привязки в карточке. Закрытие окна без привязки считается так же: 409
  // city_already_linked перечитал карточку, «Привязать» исчезла, пока окно было открыто, — Esc вернул бы
  // фокус на пропавший открыватель (ревью G8, n1). Цель одноразовая: применена, не понадобилась (фокус не
  // на body) или карточка перечитана без привязки — гаснет, опоздавшая не уводит фокус (раунд 1, minor 1).
  const queryClient = useQueryClient();
  const locale = useLocale();
  const facts = useRef<HTMLUListElement>(null);
  const linkTarget = useRef(false);
  const [closedAt, setClosedAt] = useState(0);
  const afterLink = () => {
    linkTarget.current = true;
    setClosedAt((n) => n + 1);
  };
  useEffect(() => {
    if (!linkTarget.current) return;
    if (city.geoname_id === null) {
      // ждать есть чего, только если привязка уже в кэше (отрисовка впереди) или карточка перечитывается
      const key = geoKeys.city(city.id, locale);
      const cached = queryClient.getQueryData<GeoCityDetails>(key)?.geoname_id ?? null;
      const loading = queryClient.getQueryState(key)?.fetchStatus === 'fetching';
      if (cached === null && !loading) linkTarget.current = false;
      return;
    }
    const active = document.activeElement;
    if (!active || active === document.body) facts.current?.focus();
    linkTarget.current = false;
  }, [closedAt, city.geoname_id, city.id, fetching, locale, queryClient]);
  const status = statusOf(city.status);
  return (
    <div className={styles.page}>
      <PageHeader
        title={city.name}
        subtitle={<StatusChip tone={status.tone}>{status.text}</StatusChip>}
        actions={
          canLink &&
          city.geoname_id === null && (
            <Button disabled={stale} onClick={() => setLinking(true)}>
              {t('link_open')}
            </Button>
          )
        }
      />
      <CityFacts city={city} ref={facts} />
      {canManage && (
        <Section title={t('status.title')}>
          <CityStatusCard city={city} readOnly={stale} />
        </Section>
      )}
      {canManage && (
        <Section title={t('slug.title')}>
          <CitySlugCard city={city} readOnly={stale} />
        </Section>
      )}
      <Section title={t('names.title')}>
        <CityNamesCard city={city} canEdit={canRename} readOnly={stale} />
      </Section>
      <Section title={td('title')}>
        <DistrictsCard cityId={city.id} canEdit={canDistricts} />
      </Section>
      {canLink && (
        <LinkPlaceDialog
          city={city}
          open={linking}
          onClose={() => {
            setLinking(false);
            afterLink();
          }}
          onLinked={afterLink}
        />
      )}
    </div>
  );
}

// Блок страницы города: заголовок h2 и содержимое; читалка находит его как область по заголовку.
function Section({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={styles.section}>
      <h2 id={id} className={styles.sectionTitle}>
        {title}
      </h2>
      {children}
    </section>
  );
}

function CityFacts({ city, ref }: { city: GeoCityDetails; ref?: Ref<HTMLUListElement> }) {
  const t = useTranslations('geo.city.facts');
  const format = useFormatter();
  const facts: [string, ReactNode][] = [
    [
      t('country'),
      <span key="country" className={styles.note}>
        {t('country_value', { name: city.country.name, code: city.country.code })}
        {!city.country.is_enabled && <StatusChip tone="danger">{t('country_disabled')}</StatusChip>}
      </span>,
    ],
    [t('region'), city.region?.name ?? t('no_region')],
    [t('population'), format.number(city.population)],
    [
      t('geoname'),
      city.geoname_id === null ? (
        t('not_linked')
      ) : (
        <span key="geoname" className={styles.note}>
          {city.geoname_id}
          <CopyButton value={String(city.geoname_id)} label={t('copy_geoname')} />
        </span>
      ),
    ],
    [t('timezone'), city.timezone],
    [
      t('slug'),
      <span key="slug" className={styles.note}>
        {city.slug}
        <CopyButton value={city.slug} label={t('copy_slug')} />
      </span>,
    ],
  ];
  return (
    <ul ref={ref} tabIndex={-1} aria-label={t('label')} className={styles.facts}>
      {facts.map(([label, value]) => (
        <li key={label} className={styles.fact}>
          <span className={styles.label}>{label}</span>
          <span className={styles.factValue}>{value}</span>
        </li>
      ))}
    </ul>
  );
}
