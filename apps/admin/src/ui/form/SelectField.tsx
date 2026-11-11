import type { ReactNode } from 'react';
import { type RegisterOptions, useController, useFormContext } from 'react-hook-form';
import { useField } from './field';
import { FieldShell } from './FieldShell';
import { Select } from './Select';

export type Option = { value: string; label: string; disabled?: boolean };

export type SelectFieldProps = {
  name: string;
  label: ReactNode;
  options: Option[];
  // Пустое значение ('') — текст кнопки; у необязательного поля — ещё и опция «не выбрано»
  // (обязательное пустым не выбирается).
  placeholder?: string;
  hint?: ReactNode;
  rules?: RegisterOptions; // rules.onChange получает событие с e.target.value, как у нативного поля
  disabled?: boolean;
};

// Выпадающий список формы (макет 13; спека UI-фундамента §4.16): свой Select (кнопка combobox, список
// на Popover или в шторке телефона) под Controller react-hook-form. API прежний: экраны не меняются,
// rules.onChange вызывается с { target: { value } } (react-hook-form сам оборачивает значение), setValue
// экрана меняет кнопку. Имя combobox — только подпись поля.
export function SelectField({
  name,
  label,
  options,
  placeholder,
  hint,
  rules,
  disabled,
}: SelectFieldProps) {
  const { control } = useFormContext();
  const { field: ctl } = useController({
    name,
    control,
    rules: rules as Parameters<typeof useController>[0]['rules'],
  });
  const field = useField(name, Boolean(hint));
  const optional = placeholder !== undefined && !rules?.required;
  const list =
    optional && !options.some((o) => o.value === '')
      ? [{ value: '', label: placeholder }, ...options]
      : options;
  return (
    <FieldShell name={name} field={field} label={label} hint={hint}>
      <Select
        id={field.id}
        name={name}
        value={typeof ctl.value === 'string' ? ctl.value : ''}
        onChange={ctl.onChange}
        onBlur={ctl.onBlur}
        buttonRef={ctl.ref}
        options={list}
        placeholder={placeholder}
        disabled={disabled ?? ctl.disabled}
        invalid={field.invalid}
        describedBy={field.describedBy}
        labelledBy={field.labelId}
        label={label}
      />
    </FieldShell>
  );
}
