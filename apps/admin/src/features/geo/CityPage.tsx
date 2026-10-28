import { useNavigate, useParams } from '@tanstack/react-router';
import { type ReactNode, type Ref, useEffect, useId, useRef, useState } from 'react';
import { useFormatter, useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import { useCan } from '../../app/session';
import { Button, ErrorState, ForbiddenState, LoadingRows, PageHeader, StatusChip } from '../../ui';
import { CityNamesCard } from './CityNamesCard';
import { CitySlugCard } from './CitySlugCard';
import { CityStatusCard } from './CityStatusCard';
import { DistrictsCard } from './DistrictsCard';
import styles from './geo.module.css';
import { LinkPlaceDialog } from './LinkPlaceDialog';
import { useGeoCity } from './queries';
import { useCityStatus } from './status';

type GeoCityDetails = components['schemas']['GeoCityDetails'];

// Страница города (*; спека §9.4 п. 8; план B Task 19; право раздела geo.cities.read — каркас).
// Шапка: название, статус, страна (пометка «выключена»), регион, население, geoname_id или «не
// привязан». Карточки — по правам в этом городе (useCan с cityId; решает сервер, R10): статус и
// адрес — geo.cities.manage, названия — geo.cities.rename (модератор своего), районы —
// geo.districts.manage; без права — только чтение. Привязка к GeoNames — у непривязанного города
// с geo.cities.manage. Чужой город модератора: сервер отвечает 403 — «нет доступа», 404 — «не найден».
export function CityPage() {
  const params = useParams({ strict: false });
  const cityId = typeof params.cityId === 'string' ? params.cityId : '';
  const city = useGeoCity(cityId);
  if (city.data) return <CityView city={city.data} />;
  if (!city.error) return <LoadingRows />;
  if (isApiError(city.error) && city.error.status === 403) return <ForbiddenState />;
  if (isApiError(city.error, 'geo.city_not_found')) return <NotFound />;
  return (
    <ErrorState error={city.error} onRetry={() => void city.refetch()} retrying={city.isFetching} />
  );
}

function BackButton() {
  const t = useTranslations('geo.city');
  const navigate = useNavigate();
  return (
    <Button variant="secondary" onClick={() => void navigate({ to: paths.cities })}>
      {t('back')}
    </Button>
  );
}

function NotFound() {
  const t = useTranslations('geo.city');
  return (
    <PageHeader
      title={t('not_found_title')}
      subtitle={t('not_found_text')}
      actions={<BackButton />}
    />
  );
}

function CityView({ city }: { city: GeoCityDetails }) {
  const t = useTranslations('geo.city');
  const td = useTranslations('geo.districts');
  const statusOf = useCityStatus();
  const canManage = useCan('geo.cities.manage', city.id);
  const canRename = useCan('geo.cities.rename', city.id);
  const canDistricts = useCan('geo.districts.manage', city.id);
  const [linking, setLinking] = useState(false);
  // Привязка удалась — кнопки «Привязать» (открывателя окна) больше нет, и фокус упал бы на body
  // (ревью G8, m4): ставим его на сведения о городе, где видна привязка. Окно закрывается и кнопка
  // исчезает в разных отрисовках (ответ в кэше — отдельным уведомлением), порядок любой: эффект ждёт
  // и закрытия (linked), и привязки в карточке.
  const facts = useRef<HTMLUListElement>(null);
  const [linked, setLinked] = useState(0);
  useEffect(() => {
    if (linked === 0 || city.geoname_id === null) return;
    const active = document.activeElement;
    if (!active || active === document.body) facts.current?.focus();
  }, [linked, city.geoname_id]);
  const status = statusOf(city.status);
  return (
    <div className={styles.page}>
      <PageHeader
        title={city.name}
        subtitle={<StatusChip tone={status.tone}>{status.text}</StatusChip>}
        actions={
          <>
            {canManage && city.geoname_id === null && (
              <Button onClick={() => setLinking(true)}>{t('link_open')}</Button>
            )}
            <BackButton />
          </>
        }
      />
      <CityFacts city={city} ref={facts} />
      {canManage && (
        <Section title={t('status.title')}>
          <CityStatusCard city={city} />
        </Section>
      )}
      {canManage && (
        <Section title={t('slug.title')}>
          <CitySlugCard city={city} />
        </Section>
      )}
      <Section title={t('names.title')}>
        <CityNamesCard city={city} canEdit={canRename} />
      </Section>
      <Section title={td('title')}>
        <DistrictsCard cityId={city.id} canEdit={canDistricts} />
      </Section>
      {canManage && (
        <LinkPlaceDialog
          city={city}
          open={linking}
          onClose={() => setLinking(false)}
          onLinked={() => setLinked((n) => n + 1)}
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
    [t('geoname'), city.geoname_id === null ? t('not_linked') : String(city.geoname_id)],
    [t('timezone'), city.timezone],
    [t('slug'), city.slug],
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
