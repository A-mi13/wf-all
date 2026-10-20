import createClient, { type Middleware } from 'openapi-fetch';
import { type ReauthBus, reauthBus } from '../app/reauth';
import type { components, paths } from './gen/admin';

// Клиент админского API (спека сотрудников §9.1, §9.3). Существует только в apps/admin — веб его
// физически не видит. Адрес — тот же origin (§5.2): dev-прокси Vite или сам admin-api.
//
// Middleware:
// - X-WF-Admin: 1 на изменяющих методах (CSRF вместе с Origin); ключ идемпотентности НЕ создаёт —
//   его даёт useAdminMutation, один на действие (M6);
// - 401 auth.reauth_required → ожидание разблокировки (reauthBus) и повтор исходного запроса по
//   клону, снятому до отправки (тело POST повторяется целиком, P20);
// - любой не-2xx ответ → исключение ApiError: вызов либо возвращает данные, либо бросает.

export const ADMIN_HEADER = 'X-WF-Admin';
export const BACKGROUND_HEADER = 'X-WF-Background';

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

export type FieldError = { field: string; code: string };
type Problem = components['schemas']['Problem'];

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields?: FieldError[];
  readonly retryAfter?: number;
  readonly attemptsLeft?: number;
  // 401 auth.reauth_required запроса с noReplay(): разблокировка ПРОШЛА, но запрос не повторён —
  // экран просит новый код («Вход подтверждён…»). Без метки (разблокировку отклонили: выход,
  // блокировка) экран молчит — страница уходит на вход.
  readonly replaySkipped?: true;

  constructor(init: {
    status: number;
    code: string;
    fields?: FieldError[];
    retryAfter?: number;
    attemptsLeft?: number;
    replaySkipped?: boolean;
  }) {
    super(`${init.status} ${init.code}`);
    this.name = 'ApiError';
    this.status = init.status;
    this.code = init.code;
    if (init.fields) this.fields = init.fields;
    if (init.retryAfter !== undefined) this.retryAfter = init.retryAfter;
    if (init.attemptsLeft !== undefined) this.attemptsLeft = init.attemptsLeft;
    if (init.replaySkipped) this.replaySkipped = true;
  }
}

export function isApiError(e: unknown, code?: string): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code);
}

// Исход изменяющего запроса неизвестен — сервер мог исполнить, а ответа нет: обрыв сети (не
// ApiError), 5xx (сбой после коммита), 408/499 прокси, 2xx без тела (unwrap: прокси или расхождение с
// контрактом), 409 idempotency.replay_unavailable (ключ закоммичен, ответ не сохранён). Повтор НОВЫМ
// ключом был бы вторым действием: итог — перечитыванием (решения подтверждений) или повтором тем же
// ключом (useAdminMutation: те же значения формы). Остальные 4xx — отказ сервера, исход известен.
export function isUnknownOutcome(e: unknown): boolean {
  if (!isApiError(e)) return true;
  return (
    e.status >= 500 ||
    e.status < 300 ||
    e.status === 408 ||
    e.status === 499 ||
    e.code === 'idempotency.replay_unavailable'
  );
}

// Помечает фоновый опрос (счётчик подтверждений, статус импорта): сервер не продлевает по нему
// бездействие сессии (§4.7, Review Focus 2). Возвращает только заголовки — спред не переносит в
// GET поля RequestInit вроде body. Использование: api.GET(path, { params, ...background() }).
export function background(headers?: HeadersInit): { headers: Record<string, string> } {
  return {
    headers: { ...Object.fromEntries(new Headers(headers)), [BACKGROUND_HEADER]: '1' },
  };
}

// Метка «не повторять после разблокировки» (ревью Task 14, I1) — для мутаций с кодом второго
// фактора в теле (перевыпуск резервных кодов, смена пароля). Код из приложения одноразовый:
// разблокировка кодом сдвигает last_used_step, и повтор того же тела сервер отверг бы как
// staff.mfa_invalid, засчитав неудачу в блокировку. Такой запрос после разблокировки падает исходным
// 401 auth.reauth_required — экран стирает код и просит новый. Заголовок служебный: клиент снимает
// его до отправки, на сервер он не уходит. Использование: api.POST(path, { params, body, ...noReplay() }).
export const NO_REPLAY_HEADER = 'X-WF-No-Replay';

export function noReplay(): { headers: Record<string, string> } {
  return { headers: { [NO_REPLAY_HEADER]: '1' } };
}

const nonNegative = (v: unknown): number | undefined =>
  typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : undefined;

async function readProblem(res: Response): Promise<Partial<Problem> | null> {
  try {
    const body: unknown = await res.clone().json();
    return body && typeof body === 'object' ? (body as Partial<Problem>) : null;
  } catch {
    return null;
  }
}

