import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { isApiError } from '../api/client';

// Ключ запроса GET /v1/staff/me — состояние сессии (session.tsx).
export const ME_KEY = ['staff', 'me'] as const;

export const isMeKey = (key: readonly unknown[]): boolean =>
  key.length === ME_KEY.length && key[0] === ME_KEY[0] && key[1] === ME_KEY[1];

// Одноразовый запрос (meta: ONE_SHOT): каждый вызов выпускает новое (секрет 2FA). Смена сотрудника его
// удаляет из кэша, а не перезапускает (session.tsx).
export const ONE_SHOT = { oneShot: true } as const;

export const isOneShot = (meta: Record<string, unknown> | undefined): boolean =>
  meta?.oneShot === true;

// Ответы 4xx — решение сервера, повтор их не изменит; сеть и 5xx — до двух повторов.
function retryQuery(failures: number, error: unknown): boolean {
  if (isApiError(error) && error.status < 500) return false;
  return failures < 2;
}

// QueryClient админки:
// - мутации — networkMode 'always': без сети не копятся в очереди, а сразу падают (баннер «нет
//   сети», P20);
// - 401 auth.unauthenticated от любого запроса перечитывает me — сессия становится anonymous, и
//   роутер уводит на вход с возвратом (§9.3);
// - 403 auth.forbidden — права сняли, пока вкладка открыта (ревью ветки, M5): me перечитывается,
//   меню и разделы следуют новым правам.
export function createQueryClient(opts: { retry?: boolean } = {}): QueryClient {
  const onError = (error: unknown, isMe: boolean) => {
    if (
      !isMe &&
      (isApiError(error, 'auth.unauthenticated') || isApiError(error, 'auth.forbidden'))
    ) {
      void client.invalidateQueries({ queryKey: ME_KEY });
    }
  };
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({
      onError: (error, query) => onError(error, isMeKey(query.queryKey)),
    }),
    mutationCache: new MutationCache({ onError: (error) => onError(error, false) }),
    defaultOptions: {
      queries: { retry: opts.retry === false ? false : retryQuery },
      mutations: { networkMode: 'always', retry: false },
    },
  });
  return client;
}
