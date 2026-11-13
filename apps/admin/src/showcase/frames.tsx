import { focusManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { createContext, type ReactNode, useContext, useEffect, useState } from 'react';
import { createApi } from '../api/client';
import { ApiProvider } from '../api/context';
import type { Me } from '../app/permissions';
import { ME_KEY } from '../app/query';
import { SessionProvider } from '../app/session';

// Рамки историй компонентов, которым нужны сессия и роутер (AppShell): без сети и без MSW (истории src/ui
// без API, спека UI-фундамента §5.2).

// Сессия с готовым me в своём кэше: SessionProvider его не запрашивает (свежий). Перечитывание me при
// возврате во вкладку (refetchOnWindowFocus: 'always') в витрине выключено целиком — запрос ушёл бы на
// сервер витрины и сломал бы сессию истории.
export function SessionFrame({ me, children }: { me: Me; children: ReactNode }) {
  const [client] = useState(() => {
    const c = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Infinity, networkMode: 'offlineFirst' },
      },
    });
    c.setQueryData(ME_KEY, me);
    return c;
  });
  const [api] = useState(() => createApi(''));
  useEffect(() => {
    focusManager.setEventListener(() => () => undefined);
  }, []);
  return (
    <QueryClientProvider client={client}>
      <ApiProvider api={api}>
        <SessionProvider>{children}</SessionProvider>
      </ApiProvider>
    </QueryClientProvider>
  );
}

// Актуальные children рамки для корня роутера: роутер создаётся один раз, а замыкание первого рендера держало бы
// прежние пропсы истории (язык витрины сменился — PageHeader остался бы на старом; ревью UF3, m3). Контекст, а не
// ref: RouterProvider с тем же роутером корень сам не перерисует, подписчик контекста — перерисует.
const FrameChildren = createContext<ReactNode>(null);
function FrameRoot() {
  return useContext(FrameChildren);
}

// Роутер в памяти на адресе path: любой адрес отрисует children (активный пункт меню, BackLink).
export function RouterFrame({ path, children }: { path: string; children: ReactNode }) {
  const [router] = useState(() => {
    const root = createRootRoute({ component: FrameRoot });
    const any = createRoute({ getParentRoute: () => root, path: '$' });
    return createRouter({
      routeTree: root.addChildren([any]),
      history: createMemoryHistory({ initialEntries: [path] }),
    });
  });
  return (
    <FrameChildren.Provider value={children}>
      <RouterProvider router={router} />
    </FrameChildren.Provider>
  );
}
