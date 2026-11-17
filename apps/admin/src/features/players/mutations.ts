import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useInvalidateApprovals } from '../approvals/queries';

// Сброс 2FA игрока (спека identity §7.4; план I-4, Task 9): запрос — всегда вдвоём (202 → запрос на подтверждение,
// счётчик и списки «Подтверждений» перечитываются); отмена назначенного — сразу (200, не 202). Ключ идемпотентности —
// один на действие (useAdminMutation). Причина и номер обращения (свободный текст сотрудника) не живут в кэше мутаций
// дольше экрана — gcTime: 0. Повторного поиска после ответа нет (он потребовал бы снова причину и дал бы лишнюю
// запись pii_read): карточку обновляет экран по ответу.
export type ResetVars = { reason: string; ticket: string };

export function useRequestMfaReset(userId: string) {
  const api = useApi();
  const invalidateApprovals = useInvalidateApprovals();
  return useAdminMutation(
    (v: ResetVars, key: string) => {
      const ticket = v.ticket.trim();
      return unwrap(
        api.POST('/v1/identity/users/{userId}/mfa-reset', {
          params: { path: { userId }, header: { 'Idempotency-Key': key } },
          body: { reason: v.reason.trim(), ...(ticket ? { ticket } : {}) },
        }),
      );
    },
    // и после ошибки: при неизвестном исходе (5xx, обрыв) запрос мог быть создан — счётчик не ждёт опроса
    { gcTime: 0, onSettled: () => void invalidateApprovals() },
  );
}

export function useCancelMfaReset(resetId: string) {
  const api = useApi();
  return useAdminMutation(
    (_: void, key: string) =>
      unwrap(
        api.POST('/v1/identity/mfa-resets/{resetId}/cancel', {
          params: { path: { resetId }, header: { 'Idempotency-Key': key } },
        }),
      ),
    { gcTime: 0 },
  );
}
