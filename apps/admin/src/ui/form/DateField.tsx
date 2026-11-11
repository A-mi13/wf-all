import { type ReactNode, useMemo, useRef, useState } from 'react';
import {
  type FieldValues,
  type RegisterOptions,
  useController,
  useFormContext,
  type Validate,
} from 'react-hook-form';
import { useLocale, useTranslations } from 'use-intl';
import { Icon } from '../icon';
import { useIsMobile } from '../media';
import { Sheet } from '../overlay/Sheet';
import { Popover } from '../popover/Popover';
import { Calendar } from './Calendar';
import styles from './DateField.module.css';
import { useField } from './field';
import fieldStyles from './Field.module.css';
import { FieldShell } from './FieldShell';
import { useFocusAfterSheet } from './focusAfterSheet';
import { dateFormat, parseIso, toIso } from './isoDate';

export type DateFieldProps = {
  name: string; // значение формы — 'YYYY-MM-DD' или ''
  label: string;
  min?: string; // YYYY-MM-DD: дни раньше — недоступны в сетке, текстом — ошибка min
  max?: string;
  hint?: ReactNode;
  rules?: RegisterOptions; // свои validate идут первыми (например, range фильтра журнала)
  disabled?: boolean;
  defaultOpen?: boolean; // календарь открыт с первого кадра — истории витрины и снимки
};

type Validators = Record<string, Validate<unknown, FieldValues>>;

// Поле даты (спека UI-фундамента §4.17) вместо браузерного type="date" (чужой вид, неделя по ОС): текст в
// формате языка интерфейса (маска по Intl#formatToParts: набираются цифры, разделители подставляются;
// ISO вставкой тоже понимается) и кнопка «открыть календарь» → Calendar на Popover kind 'modal' (широкий
// экран; своя ловушка Tab, Esc и щелчок снаружи в стеке оверлеев, фокус после закрытия — на кнопку) или в
// Sheet (телефон). Значение формы — строка YYYY-MM-DD;
// негодный текст остаётся значением и не проходит проверку format (ui.field_errors.format), пусто — ''.
export function DateField({
  name,
  label,
  min,
  max,
  hint,
  rules,
  disabled,
  defaultOpen = false,
}: DateFieldProps) {
  const t = useTranslations('ui.date');
  const locale = useLocale();
  const fmt = useMemo(() => dateFormat(locale), [locale]);
  const mobile = useIsMobile();
  const { control } = useFormContext();
  const own = rules?.validate;
  const validate: Validators = {
    ...(typeof own === 'function' ? { custom: own } : own),
    format: (v) => typeof v !== 'string' || v === '' || parseIso(v) !== null,
    // границы — только годные ISO (min «по» — значение «с», там может быть недописанный текст)
    min: (v) => !parseIso(min) || typeof v !== 'string' || !parseIso(v) || v >= (min ?? ''),
    max: (v) => !parseIso(max) || typeof v !== 'string' || !parseIso(v) || v <= (max ?? ''),
  };
  // поля контроллера по отдельности: ref — только в разметку (правило react-hooks/refs)
  const {
    field: { ref: inputRef, value: raw, onChange, onBlur },
  } = useController({
    name,
    control,
    rules: { ...(rules as Parameters<typeof useController>[0]['rules']), validate },
  });
  const field = useField(name, Boolean(hint));
  const button = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(defaultOpen);
  // шторка закрылась — фокус на кнопку календаря (Safari: касание её не фокусирует; ревью UF2a/II-A, I1)
  useFocusAfterSheet(open && mobile, button);

  const value = typeof raw === 'string' ? raw : '';
  const show = (v: string) => {
    const d = parseIso(v);
    return d ? fmt.format(d) : v;
  };
  const [text, setText] = useState(() => show(value));
  // Значение сменили снаружи (адрес, сброс фильтров) или сменился язык — текст за ним; собственный ввод
  // (текст уже разбирается в это значение) не трогаем.
  const [synced, setSynced] = useState(`${locale}|${value}`);
  if (synced !== `${locale}|${value}`) {
    setSynced(`${locale}|${value}`);
    const typed = fmt.parse(text);
    if (!typed || toIso(typed) !== value || !synced.startsWith(`${locale}|`)) setText(show(value));
  }

  const select = (iso: string) => {
    setText(show(iso));
    onChange(iso);
    setOpen(false);
  };
  const calendar = (
    <Calendar
      value={parseIso(value)}
      min={parseIso(min)}
      max={parseIso(max)}
      onSelect={(d) => select(toIso(d))}
    />
  );

  return (
    <FieldShell name={name} field={field} label={label} hint={hint}>
      <div className={fieldStyles.withAction}>
        <input
          ref={inputRef}
          id={field.id}
          name={name}
          type="text"
          inputMode="numeric"
          autoComplete="off"
          spellCheck={false}
          placeholder={t('format')}
          className={fieldStyles.control}
          aria-invalid={field.invalid || undefined}
          aria-describedby={field.describedBy}
          disabled={disabled}
          value={text}
          onChange={(e) => {
            // разделители языка подставляет маска (цифровая клавиатура iOS их не даёт). Набор в конец — новый текст
            // = прежний + один знак: только тогда лишняя цифра года отбрасывается (isoDate.ts). Сверка по тексту
            // строже позиции курсора: цифра в середину года даёт другой префикс, вставка — больше одного знака;
            // а та же цифра перед такой же («2026» → «20266») — та же строка, отбросить её — ничего не потерять.
            const entered = e.target.value;
            const typedAtEnd = entered.length === text.length + 1 && entered.startsWith(text);
            const next = fmt.mask(entered, { typedAtEnd });
            setText(next);
            const d = fmt.parse(next);
            onChange(next.trim() === '' ? '' : d ? toIso(d) : next.trim());
          }}
          onBlur={() => {
            const d = fmt.parse(text);
            if (d) setText(fmt.format(d));
            onBlur();
          }}
        />
        <button
          ref={button}
          type="button"
          className={fieldStyles.action}
          aria-label={t('open', { label })}
          aria-haspopup="dialog"
          aria-expanded={open}
          disabled={disabled}
          onClick={() => setOpen((v) => !v)}
        >
          <Icon name="calendar" size={20} />
        </button>
      </div>
      {!mobile && (
        <Popover
          anchor={button}
          open={open}
          onClose={() => setOpen(false)}
          kind="modal"
          placement="bottom-end"
          label={t('dialog', { label })}
          className={styles.popover}
        >
          {open && calendar}
        </Popover>
      )}
      {mobile && (
        <Sheet open={open} onClose={() => setOpen(false)} title={label}>
          {calendar}
        </Sheet>
      )}
    </FieldShell>
  );
}
