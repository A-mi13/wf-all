import { useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { useApi } from '../api/context';
import { useAdminMutation } from '../api/mutation';
import { useErrorText, useToast } from '../ui';
import { clearDrafts } from './draft';
import { useConfirmLeave } from './leaveGuard';
import { isMeKey } from './query';
import { clearRecent } from './recent';
import { useSession } from './session';

// Выход сотрудника — «Выйти» каркаса и окна разблокировки: сессия гасится на сервере
// (staffLogout), данные прежнего сотрудника — из кэша, me перечитывается (401 → anonymous →
// /login без return). 401 auth.unauthenticated — сессии уже нет: итог тот же, что у выхода (вход
// без return), тост не нужен. Следующему сотруднику за этим компьютером не достаются ни черновики
// форм (sessionStorage), ни недавнее палитры (localStorage), ни переменные мутаций (пароли, коды —
// MutationCache). onSignedOut — после выхода (окно разблокировки отпускает ждущие запросы).
// Несохранённые формы (useLeaveGuard) — вопрос «Уйти без сохранения?» до POST (спека UI-фундамента §4.11, m8):
// после выхода роутер уводит на вход редиректом с ignoreBlocker — блокировщик формы там уже не спросит.
// «Остаться» — POST не уходит.
export function useLogout(opts: { onSignedOut?: () => void } = {}) {
  const api = useApi();
  const queryClient = useQueryClient();
  const session = useSession();
  const toast = useToast();
  const errorText = useErrorText();
  const signedOut = async () => {
    queryClient.removeQueries({ predicate: (q) => !isMeKey(q.queryKey) });
    clearDrafts();
    clearRecent();
    await session.afterLogout();
    queryClient.getMutationCache().clear();
    opts.onSignedOut?.();
  };
  const confirmLeave = useConfirmLeave();
  const m = useAdminMutation(
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
  const { mutate: run } = m;
  const mutate = useCallback(() => {
    void confirmLeave().then((leave) => {
      if (leave) run();
    });
  }, [confirmLeave, run]);
  // Наружу — только то, что вопрос не обходит (m2): mutateAsync/retry ушли бы без него.
  return { mutate, isPending: m.isPending };
}
