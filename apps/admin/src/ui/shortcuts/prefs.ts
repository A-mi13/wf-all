import { useSyncExternalStore } from 'react';

// Переключатель «Односимвольные клавиши» (спека UI-фундамента §4.2, WCAG 2.1.4): localStorage
// wf.admin.shortcuts.single — '0' выключены, иначе (и по умолчанию) включены. Свойство браузера, не секрет и не
// ПД. Доступ к storage — в try/catch (приватный режим бросает): тогда выбор живёт в памяти до перезагрузки.
export const SHORTCUTS_SINGLE_KEY = 'wf.admin.shortcuts.single';

function read(): string | null {
  try {
    return localStorage.getItem(SHORTCUTS_SINGLE_KEY);
  } catch {
    return null;
  }
}

let memory: boolean | null = null;
const listeners = new Set<() => void>();

const store = {
  subscribe(onChange: () => void) {
    listeners.add(onChange);
    const onStorage = (e: StorageEvent) => {
      if (e.key !== null && e.key !== SHORTCUTS_SINGLE_KEY) return;
      memory = null;
      onChange();
    };
    window.addEventListener('storage', onStorage);
    return () => {
      listeners.delete(onChange);
      window.removeEventListener('storage', onStorage);
    };
  },
  snapshot: (): boolean => memory ?? read() !== '0',
  set(on: boolean) {
    const value = on ? '1' : '0';
    try {
      localStorage.setItem(SHORTCUTS_SINGLE_KEY, value);
    } catch {
      // приватный режим или переполнение — выбор не запомнится
    }
    // память — только если storage не запомнил (иначе источник истины — storage: его меняет другая вкладка)
    memory = read() === value ? null : on;
    for (const fn of [...listeners]) fn();
  },
};
const server = () => true;

// [включены ли односимвольные, задать].
export function useSingleKeys(): [boolean, (on: boolean) => void] {
  return [useSyncExternalStore(store.subscribe, store.snapshot, server), store.set];
}
