import type { ReactNode } from 'react';
import { cx } from '../cx';
import type { useField } from './field';
import styles from './Field.module.css';
import { FieldError } from './FieldError';

// Каркас поля по макету 13 («Поля ввода»): подпись, контрол, подсказка, ошибка. Состояние ошибки
// красит рамку и подпись (цвет — не единственный признак: под полем текст).
export function FieldShell({
  name,
  field,
  label,
  hint,
  children,
}: {
  name: string;
  field: ReturnType<typeof useField>;
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className={cx(styles.field, field.invalid && styles.invalid)}>
      <label htmlFor={field.id} className={styles.label}>
        {label}
      </label>
      {children}
      {hint && (
        <p id={field.hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      <FieldError name={name} id={field.errorId} />
    </div>
  );
}
