import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import { NextIntlClientProvider } from 'next-intl';
import type { ReactElement } from 'react';
import { createApi } from '../api/client';
import { ApiProvider } from '../api/context';
import { messagesFor } from '../i18n/messages';
import { TEST_API } from './server';

export function renderWithProviders(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ApiProvider api={createApi(TEST_API)}>
      <QueryClientProvider client={queryClient}>
        <NextIntlClientProvider locale="ru" messages={messagesFor('ru')}>
          {ui}
        </NextIntlClientProvider>
      </QueryClientProvider>
    </ApiProvider>,
  );
}
