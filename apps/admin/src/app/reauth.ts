// Шина разблокировки сессии (спека сотрудников §9.3, P20). Сервер ответил 401
// auth.reauth_required — клиент API ждёт разблокировку и повторяет запрос. Ожидание одно на все
// параллельные запросы: модалка ввода второго фактора (Task 13) подписана на шину и открывается
// при первом request(), остальные запросы ждут то же обещание.

export interface ReauthBus {
  // Ждать разблокировку; первый вызов открывает её (оповещает подписчиков), следующие — ждут ту же.
  request(): Promise<void>;
  // Текущее ожидание или null: фоновые опросы ждут уже открытую разблокировку, но не открывают свою.
  current(): Promise<void> | null;
  // Разблокировка открыта — для модалки (useSyncExternalStore).
  pending(): boolean;
  // Подписка на открытие и закрытие разблокировки; возвращает отписку.
  subscribe(fn: () => void): () => void;
  // Разблокировка прошла — ждущие запросы повторяются.
  resolve(): void;
  // Разблокировки не будет (выход, ошибка) — ждущие запросы падают с исходной ошибкой.
  reject(): void;
}

export class ReauthCancelled extends Error {
  constructor() {
    super('разблокировка сессии отменена');
    this.name = 'ReauthCancelled';
  }
}

export function createReauthBus(): ReauthBus {
  let waiting: {
    promise: Promise<void>;
    resolve: () => void;
    reject: (e: Error) => void;
  } | null = null;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((fn) => fn());

  const settle = (ok: boolean) => {
    const w = waiting;
    if (!w) return;
    waiting = null;
    if (ok) w.resolve();
    else w.reject(new ReauthCancelled());
    notify();
  };

  return {
    request() {
      if (!waiting) {
        let resolve!: () => void;
        let reject!: (e: Error) => void;
        const promise = new Promise<void>((res, rej) => {
          resolve = res;
          reject = rej;
        });
        // Отказ без ждущих (вызвавший не дождался) не должен становиться необработанным.
        promise.catch(() => {});
        waiting = { promise, resolve, reject };
        notify();
      }
      return waiting.promise;
    },
    current: () => waiting?.promise ?? null,
    pending: () => waiting !== null,
    subscribe(fn) {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
    resolve: () => settle(true),
    reject: () => settle(false),
  };
}

// Шина приложения: её слушает модалка разблокировки, в неё идут клиент API и сессия.
export const reauthBus: ReauthBus = createReauthBus();
