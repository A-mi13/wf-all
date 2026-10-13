import type { InputHTMLAttributes, ReactNode } from 'react';
import { type RegisterOptions, useFormContext } from 'react-hook-form';
import { cx } from '../cx';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldError } from './FieldError';

export type CheckboxFieldProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'name' | 'type' | 'children' | 'value' | 'defaultValue' | 'onChange' | 'onBlur'
> & {
  name: string;
  label: ReactNode; // «Я сохранил коды» (*)
  hint?: ReactNode;
  rules?: RegisterOptions;
};

// Флажок по макету 13 («Переключатели и выбор»): значение формы — boolean.
export function CheckboxField({ name, label, hint, rules, ...rest }: CheckboxFieldProps) {
  const { register } = useFormContext();
  const field = useField(name, Boolean(hint));
  return (
    <div className={cx(styles.field, field.invalid && styles.invalid)}>
      <label className={styles.checkRow}>
        <span className={styles.box}>
          <input
            {...rest}
            id={field.id}
            type="checkbox"
            className={styles.checkInput}
            aria-invalid={field.invalid || undefined}
            aria-describedby={field.describedBy}
            {...register(name, rules)}
          />
          <svg
            viewBox="0 0 24 24"
            width="14"
            height="14"
            aria-hidden="true"
            className={styles.checkMark}
          >
            <path d="M4.5 12.4 L9.6 17.5 L19.5 6.8" />
          </svg>
        </span>
        <span className={styles.checkLabel}>{label}</span>
      </label>
      {hint && (
        <p id={field.hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      <FieldError name={name} id={field.errorId} />
    </div>
  );
}
