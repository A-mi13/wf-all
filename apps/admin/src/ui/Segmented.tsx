import type { IconName } from '@wf/icons';
import { cx } from './cx';
import { Icon } from './icon';
import styles from './Segmented.module.css';

type SegmentBase<V extends string> = {
  value: V;
  title?: string; // подсказка при наведении
  lang?: string; // язык подписи сегмента
};

// Сегмент — видимый текст («RU») или значок (тема: монитор, солнце, луна). У значка видимого текста нет:
// имя для читалки обязательно, и title — для мыши (WCAG 2.5.3 к значкам без текста не применяется).
export type SegmentOption<V extends string = string> = SegmentBase<V> &
  (
    | {
        text: string; // видимый текст сегмента (короткий: «RU»)
        name?: string; // доступное имя, если видимого мало («RU — Русский»); начинаться должно с text
        icon?: never;
      }
    | { icon: IconName; name: string; text?: never }
  );

export type SegmentedProps<V extends string = string> = {
  label: string; // имя группы для читалки
  options: SegmentOption<V>[];
  value: V;
  onChange: (value: V) => void;
  busy?: boolean; // запрос в полёте: выбор не меняется, фокус остаётся (aria-disabled, не disabled)
  // подписи словами («Все · Живые · Пилот · Ожидание»): колонка по своей подписи, а не 40 px
  wide?: boolean;
  className?: string;
};

// Сегментный переключатель «RU · EN» (язык в карточке сотрудника): одна строка, выбранный сегмент
// подсвечен. Кнопки с aria-pressed в группе с именем; повторный выбор текущего onChange не зовёт.
// busy — как Button loading: disabled снял бы фокус с нажатого сегмента (в браузере он падает на
// body), поэтому aria-disabled + aria-busy, а нажатие гасится здесь.
export function Segmented<V extends string>({
  label,
  options,
  value,
  onChange,
  busy,
  wide,
  className,
}: SegmentedProps<V>) {
  return (
    <div
      role="group"
      aria-label={label}
      aria-busy={busy || undefined}
      className={cx(styles.root, wide && styles.wide, className)}
    >
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            className={styles.segment}
            lang={o.lang}
            title={o.title}
            aria-label={o.name}
            aria-pressed={selected}
            aria-disabled={busy || undefined}
            onClick={() => !busy && !selected && onChange(o.value)}
          >
            {o.icon ? <Icon name={o.icon} size={16} /> : o.text}
          </button>
        );
      })}
    </div>
  );
}
