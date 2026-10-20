import { type InfiniteData, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { background, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';

type Approval = components['schemas']['Approval'];
type ApprovalPage = components['schemas']['ApprovalPage'];

// Ключи «Подтверждений». Всё — под APPROVALS_KEY: любой 202 {approval} в других разделах одной
// инвалидацией перечитывает счётчик меню и оба списка; решение на экране — useSettleDecision.
export const APPROVALS_KEY = ['approvals'] as const;
export const COUNT_KEY = [...APPROVALS_KEY, 'count'] as const;
export const approvalsListKey = (box: 'incoming' | 'mine') =>
  [...APPROVALS_KEY, 'list', box] as const;

// Опрос счётчика — раз в минуту (спека §9.3).
export const COUNT_POLL_MS = 60_000;
// Страница счётчика — максимум контракта: больше 99 CountBadge всё равно рисует «99+».
const COUNT_LIMIT = 100;

// Счётчик «Подтверждения» в меню: входящие ожидающие (box=incoming&status=pending). Запрос фоновый
// (X-WF-Background: 1, D10) — опрос не продлевает бездействие сессии; 401 auth.reauth_required
// открывает то же окно разблокировки, что и любой запрос (клиент API). Раз в минуту, пока вкладка
// видна (refetchIntervalInBackground: false), и сразу при возврате во вкладку.
export function useIncomingApprovalsCount(): number | undefined {
  const api = useApi();
  const q = useQuery({
    queryKey: COUNT_KEY,
    queryFn: () =>
      unwrap(
        api.GET('/v1/approvals', {
          params: { query: { box: 'incoming', status: 'pending', limit: COUNT_LIMIT } },
          ...background(),
        }),
      ),
    refetchInterval: COUNT_POLL_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    select: (page) => page.items.length,
  });
  return q.data;
}

// Итог решения на экране списка box (ревью Task 15, I2). Ответ есть (200: решён) — строка остаётся
// на месте с итогом (запрос из ответа), список вкладки только помечается устаревшим (перечитается при
// следующем переходе), счётчик и другая вкладка перечитываются сейчас: строки не сдвигаются под
// курсором, и второй клик двойного не становится решением по соседнему запросу. Запрос списка в полёте
// («Показать ещё», возврат во вкладку, online) сначала отменяется (ревью m1): завершившись, он записал
// бы страницы, взятые до решения, поверх итога — строка снова pending с кнопками. Отменённый
// возвращает список к состоянию до запроса. Отменённая догрузка «Показать ещё» не теряется молча
// (уборка №8): промис отвечает true — экран повторяет fetchNextPage() после итога (курсор сервера —
// keyset (requested_at, id): страница после последней строки от решения не сдвигается). Отменённое
// обновление всего списка не повторяется — оно сдвинуло бы строки под курсором; список помечен
// устаревшим. Ответа нет (ошибка: состояние запроса неизвестно) или у списка ещё нет данных (первый
// запрос в полёте: отмена оставила бы его pending/idle — вечный скелетон, уборка №8) — перечитывается
// всё. Промис — после перечитывания.
export function useSettleDecision(
  box: 'incoming' | 'mine',
): (result?: Approval) => Promise<boolean> {
  const queryClient = useQueryClient();
  return useCallback(
    async (result) => {
      const key = approvalsListKey(box);
      if (!result || queryClient.getQueryData(key) === undefined) {
        await queryClient.invalidateQueries({ queryKey: APPROVALS_KEY });
        return false;
      }
      const state = queryClient.getQueryState(key);
      const loadingMore =
        state?.fetchStatus === 'fetching' && state.fetchMeta?.fetchMore?.direction === 'forward';
      await queryClient.cancelQueries({ queryKey: key, exact: true });
      queryClient.setQueryData<InfiniteData<ApprovalPage, string | undefined>>(key, (old) =>
        old
          ? {
              ...old,
              pages: old.pages.map((p) => ({
                ...p,
                items: p.items.map((a) => (a.id === result.id ? result : a)),
              })),
            }
          : old,
      );
      await queryClient.invalidateQueries({ queryKey: key, refetchType: 'none' });
      await queryClient.invalidateQueries({
        queryKey: APPROVALS_KEY,
        predicate: (q) => q.queryKey[1] !== key[1] || q.queryKey[2] !== key[2],
      });
      return loadingMore;
    },
    [box, queryClient],
  );
}

// Перечитывание запроса с неизвестным исходом решения: страница — максимум контракта, страниц — не
// больше REREAD_PAGES (запрос из строки экрана — среди новых; глубже — сдаёмся, экран перечитает всё).
const REREAD_LIMIT = 100;
const REREAD_PAGES = 5;

// Найти запрос a во «Входящих» во всех состояниях (⚠️ ревью 10b п. 3): ответ на approve потерян (5xx,
// обрыв, 409 idempotency.replay_unavailable — ключ закоммичен, ответ не сохранён), а повтор решения
// ошибочен. Отдельного запроса «один запрос подтверждения» в контракте нет — страницы списка
// box=incoming без status (новые сверху, keyset (requested_at, id)), пока не найден и пока страница не
// ушла старше его requested_at. Не нашли — undefined.
export function useFindIncomingApproval(): (a: Approval) => Promise<Approval | undefined> {
  const api = useApi();
  return useCallback(
    async (a) => {
      const since = Date.parse(a.requested_at);
      let cursor: string | undefined;
      for (let i = 0; i < REREAD_PAGES; i++) {
        const page = await unwrap(
          api.GET('/v1/approvals', {
            params: { query: { box: 'incoming', limit: REREAD_LIMIT, cursor } },
          }),
        );
        const found = page.items.find((x) => x.id === a.id);
        if (found) return found;
        const last = page.items.at(-1);
        if (!page.next_cursor || !last || Date.parse(last.requested_at) < since) return undefined;
        cursor = page.next_cursor;
      }
      return undefined;
    },
    [api],
  );
}

// Перечитать подтверждения (счётчик и списки): после 202 {approval} в любом разделе. Решения на экране
// «Подтверждений» — useSettleDecision.
export function useInvalidateApprovals(): () => Promise<void> {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: APPROVALS_KEY }),
    [queryClient],
  );
}
