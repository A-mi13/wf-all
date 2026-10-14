import { createMemoryHistory } from '@tanstack/react-router';
import { act, render } from '@testing-library/react';
import { StrictMode } from 'react';
import { createApi } from '../api/client';
import type { Me } from '../app/permissions';
import { AppProviders } from '../app/providers';
import { createQueryClient, ME_KEY } from '../app/query';
import { AppRouter } from '../app/router';
import { problem } from './problem';
import { http, server, TEST_API } from './server';

// Приложение целиком (провайдеры + роутер) на адресе path — для экранов, которые ведёт стадия
// сессии (вход, приглашение, 2FA, разблокировка). GET /v1/staff/me отдаёт текущее me.current;
// null — 401 auth.unauthenticated. Ручки экрана тест ставит сам и в них меняет me.current — как
// сервер меняет стадию сессии.
export function renderApp(path: string, opts: { me?: Me | null; strict?: boolean } = {}) {
  const me = { current: opts.me ?? null };
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      me.current
        ? response(200).json(me.current)
        : response.untyped(problem(401, 'auth.unauthenticated')),
    ),
  );
  const history = createMemoryHistory({ initialEntries: [path] });
  const queryClient = createQueryClient({ retry: false });
  const tree = (
    <AppProviders api={createApi(TEST_API)} queryClient={queryClient}>
      <AppRouter history={history} />
    </AppProviders>
  );
  render(opts.strict ? <StrictMode>{tree}</StrictMode> : tree);
  return {
    me,
    queryClient,
    at: () => history.location.pathname + history.location.search,
    // me перечитывается (как после входа или 401 любого запроса)
    refetchMe: () => act(() => queryClient.refetchQueries({ queryKey: ME_KEY })),
  };
}

// Содержимое sessionStorage и localStorage одной строкой — проверка, что секретов там нет.
export function storageDump(): string {
  const dump = (s: Storage) =>
    Array.from({ length: s.length }, (_, i) => {
      const k = s.key(i) ?? '';
      return `${k}=${s.getItem(k) ?? ''}`;
    }).join('\n');
  return `${dump(sessionStorage)}\n${dump(localStorage)}`;
}
