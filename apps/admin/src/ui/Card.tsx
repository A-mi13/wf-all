import { type MouseEvent, type ReactNode, useId } from 'react';
import { Button, type ButtonVariant } from './Button';
import buttonStyles from './Button.module.css';
import styles from './Card.module.css';
import { cx } from './cx';

export type CardStat = { value: ReactNode; label: string };

type CardButtonAction = {
  label: string;
  onClick: () => void;
  variant?: ButtonVariant;
  disabled?: boolean;
};
// Переход (ревью G8, m11): ссылка с href — Ctrl/⌘/Shift/средняя кнопка открывают её браузером, обычный
// клик — onNavigate (навигация приложения). name — доступное имя («Открыть: Ставрополь»), начинается с
// видимого label (WCAG 2.5.3).
type CardLinkAction = {
  label: string;
  name: string;
  href: string;
  onNavigate: () => void;
};

export type CardProps = {
  title: ReactNode;
  chip?: ReactNode; // StatusChip статуса
  stats?: CardStat[]; // показатели: значение крупно, подпись мелко
  note?: ReactNode; // примечание внизу слева
  action?: CardButtonAction | CardLinkAction;
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
          {action && 'href' in action && (
            <a
              href={action.href}
              aria-label={action.name}
              // вид — вторичной кнопки (как действие-кнопка по умолчанию)
              className={cx(
                buttonStyles.button,
                buttonStyles.secondary,
                styles.action,
                styles.link,
              )}
              onClick={(e: MouseEvent<HTMLAnchorElement>) => {
                if (e.defaultPrevented || e.button !== 0) return;
                if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                e.preventDefault();
                action.onNavigate();
              }}
            >
              {action.label}
            </a>
          )}
          {action && !('href' in action) && (
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
