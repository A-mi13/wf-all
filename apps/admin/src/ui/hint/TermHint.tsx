import { type ReactNode, useId, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Icon } from '../icon';
import { Popover } from '../popover/Popover';
import type { GlossaryKey } from './glossary';
import styles from './Hint.module.css';

// Термин в тексте (WAI «toggletip»): пунктирное подчёркивание и кнопка «i». Нажатие открывает и
// закрывает подсказку, её текст попадает в вежливую живую область (читалка слышит его сразу, без
// наведения); Esc и уход фокуса закрывают. Кнопка описана самим термином (aria-describedby).
// describedBy вместо children — термин уже стоит рядом (заголовок): только кнопка «i», описанная им, —
// кнопка, пузырь и живая область не попадают внутрь заголовка и в его имя.
export function TermHint({
  term,
  children,
  describedBy,
}: { term: GlossaryKey } & (
  { children: ReactNode; describedBy?: undefined } | { children?: undefined; describedBy: string }
)) {
  const t = useTranslations();
  const ownId = useId();
  const termId = describedBy ?? ownId;
  const tipId = useId();
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const text = t(`glossary.${term}`);
  return (
    <span className={styles.term}>
      {children !== undefined && (
        <span id={termId} className={styles.termText}>
          {children}
        </span>
      )}
      <button
        ref={anchor}
        type="button"
        className={styles.info}
        aria-label={t('ui.term.more')}
        aria-describedby={termId}
        aria-expanded={open}
        aria-controls={tipId}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="info" size={16} />
      </button>
      <Popover
        anchor={anchor}
        open={open}
        kind="hint"
        id={tipId}
        className={styles.bubble}
        onClose={() => setOpen(false)}
      >
        <div className={styles.tip}>{text}</div>
      </Popover>
      <span aria-live="polite" className={styles.srOnly} data-term-live="">
        {open ? text : ''}
      </span>
    </span>
  );
}
