import { createContext, useContext } from 'react';

// Успех и справка исчезают сами через 8 с (таймер стоит, пока на тосте курсор или фокус); ошибка
// висит, пока её не закроют руками (спека §9.2).
export const TOAST_HIDE_MS = 8000;

export type ToastTone = 'success' | 'error' | 'info';
export type ToastAction = { label: string; onClick: () => void }; // «Отменить», «Повторить»
export type ToastOptions = { action?: ToastAction };

export type ToastApi = {
  success(text: string, opts?: ToastOptions): number;
  error(text: string, opts?: ToastOptions): number;
  info(text: string, opts?: ToastOptions): number;
  dismiss(id: number): void;
};

export const ToastContext = createContext<ToastApi | null>(null);

export function useToast(): ToastApi {
  const api = useContext(ToastContext);
  if (!api) throw new Error('useToast: нет ToastProvider выше по дереву');
  return api;
}
