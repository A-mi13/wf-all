import { type InfiniteData, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { background, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';

type Approval = components['schemas']['Approval'];
type ApprovalPage = components['schemas']['ApprovalPage'];

// Ключи «Подтверждений». Всё — под APPROVALS_KEY: решение на экране и любой 202 {approval} в других
// разделах одной инвалидацией перечитывают счётчик меню и оба списка.
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
// курсором, и второй клик двойного не становится решением по соседнему запросу. Ответа нет (ошибка:
// состояние запроса неизвестно) — перечитывается всё. Промис — после перечитывания.
export function useSettleDecision(box: 'incoming' | 'mine'): (result?: Approval) => Promise<void> {
  const queryClient = useQueryClient();
  return useCallback(
    async (result) => {
      const key = approvalsListKey(box);
      if (!result) return queryClient.invalidateQueries({ queryKey: APPROVALS_KEY });
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
    },
    [box, queryClient],
  );
}

// Перечитать подтверждения (счётчик и списки): после 202 {approval} в любом разделе и после решений.
export function useInvalidateApprovals(): () => Promise<void> {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: APPROVALS_KEY }),
    [queryClient],
  );
}
