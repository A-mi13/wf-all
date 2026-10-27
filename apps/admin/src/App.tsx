import type { QueryClient } from '@tanstack/react-query';
import type { Api } from './api/client';
import { AppProviders } from './app/providers';
import { AppRouter } from './app/router';

// Админка: провайдеры (API, кэш, сессия, язык) и роутер. Клиент и кэш создаёт main.tsx — тесты
// подставляют свои.
export function App({ api, queryClient }: { api: Api; queryClient: QueryClient }) {
  return (
    <AppProviders api={api} queryClient={queryClient}>
      <AppRouter />
    </AppProviders>
  );
}
