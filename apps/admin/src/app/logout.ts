import { useQueryClient } from '@tanstack/react-query';
import { useApi } from '../api/context';
import { useAdminMutation } from '../api/mutation';
import { useErrorText, useToast } from '../ui';
import { isMeKey } from './query';
import { useSession } from './session';

// Выход сотрудника — «Выйти» каркаса и окна разблокировки: сессия гасится на сервере
// (staffLogout), данные прежнего сотрудника — из кэша, me перечитывается (401 → anonymous →
// /login без return). 401 auth.unauthenticated — сессии уже нет: итог тот же, что у выхода (вход
// без return), тост не нужен. onSignedOut — после выхода (окно разблокировки отпускает ждущие
// запросы).
export function useLogout(opts: { onSignedOut?: () => void } = {}) {
  const api = useApi();
  const queryClient = useQueryClient();
  const session = useSession();
  const toast = useToast();
  const errorText = useErrorText();
  const signedOut = async () => {
    queryClient.removeQueries({ predicate: (q) => !isMeKey(q.queryKey) });
    await session.afterLogout();
    opts.onSignedOut?.();
  };
  return useAdminMutation(
    (_: void, key: string) =>
      api.POST('/v1/staff/auth/logout', { params: { header: { 'Idempotency-Key': key } } }),
    {
      onSuccess: signedOut,
      onError: (e) => {
        if (e.code === 'auth.unauthenticated') return signedOut();
        toast.error(errorText(e));
      },
    },
  );
}
