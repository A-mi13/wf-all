import { createContext, useContext } from 'react';

// Успех и справка исчезают сами через 8 с (таймер стоит, пока на тосте курсор или фокус); ошибка
// висит, пока её не закроют руками (спека §9.2). Тост с «Отменить» (undo) тоже сам не исчезает
// (спека UI-фундамента §4.10, m12: пользователю клавиатуры и читалки 8 с не хватит дойти до кнопки).
export const TOAST_HIDE_MS = 8000;

export type ToastTone = 'success' | 'error' | 'info';
export type ToastAction = { label: string; onClick: () => void }; // «Повторить»
// undo — кнопка «Отменить» (actions.undo): нажатие снимает тост и запускает отмену; новый тост с undo
// заменяет прежний; пока такой тост на экране, на <html> стоит data-toast-open (отступы прокрутки, n5).
export type ToastOptions = { action?: ToastAction; undo?: () => Promise<void> };

export type ToastApi = {
  success(text: string, opts?: ToastOptions): number;
  error(text: string, opts?: ToastOptions): number;
  info(text: string, opts?: ToastOptions): number;
  dismiss(id: number): void;
  // Снять тосты с «Отменить» (выход, смена сотрудника: отмена от имени другого недопустима).
  dismissUndo(): void;
  // Номер «поколения» отмен: растёт при каждом dismissUndo. Итог отмены, начатой в прежнем поколении (сменился
  // сотрудник), не показывается.
  undoEpoch(): number;
};

export const ToastContext = createContext<ToastApi | null>(null);

export function useToast(): ToastApi {
  const api = useContext(ToastContext);
  if (!api) throw new Error('useToast: нет ToastProvider выше по дереву');
  return api;
}
