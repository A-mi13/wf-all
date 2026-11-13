import { type MouseEvent, type ReactNode, useId } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { Skeleton } from '../states';
import type { ChipTone } from '../StatusChip';
import styles from './Home.module.css';
import { type AttentionState, useRetryFocus } from './state';

export type AttentionTileProps = {
  title: string;
  value: number | null;
  // порог «N+»: при limit=100 больше 100 строк не придёт — по умолчанию «99+»
  cap?: number;
  tone: ChipTone;
  href: string;
  // обычный клик — переход приложения; Ctrl/⌘/Shift/средняя кнопка — браузер по href (как ссылка Card)
  onNavigate: () => void;
  // намерение (наведение, фокус): экран греет кусок раздела назначения
  onIntent?: () => void;
  state: AttentionState;
  onRetry: () => void;
  retrying?: boolean;
  // у ответа есть next_cursor: строк больше, чем пришло (общего счёта у API нет)
  more?: boolean;
  // пояснение под числом («Решать с компьютера», «ближайший срок через 2 ч») — описание ссылки, не имя
  note?: ReactNode;
};

const DEFAULT_CAP = 99;

// Сетка плиток: список с подписью для читалки (каждая плитка — элемент списка); ≥ 720 px области — четыре в ряд.
export function AttentionTiles({ label, children }: { label: string; children: ReactNode }) {
  return (
    <ul className={styles.tiles} aria-label={label}>
      {children}
    </ul>
  );
}

// Плитка главной «Что требует внимания» (спека UI-фундамента §4.13): вся плитка — одна ссылка (одна цель), имя —
// заголовок и число; тон — полоса слева (цвет не единственный носитель смысла: число и примечание — текстом).
// Ошибка — «Не загрузилось · Повторить» внутри плитки: ссылки нет (кнопка внутри ссылки недопустима), остальные
// плитки живут своими запросами.
export function AttentionTile({
  title,
  value,
  cap = DEFAULT_CAP,
  tone,
  href,
  onNavigate,
  onIntent,
  state,
  onRetry,
  retrying,
  more,
  note,
}: AttentionTileProps) {
  const t = useTranslations();
  const noteId = useId();
  // удачный «Повторить» меняет блок с кнопкой на ссылку — фокус на неё, а не на body
  const { targetRef: retryRef, mark: markRetry } = useRetryFocus<HTMLAnchorElement>(
    state !== 'error',
  );

  if (state === 'error') {
    return (
      <div className={styles.tile} data-tone="danger">
        <span className={styles.tileTitle}>{title}</span>
        <span className={styles.tileError}>
          {t('ui.home.tile_error')}
          <span aria-hidden="true"> · </span>
          <Button
            variant="link"
            loading={retrying}
            onClick={() => {
              markRetry();
              onRetry();
            }}
          >
            {t('actions.retry')}
          </Button>
        </span>
      </div>
    );
  }

  const text =
    state === 'ready' && value !== null
      ? more || value > cap
        ? `${cap}+`
        : String(Math.max(0, Math.floor(value)))
      : null;
  const showNote = state === 'ready' && note !== undefined && note !== null && note !== false;

  return (
    <a
      ref={retryRef}
      href={href}
      className={styles.tile}
      data-tone={state === 'ready' && value ? tone : 'neutral'}
      aria-label={text === null ? title : `${title} ${text}`}
      aria-describedby={showNote ? noteId : undefined}
      onPointerEnter={onIntent}
      onFocus={onIntent}
      onClick={(e: MouseEvent<HTMLAnchorElement>) => {
        if (e.defaultPrevented || e.button !== 0) return;
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        onNavigate();
      }}
    >
      <span className={styles.tileTitle}>{title}</span>
      {text === null ? (
        <span className={styles.tileValue}>
          <Skeleton.Text width="short" />
        </span>
      ) : (
        <span className={styles.tileValue}>{text}</span>
      )}
      {showNote && (
        <span id={noteId} className={styles.tileNote}>
          {note}
        </span>
      )}
    </a>
  );
}
