import type { InputHTMLAttributes, ReactNode } from 'react';
import { type RegisterOptions, useFormContext } from 'react-hook-form';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldShell } from './FieldShell';

export type TextFieldProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'name' | 'type' | 'children' | 'value' | 'defaultValue' | 'onChange' | 'onBlur'
> & {
  name: string;
  label: ReactNode;
  hint?: ReactNode;
  type?: 'text' | 'email' | 'url' | 'tel' | 'number' | 'search';
  rules?: RegisterOptions; // правила react-hook-form; текст — ui.field_errors.<правило> или message
};

export function TextField({ name, label, hint, type = 'text', rules, ...rest }: TextFieldProps) {
  const { register } = useFormContext();
  const field = useField(name, Boolean(hint));
  return (
    <FieldShell name={name} field={field} label={label} hint={hint}>
      <input
        {...rest}
        id={field.id}
        type={type}
        className={styles.control}
        aria-invalid={field.invalid || undefined}
        aria-describedby={field.describedBy}
        {...register(name, rules)}
      />
    </FieldShell>
  );
}
