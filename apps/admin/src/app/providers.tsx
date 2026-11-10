import { type QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { Api } from '../api/client';
import { ApiProvider } from '../api/context';
import { DocumentTitle } from '../i18n/DocumentTitle';
import { LocaleProvider } from '../i18n/locale';
import { ThemeProvider } from '../ui/theme';
import { ToastProvider } from '../ui/toast';
import { SessionProvider } from './session';

// Провайдеры приложения в порядке зависимостей: тема (ни от чего не зависит; атрибут уже поставил
// public/theme-init.js) → клиент API → кэш запросов → сессия (me) → язык
// (me.locale) → тосты (подписи — на языке). Те же провайдеры — в тестах (src/test/render.tsx).
export function AppProviders(props: { api: Api; queryClient: QueryClient; children: ReactNode }) {
  return (
    <ThemeProvider>
      <ApiProvider api={props.api}>
        <QueryClientProvider client={props.queryClient}>
          <SessionProvider>
            <LocaleProvider>
              <ToastProvider>
                <DocumentTitle />
                {props.children}
              </ToastProvider>
            </LocaleProvider>
          </SessionProvider>
        </QueryClientProvider>
      </ApiProvider>
    </ThemeProvider>
  );
}
