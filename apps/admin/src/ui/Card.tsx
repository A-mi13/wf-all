import { type ReactNode, useId } from 'react';
import { Button, type ButtonVariant } from './Button';
import styles from './Card.module.css';

export type CardStat = { value: ReactNode; label: string };

export type CardProps = {
  title: ReactNode;
  chip?: ReactNode; // StatusChip статуса
  stats?: CardStat[]; // показатели: значение крупно, подпись мелко
  note?: ReactNode; // примечание внизу слева
  action?: { label: string; onClick: () => void; variant?: ButtonVariant; disabled?: boolean };
  selected?: boolean; // выделение (у живого города — макет), только вид
  headingLevel?: 2 | 3; // уровень заголовка: по умолчанию 2 (под h1 PageHeader)
};

// Карточка макета «Города и запуск» (07): заголовок и чип статуса, ряд показателей, внизу —
// примечание и кнопка действия. Карточка — article с именем по заголовку; кнопка описана
// заголовком (aria-describedby): «Открыть» в сетке читалка различает по карточке.
export function Card({ title, chip, stats, note, action, selected, headingLevel = 2 }: CardProps) {
  const titleId = useId();
  const Heading = headingLevel === 3 ? 'h3' : 'h2';
  return (
    <article
      aria-labelledby={titleId}
      className={styles.card}
      data-selected={selected || undefined}
    >
      <div className={styles.head}>
        <Heading id={titleId} className={styles.title}>
          {title}
        </Heading>
        {chip}
      </div>
      {stats && stats.length > 0 && (
        <ul className={styles.stats}>
          {stats.map((s) => (
            <li key={s.label} className={styles.stat}>
              <span className={styles.value}>{s.value}</span>
              <span className={styles.statLabel}>{s.label}</span>
            </li>
          ))}
        </ul>
      )}
      {(note || action) && (
        <footer className={styles.foot}>
          {note && <div className={styles.note}>{note}</div>}
          {action && (
            <Button
              variant={action.variant ?? 'secondary'}
              disabled={action.disabled}
              aria-describedby={titleId}
              onClick={action.onClick}
              className={styles.action}
            >
              {action.label}
            </Button>
          )}
        </footer>
      )}
    </article>
  );
}
