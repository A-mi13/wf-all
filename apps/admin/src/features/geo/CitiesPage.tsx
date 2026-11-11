import { useSearch } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { useCan } from '../../app/session';
import { Button, PageHeader } from '../../ui';
import { ActivateDrawer } from './ActivateDrawer';
import { CityCards } from './CityCards';
import styles from './geo.module.css';
import { useGeoCities } from './queries';
import { parseCitiesSearch, toCitiesQuery } from './search';

// «Города и запуск» (спека §9.4 п. 8, макет 07; право geo.cities.read — проверяет каркас; открыт и
// на телефоне, включая активацию — спека UI-фундамента §3.1): карточки городов с фильтрами, «Активировать город» — с
// geo.cities.activate и geo.places.search (активация начинается с поиска места; ревью G8, m13; решает
// сервер). Пустой список без фильтров — «Активировать город» только в пустом состоянии (следующий шаг,
// UF2a.10), в шапке её нет: одно действие — одна кнопка (раунд 1, minor 5). Список — тот же запрос, что у
// CityCards (ключ общий, второго запроса нет).
export function CitiesPage() {
  const t = useTranslations();
  const search = parseCitiesSearch(useSearch({ strict: false }));
  const canActivate = useCan('geo.cities.activate');
  const canSearch = useCan('geo.places.search');
  const activator = canActivate && canSearch;
  const [activating, setActivating] = useState(false);
  const list = useGeoCities(toCitiesQuery(search));
  const empty = Object.keys(search).length === 0 && list.data?.pages[0]?.items.length === 0;
  const open = activator && (
    <Button onClick={() => setActivating(true)}>{t('geo.activate.open')}</Button>
  );
  return (
    <div className={styles.page}>
      <PageHeader
        title={t('nav.cities')}
        subtitle={t('geo.cities.subtitle')}
        actions={empty ? undefined : open}
      />
      <CityCards search={search} emptyAction={open || undefined} />
      {activator && <ActivateDrawer open={activating} onClose={() => setActivating(false)} />}
    </div>
  );
}
