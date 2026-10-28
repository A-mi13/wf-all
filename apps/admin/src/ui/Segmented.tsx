import { cx } from './cx';
import styles from './Segmented.module.css';

export type SegmentOption<V extends string = string> = {
  value: V;
  text: string; // видимый текст сегмента (короткий: «RU»)
  name?: string; // доступное имя, если видимого мало («RU — Русский»); начинаться должно с text
  title?: string; // подсказка при наведении
  lang?: string; // язык подписи сегмента
};

export type SegmentedProps<V extends string = string> = {
  label: string; // имя группы для читалки
  options: SegmentOption<V>[];
  value: V;
  onChange: (value: V) => void;
  busy?: boolean; // запрос в полёте: выбор не меняется, фокус остаётся (aria-disabled, не disabled)
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
  className,
}: SegmentedProps<V>) {
  return (
    <div
      role="group"
      aria-label={label}
      aria-busy={busy || undefined}
      className={cx(styles.root, className)}
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
            {o.text}
          </button>
        );
      })}
    </div>
  );
}
