import type { MouseEvent } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { EmptyState } from '../states';
import type { ChipTone } from '../StatusChip';
import styles from './Home.module.css';
import { useRetryFocus } from './state';

export type TodoItem = {
  id: string;
  text: string;
  href: string;
  // переход приложения (адрес строит экран из объекта search роутера); Ctrl/⌘/Shift — браузер по href
  onNavigate: () => void;
  tone?: ChipTone;
};

// Источник списка не загрузился: «Не загрузилось · Повторить» строкой списка; retrying — повтор идёт.
export type TodoError = { onRetry: () => void; retrying?: boolean };

// Список дел главной (спека UI-фундамента §4.13): строки-ссылки из тех же данных, что плитки, и личное («Осталось 2
// резервных кода»). Тон — точка слева (декоративная: смысл — в тексте). Пусто — иллюстрация и «Всё спокойно»; при
// ошибке источника (ревью UF4-H, I1: у резервных кодов своей плитки нет) — строка «Не загрузилось · Повторить» под
// делами, и «Всё спокойно» не говорится (исход неизвестен). Удачный повтор — фокус на первую ссылку или на список.
export function TodoList({
  items,
  label,
  error,
}: {
  items: TodoItem[];
  label?: string;
  error?: TodoError;
}) {
  const t = useTranslations();
  const { targetRef: retryRef, mark: markRetry } = useRetryFocus<HTMLDivElement>(!error);
  return (
    <div ref={retryRef} tabIndex={-1} className={styles.todoRoot}>
      {items.length === 0 && !error ? (
        <EmptyState
          illustration="goal"
          title={t('ui.home.calm_title')}
          text={t('ui.home.calm_text')}
        />
      ) : (
        <ul className={styles.todo} aria-label={label}>
          {items.map((item) => (
            <li key={item.id}>
              <a
                href={item.href}
                className={styles.todoLink}
                onClick={(e: MouseEvent<HTMLAnchorElement>) => {
                  if (e.defaultPrevented || e.button !== 0) return;
                  if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  item.onNavigate();
                }}
              >
                <span
                  className={styles.todoDot}
                  data-tone={item.tone ?? 'neutral'}
                  aria-hidden="true"
                />
                {item.text}
              </a>
            </li>
          ))}
          {error && (
            <li className={styles.todoError}>
              <span className={styles.todoDot} data-tone="danger" aria-hidden="true" />
              {t('ui.home.tile_error')}
              <span aria-hidden="true">·</span>
              <Button
                variant="link"
                loading={error.retrying}
                onClick={() => {
                  markRetry();
                  error.onRetry();
                }}
              >
                {t('actions.retry')}
              </Button>
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
