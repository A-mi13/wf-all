import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import { isApiError, unwrap } from '../api/client';
import { useApi } from '../api/context';
import { claimDrafts } from './draft';
import { can, decide, type Decision, type Me } from './permissions';
import { ME_KEY } from './query';
import { reauthBus } from './reauth';

// Сессия на клиенте (спека сотрудников §9.3, R15, P20) — из GET /v1/staff/me; он же проверка
// связи с admin-api (R13).
// - loading — ответа ещё нет;
// - anonymous — 401 auth.unauthenticated: экран входа с возвратом; signedOut — после явного
//   выхода (afterLogout): вход без возврата, следующий сотрудник не попадёт в чужой раздел;
// - mfa_pending — пароль принят, шаг 2 входа; me — минимум, учётки нет; sessionId — какой
//   именно вход ждёт код (экран входа не показывает шаг 2 брошенной сессии);
// - setup — первый вход по приглашению: только настройка 2FA;
// - active — работает; reauthRequired — сессия ждёт второй фактор (модалка разблокировки);
// - error — admin-api не ответил (сеть, 5xx): экран «нет связи» с повтором.
export type SessionState =
  | { status: 'loading' }
  | { status: 'anonymous'; signedOut?: boolean }
  | { status: 'mfa_pending'; sessionId: string | null }
  | { status: 'setup'; me: Me }
  | { status: 'active'; me: Me; reauthRequired: boolean }
  | { status: 'error'; error: Error };

export type Session = SessionState & {
  refresh(): Promise<void>;
  // Сессия завершена выходом (204 staffLogout): me перечитывается, вход — без return.
  afterLogout(): Promise<void>;
};

const SessionContext = createContext<Session | null>(null);

function stateOf(data: Me | undefined, error: Error | null): SessionState {
  if (isApiError(error, 'auth.unauthenticated')) return { status: 'anonymous' };
  if (data) {
    switch (data.stage) {
      case 'mfa_pending':
        return { status: 'mfa_pending', sessionId: data.session?.id ?? null };
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
  // id сессии, завершённой выходом. Флаг держится, пока me — 401; любая другая сессия (новый
  // вход) его снимает, и истёкшая потом сессия снова ведёт на вход с возвратом.
  const [ended, setEnded] = useState<string | null>(null);
  const base = stateOf(q.data, q.error);
  const sid = base.status !== 'anonymous' && q.data ? (q.data.session?.id ?? '') : null;
  if (ended !== null && sid !== null && sid !== ended) setEnded(null);
  const signedOut = base.status === 'anonymous' && ended !== null;
  const current = q.data?.session?.id ?? '';
  const afterLogout = useCallback(async () => {
    setEnded(current);
    await refresh();
  }, [current, refresh]);
  const value = useMemo<Session>(() => {
    const state = stateOf(q.data, q.error);
    return state.status === 'anonymous' && signedOut
      ? { ...state, signedOut: true, refresh, afterLogout }
      : { ...state, refresh, afterLogout };
  }, [q.data, q.error, signedOut, refresh, afterLogout]);
  const reauthRequired = value.status === 'active' && value.reauthRequired;

  // Сессия сотрудника известна (setup, active): черновики других сотрудников этой вкладки стираются
  // (⚠️ Task 14: после истечения сессии за компьютер мог сесть другой).
  const staffId = value.status === 'active' || value.status === 'setup' ? value.me.id : undefined;
  useEffect(() => {
    if (staffId) claimDrafts(staffId);
  }, [staffId]);

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
