import { useIsMutating } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { Button, FormError, Modal, useToast } from '../../ui';
import styles from './geo.module.css';
import { useLinkCity } from './mutations';
import { PlaceSearch } from './PlaceSearch';

type GeoCityDetails = components['schemas']['GeoCityDetails'];
type GeoPlace = components['schemas']['GeoPlace'];

// «Привязать к GeoNames» (*; geo.cities.manage, город без geoname_id; R14 — та же привязка, что у
// сверки импорта): поиск места, как при активации (фильтр — страна города), выбор → linkGeoCity
// {geoname_id}. 409 geo.city_already_linked / geo.city_already_active, 404 geo.place_not_found —
// текст в окне, окно открыто (можно выбрать другое место). Пока запрос идёт, окно не закрывается.
export function LinkPlaceDialog({
  city,
  open,
  onClose,
}: {
  city: GeoCityDetails;
  open: boolean;
  onClose: () => void;
}) {
  const t = useTranslations('geo.link');
  const busy = useIsMutating({ mutationKey: ['geo', 'link', city.id] }) > 0;
  return (
    <Modal open={open} onClose={onClose} title={t('title')} dismissible={!busy}>
      <LinkBody city={city} onClose={onClose} />
    </Modal>
  );
}

function LinkBody({ city, onClose }: { city: GeoCityDetails; onClose: () => void }) {
  const t = useTranslations('geo.link');
  const ta = useTranslations('geo.activate');
  const toast = useToast();
  const link = useLinkCity(city.id);
  const [place, setPlace] = useState<GeoPlace | null>(null);

  const submit = () => {
    if (!place) return;
    link.mutate(place.geoname_id, {
      onSuccess: () => {
        toast.success(t('linked'));
        onClose();
      },
    });
  };

  return (
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
                  : ta('place_value_no_region', { name: place.name, country: place.country.name })}
              </span>
            </span>
            <Button
              variant="link"
              disabled={link.isPending}
              onClick={() => {
                link.reset();
                setPlace(null);
              }}
            >
              {ta('change_place')}
            </Button>
          </div>
          <FormError error={link.error} />
          <div className={styles.row}>
            <Button loading={link.isPending} onClick={submit}>
              {t('submit')}
            </Button>
          </div>
        </>
      ) : (
        <PlaceSearch onChoose={setPlace} onClose={onClose} defaultCountry={city.country.code} />
      )}
    </div>
  );
}
