import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, test } from 'vitest';
import { createQueryClient } from '../app/query';
import { ApiError } from './client';
import { useAdminMutation } from './mutation';

function wrapper(client: QueryClient = createQueryClient()) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

// Мутация, которая запоминает ключи и падает, пока fail = true.
function recorder() {
  const calls: { vars: string; key: string }[] = [];
  const state = { fail: true };
  const fn = async (vars: string, key: string) => {
    calls.push({ vars, key });
    if (state.fail) throw new ApiError({ status: 503, code: 'internal' });
    return `ok:${vars}`;
  };
  return { calls, state, fn };
}

test('новое действие — новый ключ; retry() — тот же ключ и те же данные', async () => {
  const { calls, state, fn } = recorder();
  const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });

  act(() => result.current.mutate('a'));
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(result.current.error).toBeInstanceOf(ApiError);
  expect(result.current.variables).toBe('a');

  state.fail = false;
  act(() => result.current.retry());
  await waitFor(() => expect(result.current.data).toBe('ok:a'));

  act(() => result.current.mutate('b'));
  await waitFor(() => expect(result.current.data).toBe('ok:b'));

  expect(calls.map((c) => c.vars)).toEqual(['a', 'a', 'b']);
  expect(calls[1]?.key).toBe(calls[0]?.key);
  expect(calls[2]?.key).not.toBe(calls[0]?.key);
  expect(calls[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
});

test('повтор того же значения новым действием — всё равно новый ключ', async () => {
  const { calls, state, fn } = recorder();
  state.fail = false;
  const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });
  await act(() => result.current.mutateAsync('a'));
  await act(() => result.current.mutateAsync('a'));
  expect(calls[0]?.key).not.toBe(calls[1]?.key);
});

test('колбэки получают исходные переменные, а не обёртку с ключом', async () => {
  const { state, fn } = recorder();
  state.fail = false;
  const seen: unknown[] = [];
  const { result } = renderHook(
    () =>
      useAdminMutation(fn, {
        onSuccess: (data, vars) => seen.push(['hook', data, vars]),
      }),
    { wrapper: wrapper() },
  );
  await act(() =>
    result.current.mutateAsync('x', { onSuccess: (data, vars) => seen.push(['call', data, vars]) }),
  );
  expect(seen).toEqual([
    ['hook', 'ok:x', 'x'],
    ['call', 'ok:x', 'x'],
  ]);
});

test('мутации не ждут сети: networkMode always (P20)', () => {
  expect(createQueryClient().getDefaultOptions().mutations?.networkMode).toBe('always');
});

// Ревью ветки, M3: форма отправлена, исход неизвестен (5xx, обрыв, 408/499, 2xx без тела) — сервер мог
// исполнить. Повтор с теми же значениями — то же действие, тот же ключ: сервер вернёт сохранённый ответ
// или replay_unavailable, но не исполнит второй раз (второе приглашение, второй pending-запрос). Значения
// изменились или прежний отказ известен (4xx) — новое действие, новый ключ.
describe('повтор после неизвестного исхода', () => {
  function failing(errors: unknown[]) {
    const calls: { vars: unknown; key: string }[] = [];
    const fn = async (vars: unknown, key: string) => {
      calls.push({ vars, key });
      const e = errors.shift();
      if (e) throw e;
      return 'ok';
    };
    return { calls, fn };
  }
  const run = async (
    m: { current: { mutateAsync: (v: unknown) => Promise<unknown> } },
    v: unknown,
  ) => {
    await act(() => m.current.mutateAsync(v).catch(() => undefined));
  };

  test('те же значения (новый объект формы) после 5xx и обрыва — тот же ключ', async () => {
    const { calls, fn } = failing([
      new ApiError({ status: 502, code: 'internal' }),
      new TypeError('Failed to fetch'),
    ]);
    const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });
    await run(result, { email: 'ivan@wf.example', role: 'support' });
    await run(result, { email: 'ivan@wf.example', role: 'support' });
    await run(result, { email: 'ivan@wf.example', role: 'support' });
    expect(calls).toHaveLength(3);
    expect(calls[1]?.key).toBe(calls[0]?.key);
    expect(calls[2]?.key).toBe(calls[0]?.key);
    // успех закрыл действие: те же значения дальше — новое действие
    await run(result, { email: 'ivan@wf.example', role: 'support' });
    expect(calls[3]?.key).not.toBe(calls[0]?.key);
  });

  test('2xx без тела и 408 — тоже неизвестный исход', async () => {
    const { calls, fn } = failing([
      new ApiError({ status: 200, code: 'internal' }),
      new ApiError({ status: 408, code: 'internal' }),
    ]);
    const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });
    await run(result, 'a');
    await run(result, 'a');
    await run(result, 'a');
    expect(new Set(calls.map((c) => c.key)).size).toBe(1);
  });

  test('значения изменились — новый ключ', async () => {
    const { calls, fn } = failing([new ApiError({ status: 500, code: 'internal' })]);
    const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });
    await run(result, { email: 'ivan@wf.example' });
    await run(result, { email: 'petr@wf.example' });
    expect(calls[1]?.key).not.toBe(calls[0]?.key);
  });

  test('известный отказ (4xx) — новый ключ и при тех же значениях', async () => {
    const { calls, fn } = failing([new ApiError({ status: 409, code: 'staff.email_taken' })]);
    const { result } = renderHook(() => useAdminMutation(fn), { wrapper: wrapper() });
    await run(result, { email: 'ivan@wf.example' });
    await run(result, { email: 'ivan@wf.example' });
    expect(calls[1]?.key).not.toBe(calls[0]?.key);
  });
});
