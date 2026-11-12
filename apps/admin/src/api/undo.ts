import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { Me } from '../app/permissions';
import { ME_KEY } from '../app/query';
import { useErrorText } from '../ui/errorText';
import { useToast } from '../ui/toast';
import { isApiError, isUnknownOutcome } from './client';
import { newIdempotencyKey } from './idempotency';
import type { AdminMutationResult } from './mutation';

// Обратное действие «Отменить» (спека UI-фундамента §4.10, I5). Подходит только обратимое по критерию
// admin.md («Отменить» в тосте): одна ручка того же права, 200 у прямого и обратного, без внешних последствий.
export type UndoInverse<V, R> = {
  // Обратный запрос: v — переменные прямого (прежнее значение кладёт экран), result — ответ прямого
  // (актуальная version), key — НОВЫЙ ключ идемпотентности: отмена — новое действие, в журнале своей записью.
  run: (v: V, result: R, key: string) => Promise<void>;
  // Исход обратного неизвестен (обрыв, 5xx): перечитать цель и сказать факт. Повтора вслепую нет.
  reread: (v: V, result: R) => Promise<'undone' | 'not_undone'>;
};

// Чья сессия во вкладке сейчас — по кэшу me, синхронно: id сотрудника (setup, active), иначе null (выход — me
// перечитан с 401, mfa_pending). Кэш меняется раньше рендера, в отличие от поколения тостов (UndoReset — эффект).
function staffNow(queryClient: QueryClient): string | null {
  const state = queryClient.getQueryState<Me>(ME_KEY);
  if (state?.status === 'error' && isApiError(state.error, 'auth.unauthenticated')) return null;
  const me = state?.data;
  return me && (me.stage === 'active' || me.stage === 'setup') ? (me.id ?? null) : null;
}

// Выполнить прямое → тост успеха с «Отменить» (сам не исчезает) → по нажатию обратное → тост итога:
// 4xx — по коду (исходное осталось сделанным); 409 *_stale — «уже изменили», отмены нет; неизвестный исход —
// reread. Отмена не зависит от экрана: тост и запрос живут на уровне приложения (ушли со страницы — дойдёт).
// Повтор отмены — только тем же ключом (общее правило мутаций): «Отменить» одноразовая, тост снят нажатием.
export function useUndoable<V, R, C = unknown>(
  mutation: AdminMutationResult<R, V, C>,
  inverse: UndoInverse<V, R>,
): { mutate(v: V, opts: { successText: string }): void } {
  const toast = useToast();
  const queryClient = useQueryClient();
  const t = useTranslations('ui.toast');
  const errorText = useErrorText();
  const { mutateAsync: run } = mutation;

  const mutate = useCallback(
    (v: V, { successText }: { successText: string }) => {
      // Промис действия, а не колбэк вызова mutate: TanStack глушит колбэк вызова, если наблюдателя нет (экран
      // снят) или следующий mutate перезаписал опции (ревью UF2b/II-A, I2). Ошибку прямого показывает экран (onError).
      run(v).then(
        (result) => {
          // запущенная отмена держит обратное своего рендера: экран мог уйти
          const inv = inverse;
          let started = false;
          toast.success(successText, {
            undo: async () => {
              if (started) return;
              started = true;
              // сменился сотрудник или вышел, пока обратное в полёте, — итог не его, не показываем: поколение
              // тостов (dismissUndo, m6) и сотрудник по кэшу me — смена сотрудника отпускает ждущую разблокировку
              // (reauthBus.reject) раньше, чем UndoReset сменит поколение (ревью UF2b/II-A, r1-2)
              const epoch = toast.undoEpoch();
              const staff = staffNow(queryClient);
              const say = (show: () => void) => {
                if (toast.undoEpoch() === epoch && staffNow(queryClient) === staff) show();
              };
              try {
                await inv.run(v, result, newIdempotencyKey());
                say(() => toast.success(t('undone')));
              } catch (e) {
                if (isApiError(e) && e.status === 409 && e.code.endsWith('_stale')) {
                  say(() => toast.info(t('stale')));
                  // данные экрана обновятся перечитыванием; его исход тост не меняет
                  void inv.reread(v, result).catch(() => {});
                } else if (isUnknownOutcome(e)) {
                  try {
                    const fact = await inv.reread(v, result);
                    say(() =>
                      fact === 'undone' ? toast.success(t('undone')) : toast.error(t('not_undone')),
                    );
                  } catch (e2) {
                    say(() => toast.error(errorText(isApiError(e2) ? e2 : 'internal')));
                  }
                } else {
                  say(() => toast.error(errorText(isApiError(e) ? e : 'internal')));
                }
              }
            },
          });
        },
        () => {},
      );
    },
    [run, inverse, toast, queryClient, t, errorText],
  );
  return { mutate };
}
