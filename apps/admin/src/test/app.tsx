import { createBrowserHistory, createMemoryHistory } from '@tanstack/react-router';
import { act, render } from '@testing-library/react';
import { StrictMode } from 'react';
import { onTestFinished } from 'vitest';
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
// history: 'memory' (по умолчанию) — адрес окна не трогается; 'browser' — настоящая история окна jsdom
// (спека UI-фундамента §4.11, R3-1, m-l): memory history back() блокировщики не опрашивает, поэтому тесты
// «Назад» с блокировщиком и закрытия панели — только на ней. Адрес ставится до createBrowserHistory;
// после теста — destroy(): @tanstack/history 1.162.5 подменяет window.history.pushState/replaceState и
// вешает popstate/beforeunload, destroy возвращает исходные и снимает слушатели (проверено по исходникам;
// страж — app.test.tsx). Записи истории окна jsdom живут весь файл (destroy их не стирает, m10): тест
// «Назад» начинает со своего адреса и не делает back больше, чем своих push.
export function renderApp(
  path: string,
  opts: { me?: Me | null; strict?: boolean; history?: 'memory' | 'browser' } = {},
) {
  const me = { current: opts.me ?? null };
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      me.current
        ? response(200).json(me.current)
        : response.untyped(problem(401, 'auth.unauthenticated')),
    ),
  );
  let history;
  if (opts.history === 'browser') {
    window.history.replaceState(null, '', path);
    history = createBrowserHistory();
    const browser = history;
    onTestFinished(() => browser.destroy());
  } else {
    history = createMemoryHistory({ initialEntries: [path] });
  }
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
    history,
    // «Назад»: memory — сразу; browser — window.history.back() и ожидание popstate (в jsdom он приходит
    // задачей, а не синхронно)
    back: () =>
      opts.history === 'browser'
        ? act(async () => {
            const popped = new Promise<void>((resolve) =>
              window.addEventListener('popstate', () => resolve(), { once: true }),
            );
            window.history.back();
            await popped;
          })
        : act(() => history.back()),
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
