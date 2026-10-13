import { type QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { Api } from '../api/client';
import { ApiProvider } from '../api/context';
import { DocumentTitle } from '../i18n/DocumentTitle';
import { LocaleProvider } from '../i18n/locale';
import { SessionProvider } from './session';

// Провайдеры приложения в порядке зависимостей: клиент API → кэш запросов → сессия (me) → язык
// (me.locale). Те же провайдеры — в тестах (src/test/render.tsx).
export function AppProviders(props: { api: Api; queryClient: QueryClient; children: ReactNode }) {
  return (
    <ApiProvider api={props.api}>
      <QueryClientProvider client={props.queryClient}>
        <SessionProvider>
          <LocaleProvider>
            <DocumentTitle />
            {props.children}
          </LocaleProvider>
        </SessionProvider>
      </QueryClientProvider>
    </ApiProvider>
  );
}
