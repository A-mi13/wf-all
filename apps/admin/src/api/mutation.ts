import {
  type MutateOptions,
  useMutation,
  type UseMutationOptions,
  type UseMutationResult,
} from '@tanstack/react-query';
import { useCallback, useRef } from 'react';
import type { ApiError } from './client';

// Мутация админки (спека сотрудников §9.1, M6): ключ идемпотентности создаётся здесь — один на
// действие пользователя. mutate()/mutateAsync() — новое действие, новый ключ; retry() — повтор
// того же действия (кнопка «Повторить» после сбоя сети) с теми же данными и тем же ключом: сервер
// не исполнит его дважды. Повтор после разблокировки сессии клиент API делает сам, тоже с тем же
// ключом (клон запроса).

type Keyed<V> = { vars: V; key: string };

export type AdminMutationOptions<TData, TVars, TCtx> = Omit<
  UseMutationOptions<TData, ApiError, TVars, TCtx>,
  'mutationFn' | 'networkMode'
>;

export type AdminMutationResult<TData, TVars, TCtx = unknown> = UseMutationResult<
  TData,
  ApiError,
  TVars,
  TCtx
> & {
  // Повторить последнее действие с тем же ключом; без действия — ничего.
  retry(): void;
};

const newKey = () => crypto.randomUUID();

// Колбэки снаружи видят исходные переменные, внутри мутация несёт { vars, key }.
function unwrap<TData, TVars, TCtx>(
  o: MutateOptions<TData, ApiError, TVars, TCtx> | undefined,
): MutateOptions<TData, ApiError, Keyed<TVars>, TCtx> | undefined {
  if (!o) return undefined;
  const { onSuccess, onError, onSettled } = o;
  return {
    onSuccess: onSuccess && ((d, k, r, c) => onSuccess(d, k.vars, r, c)),
    onError: onError && ((e, k, r, c) => onError(e, k.vars, r, c)),
    onSettled: onSettled && ((d, e, k, r, c) => onSettled(d, e, k.vars, r, c)),
  };
}

export function useAdminMutation<TVars, TData, TCtx = unknown>(
  fn: (vars: TVars, idempotencyKey: string) => Promise<TData>,
  opts: AdminMutationOptions<TData, TVars, TCtx> = {},
): AdminMutationResult<TData, TVars, TCtx> {
  const { onMutate, onSuccess, onError, onSettled, ...rest } = opts;
  const m = useMutation<TData, ApiError, Keyed<TVars>, TCtx>({
    ...rest,
    networkMode: 'always',
    mutationFn: ({ vars, key }) => fn(vars, key),
    // колбэки хука возвращают свой результат: промис (инвалидация) мутация дожидается
    onMutate: onMutate && ((k, c) => onMutate(k.vars, c)),
    onSuccess: onSuccess && ((d, k, r, c) => onSuccess(d, k.vars, r, c)),
    onError: onError && ((e, k, r, c) => onError(e, k.vars, r, c)),
    onSettled: onSettled && ((d, e, k, r, c) => onSettled(d, e, k.vars, r, c)),
  });
  const last = useRef<Keyed<TVars> | null>(null);
  const { mutate: run, mutateAsync: runAsync } = m;

  const mutate = useCallback(
    (vars: TVars, o?: MutateOptions<TData, ApiError, TVars, TCtx>) => {
      last.current = { vars, key: newKey() };
      run(last.current, unwrap(o));
    },
    [run],
  );
  const mutateAsync = useCallback(
    (vars: TVars, o?: MutateOptions<TData, ApiError, TVars, TCtx>) => {
      last.current = { vars, key: newKey() };
      return runAsync(last.current, unwrap(o));
    },
    [runAsync],
  );
  const retry = useCallback(() => {
    if (last.current) run(last.current);
  }, [run]);

  return {
    ...m,
    variables: m.variables?.vars,
    mutate,
    mutateAsync,
    retry,
    // наружу — исходные переменные вместо обёртки { vars, key }
  } as unknown as AdminMutationResult<TData, TVars, TCtx>;
}
