import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { isApiError, unwrap } from '../api/client';
import { useApi } from '../api/context';
import { claimDrafts, clearDrafts } from './draft';
import { can, decide, type Decision, type Me } from './permissions';
import { isMeKey, isOneShot, ME_KEY } from './query';
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

// Сотрудник, пришедший в эту вкладку сменой без 401 (подписка ниже): id или null. Экран настройки 2FA
// по нему не выпускает секрет сам — выпуск сжёг бы QR, который этот сотрудник сканирует в другой вкладке.
type Switched = { staffId: string | null; acknowledge(): void };
const SwitchedContext = createContext<Switched>({ staffId: null, acknowledge: () => {} });

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

// Чьи данные лежат в памяти вкладки по ответу me: id сотрудника (setup, active); null — ничьи
// (mfa_pending, незнакомая стадия: учётки ещё нет).
function ownerOf(me: Me): string | null {
  return me.stage === 'active' || me.stage === 'setup' ? (me.id ?? null) : null;
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const api = useApi();
  const queryClient = useQueryClient();
  const q = useQuery({
    queryKey: ME_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/me')),
    // me перечитывается явно: refresh() после входа и смены языка, и при 401 других запросов
    staleTime: 60_000,
    // и при каждом возврате во вкладку, даже свежий (повторное ревью 27.10, I1): в другой вкладке мог
    // смениться сотрудник (cookie общая) — подписка ниже сотрёт данные прежнего сразу, а не через 60 с
    refetchOnWindowFocus: 'always',
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

  // Без сессии (401: истекла, вышли): данные прошлого сотрудника — из кэша запросов и мутаций (в
  // переменных — почта и имя приглашённого, комментарий отклонения). Экраны
  // с ними уже сняты (роутер увёл на вход), а следующий за компьютером не увидит их, пока грузятся
  // его собственные. me остаётся — по нему живёт сессия.
  const anonymous = value.status === 'anonymous';
  useEffect(() => {
    if (!anonymous) return;
    queryClient.removeQueries({ predicate: (query) => !isMeKey(query.queryKey) });
    queryClient.getMutationCache().clear();
  }, [anonymous, queryClient]);

  // Сотрудник сменился без 401 между (ревью ветки, I1): в другой вкладке A вышел и вошёл B или B ввёл
  // пароль — cookie общая, и эта вкладка получает me B (или mfa_pending) прямо после me A. Данные A
  // стираются в момент, когда кэш получил новый me, — до первого рендера с ним (эффект по стадии
  // успел бы показать B список A на кадр): кэш запросов, кроме me (активные сбрасываются и
  // перечитываются уже под B), кэш мутаций (ПД в переменных), черновики вкладки (почта A на шаге 2
  // входа), ждущая разблокировка (клон запроса A не уйдёт под cookie B). Каркас размонтирует экраны
  // A сам (key по me.id в router.tsx). Тот же сотрудник (язык, права) ничего не стирает; после 401
  // данные уже стёрты выше — вход в этой же вкладке (mfa_pending, active) ничего не стирает.
  const owner = useRef<string | null | undefined>(undefined);
  // кто пришёл сменой без 401 (доделка W5-front): вход после 401 и новый монтаж его снимают
  const [switchedTo, setSwitchedTo] = useState<string | null>(null);
  useEffect(() => {
    const cached = queryClient.getQueryData<Me>(ME_KEY);
    owner.current = cached ? ownerOf(cached) : undefined;
    return queryClient.getQueryCache().subscribe((event) => {
      if (event.type !== 'updated' || !isMeKey(event.query.queryKey)) return;
      const { action } = event;
      if (action.type === 'error') {
        if (isApiError(action.error, 'auth.unauthenticated')) {
          owner.current = null;
          setSwitchedTo(null);
        }
        return;
      }
      if (action.type !== 'success') return;
      const data = event.query.state.data as Me | undefined;
      if (!data) return;
      const prev = owner.current;
      const next = ownerOf(data);
      owner.current = next;
      if (typeof prev !== 'string' || next === prev) return;
      setSwitchedTo(next);
      // одноразовые выпуски (секрет 2FA: каждый запрос — новый секрет) удаляются, а не перезапускаются:
      // повтор под cookie нового сотрудника сжёг бы секрет, который он сканирует в другой вкладке
      // (повторное ревью 27.10, m2); остальное сбрасывается и перечитывается уже под ним
      queryClient.removeQueries({ predicate: (query) => isOneShot(query.meta) });
      void queryClient.resetQueries({ predicate: (query) => !isMeKey(query.queryKey) });
      queryClient.getMutationCache().clear();
      clearDrafts();
      reauthBus.reject();
    });
  }, [queryClient]);

  // После перезагрузки с reauth_required модалка открывается сразу; разблокировка прошла —
  // me перечитывается (P20).
  useEffect(() => {
    if (!reauthRequired) return;
    reauthBus.request().then(refresh, () => {});
  }, [reauthRequired, refresh]);

  const acknowledge = useCallback(() => setSwitchedTo(null), []);
  const switched = useMemo(() => ({ staffId: switchedTo, acknowledge }), [switchedTo, acknowledge]);

  return (
    <SessionContext value={value}>
      <SwitchedContext value={switched}>{children}</SwitchedContext>
    </SessionContext>
  );
}

// react-refresh не различает хуки среди именованных экспортов файла с компонентом —
// ложное срабатывание для пары Provider + хуки (как в api/context.tsx).
// eslint-disable-next-line react-refresh/only-export-components
export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error('useSession: нет SessionProvider выше по дереву');
  return s;
}

// Текущий сотрудник пришёл в эту вкладку сменой без 401 (в другой вкладке прежний вышел, он вошёл или
// принял приглашение); acknowledge() — пользователь решил продолжить здесь.
// eslint-disable-next-line react-refresh/only-export-components
export function useStaffSwitched(staffId: string | undefined): {
  switched: boolean;
  acknowledge(): void;
} {
  const { staffId: to, acknowledge } = useContext(SwitchedContext);
  return { switched: staffId !== undefined && to === staffId, acknowledge };
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
