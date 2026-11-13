import { useNavigate } from '@tanstack/react-router';
import { type ReactNode, type Ref, useEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { paths } from '../../app/paths';
import { useCan } from '../../app/session';
import { useNumber } from '../../i18n/format';
import { Button, DataState, FormError, Skeleton, StatusChip } from '../../ui';
import styles from './imports.module.css';
import { useLinkCity } from './mutations';
import { useGeoImport } from './queries';

type GeoReconcileCity = components['schemas']['GeoReconcileCity'];
type GeoReconcileCandidate = components['schemas']['GeoReconcileCandidate'];

// Отчёт сверки импорта (план B, Task 20; спека UI-фундамента §6.2 — раскрывающиеся группы и на телефоне): связанные
// (город → место), неоднозначные и конфликтные (кандидаты: название, регион, население; «Привязать» — linkGeoCity
// выбранного кандидата, право geo.cities.manage в городе), не найденные (переход на город), места missing
// (geoname_id), пропущено без часового пояса — число. Отчёт — снимок на момент импорта: привязанный здесь город
// помечается «привязан» в строке (в отчёте сервера он останется в своей группе).
// Панель над журналом во всю ширину (ревью m-5: в последнем столбце на 1440 отчёт был узким и вытягивал строку);
// импорт, которого нет в отфильтрованном журнале, тоже открывается — по id из адреса (ревью m-2).
export function ImportReport({
  importId,
  id,
  onClose,
  focusOnOpen = false,
}: {
  importId: string;
  id?: string;
  onClose: () => void;
  focusOnOpen?: boolean; // открыт щелчком по строке ниже — фокус (и прокрутка) на панель, не по глубокой ссылке
}) {
  const t = useTranslations('geo.imports.report');
  const ta = useTranslations('actions');
  const query = useGeoImport(importId);
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    if (focusOnOpen) ref.current?.focus();
  }, [focusOnOpen]);
  return (
    <section ref={ref} tabIndex={-1} id={id} className={styles.report} aria-label={t('label')}>
      <div className={styles.jobHead}>
        {query.data && (
          <h2 className={styles.title}>{t('title', { country: query.data.country_code })}</h2>
        )}
        <Button variant="link" onClick={onClose}>
          {ta('close')}
        </Button>
      </div>
      <DataState query={query} scope="block" skeleton={<Skeleton.Text lines={3} />}>
        {(report) => {
          const r = report.reconciled;
          if (!r)
            return (
              <>
                <p className={styles.muted}>{t('none')}</p>
                {report.error && (
                  <p className={styles.text}>{t('error', { error: report.error })}</p>
                )}
              </>
            );
          return (
            <>
              <Group title={t('groups.linked')} count={r.linked.length}>
                {r.linked.map((l) => (
                  <li key={l.city_id} className={styles.item}>
                    {t('linked_item', { slug: l.slug, geoname: l.geoname_id })}
                  </li>
                ))}
              </Group>
              <Group title={t('groups.ambiguous')} count={r.ambiguous.length} open>
                {r.ambiguous.map((c) => (
                  <ReconcileCity key={c.city_id} city={c} />
                ))}
              </Group>
              <Group title={t('groups.conflict')} count={r.conflict.length} open>
                {r.conflict.map((c) => (
                  <ReconcileCity key={c.city_id} city={c} />
                ))}
              </Group>
              <Group title={t('groups.not_found')} count={r.not_found.length} open>
                {r.not_found.map((c) => (
                  <li key={c.city_id} className={styles.item}>
                    <CityLink cityId={c.city_id} slug={c.slug} />
                  </li>
                ))}
              </Group>
              <Group title={t('groups.missing')} count={r.missing.length}>
                {r.missing.map((g) => (
                  <li key={g} className={styles.item}>
                    {g}
                  </li>
                ))}
              </Group>
              <p className={styles.muted}>{t('skipped', { count: r.skipped_timezone })}</p>
            </>
          );
        }}
      </DataState>
    </section>
  );
}

// Группа отчёта — <details>: на любой ширине сворачивается (принцип 5); пустая — «Нет» без раскрытия.
function Group({
  title,
  count,
  open = false,
  children,
}: {
  title: string;
  count: number;
  open?: boolean;
  children: ReactNode;
}) {
  const t = useTranslations('geo.imports.report');
  const format = useNumber();
  if (count === 0)
    return (
      <p className={styles.muted}>
        {title}: {t('empty_group')}
      </p>
    );
  return (
    <details className={styles.group} open={open}>
      <summary>
        {title} · {format(count)}
      </summary>
      <ul className={styles.items}>{children}</ul>
    </details>
  );
}

function CityLink({
  cityId,
  slug,
  ref,
}: {
  cityId: string;
  slug: string;
  ref?: Ref<HTMLButtonElement>;
}) {
  const t = useTranslations('geo.imports.report');
  const navigate = useNavigate();
  return (
    <Button
      ref={ref}
      variant="link"
      onClick={() => void navigate({ to: paths.city, params: { cityId } })}
    >
      {t('open_city', { slug })}
    </Button>
  );
}

// Город без автоматической привязки: кандидаты и «Привязать» (linkGeoCity выбранного geoname_id). Привязан — строка
// помечена «привязан», остальные кнопки города сняты; отказ (409 geo.city_already_linked и др.) — текст под городом.
function ReconcileCity({ city }: { city: GeoReconcileCity }) {
  const t = useTranslations('geo.imports.report');
  const canLink = useCan('geo.cities.manage', city.city_id);
  const link = useLinkCity(city.city_id);
  const [linked, setLinked] = useState<number | null>(null);
  // «Привязать» исчезают после успеха — фокус на городе, не на body (WCAG 2.4.3, ревью m-4)
  const cityRef = useRef<HTMLButtonElement>(null);
  return (
    <li className={styles.item}>
      <CityLink ref={cityRef} cityId={city.city_id} slug={city.slug} />
      {city.candidates.length === 0 ? (
        <span className={styles.muted}>{t('no_candidates')}</span>
      ) : (
        <ul className={styles.items}>
          {city.candidates.map((c) => (
            <Candidate
              key={c.geoname_id}
              candidate={c}
              linked={linked === c.geoname_id}
              action={
                canLink &&
                linked === null && (
                  <Button
                    variant="secondary"
                    aria-label={t('link_label', { city: city.slug, place: c.name })}
                    loading={link.isPending && link.variables === c.geoname_id}
                    disabled={link.isPending && link.variables !== c.geoname_id}
                    onClick={() =>
                      link.mutate(c.geoname_id, {
                        onSuccess: () => {
                          flushSync(() => setLinked(c.geoname_id));
                          cityRef.current?.focus();
                        },
                      })
                    }
                  >
                    {t('link')}
                  </Button>
                )
              }
            />
          ))}
        </ul>
      )}
      <FormError error={link.error} />
    </li>
  );
}

function Candidate({
  candidate: c,
  linked,
  action,
}: {
  candidate: GeoReconcileCandidate;
  linked: boolean;
  action: ReactNode;
}) {
  const t = useTranslations('geo.imports.report');
  const format = useNumber();
  return (
    <li className={styles.candidate}>
      <span>{t('candidate', { name: c.name, region: c.region_name ?? t('no_region') })}</span>
      <span className={styles.muted}>{t('population', { population: format(c.population) })}</span>
      {linked && <StatusChip tone="success">{t('linked_mark')}</StatusChip>}
      {action}
    </li>
  );
}
