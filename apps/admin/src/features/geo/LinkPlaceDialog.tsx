import { useIsMutating } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Button, FormError, Modal, useToast } from '../../ui';
import styles from './geo.module.css';
import { useLinkCity } from './mutations';
import { PlaceAction, type PlaceFilter, PlaceSearch } from './PlaceSearch';

type GeoCityDetails = components['schemas']['GeoCityDetails'];
type GeoPlace = components['schemas']['GeoPlace'];

// «Привязать к GeoNames» (*; geo.cities.manage, город без geoname_id; R14 — та же привязка, что у
// сверки импорта): поиск места, как при активации (фильтр — страна города), выбор → linkGeoCity
// {geoname_id}. 409 geo.city_already_linked / geo.city_already_active, 404 geo.place_not_found —
// текст в окне, окно открыто (можно выбрать другое место). Пока запрос идёт, окно не закрывается.
// Действие шага («Выбрать», «Привязать») — в закреплённом подвале окна (V1 п. 9); закрытое окно не
// рендерится — выбор при новом открытии чистый. Шаг 2 открылся — фокус на «Привязать»; «Другое место» —
// прежние результаты, выбор снят, фокус на списке (ревью V1, m1, m2).
export function LinkPlaceDialog({
  city,
  open,
  onClose,
  onLinked,
}: {
  city: GeoCityDetails;
  open: boolean;
  onClose: () => void;
  onLinked: () => void; // привязано: окно закрыто, открывателя «Привязать» больше нет — фокус ставит страница
}) {
  return open ? <LinkFlow city={city} onClose={onClose} onLinked={onLinked} /> : null;
}

function LinkFlow({
  city,
  onClose,
  onLinked,
}: {
  city: GeoCityDetails;
  onClose: () => void;
  onLinked: () => void;
}) {
  const t = useTranslations('geo.link');
  const ta = useTranslations('geo.activate');
  const toast = useToast();
  const link = useLinkCity(city.id);
  const busy = useIsMutating({ mutationKey: ['geo', 'link', city.id] }) > 0;
  const [filter, setFilter] = useState<PlaceFilter>({ q: '', country: city.country.code });
  const [selected, setSelected] = useState<GeoPlace | null>(null);
  const [place, setPlace] = useState<GeoPlace | null>(null);
  const [returned, setReturned] = useState(false);
  const submitRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (place) submitRef.current?.focus();
  }, [place]);

  const submit = () => {
    if (!place) return;
    link.mutate(place.geoname_id, {
      onSuccess: () => {
        toast.success(t('linked'));
        onLinked();
        onClose();
      },
    });
  };

  const footer = place ? (
    <Button ref={submitRef} loading={link.isPending} onClick={submit}>
      {t('submit')}
    </Button>
  ) : (
    <PlaceAction selected={selected} onChoose={setPlace} onClose={onClose} />
  );

  return (
    <Modal open onClose={onClose} title={t('title')} dismissible={!busy} footer={footer}>
      <div className={styles.panel}>
        <p className={styles.text}>{t('text')}</p>
        {place ? (
          <>
            <div className={styles.chosen}>
              <span className={styles.place}>
                <span className={styles.label}>{t('chosen')}</span>
                <span className={styles.name}>
                  {place.region_name
                    ? ta('place_value', {
                        name: place.name,
                        region: place.region_name,
                        country: place.country.name,
                      })
                    : ta('place_value_no_region', {
                        name: place.name,
                        country: place.country.name,
                      })}
                </span>
              </span>
              <Button
                variant="link"
                disabled={link.isPending}
                onClick={() => {
                  link.reset();
                  setSelected(null);
                  setPlace(null);
                  setReturned(true);
                }}
              >
                {ta('change_place')}
              </Button>
            </div>
            <FormError error={link.error} />
          </>
        ) : (
          <PlaceSearch
            selected={selected}
            onSelect={setSelected}
            onChoose={setPlace}
            filter={filter}
            onFilterChange={setFilter}
            focusList={returned}
            bounded
          />
        )}
      </div>
    </Modal>
  );
}
