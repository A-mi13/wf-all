import { type KeyboardEvent, type ReactNode, useRef, useState } from 'react';
import { type RegisterOptions, useController, useFormContext } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
import { Icon } from '../icon';
import { useIsMobile } from '../media';
import { Sheet } from '../overlay/Sheet';
import { Popover } from '../popover/Popover';
import { useField } from './field';
import styles from './Field.module.css';
import { FieldError } from './FieldError';
import { useFocusAfterSheet } from './focusAfterSheet';
import type { Option } from './SelectField';
import list from './Select.module.css';

export type MultiSelectFieldProps = {
  name: string; // значение формы — string[]
  label: string;
  options: Option[];
  hint?: ReactNode;
  rules?: RegisterOptions;
  disabled?: boolean;
  // Поиск: без onSearch фильтрует options по подписи; с onSearch — экран ищет сам (города модератора —
  // listGeoCities) и передаёт найденное в options.
  searchable?: boolean;
  onSearch?: (query: string) => void;
  searchPlaceholder?: string;
  defaultOpen?: boolean; // открыт с первого кадра — истории витрины и снимки
};

// Выбор нескольких значений (* — города модератора): выбранное — чипами с кнопкой «Убрать», варианты —
// список role="listbox" aria-multiselectable на том же Popover, что Select (спека UI-фундамента §4.16; на
// телефоне — Sheet). Поиск — в якоре (m-o: поле ввода в пузыре забрало бы фокус): поле role="combobox",
// ввод открывает список; без поиска якорь — кнопка combobox. Нажатие опции отмечает и снимает, список
// остаётся открытым; ↓/↑ — активная, Enter (и Space на кнопке) — отметить; Esc, Tab, нажатие снаружи —
// закрыть. Enter в поиске форму не отправляет. Подпись выбранного помнится, даже если поиск его скрыл.
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
  defaultOpen = false,
}: MultiSelectFieldProps) {
  const t = useTranslations('ui.multiselect');
  const mobile = useIsMobile();
  const { control } = useFormContext();
  // поля контроллера по отдельности: ref — только в разметку (правило react-hooks/refs)
  const {
    field: { ref: fieldRef, value: raw, onChange, onBlur },
  } = useController({ name, control, rules });
  const field = useField(name, Boolean(hint));
  const anchor = useRef<HTMLElement | null>(null);
  const [open, setOpen] = useState(defaultOpen);
  const [active, setActive] = useState(-1);
  const [query, setQuery] = useState('');
  const [labels, setLabels] = useState<Record<string, string>>({});

  // подписи всех когда-либо показанных вариантов (состояние из пропсов, без эффекта)
  if (options.some((o) => labels[o.value] !== o.label)) {
    setLabels((prev) => ({
      ...prev,
      ...Object.fromEntries(options.map((o) => [o.value, o.label])),
    }));
  }

  // Шторка закрылась — фокус на кнопку (Safari: нажатие кнопку не фокусирует).
  useFocusAfterSheet(open && mobile, anchor);
  // Space/Enter обработаны на keydown — click клавиатуры (Firefox шлёт его на keyup Space) список не
  // переключает второй раз (ревью UF2a/II-A, m2; как у Select)
  const skipClick = useRef(false);

  const value: string[] = Array.isArray(raw)
    ? raw.filter((v): v is string => typeof v === 'string')
    : [];
  const toggle = (v: string) =>
    onChange(value.includes(v) ? value.filter((x) => x !== v) : [...value, v]);
  const q = query.trim().toLocaleLowerCase();
  const shown =
    onSearch || !q ? options : options.filter((o) => o.label.toLocaleLowerCase().includes(q));
  const enabled = (i: number) => i >= 0 && i < shown.length && !shown[i]?.disabled;
  const listId = `${field.id}-list`;
  const legendId = `${field.id}-legend`;
  const optionId = (i: number) => `${field.id}-opt-${i}`;
  const step = (from: number, dir: 1 | -1) => {
    for (let i = from; i >= 0 && i < shown.length; i += dir) if (enabled(i)) return i;
    return -1;
  };
  const close = () => {
    setOpen(false);
    setActive(-1);
  };

  const onKey = (e: KeyboardEvent<HTMLElement>, space: boolean) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        setActive(step(0, 1));
        return;
      }
      const next =
        e.key === 'ArrowDown'
          ? step(active + 1, 1)
          : step(active < 0 ? shown.length - 1 : active - 1, -1);
      if (next >= 0) setActive(next);
    } else if (e.key === 'Enter' || (space && e.key === ' ')) {
      // Enter в поиске — не отправка формы приглашения
      e.preventDefault();
      if (!open) setOpen(true);
      else if (enabled(active)) toggle(shown[active]?.value ?? '');
    }
  };

  const items = (inSheet: boolean) => (
    <div
      role="listbox"
      id={listId}
      aria-labelledby={legendId}
      aria-multiselectable="true"
      className={cx(list.list, inSheet && list.sheetList)}
    >
      {shown.map((o, i) => (
        <div
          key={o.value}
          id={optionId(i)}
          role="option"
          aria-selected={value.includes(o.value)}
          aria-disabled={o.disabled ? 'true' : undefined}
          data-value={o.value}
          data-active={i === active || undefined}
          className={cx(list.option, inSheet && list.sheetOption)}
          onClick={() => {
            if (o.disabled) return;
            setActive(i);
            toggle(o.value);
          }}
          onMouseMove={() => {
            if (!o.disabled && i !== active) setActive(i);
          }}
        >
          <span className={list.optionText}>{o.label}</span>
          {value.includes(o.value) && <Icon name="check" size={16} className={list.check} />}
        </div>
      ))}
      {shown.length === 0 && <p className={list.empty}>{t('empty')}</p>}
    </div>
  );

  // якорь пузыря и фокус ошибки поля (field.ref react-hook-form) — один элемент
  const setAnchor = (el: HTMLElement | null) => {
    anchor.current = el;
    fieldRef(el);
  };

  const search = (inSheet: boolean) => (
    <input
      ref={inSheet ? undefined : setAnchor}
      type="search"
      role="combobox"
      className={styles.control}
      aria-label={t('search', { label })}
      aria-expanded={inSheet ? undefined : open}
      aria-controls={listId}
      aria-autocomplete="list"
      aria-activedescendant={open && enabled(active) ? optionId(active) : undefined}
      placeholder={searchPlaceholder}
      value={query}
      autoComplete="off"
      onClick={inSheet ? undefined : () => setOpen(true)}
      onKeyDown={(e) => onKey(e, false)}
      onBlur={inSheet ? undefined : onBlur}
      onChange={(e) => {
        setQuery(e.target.value);
        setActive(-1);
        setOpen(true);
        onSearch?.(e.target.value);
      }}
    />
  );

  // Без поиска и на телефоне якорь — кнопка combobox: «Выбрать» или «Выбрано: N».
  const button = (
    <button
      ref={setAnchor}
      type="button"
      role="combobox"
      className={cx(styles.control, list.trigger)}
      aria-labelledby={legendId}
      aria-haspopup="listbox"
      aria-expanded={open}
      aria-controls={listId}
      aria-activedescendant={open && !mobile && enabled(active) ? optionId(active) : undefined}
      onClick={(e) => {
        const skip = skipClick.current && e.detail === 0;
        skipClick.current = false;
        if (skip) return;
        if (open) close();
        else setOpen(true);
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') skipClick.current = true;
        onKey(e, true);
      }}
      onBlur={onBlur}
    >
      <span className={cx(list.value, value.length === 0 && list.placeholder)}>
        {value.length ? t('selected', { count: value.length }) : t('choose')}
      </span>
      <Icon name="chevron-down" size={16} className={list.chevron} />
    </button>
  );

  return (
    <fieldset
      className={cx(styles.field, styles.fieldset, field.invalid && styles.invalid)}
      aria-describedby={field.describedBy}
      disabled={disabled}
    >
      <legend id={legendId} className={styles.label}>
        {label}
      </legend>
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
                  <Icon name="close" size={12} />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {searchable && !mobile ? search(false) : button}
      {!mobile && (
        <Popover anchor={anchor} open={open} onClose={close} kind="menu" className={list.menu}>
          {open && items(false)}
        </Popover>
      )}
      {mobile && (
        <Sheet open={open} onClose={close} title={label}>
          {/* поиск закреплён сверху тела шторки (ревью UF2a/II-A, I2) */}
          {searchable && <div className={list.sheetSearch}>{search(true)}</div>}
          {items(true)}
        </Sheet>
      )}
      {hint && (
        <p id={field.hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      <FieldError name={name} id={field.errorId} />
    </fieldset>
  );
}
