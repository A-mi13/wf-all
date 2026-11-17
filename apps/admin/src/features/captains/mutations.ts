import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import type { RejectionCode } from './labels';
import { useInvalidateCaptains } from './queries';

// Решения по заявке (план I-4, Task 8): ключ идемпотентности — один на действие (useAdminMutation: повтор с теми же
// значениями после неизвестного исхода — тот же ключ, сервер не решит дважды). После ответа — и успешного, и с
// ошибкой — перечитывается весь раздел (счётчик, плитка, очередь, карточка): решённая заявка уходит из pending,
// закрытая другим сотрудником — показывает итог. Комментарий — без пробелов по краям; пустой не уходит.
// Переменные (комментарий для игрока) не живут в кэше мутаций дольше экрана — gcTime: 0.
export const DECIDE_KEY = ['identity', 'captains', 'decide'] as const;

export type ApproveVars = { comment: string };
export type RejectVars = { rejection_code: RejectionCode; comment: string };

const comment = (v: string) => {
  const c = v.trim();
  return c ? { comment: c } : {};
};

export function useApproveApplication(id: string) {
  const api = useApi();
  const invalidate = useInvalidateCaptains();
  return useAdminMutation(
    (v: ApproveVars, key: string) =>
      unwrap(
        api.POST('/v1/identity/captain-applications/{applicationId}/approve', {
          params: { path: { applicationId: id }, header: { 'Idempotency-Key': key } },
          body: comment(v.comment),
        }),
      ),
    { mutationKey: DECIDE_KEY, gcTime: 0, onSettled: () => invalidate() },
  );
}

export function useRejectApplication(id: string) {
  const api = useApi();
  const invalidate = useInvalidateCaptains();
  return useAdminMutation(
    (v: RejectVars, key: string) =>
      unwrap(
        api.POST('/v1/identity/captain-applications/{applicationId}/reject', {
          params: { path: { applicationId: id }, header: { 'Idempotency-Key': key } },
          body: { rejection_code: v.rejection_code, ...comment(v.comment) },
        }),
      ),
    { mutationKey: DECIDE_KEY, gcTime: 0, onSettled: () => invalidate() },
  );
}
