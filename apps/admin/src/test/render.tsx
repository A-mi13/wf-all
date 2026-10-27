import { render } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement, ReactNode } from 'react';
import { IntlProvider } from 'use-intl';
import { createApi } from '../api/client';
import type { Me } from '../app/permissions';
import { AppProviders } from '../app/providers';
import { createQueryClient, ME_KEY } from '../app/query';
import { browserTimeZone } from '../i18n/format';
import { type Locale, messagesFor } from '../i18n/messages';
import { ToastProvider } from '../ui/toast';
import { meOwner } from './me';
import { TEST_API } from './server';

export type ProvidersOptions = {
  // Ответ me, уже лежащий в кэше (сессия сразу в нужной стадии, без запроса); null — me
  // запрашивается, тест сам ставит мок GET /v1/staff/me. По умолчанию — активный владелец.
  me?: Me | null;
};

// Провайдеры приложения для теста: обёртка для renderHook и её QueryClient.
export function providers({ me = meOwner }: ProvidersOptions = {}) {
  const queryClient = createQueryClient({ retry: false });
  if (me) queryClient.setQueryData(ME_KEY, me);
  const api = createApi(TEST_API);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <AppProviders api={api} queryClient={queryClient}>
      {children}
    </AppProviders>
  );
  return Object.assign(wrapper, { queryClient, api });
}

export function renderWithProviders(ui: ReactElement, opts: ProvidersOptions = {}) {
  const wrapper = providers(opts);
  return { ...render(ui, { wrapper }), queryClient: wrapper.queryClient };
}

// Компонент src/ui без API и сессии: только язык (по умолчанию ru), пояс (по умолчанию — браузера,
// как у LocaleProvider; тест дат может задать свой) и тосты. Возвращает и user (user-event). С
// vi.useFakeTimers user-event зависает (RTL ждёт настоящий setTimeout) — там события через fireEvent.
export function renderWithIntl(
  ui: ReactElement,
  { locale = 'ru', timeZone = browserTimeZone() }: { locale?: Locale; timeZone?: string } = {},
) {
  const user = userEvent.setup();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <IntlProvider locale={locale} messages={messagesFor(locale)} timeZone={timeZone}>
      <ToastProvider>{children}</ToastProvider>
    </IntlProvider>
  );
  return { ...render(ui, { wrapper }), user };
}