// Problem (RFC 9457) → ApiError. Ответ без Problem (прокси, HTML) — код internal: у него есть
// текст в локалях, а статус сохраняется. Срок 429 — только заголовок Retry-After (решение 10a: поля
// retry_after в Problem нет).
async function toApiError(res: Response, replaySkipped = false): Promise<ApiError> {
  const p = await readProblem(res);
  const header = Number.parseInt(res.headers.get('Retry-After') ?? '', 10);
  const errors = p?.errors;
  const fields = Array.isArray(errors)
    ? errors.map(({ field, code }) => ({ field, code }))
    : undefined;
  return new ApiError({
    status: res.status,
    code: typeof p?.code === 'string' && p.code ? p.code : 'internal',
    fields,
    retryAfter: nonNegative(header),
    attemptsLeft: nonNegative(p?.attempts_left),
    replaySkipped,
  });
}

function adminMiddleware(bus: ReauthBus): Middleware {
  // Клоны исходных запросов до отправки: тело Request читается один раз.
  const originals = new Map<string, Request>();
  // Запросы с меткой noReplay(): после разблокировки не повторяются.
  const once = new Set<string>();
  return {
    onRequest({ request, id }) {
      if (MUTATING.has(request.method)) request.headers.set(ADMIN_HEADER, '1');
      if (request.headers.has(NO_REPLAY_HEADER)) {
        request.headers.delete(NO_REPLAY_HEADER);
        once.add(id);
      }
      originals.set(id, request.clone());
      return request;
    },
    async onResponse({ response, id, options }) {
      const original = originals.get(id);
      originals.delete(id);
      const replay = !once.delete(id);
      let res = response;
      let skipped = false;
      if (res.status === 401 && original) {
        const p = await readProblem(res);
        if (p?.code === 'auth.reauth_required') {
          // Любой запрос, и фоновый опрос тоже, открывает разблокировку (§9.3): вкладка с опросом
          // через 20 минут показывает модалку, а не замёрзший счётчик. Открыта уже — ждёт её же.
          const unlocked = await bus.request().then(
            () => true,
            () => false,
          );
          // Повтор — один раз: снова reauth_required уходит ошибкой, без второго круга. Запрос с
          // noReplay() не повторяется: экран получает исходный 401 и просит новый код.
          if (unlocked && replay) res = await options.fetch(original);
          skipped = unlocked && !replay;
        }
      }
      if (!res.ok) throw await toApiError(res, skipped);
      return res;
    },
    onError({ id }) {
      originals.delete(id);
      once.delete(id);
    },
  };
}

// Типы операций без заголовка X-WF-Admin: его ставит middleware, вызывающий код его не передаёт.
// Idempotency-Key остаётся обязательным — его даёт useAdminMutation (M6).
type WithoutAdminHeader<H> = Omit<H, typeof ADMIN_HEADER>;
type FixParams<P> = P extends { header: infer H }
  ? Omit<P, 'header'> &
      ([keyof WithoutAdminHeader<H>] extends [never]
        ? { header?: never }
        : { header: WithoutAdminHeader<H> })
  : P;
type FixOperation<O> = O extends { parameters: infer P }
  ? Omit<O, 'parameters'> & { parameters: FixParams<P> }
  : O;
export type AdminPaths = {
  [Path in keyof paths]: { [M in keyof paths[Path]]: FixOperation<paths[Path][M]> };
};

export function createApi(baseUrl: string, opts: { reauth?: ReauthBus } = {}) {
  const client = createClient<AdminPaths>({
    baseUrl,
    credentials: 'same-origin',
    // globalThis.fetch читается при каждом запросе, а не при создании клиента (подмена в тестах).
    fetch: (request) => globalThis.fetch(request),
  });
  client.use(adminMiddleware(opts.reauth ?? reauthBus));
  return client;
}

export type Api = ReturnType<typeof createApi>;

// Данные ответа openapi-fetch без `!` и `as`: middleware бросает ApiError на любой не-2xx, поэтому
// у дошедшего сюда ответа data есть — кроме 2xx без тела там, где тело ожидалось (сломанный
// прокси, расхождение с контрактом); это ApiError internal со статусом ответа, а не undefined в
// данных. Операции с 204 (выход, язык) unwrap не нужен. Использование:
// queryFn: () => unwrap(api.GET('/v1/staff/me')).
export async function unwrap<T>(request: Promise<{ data?: T; response: Response }>): Promise<T> {
  const { data, response } = await request;
  if (data === undefined) throw new ApiError({ status: response.status, code: 'internal' });
  return data;
}
