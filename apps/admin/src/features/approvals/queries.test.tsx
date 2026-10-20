import { QueryClient, QueryClientProvider, useInfiniteQuery } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { INCOMING } from '../../test/approvals';
import { approvalsListKey, useSettleDecision } from './queries';

type ApprovalPage = components['schemas']['ApprovalPage'];

// Итог решения (useSettleDecision) у списка без данных (ревью уборки №7 m2): первый запрос вкладки ещё
// в полёте — отменить его значило бы оставить список в pending/idle навсегда (вечный скелетон: отмена
// без данных откатывает к «ничего не загружено», и никто не перезапрашивает).
test('решение, пока первый запрос списка в полёте, — список загружается, а не висит скелетоном', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => (release = r));
  const page: ApprovalPage = { items: [INCOMING], next_cursor: null };
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(
    () => ({
      list: useInfiniteQuery({
        queryKey: approvalsListKey('incoming'),
        queryFn: async () => {
          await gate;
          return page;
        },
        initialPageParam: undefined as string | undefined,
        getNextPageParam: (p) => p.next_cursor ?? undefined,
      }),
      settle: useSettleDecision('incoming'),
    }),
    { wrapper },
  );
  await waitFor(() => expect(result.current.list.fetchStatus).toBe('fetching'));
  expect(result.current.list.data).toBeUndefined();
  // итог может ждать перечитывания — оно идёт, когда ворота открыты
  const settling = act(() => result.current.settle({ ...INCOMING, status: 'executed' }));
  release();
  await settling;
  await waitFor(() => expect(result.current.list.status).toBe('success'));
  expect(result.current.list.data?.pages).toHaveLength(1);
});
