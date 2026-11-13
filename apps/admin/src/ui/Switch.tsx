import { cx } from './cx';
import styles from './Switch.module.css';

export type SwitchProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string; // имя переключателя для читалки (и видимая подпись, если не hideLabel)
  hideLabel?: boolean; // подпись только для читалки — ячейка матрицы «функция × город»
  disabled?: boolean; // нельзя (нет права)
  busy?: boolean; // запрос в полёте: значение не меняется, фокус остаётся (aria-disabled, не disabled)
  className?: string;
};

// Переключатель (флаги функций — макет 07, включение страны): кнопка role="switch" с aria-checked.
// Клик, Space и Enter — нативное нажатие кнопки. Сам значение не хранит: экран меняет checked по
// ответу или оптимистично. busy — как Button loading: disabled снял бы фокус с переключателя (в
// браузере он падает на body), поэтому aria-disabled + aria-busy, а нажатие гасится здесь.
export function Switch({
  checked,
  onChange,
  label,
  hideLabel,
  disabled,
  busy,
  className,
}: SwitchProps) {
  const held = Boolean(busy) && !disabled;
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      aria-disabled={held || undefined}
      aria-busy={busy || undefined}
      onClick={held ? undefined : () => onChange(!checked)}
      className={cx(styles.switch, className)}
    >
      <span className={cx(styles.track, checked && styles.on)} aria-hidden="true">
        <span className={styles.thumb} />
      </span>
      <span className={hideLabel ? styles.srOnly : styles.label}>{label}</span>
    </button>
  );
}
