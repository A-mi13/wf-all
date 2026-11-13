import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { useLocale, useTranslations } from 'use-intl';
import { firstDayOfWeek } from '../../i18n/week';
import { Icon } from '../icon';
import styles from './DateField.module.css';
import {
  addDays,
  addMonths,
  compare,
  daysInMonth,
  isoWeekday,
  today,
  toIso,
  utc,
  type Ymd,
} from './isoDate';

export type CalendarProps = {
  value: Ymd | null;
  min?: Ymd | null;
  max?: Ymd | null;
  onSelect: (value: Ymd) => void;
};

// Сетка месяца по шаблону WAI-ARIA «Date Picker Dialog» (спека UI-фундамента §4.17): role="grid", в Tab-порядке
// только день с фокусом (tabIndex 0), остальные -1 — ловушка диалога ходит «назад · вперёд · день». Стрелки —
// дни, PageUp/PageDown — месяцы, Shift+PageUp/PageDown — годы, Home/End — начало и конец недели языка,
// Enter/Space — выбрать; Esc закрывает диалог (стек оверлеев, не сетка). Заголовок месяца — живая область.
// Дни вне min/max — aria-disabled, не выбираются. Названия дней и месяцев — Intl языка интерфейса.
export function Calendar({ value, min, max, onSelect }: CalendarProps) {
  const t = useTranslations('ui.date');
  const locale = useLocale();
  const titleId = useId();
  const first = firstDayOfWeek(locale); // 1 — пн, 7 — вс
  const [focused, setFocused] = useState<Ymd>(() => value ?? clamp(today(), min, max));
  const grid = useRef<HTMLTableElement>(null);
  // Фокус переезжает на день при открытии и при ходе клавишами по сетке; кнопки месяцев фокус оставляют себе.
  const moveFocus = useRef(true);

  const iso = toIso(focused);
  useEffect(() => {
    if (!moveFocus.current) return;
    moveFocus.current = false;
    grid.current?.querySelector<HTMLElement>(`[data-date="${iso}"]`)?.focus();
  }, [iso]);

  const fmt = (o: Intl.DateTimeFormatOptions) =>
    new Intl.DateTimeFormat(locale, { ...o, timeZone: 'UTC' });
  const monthTitle = fmt({ month: 'long', year: 'numeric' }).format(utc({ ...focused, d: 1 }));
  const dayLabel = fmt({ day: 'numeric', month: 'long', year: 'numeric' });
  const weekdayShort = fmt({ weekday: 'short' });
  const weekdayLong = fmt({ weekday: 'long' });
  // 2024-01-01 — понедельник: столбцы с первого дня недели языка
  const columns = Array.from({ length: 7 }, (_, i) =>
    addDays({ y: 2024, m: 1, d: 1 }, (first - 1 + i) % 7),
  );

  const startOffset = (isoWeekday({ ...focused, d: 1 }) - first + 7) % 7;
  const cells: (Ymd | null)[] = Array.from({ length: startOffset }, () => null);
  for (let d = 1; d <= daysInMonth(focused.y, focused.m); d++) cells.push({ ...focused, d });
  while (cells.length % 7) cells.push(null);
  const weeks = Array.from({ length: cells.length / 7 }, (_, w) => cells.slice(w * 7, w * 7 + 7));

  const disabled = (v: Ymd) => (min && compare(v, min) < 0) || (max && compare(v, max) > 0);
  const go = (next: Ymd) => {
    moveFocus.current = true;
    setFocused(next);
  };

  const onKey = (e: KeyboardEvent<HTMLTableElement>) => {
    const dow = (isoWeekday(focused) - first + 7) % 7; // позиция в неделе языка
    const keys: Record<string, () => Ymd> = {
      ArrowLeft: () => addDays(focused, -1),
      ArrowRight: () => addDays(focused, 1),
      ArrowUp: () => addDays(focused, -7),
      ArrowDown: () => addDays(focused, 7),
      PageUp: () => addMonths(focused, e.shiftKey ? -12 : -1),
      PageDown: () => addMonths(focused, e.shiftKey ? 12 : 1),
      Home: () => addDays(focused, -dow),
      End: () => addDays(focused, 6 - dow),
    };
    const step = keys[e.key];
    if (step) {
      e.preventDefault();
      go(step());
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!disabled(focused)) onSelect(focused);
    }
  };

  const now = today();
  return (
    <div className={styles.calendar}>
      <div className={styles.head}>
        <button
          type="button"
          className={styles.nav}
          aria-label={t('prev_month')}
          onClick={() => setFocused((f) => addMonths(f, -1))}
        >
          <Icon name="chevron-down" size={16} className={styles.prev} />
        </button>
        <div id={titleId} className={styles.title} aria-live="polite">
          {monthTitle}
        </div>
        <button
          type="button"
          className={styles.nav}
          aria-label={t('next_month')}
          onClick={() => setFocused((f) => addMonths(f, 1))}
        >
          <Icon name="chevron-down" size={16} className={styles.next} />
        </button>
      </div>
      <table
        ref={grid}
        role="grid"
        aria-labelledby={titleId}
        className={styles.grid}
        onKeyDown={onKey}
      >
        <thead>
          <tr role="row">
            {columns.map((c) => (
              <th
                key={toIso(c)}
                role="columnheader"
                scope="col"
                abbr={weekdayLong.format(utc(c))}
                className={styles.weekday}
              >
                {weekdayShort.format(utc(c))}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {weeks.map((week, w) => (
            <tr key={w} role="row">
              {week.map((day, i) =>
                day ? (
                  <td
                    key={toIso(day)}
                    role="gridcell"
                    data-date={toIso(day)}
                    tabIndex={compare(day, focused) === 0 ? 0 : -1}
                    // фокус при показе пузыря — сюда, а не на «‹» (Popover modal, ревью UF2a/II r3)
                    data-autofocus={compare(day, focused) === 0 || undefined}
                    aria-selected={value ? compare(day, value) === 0 : false}
                    aria-disabled={disabled(day) ? 'true' : undefined}
                    aria-current={compare(day, now) === 0 ? 'date' : undefined}
                    aria-label={dayLabel.format(utc(day))}
                    className={styles.day}
                    onClick={() => {
                      if (disabled(day)) return;
                      setFocused(day);
                      onSelect(day);
                    }}
                  >
                    {day.d}
                  </td>
                ) : (
                  <td key={`pad-${i}`} role="gridcell" className={styles.pad} />
                ),
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function clamp(v: Ymd, min?: Ymd | null, max?: Ymd | null): Ymd {
  if (min && compare(v, min) < 0) return min;
  if (max && compare(v, max) > 0) return max;
  return v;
}
