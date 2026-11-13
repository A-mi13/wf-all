import type { ChipTone } from '../../ui';

// Окно опроса задачи (план B, Task 20; ревью планов minor 5; бриф G9-I m-7): первые 2 минуты от монтирования строки
// — раз в 5 с, потом раз в 60 с; в queued и в незнакомом состоянии (m-8) через 10 минут опрос останавливается —
// бесконечный опрос держал бы базу Neon проснувшейся (стенд без воркера). running — до конечного состояния (импорт
// идёт до 30 минут, зависшую задачу River снимает сам). Время — Date.now() от монтирования, не число тиков: скрытая
// вкладка троттлит таймеры (TanStack в скрытой вкладке не опрашивает вовсе — refetchIntervalInBackground: false), и
// после возврата окно уже прошло. F5 с ?job= — новое монтирование, окно с начала.
export const POLL_FAST_MS = 5_000;
export const POLL_SLOW_MS = 60_000;
export const FAST_WINDOW_MS = 2 * 60_000;
export const POLL_CEILING_MS = 10 * 60_000;

export const TERMINAL: readonly string[] = ['completed', 'failed', 'cancelled'];
export const JOB_TONES: Record<string, ChipTone> = {
  queued: 'neutral',
  running: 'accent',
  completed: 'success',
  failed: 'danger',
  cancelled: 'neutral',
};
export const knownJobState = (state: string) => Object.hasOwn(JOB_TONES, state);
// Потолок 10 минут — у ожидающей воркер и у незнакомого состояния; running и конечные его не знают.
export const capped = (state: string) => state === 'queued' || !knownJobState(state);

export function pollInterval(state: string | undefined, elapsed: number): number | false {
  if (state === undefined || TERMINAL.includes(state)) return false;
  const step = elapsed < FAST_WINDOW_MS ? POLL_FAST_MS : POLL_SLOW_MS;
  if (!capped(state)) return step;
  const left = POLL_CEILING_MS - elapsed;
  // последний запрос — ровно на потолке: после него строка показывает остановку
  return left <= 0 ? false : Math.min(step, left);
}
