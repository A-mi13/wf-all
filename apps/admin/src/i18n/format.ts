import { useCallback } from 'react';
import { useFormatter } from 'use-intl';

// Дата и время на языке интерфейса в поясе браузера сотрудника («13 окт. 2026 г., 12:15») —
// единственный способ форматировать даты в админке (журнал, сеансы, сотрудники). Пояс задаёт
// LocaleProvider (IntlProvider timeZone): без него use-intl пишет ENVIRONMENT_FALLBACK в консоль.
export function useDateTime(): (iso: string) => string {
  const format = useFormatter();
  return useCallback(
    (iso: string) => format.dateTime(new Date(iso), { dateStyle: 'medium', timeStyle: 'short' }),
    [format],
  );
}
