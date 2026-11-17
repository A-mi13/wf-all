import { useQuery } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { ONE_SHOT } from '../../app/query';
import { encodeAccessReason } from '../captains/reason';

// Поиск игрока по почте (findPlayerByEmail; спека identity §7.4; план I-4, Task 9, R19): почта и причина —
// заголовками X-WF-Lookup-Email и X-WF-Access-Reason в percent-encoding (encodeURIComponent; не в query: URL и
// журналы прокси не несут ПД). Каждое чтение сервер пишет в журнал ПД с причиной — поиск только по явной отправке:
// без повторов, без перезапросов по фокусу окна и сети (staleTime ∞, retry false, refetchOnWindowFocus/Reconnect
// false). Почта и причина — только в памяти экрана (ref): ключ — без них, номер отправки (каждая — новый ключ, ответ
// прошлой не показывается под новой почтой); кэш живёт, пока есть экран (gcTime 0), смена сотрудника его удаляет
// (ONE_SHOT). Тот же поиск — в «Игроках» и в связи сотрудника с учёткой игрока (scope ключа — у каждого свой).
export const PII_PERMISSION = 'identity.pii.read';
export const RESET_PERMISSION = 'identity.mfa.reset';

export const LOOKUP_KEY = ['identity', 'player-lookup'] as const;
export const lookupKey = (scope: string, n: number) => [...LOOKUP_KEY, scope, n] as const;

export function usePlayerLookup(scope: string) {
  const api = useApi();
  const request = useRef({ email: '', reason: '' });
  const [n, setN] = useState(0);
  const key = lookupKey(scope, n);
  const query = useQuery({
    queryKey: key,
    queryFn: () =>
      unwrap(
        api.GET('/v1/identity/users/lookup', {
          params: {
            header: {
              'X-WF-Lookup-Email': encodeURIComponent(request.current.email),
              'X-WF-Access-Reason': encodeAccessReason(request.current.reason),
            },
          },
        }),
      ),
    enabled: n > 0,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: ONE_SHOT,
  });
  // первое чтение ещё не пришло (отправка была) или идёт — повторная отправка гасится
  const busy = n > 0 && (query.isFetching || query.isPending);
  return {
    key,
    query,
    busy,
    asked: n > 0,
    // email и reason — уже очищенные (cleanAccessReason); вызов, пока идёт прошлый поиск, — ничего
    search(email: string, reason: string) {
      if (busy) return;
      request.current = { email, reason };
      setN((x) => x + 1);
    },
  };
}

export type PlayerLookup = ReturnType<typeof usePlayerLookup>;
