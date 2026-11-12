import { Fragment, useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Modal } from '../overlay';
import { Switch } from '../Switch';
import { isApplePlatform, parseCombo, type Step } from './combo';
import styles from './Shortcuts.module.css';

// Шпаргалка горячих клавиш (спека UI-фундамента §4.2): строки — из реестра ShortcutsProvider, клавиши — символом
// текущей раскладки (navigator.keyboard.getLayoutMap(), где есть: «g» в русской — «п»; нет — латиница),
// переключатель «Односимвольные клавиши» (WCAG 2.1.4; Ctrl/⌘+K работает всегда). Сам реестр не читает — показ
// и витрина на любых строках.

export type ShortcutsHelpItem = { id: string; label: string; steps: readonly Step[] };

export type ShortcutsHelpProps = {
  open: boolean;
  onClose: () => void;
  items: readonly ShortcutsHelpItem[];
  single: boolean;
  onSingleChange: (on: boolean) => void;
};

type LayoutMap = ReadonlyMap<string, string>;
type KeyboardApi = { getLayoutMap?: () => Promise<LayoutMap> };

const ESCAPE = parseCombo('escape').steps;
const NAMES: Record<string, string> = { escape: 'Esc', enter: 'Enter' };

// Клавиши шага по порядку: модификаторы, затем сама клавиша.
function stepParts(step: Step, layout: LayoutMap | null, mac: boolean): string[] {
  const parts: string[] = [];
  if (step.mod) parts.push(mac ? '⌘' : 'Ctrl');
  if (step.alt) parts.push(mac ? '⌥' : 'Alt');
  const named = NAMES[step.token];
  if (named) return [...parts, named];
  const own = layout?.get(step.code);
  // «?» — Shift и клавиша «/»: в латинице так и пишется «?», в другой раскладке — Shift и её символ
  if (step.token === '?')
    return [...parts, own && own !== '/' ? 'Shift' : '?', ...(own && own !== '/' ? [own] : [])];
  if (step.shift) parts.push('Shift');
  const base = own ?? step.token;
  return [...parts, step.mod || step.alt ? base.toUpperCase() : base];
}

export function ShortcutsHelp({
  open,
  onClose,
  items,
  single,
  onSingleChange,
}: ShortcutsHelpProps) {
  const t = useTranslations('ui.shortcuts');
  const [layout, setLayout] = useState<LayoutMap | null>(null);
  const mac = isApplePlatform();
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const keyboard = (navigator as Navigator & { keyboard?: KeyboardApi }).keyboard;
    try {
      keyboard
        ?.getLayoutMap?.()
        .then((map) => {
          if (alive) setLayout(map);
        })
        .catch(() => {});
    } catch {
      // нет доступа (фрейм) — латиница
    }
    return () => {
      alive = false;
    };
  }, [open]);

  // Односимвольные выключены — их строки остаются (переключатель рядом), но помечены «выключено» (ревью UF4-K m6);
  // Esc закрывает окно всегда.
  const off = (row: ShortcutsHelpItem) =>
    !single && row.id !== 'escape' && row.steps.every((s) => !s.mod && !s.alt);
  const rows: ShortcutsHelpItem[] = [...items, { id: 'escape', label: t('escape'), steps: ESCAPE }];
  return (
    <Modal open={open} onClose={onClose} title={t('title')}>
      <ul className={styles.list}>
        {rows.map((row) => (
          <li key={row.id} className={styles.row} data-off={off(row) ? '' : undefined}>
            <span className={styles.label}>
              {row.label}
              {off(row) && <span className={styles.off}> · {t('off')}</span>}
            </span>
            <span className={styles.keys}>
              {row.steps.map((step, i) => (
                <Fragment key={i}>
                  {i > 0 && <span className={styles.then}> {t('then')} </span>}
                  {stepParts(step, layout, mac).map((part, j) => (
                    <Fragment key={j}>
                      {j > 0 && <span aria-hidden="true">+</span>}
                      <kbd className={styles.kbd}>{part}</kbd>
                    </Fragment>
                  ))}
                </Fragment>
              ))}
            </span>
          </li>
        ))}
      </ul>
      <div className={styles.toggle}>
        <Switch checked={single} onChange={onSingleChange} label={t('single')} />
        <p className={styles.hint}>{t('single_hint', { mod: mac ? '⌘' : 'Ctrl' })}</p>
      </div>
    </Modal>
  );
}
