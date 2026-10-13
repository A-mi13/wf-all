import { type ReactNode, useState } from 'react';
import { type RegisterOptions, useController, useFormContext } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldError } from './FieldError';
import type { Option } from './SelectField';

export type MultiSelectFieldProps = {
  name: string; // значение формы — string[]
  label: string;
  options: Option[];
  hint?: ReactNode;
  rules?: RegisterOptions;
  disabled?: boolean;
  // Поиск над списком: без onSearch фильтрует options по подписи; с onSearch — экран ищет сам
  // (города модератора — listGeoCities) и передаёт найденное в options.
  searchable?: boolean;
  onSearch?: (query: string) => void;
  searchPlaceholder?: string;
};

// Выбор нескольких значений (* — города модератора в форме приглашения): выбранное — чипами с
// кнопкой «Убрать», список — флажками. Подпись выбранного помнится, даже если поиск его скрыл.
export function MultiSelectField({
  name,
  label,
  options,
  hint,
  rules,
  disabled,
  searchable,
  onSearch,
  searchPlaceholder,
}: MultiSelectFieldProps) {
  const t = useTranslations('ui.multiselect');
  const { control } = useFormContext();
  const { field: ctl } = useController({ name, control, rules });
  const field = useField(name, Boolean(hint));
  const [query, setQuery] = useState('');
  const [labels, setLabels] = useState<Record<string, string>>({});

  // подписи всех когда-либо показанных вариантов (состояние из пропсов, без эффекта)
  if (options.some((o) => labels[o.value] !== o.label)) {
    setLabels((prev) => ({
      ...prev,
      ...Object.fromEntries(options.map((o) => [o.value, o.label])),
    }));
  }

  const value: string[] = Array.isArray(ctl.value) ? (ctl.value as string[]) : [];
  const toggle = (v: string) =>
    ctl.onChange(value.includes(v) ? value.filter((x) => x !== v) : [...value, v]);
  const q = query.trim().toLocaleLowerCase();
  const shown =
    onSearch || !q ? options : options.filter((o) => o.label.toLocaleLowerCase().includes(q));

  return (
    <fieldset
      className={cx(styles.field, styles.fieldset, field.invalid && styles.invalid)}
      aria-describedby={field.describedBy}
      disabled={disabled}
    >
      <legend className={styles.label}>{label}</legend>
      {value.length > 0 && (
        <ul className={styles.chips}>
          {value.map((v) => {
            const text = labels[v] ?? v;
            return (
              <li key={v} className={styles.chip}>
                {text}
                <button
                  type="button"
                  className={styles.chipRemove}
                  aria-label={t('remove', { label: text })}
                  onClick={() => toggle(v)}
                >
                  <svg
                    viewBox="0 0 24 24"
                    width="12"
                    height="12"
                    aria-hidden="true"
                    className={styles.icon}
                  >
                    <path d="M6 6 L18 18 M18 6 L6 18" />
                  </svg>
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {searchable && (
        <input
          type="search"
          className={styles.control}
          aria-label={t('search', { label })}
          placeholder={searchPlaceholder}
          value={query}
          autoComplete="off"
          // Enter в поиске — не отправка формы приглашения
          onKeyDown={(e) => {
            if (e.key === 'Enter') e.preventDefault();
          }}
          onChange={(e) => {
            setQuery(e.target.value);
            onSearch?.(e.target.value);
          }}
        />
      )}
      <div className={styles.options}>
        {shown.map((o, i) => (
          <label key={o.value} className={styles.option}>
            <span className={styles.box}>
              <input
                type="checkbox"
                className={styles.checkInput}
                checked={value.includes(o.value)}
                disabled={o.disabled}
                onChange={() => toggle(o.value)}
                onBlur={ctl.onBlur}
                // фокус ошибки сервера (setError с shouldFocus) — на первый вариант
                ref={i === 0 ? ctl.ref : undefined}
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
            <span className={styles.checkLabel}>{o.label}</span>
          </label>
        ))}
        {shown.length === 0 && <p className={styles.hint}>{t('empty')}</p>}
      </div>
      {hint && (
        <p id={field.hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      <FieldError name={name} id={field.errorId} />
    </fieldset>
  );
}
