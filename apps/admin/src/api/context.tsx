import { createContext, type ReactNode, useContext } from 'react';
import type { Api } from './client';

const ApiContext = createContext<Api | null>(null);

export function ApiProvider({ api, children }: { api: Api; children: ReactNode }) {
  return <ApiContext value={api}>{children}</ApiContext>;
}

// react-refresh не различает хуки среди именованных экспортов файла с компонентом —
// это ложное срабатывание для пары Provider + хук, HMR тут не страдает.
// eslint-disable-next-line react-refresh/only-export-components
export function useApi(): Api {
  const api = useContext(ApiContext);
  if (!api) throw new Error('useApi: нет ApiProvider выше по дереву');
  return api;
}
