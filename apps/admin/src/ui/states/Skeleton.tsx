import { cx } from '../cx';
import styles from './Skeleton.module.css';

// Скелетоны по форме будущего содержимого (спека UI-фундамента §4.3): страница собирает свою форму из этих
// частей рядом с экраном (CityPageSkeleton в features/*). Все части декоративные (aria-hidden): «Загрузка…»
// для читалки говорит DataState один раз, а не каждая полоса. Размеры — именами по токенам: CSP админки не
// пускает инлайн-стили, произвольной высоты в пикселях нет.

export type SkeletonWidth = 'full' | 'long' | 'medium' | 'short';
export type SkeletonHeight = 'sm' | 'md' | 'lg' | 'xl';

const WIDTH: Record<SkeletonWidth, string | undefined> = {
  full: styles.wFull,
  long: styles.wLong,
  medium: styles.wMedium,
  short: styles.wShort,
};

const HEIGHT: Record<SkeletonHeight, string | undefined> = {
  sm: styles.hSm, // space-6
  md: styles.hMd, // size-control — поле, кнопка
  lg: styles.hLg, // space-16
  xl: styles.hXl, // size-avatar-lg — график, крупный блок
};

const count = (n: number) => Array.from({ length: Math.max(0, Math.floor(n)) }, (_, i) => i);

// Абзац: lines строк; последняя короче, если строк больше одной (width — ширина остальных).
function Text({ lines = 1, width = 'full' }: { lines?: number; width?: SkeletonWidth }) {
  return (
    <span className={cx(styles.stack, styles.paragraph)} aria-hidden="true" data-skeleton="text">
      {count(lines).map((i) => (
        <span key={i} className={cx(styles.bone, styles.line, WIDTH[width])} />
      ))}
    </span>
  );
}

function Block({ height = 'md' }: { height?: SkeletonHeight }) {
  return (
    <span
      className={cx(styles.bone, styles.wFull, HEIGHT[height])}
      aria-hidden="true"
      data-skeleton="block"
    />
  );
}

// Карточка: заголовок, две строки текста, блок.
function Card() {
  return (
    <div className={styles.card} aria-hidden="true" data-skeleton="card">
      <span className={cx(styles.bone, styles.title)} />
      <Text lines={2} />
      <Block height="sm" />
    </div>
  );
}

// Таблица: шапка (head) и rows строк по columns ячеек; data-skeleton-row — у строк данных.
function Table({
  columns = 4,
  rows = 5,
  head = true,
}: {
  columns?: number;
  rows?: number;
  head?: boolean;
}) {
  const cells = count(Math.max(1, columns));
  return (
    <div className={styles.table} aria-hidden="true" data-skeleton="table">
      {head && (
        <div className={cx(styles.row, styles.head)}>
          {cells.map((c) => (
            <span key={c} className={cx(styles.bone, styles.cell)} />
          ))}
        </div>
      )}
      {count(rows).map((r) => (
        <div key={r} className={styles.row} data-skeleton-row="">
          {cells.map((c) => (
            <span key={c} className={cx(styles.bone, styles.cell)} />
          ))}
        </div>
      ))}
    </div>
  );
}

// Форма: fields полей «подпись + поле».
function Form({ fields = 3 }: { fields?: number }) {
  return (
    <div className={styles.form} aria-hidden="true" data-skeleton="form">
      {count(fields).map((i) => (
        <div key={i} className={styles.field}>
          <span className={cx(styles.bone, styles.label)} />
          <span className={cx(styles.bone, styles.control)} />
        </div>
      ))}
    </div>
  );
}

export const Skeleton = { Text, Block, Card, Table, Form };
