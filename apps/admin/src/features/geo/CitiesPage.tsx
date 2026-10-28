import { useSearch } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { useCan } from '../../app/session';
import { Button, PageHeader } from '../../ui';
import { ActivateDrawer } from './ActivateDrawer';
import { CityCards } from './CityCards';
import styles from './geo.module.css';
import { parseCitiesSearch } from './search';

// «Города и запуск» (спека §9.4 п. 8, макет 07; право geo.cities.read — проверяет каркас; только
// с компьютера — desktopOnly в NAV): карточки городов с фильтрами, «Активировать город» — с
// geo.cities.activate (решает сервер).
export function CitiesPage() {
  const t = useTranslations();
  const search = parseCitiesSearch(useSearch({ strict: false }));
  const canActivate = useCan('geo.cities.activate');
  const [activating, setActivating] = useState(false);
  return (
    <div className={styles.page}>
      <PageHeader
        title={t('nav.cities')}
        subtitle={t('geo.cities.subtitle')}
        actions={
          canActivate && (
            <Button onClick={() => setActivating(true)}>{t('geo.activate.open')}</Button>
          )
        }
      />
      <CityCards search={search} />
      {canActivate && <ActivateDrawer open={activating} onClose={() => setActivating(false)} />}
    </div>
  );
}
