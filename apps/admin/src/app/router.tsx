import { useQueryClient } from '@tanstack/react-query';
import { createRouter, RouterProvider, type RouterHistory } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { ReauthModal } from '../features/auth/ReauthModal';
import { isApiError } from '../api/client';
import { ConnectionErrorState, RateLimitedState } from '../ui';
import { restoreKeyOf, scrollRestorationId } from '../ui/table/restore';
import { ME_KEY } from './query';
import { routeTree } from './routes';
import { useSession } from './session';
import { LoadingStub } from './stubs';

// Роутер админки поверх дерева маршрутов (routes.tsx: маршруты, каркас, ленивые разделы).

function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    // «Назад» — та же прокрутка страницы и областей с data-scroll-restoration-id (таблицы; спека
    // UI-фундамента §4.5). Инлайн-скрипта на клиенте нет (ScriptOnce — только SSR: react-router Match.js:69,
    // ScriptOnce.js:10) — CSP script-src 'self' не задета.
    scrollRestoration: true,
    // Ключ позиции — список без ключей панелей (restoreKeyOf: путь + search без PANEL_SEARCH_KEYS), а не запись
    // истории (по умолчанию __TSR_key): открытие, смена и закрытие панели (history.back) — одна позиция, и «Свернуть»
    // не откатывает прокрутку к моменту первого раскрытия (ревью UF2b.10-S, I1; router-core 1.171.39
    // scroll-restoration.ts:164-165 — getKey, :203 — снимок по ключу ухода, :267-345 — восстановление по ключу
    // прихода). В sessionStorage роутера уходит хеш (scrollRestorationId), не текст адреса.
    getScrollRestorationKey: (location) => scrollRestorationId(restoreKeyOf(location)),
    // кусок раздела — по намерению: наведение и фокус на пункте меню и ссылке (§4.15)
    defaultPreload: 'intent',
    // настоящая сессия приходит из RouterProvider (AppRouter)
    context: {
      session: { status: 'loading', refresh: async () => {}, afterLogout: async () => {} },
    },
  });
}

// Роутер поверх сессии: пока me не ответил — загрузка; admin-api недоступен — «нет связи» с
// повтором, отказал (ревью ветки M1) — 429 с отсчётом (лимит staff_auth по общему NAT) или текст по
// коду (403 auth.ip_not_allowed). Смена сессии (вход, выход, настройка 2FA) перезапускает beforeLoad.
export function AppRouter({ history }: { history?: RouterHistory }) {
  const session = useSession();
  const queryClient = useQueryClient();
  const [router] = useState(() => createAppRouter(history));
  useEffect(() => {
    void router.invalidate();
  }, [router, session]);

  if (session.status === 'loading') return <LoadingStub />;
  if (session.status === 'error') {
    const retry = () => void session.refresh();
    return (
      <main>
        {isApiError(session.error) && session.error.status === 429 ? (
          // повторный 429 с тем же сроком — отсчёт заново (новый ответ — новый key)
          <RateLimitedState
            key={queryClient.getQueryState(ME_KEY)?.errorUpdatedAt}
            page
            retryAfter={session.error.retryAfter}
            onRetry={retry}
          />
        ) : (
          <ConnectionErrorState error={session.error} onRetry={retry} />
        )}
      </main>
    );
  }
  return (
    <>
      <RouterProvider router={router} context={{ session }} />
      {/* 401 auth.reauth_required любого запроса — окно поверх текущей страницы (§9.3) */}
      <ReauthModal />
    </>
  );
}
