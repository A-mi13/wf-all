import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { IntlProvider, useTranslations } from 'use-intl';
import { createApi } from './api/client';
import { ApiProvider } from './api/context';
import { DocumentTitle } from './i18n/DocumentTitle';
import { defaultLocale, messagesFor } from './i18n/messages';

const api = createApi(import.meta.env.VITE_ADMIN_API_URL || window.location.origin);
const queryClient = new QueryClient();

// Временная главная до Task 11 (роутер, вход, сессия): проверки связи нет — /v1/health удалён из
// контракта admin (R13), связь проверит GET /v1/staff/me.
function Home() {
  const t = useTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
    </main>
  );
}

export function App() {
  return (
    <ApiProvider api={api}>
      <QueryClientProvider client={queryClient}>
        <IntlProvider locale={defaultLocale} messages={messagesFor(defaultLocale)}>
          <DocumentTitle />
          <Home />
        </IntlProvider>
      </QueryClientProvider>
    </ApiProvider>
  );
}
