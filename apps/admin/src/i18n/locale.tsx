import { useQueryClient } from '@tanstack/react-query';
import { type ReactNode, useEffect } from 'react';
import { IntlProvider } from 'use-intl';
import type { Me } from '../app/permissions';
import { ME_KEY } from '../app/query';
import { useSession } from '../app/session';
import { defaultLocale, type Locale, messagesFor } from './messages';

const isLocale = (v: unknown): v is Locale => v === 'ru' || v === 'en';

// Язык интерфейса — me.locale (спека §9.1): одно место хранения на сервере, выбор переживает
// перезагрузку и другие устройства. До входа и при незнакомом значении — язык по умолчанию.
// eslint-disable-next-line react-refresh/only-export-components
export function useLocale(): Locale {
  const s = useSession();
  const own = s.status === 'active' || s.status === 'setup' ? s.me.locale : undefined;
  return isLocale(own) ? own : defaultLocale;
}

// Сохранённый на сервере язык (204 updateStaffMeLocale) — сразу в кэш me: тексты меняются без
// перезагрузки и без лишнего запроса.
// eslint-disable-next-line react-refresh/only-export-components
export function useApplyLocale(): (locale: Locale) => void {
  const queryClient = useQueryClient();
  return (locale) => queryClient.setQueryData<Me>(ME_KEY, (me) => (me ? { ...me, locale } : me));
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  const locale = useLocale();
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  return (
    <IntlProvider locale={locale} messages={messagesFor(locale)}>
      {children}
    </IntlProvider>
  );
}
