import { useQueryClient } from '@tanstack/react-query';
import { type ReactNode, useEffect, useState } from 'react';
import { IntlProvider } from 'use-intl';
import type { Me } from '../app/permissions';
import { ME_KEY } from '../app/query';
import { useSession } from '../app/session';
import { useDeviceLocale } from './deviceLocale';
import { browserTimeZone } from './format';
import { type Locale, messagesFor } from './messages';

const isLocale = (v: unknown): v is Locale => v === 'ru' || v === 'en';

// Язык интерфейса (спека §9.1 «Локали»): на setup и active — me.locale (хранится на сервере, переживает
// перезагрузку и другие устройства; меняет его LocaleSwitcher через PATCH). До входа (anonymous, mfa_pending,
// загрузка, ошибка) и при незнакомом me.locale — выбор устройства wf.locale (./deviceLocale.ts; решение 10.11):
// он на сервер не уходит и после входа учётку не переписывает.
// eslint-disable-next-line react-refresh/only-export-components
export function useLocale(): Locale {
  const s = useSession();
  const device = useDeviceLocale();
  const own = s.status === 'active' || s.status === 'setup' ? s.me.locale : undefined;
  return isLocale(own) ? own : device;
}

// Сохранённый на сервере язык (204 updateStaffMeLocale) — сразу в кэш me: тексты меняются без
// перезагрузки и без лишнего запроса.
// eslint-disable-next-line react-refresh/only-export-components
export function useApplyLocale(): (locale: Locale) => void {
  const queryClient = useQueryClient();
  return (locale) => queryClient.setQueryData<Me>(ME_KEY, (me) => (me ? { ...me, locale } : me));
}

// Часовой пояс дат — браузера сотрудника: журнал и сеансы читаются в его местном времени. Задан явно:
// без него use-intl на каждом форматировании пишет ENVIRONMENT_FALLBACK в консоль.
export function LocaleProvider({ children }: { children: ReactNode }) {
  const locale = useLocale();
  const [timeZone] = useState(browserTimeZone);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  return (
    <IntlProvider locale={locale} messages={messagesFor(locale)} timeZone={timeZone}>
      {children}
    </IntlProvider>
  );
}
