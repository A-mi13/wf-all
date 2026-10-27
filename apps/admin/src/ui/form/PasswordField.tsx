import { type InputHTMLAttributes, type ReactNode, useState } from 'react';
import { type RegisterOptions, useFormContext } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldShell } from './FieldShell';

export type PasswordFieldProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'name' | 'type' | 'children' | 'value' | 'defaultValue' | 'onChange' | 'onBlur'
> & {
  name: string;
  label: ReactNode;
  hint?: ReactNode; // требования к паролю (принятие приглашения, смена)
  rules?: RegisterOptions;
  autoComplete?: 'current-password' | 'new-password';
};

// Поле пароля (* в макете — дорисовано): кнопка «Показать/Скрыть» рядом. Пароль не попадает в
// черновики формы — экран не передаёт его в useDraft.
export function PasswordField({
  name,
  label,
  hint,
  rules,
  autoComplete = 'current-password',
  ...rest
}: PasswordFieldProps) {
  const t = useTranslations('ui.password');
  const { register } = useFormContext();
  const field = useField(name, Boolean(hint));
  const [visible, setVisible] = useState(false);
  return (
    <FieldShell name={name} field={field} label={label} hint={hint}>
      <div className={styles.withAction}>
        <input
          {...rest}
          id={field.id}
          type={visible ? 'text' : 'password'}
          autoComplete={autoComplete}
          spellCheck={false}
          autoCapitalize="off"
          className={styles.control}
          aria-invalid={field.invalid || undefined}
          aria-describedby={field.describedBy}
          {...register(name, rules)}
        />
        <button
          type="button"
          className={styles.action}
          aria-controls={field.id}
          aria-label={visible ? t('hide') : t('show')}
          onClick={() => setVisible((v) => !v)}
          disabled={rest.disabled}
        >
          <svg
            viewBox="0 0 24 24"
            width="18"
            height="18"
            aria-hidden="true"
            className={styles.icon}
          >
            <path d="M2.5 12 C5 7 8.3 5 12 5 S19 7 21.5 12 C19 17 15.7 19 12 19 S5 17 2.5 12 Z" />
            <circle cx="12" cy="12" r="3" />
            {visible && <path d="M4 4 L20 20" />}
          </svg>
        </button>
      </div>
    </FieldShell>
  );
}
