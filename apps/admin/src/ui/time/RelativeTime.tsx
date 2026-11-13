import { type RefObject, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { type RelativeMode, useExactTime, useRelativeTime } from '../../i18n/format';
import { Tooltip } from '../hint/Tooltip';
import styles from './RelativeTime.module.css';
import { useNow } from './useNow';

// Строка, внутри которой фокус открывает подсказку точного времени (таблица, карточка, список).
const ROW = 'tr, [role="row"], article, li, [data-wf-row]';

// Относительное время (спека UI-фундамента §4.7, план UF2a.9): <time dateTime> с текстом «5 мин назад»; точное
// время с поясом интерфейса — скрытым текстом внутри <time> (читалка слышит сразу) и подсказкой: мышью — по
// наведению, клавиатурой — пока фокус внутри строки таблицы или карточки. Своей остановки Tab нет (50 строк
// журнала — не 50 лишних остановок), title не используется. auto: < 1 мин — «только что», < 7 дней —
// относительно, старше — дата; relative — всегда относительно; exact — точное видимым текстом.
export function RelativeTime({
  value,
  mode = 'auto',
}: {
  value: string | Date;
  mode?: RelativeMode;
}) {
  const t = useTranslations('time');
  const now = useNow('minute');
  const relative = useRelativeTime();
  const exactOf = useExactTime();
  const ref = useRef<HTMLTimeElement>(null);
  const row = useRowFocus(ref, mode !== 'exact');

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return <time>{String(value)}</time>;
  const iso = typeof value === 'string' ? value : date.toISOString();
  const exact = exactOf(date);

  if (mode === 'exact')
    return (
      <time ref={ref} dateTime={iso} className={styles.time}>
        {exact}
      </time>
    );
  return (
    <Tooltip content={exact} describe={false} open={row.open || undefined} onEscape={row.dismiss}>
      <time ref={ref} dateTime={iso} className={styles.time} data-wf-relative="">
        <span data-relative="">{relative(date, now, mode, t('just_now'))}</span>
        <span data-exact="" className={styles.srOnly}>
          {exact}
        </span>
      </time>
    </Tooltip>
  );
}

// Подсказка по фокусу внутри ближайшей строки (focusin / focusout с relatedTarget вне строки). Только у первого
// времени строки — два времени («создан», «изменён») не открывают две подсказки разом (ревью m7). Esc скрывает её
// до следующего перехода фокуса (WCAG 1.4.13): управляемая подсказка иначе оставалась бы верхней записью стека и
// съедала бы Esc панели (ревью I2).
function useRowFocus(
  ref: RefObject<HTMLElement | null>,
  enabled: boolean,
): { open: boolean; dismiss: () => void } {
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  useEffect(() => {
    const row = enabled ? ref.current?.closest<HTMLElement>(ROW) : null;
    if (!row) return;
    const onIn = () => {
      if (row.querySelector('[data-wf-relative]') !== ref.current) return;
      setFocused(true);
      setDismissed(false);
    };
    const onOut = (e: FocusEvent) => {
      if (!(e.relatedTarget instanceof Node && row.contains(e.relatedTarget))) setFocused(false);
    };
    row.addEventListener('focusin', onIn);
    row.addEventListener('focusout', onOut);
    return () => {
      row.removeEventListener('focusin', onIn);
      row.removeEventListener('focusout', onOut);
      setFocused(false);
    };
  }, [ref, enabled]);
  return { open: focused && !dismissed, dismiss: () => setDismissed(true) };
}
