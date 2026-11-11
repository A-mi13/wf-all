import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Icon } from '../icon';
import { Popover, type PopoverCloseReason } from '../popover/Popover';
import styles from './ColumnsMenu.module.css';
import type { Density } from './prefs';

export type ColumnsMenuItem = {
  id: string;
  header: string;
  checked: boolean; // столбец виден
  disabled: boolean; // нескрываемый или последний видимый
  byWidth: boolean; // скрыт шириной окна, а не пользователем — флажок снят, подпись-пояснение
};

export type ColumnsMenuProps = {
  items: ColumnsMenuItem[];
  onToggle: (id: string) => void;
  density: Density;
  onDensity: (d: Density) => void;
  showDensity: boolean; // на грубом указателе плотность всегда normal — выбора нет
};

const DENSITIES: Density[] = ['compact', 'normal'];

// Меню «Столбцы» (спека UI-фундамента §4.5, U23): кнопка-открыватель и Popover kind 'menu' по шаблону WAI-ARIA
// menu button — role="menu" с menuitemcheckbox (столбцы) и группой menuitemradio (плотность). Открытие — фокус на
// первый пункт; ↑/↓ по кругу, Home/End, Enter/Space — переключить (меню остаётся открытым); Esc — закрыть только
// меню (стек оверлеев) и вернуть фокус на кнопку; Tab и Shift+Tab — меню закрывается, фокус уходит дальше от кнопки.
export function ColumnsMenu({
  items,
  onToggle,
  density,
  onDensity,
  showDensity,
}: ColumnsMenuProps) {
  const t = useTranslations('table');
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const base = useId();
  const menuId = `${base}-menu`;

  const entries = () =>
    Array.from(menu.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]') ?? []);
  const focusAt = (i: number) => {
    const list = entries();
    if (list.length === 0) return;
    list[(i + list.length) % list.length]?.focus();
  };

  // фокус на первый пункт — когда меню в DOM (и при втором запуске StrictMode — уже внутри, не трогаем)
  useEffect(() => {
    if (!open) return;
    const el = menu.current;
    if (!el || el.contains(document.activeElement)) return;
    el.querySelector<HTMLElement>('[role^="menuitem"]')?.focus();
  }, [open]);

  const onClose = (reason: PopoverCloseReason) => {
    setOpen(false);
    if (reason === 'escape') button.current?.focus();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const list = entries();
    const at = list.findIndex((el) => el === document.activeElement);
    const current = list[at];
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        focusAt(at + 1);
        return;
      case 'ArrowUp':
        e.preventDefault();
        focusAt(at < 0 ? -1 : at - 1);
        return;
      case 'Home':
        e.preventDefault();
        focusAt(0);
        return;
      case 'End':
        e.preventDefault();
        focusAt(-1);
        return;
      case 'Tab':
        // Tab и Shift+Tab закрывают меню (APG); фокус — с кнопки, браузер или ловушка панели ведут его дальше от
        // неё (пункт с tabindex -1 ловушка Drawer не знает — увела бы на первый элемент панели)
        button.current?.focus();
        setOpen(false);
        return;
      case 'Enter':
      case ' ':
        e.preventDefault();
        current?.click();
        return;
    }
  };

  return (
    <>
      <button
        ref={button}
        type="button"
        className={styles.button}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        {t('columns.button')}
      </button>
      <Popover
        anchor={button}
        open={open}
        onClose={onClose}
        kind="menu"
        placement="bottom-end"
        className={styles.popover}
      >
        {open && (
          <div
            ref={menu}
            id={menuId}
            role="menu"
            aria-label={t('columns.button')}
            className={styles.menu}
            onKeyDown={onKeyDown}
          >
            {items.map((c) => {
              const hint = `${base}-${c.id}-hint`;
              return (
                <div
                  key={c.id}
                  role="menuitemcheckbox"
                  tabIndex={-1}
                  aria-checked={c.checked}
                  // имя — только подпись столбца; «скрыт по ширине» — описание
                  aria-label={c.header}
                  aria-disabled={c.disabled || undefined}
                  aria-describedby={c.byWidth ? hint : undefined}
                  className={styles.item}
                  onClick={() => {
                    if (!c.disabled) onToggle(c.id);
                  }}
                >
                  <span className={styles.mark} aria-hidden="true">
                    {c.checked && <Icon name="check" size={16} />}
                  </span>
                  <span className={styles.text}>
                    {c.header}
                    {c.byWidth && (
                      <span id={hint} className={styles.hint}>
                        {t('columns.hidden_by_width')}
                      </span>
                    )}
                  </span>
                </div>
              );
            })}
            {showDensity && (
              <div role="group" aria-label={t('density.label')} className={styles.group}>
                <span className={styles.groupLabel} aria-hidden="true">
                  {t('density.label')}
                </span>
                {DENSITIES.map((d) => (
                  <div
                    key={d}
                    role="menuitemradio"
                    tabIndex={-1}
                    aria-checked={d === density}
                    className={styles.item}
                    onClick={() => onDensity(d)}
                  >
                    <span className={styles.mark} aria-hidden="true">
                      {d === density && <Icon name="check" size={16} />}
                    </span>
                    <span className={styles.text}>{t(`density.${d}`)}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </Popover>
    </>
  );
}
