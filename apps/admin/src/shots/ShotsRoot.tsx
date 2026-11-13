import type { QueryClient } from '@tanstack/react-query';
import type { ComponentType } from 'react';
import type { Api } from '../api/client';
import { AppProviders } from '../app/providers';
import { AppRouter } from '../app/router';
import { SHOWCASE_NOW } from '../showcase/params';
import { NowProvider } from '../ui/time';

// Корень режима shots: те же провайдеры, что у приложения (App: API, кэш, сессия, язык, тосты), внутри —
// роутер или история компонента (?shots-story=): история с useTranslations, useApi, тостами рендерится
// так же, как в приложении (раунд 1 ревью UF1a, m4). «Сейчас» — SHOWCASE_NOW, как в витрине: относительное
// время («5 мин назад») в снимках экранов и в визуальном сравнении CI не плывёт (план UF2a.9).
export function ShotsRoot({
  api,
  queryClient,
  Story,
}: {
  api: Api;
  queryClient: QueryClient;
  Story: ComponentType | null;
}) {
  return (
    <AppProviders api={api} queryClient={queryClient}>
      <NowProvider now={SHOWCASE_NOW}>{Story ? <Story /> : <AppRouter />}</NowProvider>
    </AppProviders>
  );
}
