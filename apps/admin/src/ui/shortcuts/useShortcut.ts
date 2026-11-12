import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useRef } from 'react';
import { isDesktopOnly } from '../../app/desktopOnly';
import { type Combo, parseCombo } from './combo';

// Реестр горячих клавиш (спека UI-фундамента §4.2): регистрирует useShortcut, слушает ShortcutsProvider, из него же
// строится шпаргалка — устаревших строк в ней не бывает.

export type Registration = {
  combo: Combo;
  source: string; // как записал вызыватель — в тексте ошибки конфликта
  run: () => void;
  allowInInput: boolean;
  label?: string;
  desktopOnly: boolean; // право из реестра «только с компьютера» — на карманном устройстве нет
};

export function createShortcutsStore() {
  let list: Registration[] = [];
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((fn) => fn());
  return {
    register(entry: Registration) {
      const key = entry.combo.key;
      const other = list.find(
        (r) =>
          r.combo.key === key ||
          r.combo.key.startsWith(key + ' ') ||
          key.startsWith(r.combo.key + ' '),
      );
      // Конфликт — ошибка в разработке (§4.2): одна комбинация (срабатывает последняя регистрация) или префикс
      // чужой последовательности («g» и «g c»: «g» сработает сразу, «g c» недостижима).
      if (other && import.meta.env.DEV) {
        console.error(
          `useShortcut: «${entry.source}» уже занято («${other.source}») — одна комбинация, два действия`,
        );
      }
      list = [...list, entry];
      notify();
      return () => {
        list = list.filter((r) => r !== entry);
        notify();
      };
    },
    list: (): readonly Registration[] => list,
    subscribe(fn: () => void) {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
  };
}

export type ShortcutsStore = ReturnType<typeof createShortcutsStore>;
export const ShortcutsContext = createContext<ShortcutsStore | null>(null);

// Открыть шпаргалку не клавишей «?» (команда палитры «Горячие клавиши»).
export const OpenHelpContext = createContext<(() => void) | null>(null);

export function useOpenShortcutsHelp(): () => void {
  const open = useContext(OpenHelpContext);
  if (!open) throw new Error('useOpenShortcutsHelp: нет ShortcutsProvider выше по дереву');
  return open;
}

export type ShortcutOptions = {
  enabled?: boolean; // false — не зарегистрирована (и нет в шпаргалке)
  allowInInput?: boolean; // срабатывает и в поле ввода (Ctrl+K)
  label?: string; // строка шпаргалки (уже переведённая)
  permission?: string | readonly string[]; // право действия: из реестра «только с компьютера» — не на телефоне
};

// combo: «mod+k», «g c», «/», «?» (combo.ts). handler — последний переданный (без перерегистрации).
export function useShortcut(combo: string, handler: () => void, opts: ShortcutOptions = {}): void {
  const store = useContext(ShortcutsContext);
  if (!store) throw new Error('useShortcut: нет ShortcutsProvider выше по дереву');
  const ref = useRef(handler);
  useLayoutEffect(() => {
    ref.current = handler;
  });
  const parsed = useMemo(() => parseCombo(combo), [combo]);
  const { enabled = true, allowInInput = false, label, permission } = opts;
  const desktopOnly = permission ? isDesktopOnly(permission) : false;
  useEffect(() => {
    if (!enabled) return;
    return store.register({
      combo: parsed,
      source: combo,
      run: () => ref.current(),
      allowInInput,
      label,
      desktopOnly,
    });
  }, [store, parsed, combo, enabled, allowInInput, label, desktopOnly]);
}
