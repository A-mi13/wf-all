import { useQueryClient } from '@tanstack/react-query';
import { isApiError, isUnknownOutcome, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { geoKeys, IMPORTS_KEY } from './queries';

// Мутации «Импорта GeoNames» (план B, Task 20): постановка и отмена задачи очереди. Отдельно от mutations.ts —
// правки городов и районов (его инвалидацию правит задача стран). Журнал (IMPORTS_KEY) перечитывается после
// постановки, отмены и при любом отказе отмены, кроме сбоя сети: строка журнала появляется, когда задача начала
// работу, отменённую задачу River через 24 ч удаляет (404 — журнал покажет итог).

export function useStartImport() {
  const api = useApi();
  const queryClient = useQueryClient();
  return useAdminMutation(
    (country: string, key: string) =>
      unwrap(
        api.POST('/v1/geo/imports', {
          params: { header: { 'Idempotency-Key': key } },
          body: { country },
        }),
      ),
    {
      mutationKey: ['geo', 'imports', 'start'],
      onSettled: () => queryClient.invalidateQueries({ queryKey: IMPORTS_KEY }),
    },
  );
}

// Отмена задачи «в очереди» (geo.imports.run). Ответ — задача (cancelled; повторная отмена — тоже 200, K6) — в кэш
// опроса. 409 geo.import_job_not_cancellable (задача уже идёт или кончилась) и неизвестный исход — состояние
// перечитывается: экран показывает факт, а не догадку. 404 — задачи нет: журнал и состояние перечитаны.
export function useCancelImportJob(jobId: number) {
  const api = useApi();
  const queryClient = useQueryClient();
  const reread = () => queryClient.invalidateQueries({ queryKey: geoKeys.importJob(jobId) });
  const journal = () => queryClient.invalidateQueries({ queryKey: [...IMPORTS_KEY, 'list'] });
  return useAdminMutation(
    (_: void, key: string) =>
      unwrap(
        api.POST('/v1/geo/imports/jobs/{jobId}/cancel', {
          params: { path: { jobId }, header: { 'Idempotency-Key': key } },
        }),
      ),
    {
      mutationKey: ['geo', 'imports', 'cancel', jobId],
      onSuccess: async (job) => {
        queryClient.setQueryData(geoKeys.importJob(jobId), job);
        await journal();
      },
      onError: async (e) => {
        // задачи нет — журнал и состояние (опрос получит 404: строка покажет «задачи нет»)
        if (isApiError(e, 'geo.import_job_not_found')) await Promise.all([journal(), reread()]);
        else if (isApiError(e, 'geo.import_job_not_cancellable') || isUnknownOutcome(e))
          await reread();
      },
    },
  );
}
