import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { IntlProvider, useTranslations } from 'use-intl';
import { createApi } from './api/client';
import { ApiProvider } from './api/context';
import { HealthStatus } from './features/health/HealthStatus';
import { defaultLocale, messagesFor } from './i18n/messages';

const api = createApi(import.meta.env.VITE_ADMIN_API_URL || window.location.origin);
const queryClient = new QueryClient();

function Home() {
  const t = useTranslations('app');
  return (
    <main>
      <h1>{t('title')}</h1>
      <HealthStatus />
    </main>
  );
}

export function App() {
  return (
    <ApiProvider api={api}>
      <QueryClientProvider client={queryClient}>
        <IntlProvider locale={defaultLocale} messages={messagesFor(defaultLocale)}>
          <Home />
        </IntlProvider>
      </QueryClientProvider>
    </ApiProvider>
  );
}
