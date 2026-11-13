import { type MouseEvent, type ReactNode, useEffect, useId, useSyncExternalStore } from 'react';
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
  // намерение (наведение, фокус): экран греет кусок страницы назначения (ленивый маршрут, §4.15)
  onIntent?: () => void;
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
// Источник перехода «список → карточка» (спека UI-фундамента §4.15): имя wf-entity — одно статическое (CSP: без
// инлайн-имён), поэтому класс .vtSource — только у нажатой карточки (адрес её ссылки — общий для вкладки, одно
// значение: двух карточек с этим именем не бывает, иначе браузер пропустил бы переход). Состояние — вне карточки:
// список, перерисованный или пересозданный до начала перехода, класс не теряет. Нажатая карточка снята (переход
// состоялся: уборка эффектов — уже после снимка старого вида) — источник стирается, при возврате на список имя
// wf-entity ни у кого (ревью UF2b/II-B, m5).
let source: string | null = null;
const listeners = new Set<() => void>();
const mounted = new Map<string, number>();
const sourceStore = {
  set(href: string | null) {
    source = href;
    for (const fn of listeners) fn();
  },
  subscribe(fn: () => void) {
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  },
  get: () => source,
  // Карточка с этой ссылкой на экране; сняли последнюю — источник стирается. Проверка — микрозадачей: список,
  // пересозданный в том же коммите (снятие и монтирование подряд), источник не теряет.
  mount(href: string | undefined) {
    if (href === undefined) return undefined;
    mounted.set(href, (mounted.get(href) ?? 0) + 1);
    return () => {
      mounted.set(href, (mounted.get(href) ?? 1) - 1);
      queueMicrotask(() => {
        if (!mounted.get(href) && source === href) sourceStore.set(null);
      });
    };
  },
};

export function Card({ title, chip, stats, note, action, selected, headingLevel = 2 }: CardProps) {
  const titleId = useId();
  const href = action && 'href' in action ? action.href : undefined;
  const vtSource =
    useSyncExternalStore(sourceStore.subscribe, sourceStore.get, () => null) === href;
  useEffect(() => sourceStore.mount(href), [href]);
  const Heading = headingLevel === 3 ? 'h3' : 'h2';
  return (
    <article
      aria-labelledby={titleId}
      className={cx(styles.card, vtSource && styles.vtSource)}
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
              onPointerEnter={action.onIntent}
              onFocus={action.onIntent}
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
                // класс — до навигации: flushSync не нужен, переход начнётся после загрузки маршрута
                sourceStore.set(action.href);
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
