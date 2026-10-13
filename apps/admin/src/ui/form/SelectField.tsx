import type { ReactNode, SelectHTMLAttributes } from 'react';
import { type RegisterOptions, useFormContext } from 'react-hook-form';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldShell } from './FieldShell';

export type Option = { value: string; label: string; disabled?: boolean };

export type SelectFieldProps = Omit<
  SelectHTMLAttributes<HTMLSelectElement>,
  'name' | 'children' | 'value' | 'defaultValue' | 'onChange' | 'onBlur' | 'multiple'
> & {
  name: string;
  label: ReactNode;
  options: Option[];
  placeholder?: string; // пустой выбор (значение ''), сам не выбирается
  hint?: ReactNode;
  rules?: RegisterOptions;
};

// Выпадающий список (макет 13): нативный select — клавиатура, читалка экрана и телефон «из
// коробки»; вид — по макету, стрелка — своя.
export function SelectField({
  name,
  label,
  options,
  placeholder,
  hint,
  rules,
  ...rest
}: SelectFieldProps) {
  const { register } = useFormContext();
  const field = useField(name, Boolean(hint));
  return (
    <FieldShell name={name} field={field} label={label} hint={hint}>
      <div className={styles.withChevron}>
        <select
          {...rest}
          id={field.id}
          className={styles.control}
          aria-invalid={field.invalid || undefined}
          aria-describedby={field.describedBy}
          {...register(name, rules)}
        >
          {placeholder !== undefined && (
            <option value="" disabled>
              {placeholder}
            </option>
          )}
          {options.map((o) => (
            <option key={o.value} value={o.value} disabled={o.disabled}>
              {o.label}
            </option>
          ))}
        </select>
        <svg
          viewBox="0 0 24 24"
          width="16"
          height="16"
          aria-hidden="true"
          className={styles.chevron}
        >
          <path d="M4.5 9 L12 15.8 L19.5 9" />
        </svg>
      </div>
    </FieldShell>
  );
}
