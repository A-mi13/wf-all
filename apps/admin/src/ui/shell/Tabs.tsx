import { type KeyboardEvent, type ReactNode, useId, useRef } from 'react';
import { CountBadge } from '../CountBadge';
import styles from './Tabs.module.css';

export type TabItem = { value: string; label: string; count?: number };

// Вкладки (журнал по разделам, «Входящие» и «Мои» в подтверждениях): управляемые — значение
// держит экран (обычно search-параметр ?tab=). Счётчик — CountBadge в имени вкладки: имя
// «Входящие 3» (e2e ищет вкладку по подстроке). Клавиатура по WAI-ARIA: в цикле Tab — только
// выбранная вкладка, стрелки и Home/End переводят фокус и сразу выбирают. children — содержимое
// выбранной вкладки (role="tabpanel").
export function Tabs({
  label,
  items,
  value,
  onChange,
  children,
}: {
  label: string;
  items: TabItem[];
  value: string;
  onChange: (value: string) => void;
  children?: ReactNode;
}) {
  const base = useId();
  const list = useRef<HTMLDivElement>(null);
  const tabId = (v: string) => `${base}-tab-${v}`;
  const panelId = `${base}-panel`;

  const onKeyDown = (e: KeyboardEvent) => {
    const i = items.findIndex((it) => it.value === value);
    const last = items.length - 1;
    const next =
      e.key === 'ArrowRight'
        ? (i + 1) % items.length
        : e.key === 'ArrowLeft'
          ? (i - 1 + items.length) % items.length
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? last
              : -1;
    const item = items[next];
    if (!item) return;
    e.preventDefault();
    onChange(item.value);
    list.current?.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus();
  };

  return (
    <div className={styles.root}>
      <div
        ref={list}
        role="tablist"
        aria-label={label}
        className={styles.list}
        onKeyDown={onKeyDown}
      >
        {items.map((it) => {
          const selected = it.value === value;
          return (
            <button
              key={it.value}
              id={tabId(it.value)}
              type="button"
              role="tab"
              aria-selected={selected}
              aria-controls={selected ? panelId : undefined}
              tabIndex={selected ? 0 : -1}
              className={styles.tab}
              onClick={() => onChange(it.value)}
            >
              {it.label}
              {it.count ? ' ' : null}
              {it.count !== undefined && <CountBadge count={it.count} active={selected} />}
            </button>
          );
        })}
      </div>
      <div id={panelId} role="tabpanel" aria-labelledby={tabId(value)} className={styles.panel}>
        {children}
      </div>
    </div>
  );
}
