import { queryOptions, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { background, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { useCan } from '../../app/session';

type Page = components['schemas']['IdentityCaptainApplicationPage'];

// Ключи «Капитанов» (план I-4, Task 8). Всё — под CAPTAINS_KEY: решение по заявке одной инвалидацией перечитывает
// счётчик меню, плитку главной, очередь и карточку. Контакты заявителя — НЕ здесь (CONTACT_KEY): инвалидация
// очереди не должна повторять чтение ПД — каждое пишется в журнал.
export const CAPTAINS_KEY = ['identity', 'captains'] as const;
export const COUNT_KEY = [...CAPTAINS_KEY, 'count'] as const;
export const captainsListKey = (status: string, city: string | undefined) =>
  [...CAPTAINS_KEY, 'list', { status, city }] as const;
export const applicationKey = (id: string) => [...CAPTAINS_KEY, 'card', id] as const;
export const contactKey = (id: string) => ['identity', 'captain-contact', id] as const;

export const READ_PERMISSION = 'identity.captain_applications.read';
export const DECIDE_PERMISSION = 'identity.captain_applications.decide';
export const PII_PERMISSION = 'identity.pii.read';

// Опрос счётчика — раз в минуту (как «Подтверждения», спека сотрудников §9.3).
export const COUNT_POLL_MS = 60_000;
// Страница счётчика — максимум контракта: больше 99 CountBadge всё равно рисует «99+».
const COUNT_LIMIT = 100;

// Число ожидающих по странице: «99+» — 100 строк или next_cursor (числа больше 99 бейдж не пишет).
export function pendingCount(page: Page): number {
  return page.next_cursor !== null ? Math.max(page.items.length, COUNT_LIMIT) : page.items.length;
}

function usePendingOptions() {
  const api = useApi();
  const enabled = useCan(READ_PERMISSION);
  return queryOptions({
    queryKey: COUNT_KEY,
    queryFn: () =>
      unwrap(
        api.GET('/v1/identity/captain-applications', {
          params: { query: { status: 'pending', limit: COUNT_LIMIT } },
          ...background(),
        }),
      ),
    // без права нет ни пункта, ни плитки, ни запроса
    enabled,
    refetchOnWindowFocus: true,
  });
}

// Счётчик «Капитаны» в меню: ожидающие заявки фоном (X-WF-Background: 1 — опрос не продлевает бездействие), раз в
// минуту, пока вкладка видна, и при возврате во вкладку.
export function useCaptainQueueCount(): number | undefined {
  const q = useQuery({
    ...usePendingOptions(),
    refetchInterval: COUNT_POLL_MS,
    refetchIntervalInBackground: false,
    select: pendingCount,
  });
  return q.data;
}

// Та же страница целиком — плитка главной (одна подписка с счётчиком: ключ общий, своего интервала нет).
export function useCaptainQueue() {
  return useQuery(usePendingOptions());
}

// Перечитать всё «Капитанов» (после решения): счётчик, плитку, очередь, карточку.
export function useInvalidateCaptains(): () => Promise<void> {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: CAPTAINS_KEY }),
    [queryClient],
  );
}
