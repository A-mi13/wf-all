import { useCallback } from 'react';
import { useLocale } from './locale';

// Дата и время на языке интерфейса, в часовом поясе браузера сотрудника («вход 13 окт. 2026 г.,
// 12:00»). Intl напрямую, а не useFormatter use-intl: тот без заданного timeZone пишет ошибку
// ENVIRONMENT_FALLBACK в консоль на каждом вызове.
export function useDateTime(): (iso: string) => string {
  const locale = useLocale();
  return useCallback(
    (iso: string) =>
      new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
        new Date(iso),
      ),
    [locale],
  );
}
