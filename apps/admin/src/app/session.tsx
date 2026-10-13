import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, type ReactNode, useCallback, useContext, useEffect, useMemo } from 'react';
import { isApiError, unwrap } from '../api/client';
import { useApi } from '../api/context';
import { can, decide, type Decision, type Me } from './permissions';
import { ME_KEY } from './query';
import { reauthBus } from './reauth';

// Сессия на клиенте (спека сотрудников §9.3, R15, P20) — из GET /v1/staff/me; он же проверка
// связи с admin-api (R13).
// - loading — ответа ещё нет;
// - anonymous — 401 auth.unauthenticated: экран входа с возвратом;
// - mfa_pending — пароль принят, шаг 2 входа; me — минимум, учётки нет;
// - setup — первый вход по приглашению: только настройка 2FA;
// - active — работает; reauthRequired — сессия ждёт второй фактор (модалка разблокировки);
// - error — admin-api не ответил (сеть, 5xx): экран «нет связи» с повтором.
export type SessionState =
  | { status: 'loading' }
  | { status: 'anonymous' }
  | { status: 'mfa_pending' }
  | { status: 'setup'; me: Me }
  | { status: 'active'; me: Me; reauthRequired: boolean }
  | { status: 'error'; error: Error };

export type Session = SessionState & { refresh(): Promise<void> };

const SessionContext = createContext<Session | null>(null);

function stateOf(data: Me | undefined, error: Error | null): SessionState {
  if (isApiError(error, 'auth.unauthenticated')) return { status: 'anonymous' };
  if (data) {
    switch (data.stage) {
      case 'mfa_pending':
        return { status: 'mfa_pending' };
      case 'setup':
        return { status: 'setup', me: data };
      case 'active':
        return { status: 'active', me: data, reauthRequired: data.reauth_required };
    }
    // Незнакомая стадия (enum открытый) — как без входа: сервер всё равно не пустит.
    return { status: 'anonymous' };
  }
  return error ? { status: 'error', error } : { status: 'loading' };
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const q = useQuery({
    queryKey: ME_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/me')),
    // me перечитывается явно: refresh() после входа и смены языка, и при 401 других запросов
    staleTime: 60_000,
  });
  const refresh = useCallback(
    () => queryClient.refetchQueries({ queryKey: ME_KEY, exact: true }),
    [queryClient],
  );
  const value = useMemo(
    () => ({ ...stateOf(q.data, q.error), refresh }),
    [q.data, q.error, refresh],
  );
  const reauthRequired = value.status === 'active' && value.reauthRequired;

  // После перезагрузки с reauth_required модалка открывается сразу; разблокировка прошла —
  // me перечитывается (P20).
  useEffect(() => {
    if (!reauthRequired) return;
    reauthBus.request().then(refresh, () => {});
  }, [reauthRequired, refresh]);

  return <SessionContext value={value}>{children}</SessionContext>;
}

// react-refresh не различает хуки среди именованных экспортов файла с компонентом —
// ложное срабатывание для пары Provider + хуки (как в api/context.tsx).
// eslint-disable-next-line react-refresh/only-export-components
export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error('useSession: нет SessionProvider выше по дереву');
  return s;
}

// Право по me.permissions: allow, scoped в скоупе городов; dual — true как «можно запросить».
// Вне стадии active прав нет.
// eslint-disable-next-line react-refresh/only-export-components
export function useCan(permission: string, cityId?: string): boolean {
  return usePermissionCheck()(permission, cityId);
}

// То же, что useCan, для многих прав разом (меню AppShell: хук нельзя звать в цикле).
// eslint-disable-next-line react-refresh/only-export-components
export function usePermissionCheck(): (permission: string, cityId?: string) => boolean {
  const s = useSession();
  const me = s.status === 'active' ? s.me : null;
  return useCallback((permission, cityId) => me !== null && can(me, permission, cityId), [me]);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useDecide(permission: string, cityId?: string): Decision {
  const s = useSession();
  return s.status === 'active' ? decide(s.me, permission, cityId) : 'deny';
}
