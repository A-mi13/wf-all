import { type ApiError, isApiError, isUnknownOutcome } from '../../api/client';
import { newIdempotencyKey } from '../../api/idempotency';
import type { Decision } from '../../app/permissions';
import type { BulkAction } from './model';

// Исполнение массового действия (спека UI-фундамента §4.5, стр. 561–572; I14) — без React. Серверных
// массовых ручек нет: по одной строке, не больше concurrency в полёте, у каждой строки свой ключ
// идемпотентности (тот же генератор, что у useAdminMutation). Итог — по видам:
// - executed / pending (202 {approval}) — исход известен, повтора нет никогда;
// - failed — известный отказ (4xx): «Повторить неудавшиеся» шлёт НОВЫЙ ключ (новое действие);
// - unknown — isUnknownOutcome (обрыв, 5xx, in_progress…): повтор ТЕМ ЖЕ ключом — сервер вернёт
//   сохранённый ответ, а не исполнит второй раз;
// - skipped — права на строку нет (decide(perm, cityId строки) → deny) до запроса, в том числе после
//   403/401 посреди пачки; строка, у которой permission(row) бросил (ошибка экрана), — тоже;
// - cancelled — не начаты: пачку отменили (signal — ушли со страницы, сменился сотрудник).
// 429 любой строки — общая пауза всей пачки (Retry-After или BULK_RETRY_DEFAULT_MS): ни один исполнитель
// не стартует новый запрос до её конца, строка с 429 повторяется тем же ключом. 403 auth.forbidden и
// 401 auth.unauthenticated — пауза до перечитывания me (onForbidden; запросы пачки идут мимо кэшей
// TanStack, которые перечитывают me сами): оставшиеся строки перепроверяются decide до запроса, а при
// конце сессии каркас снимается и размонтирование отменяет пачку (оставшиеся — cancelled).
// 401 auth.reauth_required сюда не доходит: клиент API ждёт разблокировку и повторяет запрос сам.

export const BULK_CONCURRENCY = 3;
export const BULK_RETRY_DEFAULT_MS = 5000;
// Retry-After: 0 не превращается в мгновенный круг 429 → повтор → 429.
const BULK_RETRY_MIN_MS = 1000;

export type BulkReport<T> = {
  executed: T[];
  pending: T[];
  failed: { row: T; error: ApiError }[];
  unknown: { row: T; key: string }[];
  skipped: T[];
  cancelled: T[];
};

export type BulkOptions = {
  // решение по праву строки и её городу (permission(row)) — читается заново перед каждым запросом
  decide: (permission: string, cityId?: string) => Decision;
  signal?: AbortSignal;
  onProgress?: (done: number, total: number) => void;
  // после 403 auth.forbidden / 401 auth.unauthenticated: перечитать me; до конца пачка стоит
  onForbidden?: () => Promise<unknown>;
};

// unknown — повтор строки с неизвестным исходом: без права она остаётся unknown (могла исполниться)
type Task<T> = { row: T; key: string; unknown?: boolean };

const emptyReport = <T>(): BulkReport<T> => ({
  executed: [],
  pending: [],
  failed: [],
  unknown: [],
  skipped: [],
  cancelled: [],
});

export function runBulk<T>(
  rows: readonly T[],
  action: BulkAction<T>,
  opts: BulkOptions,
): Promise<BulkReport<T>> {
  return execute(
    rows.map((row) => ({ row, key: newIdempotencyKey() })),
    action,
    opts,
  );
}

// «Повторить неудавшиеся»: failed — новым ключом, unknown — прежним; executed, pending и skipped
// переходят в новый итог как есть (202 не повторяется никогда), cancelled — не повторяются (их не
// начинали: это новый выбор, а не повтор).
export async function retryBulk<T>(
  report: BulkReport<T>,
  action: BulkAction<T>,
  opts: BulkOptions,
): Promise<BulkReport<T>> {
  const tasks: Task<T>[] = [
    ...report.failed.map(({ row }) => ({ row, key: newIdempotencyKey() })),
    ...report.unknown.map(({ row, key }) => ({ row, key, unknown: true })),
  ];
  const next = await execute(tasks, action, opts);
  return {
    ...next,
    executed: [...report.executed, ...next.executed],
    pending: [...report.pending, ...next.pending],
    skipped: [...report.skipped, ...next.skipped],
  };
}

// Пауза, прерываемая отменой (таймер снимается).
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal?.aborted) return resolve();
    const done = () => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal?.addEventListener('abort', done, { once: true });
  });
}

function retryDelay(e: ApiError): number {
  if (e.retryAfter === undefined) return BULK_RETRY_DEFAULT_MS;
  return Math.max(e.retryAfter * 1000, BULK_RETRY_MIN_MS);
}

async function execute<T>(
  tasks: Task<T>[],
  action: BulkAction<T>,
  { decide, signal, onProgress, onForbidden }: BulkOptions,
): Promise<BulkReport<T>> {
  const report = emptyReport<T>();
  const queue = [...tasks];
  const total = tasks.length;
  let done = 0;
  const settle = () => onProgress?.(++done, total);
  // общая пауза пачки: пока она есть, никто не берёт новую строку
  let pause: Promise<void> | null = null;
  const hold = (until: Promise<unknown>) => {
    const next: Promise<void> = Promise.all([pause, until.catch(() => undefined)]).then(() => {
      if (pause === next) pause = null;
    });
    pause = next;
  };

  // permission(row) — код экрана: бросил (неожиданная форма строки) — строка без запроса, пачка идёт
  const allowed = (task: Task<T>) => {
    try {
      const { perm, cityId } = action.permission(task.row);
      return decide(perm, cityId) !== 'deny';
    } catch (e) {
      console.error(e);
      return false;
    }
  };

  const worker = async () => {
    for (;;) {
      while (pause) await pause;
      if (signal?.aborted) return;
      const task = queue.shift();
      if (!task) return;
      if (!allowed(task)) {
        if (task.unknown) report.unknown.push({ row: task.row, key: task.key });
        else report.skipped.push(task.row);
        settle();
        continue;
      }
      try {
        const outcome = await action.run(task.row, task.key);
        (outcome === 'pending_approval' ? report.pending : report.executed).push(task.row);
      } catch (e) {
        if (isApiError(e) && e.status === 429) {
          hold(sleep(retryDelay(e), signal));
          queue.unshift(task); // тот же ключ после паузы
          continue;
        }
        if (isUnknownOutcome(e) || !isApiError(e)) {
          report.unknown.push({ row: task.row, key: task.key });
        } else {
          report.failed.push({ row: task.row, error: e });
          const authChanged = e.status === 403 || e.code === 'auth.unauthenticated';
          if (authChanged && onForbidden) hold(onForbidden());
        }
      }
      settle();
    }
  };

  const n = Math.max(1, Math.min(action.concurrency ?? BULK_CONCURRENCY, tasks.length));
  await Promise.all(Array.from({ length: n }, worker));
  report.cancelled.push(...queue.map((t) => t.row));
  return report;
}
