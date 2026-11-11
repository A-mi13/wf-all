import { type KeyboardEvent, type ReactNode, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
import { Icon } from '../icon';
import { useIsMobile } from '../media';
import { Sheet } from '../overlay/Sheet';
import { Popover } from '../popover/Popover';
import fieldStyles from './Field.module.css';
import { useFocusAfterSheet } from './focusAfterSheet';
import styles from './Select.module.css';
import type { Option } from './SelectField';

export type SelectProps = {
  id: string;
  name: string;
  value: string;
  onChange: (value: string) => void;
  onBlur: () => void;
  options: Option[];
  placeholder?: string; // текст кнопки, когда значение не среди опций (обычно '')
  disabled?: boolean;
  invalid?: boolean;
  describedBy?: string;
  labelledBy: string; // id подписи поля — единственный источник имени combobox (APG, I2)
  label: ReactNode; // заголовок шторки на телефоне
  buttonRef?: (el: HTMLButtonElement | null) => void; // фокус ошибки поля (field.ref react-hook-form)
  defaultOpen?: boolean; // открыт с первого кадра — истории витрины и снимки
};

// Пауза, после которой набор typeahead начинается заново.
const TYPEAHEAD_MS = 500;
// Шторка телефона показывает поиск, когда опций больше этого.
const SEARCH_FROM = 10;
const PAGE = 10;

// Свой выпадающий список (спека UI-фундамента §4.16) — шаблон WAI-ARIA «select-only combobox»: кнопка
// role="combobox" (имя — только подпись поля, значение — текст кнопки), список role="listbox" с
// role="option" (aria-selected, data-value, aria-disabled). Фокус всегда на кнопке, активная опция —
// aria-activedescendant. Широкий экран — Popover kind 'menu' под полем (верхний слой, стек оверлеев:
// Esc закрывает только список, focusout и нажатие снаружи — тоже); телефон — Sheet (подпись поля в
// заголовке, крупные строки, выбранная — галочкой, > 10 опций — поиск сверху, выбор закрывает).
// Клавиатура: ↓/↑/Home/End/PageUp/PageDown, Enter/Space — выбрать, Esc — закрыть без выбора, буквы —
// typeahead (на закрытом — открывает), Tab — закрыть без выбора и уйти. Enter на кнопке не отправляет
// форму: кнопка type="button", нажатие гасится.
export function Select({
  id,
  name,
  value,
  onChange,
  onBlur,
  options,
  placeholder,
  disabled,
  invalid,
  describedBy,
  labelledBy,
  label,
  buttonRef,
  defaultOpen = false,
}: SelectProps) {
  const t = useTranslations('ui.select');
  const mobile = useIsMobile();
  const button = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(defaultOpen);
  const [active, setActive] = useState(() =>
    defaultOpen ? options.findIndex((o) => o.value === value && !o.disabled) : -1,
  );
  const [query, setQuery] = useState('');
  const typed = useRef({ text: '', at: 0 });
  // Нажатие Enter/Space обработано на keydown — синтетический click кнопки (Space — на keyup) не
  // переключает список второй раз.
  const skipClick = useRef(false);

  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  const q = query.trim().toLocaleLowerCase();
  const sheet = open && mobile;
  const shown =
    sheet && q ? options.filter((o) => o.label.toLocaleLowerCase().includes(q)) : options;
  const selected = options.find((o) => o.value === value);
  const enabled = (i: number) => i >= 0 && i < shown.length && !shown[i]?.disabled;

  // Активная — в видимую часть списка (длинные списки, страны ~250).
  useEffect(() => {
    if (!open || active < 0) return;
    document.getElementById(`${id}-opt-${active}`)?.scrollIntoView?.({ block: 'nearest' });
  }, [open, active, id]);

  useFocusAfterSheet(sheet, button);

  const firstEnabled = (from: number, step: 1 | -1) => {
    for (let i = from; i >= 0 && i < shown.length; i += step) if (enabled(i)) return i;
    return -1;
  };

  const show = (at?: number) => {
    if (disabled) return;
    const current = options.findIndex((o) => o.value === value);
    setQuery('');
    setActive(at ?? (enabled(current) ? current : firstEnabled(0, 1)));
    setOpen(true);
  };
  const close = () => {
    setOpen(false);
    setActive(-1);
    setQuery('');
  };
  const choose = (i: number) => {
    const o = shown[i];
    if (!o || o.disabled) return;
    if (o.value !== value) onChange(o.value);
    close();
  };

  // Переход к опции по началу подписи: повтор одной буквы — по кругу по опциям на неё; несколько
  // разных букв подряд — по началу строки от текущей; совпадения нет — активная прежняя.
  const typeahead = (ch: string, from: number) => {
    const now = Date.now();
    const prev = now - typed.current.at > TYPEAHEAD_MS ? '' : typed.current.text;
    const text = (prev + ch).toLocaleLowerCase();
    typed.current = { text, at: now };
    const same = [...text].every((c) => c === text[0]);
    const needle = same ? (text[0] ?? '') : text;
    const start = same ? from + 1 : Math.max(from, 0);
    for (let k = 0; k < shown.length; k++) {
      const i = (start + k + shown.length) % shown.length;
      if (enabled(i) && shown[i]?.label.toLocaleLowerCase().startsWith(needle)) return i;
    }
    return from;
  };
  const typing = () => Date.now() - typed.current.at <= TYPEAHEAD_MS && typed.current.text !== '';

  // Клавиши открытого списка — общие для кнопки (Popover) и списка в шторке.
  const onListKey = (e: KeyboardEvent<HTMLElement>, allowType: boolean): boolean => {
    const move = (i: number) => {
      e.preventDefault();
      if (i >= 0) setActive(i);
    };
    switch (e.key) {
      case 'ArrowDown':
        move(firstEnabled(active + 1, 1));
        return true;
      case 'ArrowUp':
        move(active < 0 ? firstEnabled(shown.length - 1, -1) : firstEnabled(active - 1, -1));
        return true;
      case 'Home':
        move(firstEnabled(0, 1));
        return true;
      case 'End':
        move(firstEnabled(shown.length - 1, -1));
        return true;
      case 'PageDown':
        move(firstEnabled(Math.min(active + PAGE, shown.length - 1), -1));
        return true;
      case 'PageUp':
        move(firstEnabled(Math.max(active - PAGE, 0), 1));
        return true;
      case 'Enter':
        e.preventDefault();
        if (enabled(active)) choose(active);
        return true;
      case ' ':
        if (allowType && typing()) {
          e.preventDefault();
          setActive(typeahead(' ', active));
          return true;
        }
        e.preventDefault();
        if (enabled(active)) choose(active);
        return true;
      default:
        if (allowType && isPrintable(e)) {
          e.preventDefault();
          setActive(typeahead(e.key, active));
          return true;
        }
        return false;
    }
  };

  const onButtonKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (disabled) return;
    if (e.key === 'Enter' || e.key === ' ') skipClick.current = true;
    if (open) {
      if (!sheet) onListKey(e, true);
      return;
    }
    const current = options.findIndex((o) => o.value === value);
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
      case 'Enter':
      case ' ':
        e.preventDefault();
        show();
        return;
      case 'Home':
        e.preventDefault();
        show(firstEnabled(0, 1));
        return;
      case 'End':
        e.preventDefault();
        show(firstEnabled(options.length - 1, -1));
        return;
      default:
        if (isPrintable(e)) {
          e.preventDefault();
          show(typeahead(e.key, enabled(current) ? current : -1));
        }
    }
  };

  const list = (inSheet: boolean) => (
    <div
      role="listbox"
      id={listId}
      aria-labelledby={labelledBy}
      aria-activedescendant={inSheet && enabled(active) ? optionId(active) : undefined}
      tabIndex={inSheet ? 0 : undefined}
      className={cx(styles.list, inSheet && styles.sheetList)}
      onKeyDown={inSheet ? (e) => onListKey(e, true) : undefined}
    >
      {shown.map((o, i) => (
        <div
          key={o.value}
          id={optionId(i)}
          role="option"
          aria-selected={o.value === value}
          aria-disabled={o.disabled ? 'true' : undefined}
          data-value={o.value}
          data-active={i === active || undefined}
          className={cx(styles.option, inSheet && styles.sheetOption)}
          onClick={() => choose(i)}
          onMouseMove={() => {
            if (!o.disabled && i !== active) setActive(i);
          }}
        >
          <span className={styles.optionText}>{o.label}</span>
          {o.value === value && <Icon name="check" size={16} className={styles.check} />}
        </div>
      ))}
      {shown.length === 0 && q && <p className={styles.empty}>{t('empty')}</p>}
    </div>
  );

  return (
    <div className={styles.root}>
      <button
        ref={(el) => {
          button.current = el;
          buttonRef?.(el);
        }}
        type="button"
        id={id}
        name={name}
        role="combobox"
        aria-labelledby={labelledBy}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open && !sheet && enabled(active) ? optionId(active) : undefined}
        aria-invalid={invalid || undefined}
        aria-describedby={describedBy}
        disabled={disabled}
        className={cx(fieldStyles.control, styles.trigger)}
        onClick={(e) => {
          // click клавиатуры (detail 0) после обработанного keydown гасится; щелчок мышью (detail ≥ 1)
          // — всегда: метку мог оставить Enter, чей keyup ушёл в шторку (ревью UF2a/II-A, m1)
          const skip = skipClick.current && e.detail === 0;
          skipClick.current = false;
          if (skip) return;
          if (open) close();
          else show();
        }}
        onKeyDown={onButtonKey}
        onKeyUp={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            // click от Space приходит после keyup — снять метку следующим тиком
            setTimeout(() => {
              skipClick.current = false;
            }, 0);
          }
        }}
        onBlur={onBlur}
      >
        <span className={cx(styles.value, !selected && styles.placeholder)}>
          {selected?.label ?? placeholder ?? ''}
        </span>
        <Icon name="chevron-down" size={16} className={styles.chevron} />
      </button>
      {!mobile && (
        <Popover anchor={button} open={open} onClose={close} kind="menu" className={styles.menu}>
          {/* закрытый — списка нет в DOM: подпись поля не находит второй элемент (getByLabelText), 250
              опций не висят в дереве */}
          {open && list(false)}
        </Popover>
      )}
      {mobile && (
        <Sheet open={open} onClose={close} title={label}>
          {options.length > SEARCH_FROM && (
            // поиск закреплён сверху тела шторки: прокрутка к выбранной его не уносит (ревью UF2a/II-A, I2)
            <div className={styles.sheetSearch}>
              <input
                type="search"
                className={fieldStyles.control}
                aria-label={t('search')}
                aria-controls={listId}
                aria-activedescendant={enabled(active) ? optionId(active) : undefined}
                value={query}
                autoComplete="off"
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActive(-1);
                }}
                // в поле поиска — только переход по списку и выбор; Space, Home, End — для текста
                onKeyDown={(e) => {
                  if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter') {
                    onListKey(e, false);
                  }
                }}
              />
            </div>
          )}
          {list(true)}
        </Sheet>
      )}
    </div>
  );
}

const isPrintable = (e: KeyboardEvent) =>
  e.key.length === 1 && e.key !== ' ' && !e.ctrlKey && !e.metaKey && !e.altKey;
