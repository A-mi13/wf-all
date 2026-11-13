import { Skeleton } from '../../ui';
import styles from './geo.module.css';

// Скелетон страницы города по форме содержимого (спека UI-фундамента §4.3): шапка (название и статус),
// «Сведения о городе», карточки «Статус», «Адрес в вебе», «Названия» и таблица районов. Декоративный:
// объявление загрузки для читалки делает DataState.
export function CityPageSkeleton() {
  return (
    <div className={styles.page} data-city-skeleton="">
      <Skeleton.Text lines={2} width="short" />
      <Skeleton.Text lines={3} width="long" />
      <div className={styles.section}>
        <Skeleton.Card />
      </div>
      <div className={styles.section}>
        <Skeleton.Card />
      </div>
      <div className={styles.section}>
        <Skeleton.Form fields={2} />
      </div>
      <div className={styles.section}>
        <Skeleton.Table columns={3} rows={3} />
      </div>
    </div>
  );
}
