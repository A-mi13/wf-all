import { focusManager } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { ApiError, unwrap } from '../api/client';
import { useApi } from '../api/context';
import {
  CITY_OTHER,
  CITY_OWN,
  meAdmin,
  meModerator,
  meMfaPending,
  meOwner,
  meSetup,
} from '../test/me';
import { problem } from '../test/problem';
import { providers } from '../test/render';
import { http, server } from '../test/server';
import type { Me } from './permissions';
import { ME_KEY } from './query';
import { reauthBus } from './reauth';
import { useCan, useDecide, useSession } from './session';

afterEach(() => reauthBus.reject());

function mockMe(body: Me | (() => Response)) {
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      typeof body === 'function' ? response.untyped(body()) : response(200).json(body),
    ),
  );
}

const session = () => renderHook(() => useSession(), { wrapper: providers({ me: null }) });

describe('состояние сессии из GET /v1/staff/me', () => {
  test('сначала loading', () => {
    mockMe(meOwner);
    expect(session().result.current.status).toBe('loading');
  });

  test('mfa_pending → шаг 2 входа, без учётки', async () => {
    mockMe(meMfaPending);
    const { result } = session();
    await waitFor(() => expect(result.current.status).toBe('mfa_pending'));
    expect('me' in result.current).toBe(false);
  });

  test('setup → только настройка 2FA, с учёткой', async () => {
    mockMe(meSetup);
    const { result } = session();
    await waitFor(() => expect(result.current).toMatchObject({ status: 'setup', me: meSetup }));
  });

  test('active → me и reauthRequired = false', async () => {
    mockMe(meOwner);
    const { result } = session();
    await waitFor(() =>
      expect(result.current).toMatchObject({
        status: 'active',
        me: meOwner,
        reauthRequired: false,
      }),
    );
    expect(reauthBus.pending()).toBe(false);
  });

  test('active с reauth_required → модалка разблокировки открыта сразу (P20)', async () => {
    mockMe({ ...meOwner, reauth_required: true });
    const { result } = session();
    await waitFor(() => expect(result.current).toMatchObject({ reauthRequired: true }));
    expect(reauthBus.pending()).toBe(true);

    // разблокировка прошла — me перечитан
    mockMe(meOwner);
    act(() => reauthBus.resolve());
    await waitFor(() => expect(result.current).toMatchObject({ reauthRequired: false }));
  });

  test('401 auth.unauthenticated → anonymous', async () => {
    mockMe(() => problem(401, 'auth.unauthenticated'));
    const { result } = session();
    await waitFor(() => expect(result.current.status).toBe('anonymous'));
  });

  test('нет связи с API → error (проверка связи — GET /v1/staff/me, R13)', async () => {
    mockMe(() => problem(503, 'internal'));
    const { result } = session();
    await waitFor(() => expect(result.current.status).toBe('error'));
  });

  test('refresh() перечитывает me', async () => {
    mockMe(() => problem(401, 'auth.unauthenticated'));
    const { result } = session();
    await waitFor(() => expect(result.current.status).toBe('anonymous'));
    mockMe(meOwner);
    await act(() => result.current.refresh());
    await waitFor(() => expect(result.current.status).toBe('active'));
  });

  test('401 auth.unauthenticated у другого запроса → сессия перечитана, anonymous', async () => {
    mockMe(meOwner);
    server.use(
      http.get('/v1/staff/me/sessions', ({ response }) =>
        response.untyped(problem(401, 'auth.unauthenticated')),
      ),
    );
    const wrapper = providers({ me: null });
    const { result } = renderHook(() => ({ s: useSession(), api: useApi() }), { wrapper });
    await waitFor(() => expect(result.current.s.status).toBe('active'));
    mockMe(() => problem(401, 'auth.unauthenticated'));
    // запрос через QueryClient — так, как его делают экраны
    await wrapper.queryClient
      .fetchQuery({
        queryKey: ['sessions'],
        queryFn: () => result.current.api.GET('/v1/staff/me/sessions'),
      })
      .catch(() => {});
    await waitFor(() => expect(result.current.s.status).toBe('anonymous'));
  });
});

describe('useCan / useDecide', () => {
  const hooks = (me: Me, perm: string, city?: string) =>
    renderHook(() => ({ can: useCan(perm, city), decide: useDecide(perm, city) }), {
      wrapper: providers({ me }),
    }).result.current;

  test('allow', () => {
    expect(hooks(meOwner, 'audit.log.read')).toEqual({ can: true, decide: 'allow' });
  });
  test('scoped — свой и чужой город', () => {
    expect(hooks(meModerator, 'geo.cities.rename', CITY_OWN)).toEqual({
      can: true,
      decide: 'allow',
    });
    expect(hooks(meModerator, 'geo.cities.rename', CITY_OTHER)).toEqual({
      can: false,
      decide: 'deny',
    });
  });
  test('dual — можно запросить', () => {
    expect(hooks(meOwner, 'staff.roles.grant')).toEqual({ can: true, decide: 'needs_approval' });
  });
  test('ключа нет', () => {
    expect(hooks(meModerator, 'staff.accounts.read')).toEqual({ can: false, decide: 'deny' });
  });
  test('не active (setup) — прав нет', () => {
    expect(
      hooks({ ...meSetup, permissions: { 'audit.log.read': 'allow' } }, 'audit.log.read'),
    ).toEqual({
      can: false,
      decide: 'deny',
    });
  });
});

// Общее устройство (ревью ветки, I1): в другой вкладке A вышел и вошёл B (или B ввёл пароль) — эта
// вкладка видит me другого сотрудника без 401 между ними. Данные A (кэш запросов и мутаций, черновики
// вкладки, ждущий разблокировки запрос) не должны достаться B — стираются в момент, когда кэш получил
// me B, до первого рендера с ним. Тот же сотрудник (язык, права) — кэш цел.
describe('общее устройство: сотрудник сменился без выхода', () => {
  const LIST_KEY = ['approvals', 'list', 'incoming'];
  const LOGIN_DRAFT = 'wf-admin:draft:/login';

  afterEach(() => sessionStorage.clear());

  function seeded(opts: { strict?: boolean; me?: Me | null } = {}) {
    const wrapper = providers({ me: opts.me === undefined ? meOwner : opts.me });
    const qc = wrapper.queryClient;
    qc.setQueryData(LIST_KEY, { pages: [{ items: ['запрос A'] }], pageParams: [undefined] });
    qc.getMutationCache().build(
      qc,
      { gcTime: Infinity },
      {
        context: undefined,
        data: undefined,
        error: null,
        failureCount: 0,
        failureReason: null,
        isPaused: false,
        status: 'success',
        variables: { comment: 'комментарий A' },
        submittedAt: 0,
      },
    );
    // рендер с me B, при котором в кэше ещё лежал список A, — утечка на кадр
    const leaks = { count: 0 };
    const view = renderHook(
      () => {
        const s = useSession();
        if (s.status === 'active' && s.me.id === meAdmin.id && qc.getQueryData(LIST_KEY))
          leaks.count++;
        return s;
      },
      { wrapper, reactStrictMode: opts.strict },
    );
    const mutations = () =>
      JSON.stringify(
        qc
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      );
    // me перечитан с ответом next; сразу после — до рендера с ним — проверка кэша
    const refetchMe = async (next: Me) => {
      mockMe(next);
      await act(() => qc.refetchQueries({ queryKey: ME_KEY, exact: true }));
    };
    return { qc, view, mutations, refetchMe, leaks };
  }

  test('active(A) → active(B): кэш запросов и мутаций A стёрт, ждущая разблокировка отклонена', async () => {
    const { qc, mutations, refetchMe } = seeded();
    const waiting = reauthBus.request();
    const rejected = waiting.then(
      () => false,
      () => true,
    );
    await refetchMe(meAdmin);
    expect(qc.getQueryData(LIST_KEY)).toBeUndefined();
    expect(mutations()).not.toContain('комментарий A');
    expect(reauthBus.pending()).toBe(false);
    expect(await rejected).toBe(true);
    expect(qc.getQueryData(ME_KEY)).toEqual(meAdmin);
  });

  test('то же в StrictMode (двойная подписка): стёрто один раз и до рендера с B', async () => {
    const { qc, view, refetchMe, leaks } = seeded({ strict: true });
    await refetchMe(meAdmin);
    expect(qc.getQueryData(LIST_KEY)).toBeUndefined();
    await waitFor(() => expect(view.result.current).toMatchObject({ me: meAdmin }));
    expect(leaks.count).toBe(0);
  });

  test('active(A) → mfa_pending (B ввёл пароль в другой вкладке): кэш и черновики вкладки стёрты', async () => {
    sessionStorage.setItem(LOGIN_DRAFT, JSON.stringify({ email: meOwner.email }));
    const { qc, mutations, refetchMe } = seeded();
    await refetchMe(meMfaPending);
    expect(qc.getQueryData(LIST_KEY)).toBeUndefined();
    expect(mutations()).not.toContain('комментарий A');
    // шаг 2 входа не покажет «Входите как <почта A>»
    expect(sessionStorage.getItem(LOGIN_DRAFT)).toBeNull();
  });

  test('тот же сотрудник (me перечитан: язык, права) — кэш и черновики целы', async () => {
    sessionStorage.setItem(LOGIN_DRAFT, JSON.stringify({ email: meOwner.email }));
    const { qc, mutations, refetchMe } = seeded();
    const waiting = reauthBus.request();
    await refetchMe({ ...meOwner, locale: 'en', reauth_required: true });
    expect(qc.getQueryData(LIST_KEY)).toBeDefined();
    expect(mutations()).toContain('комментарий A');
    expect(reauthBus.pending()).toBe(true);
    expect(sessionStorage.getItem(LOGIN_DRAFT)).not.toBeNull();
    reauthBus.resolve();
    await waiting;
  });

  // Повторное ревью (27.10), Important 1: смена сотрудника в другой вкладке видна при возврате в эту, а не
  // через staleTime me (60 с) — до тех пор B видел бы каркас A (меню, права, кэш, черновик). Возврат во
  // вкладку (visibilitychange → focusManager) перечитывает me даже свежим; скрытая вкладка me не трогает.
  test('возврат во вкладку: me перечитан сразу (свежий), данные A стёрты; скрытая вкладка — без запросов', async () => {
    const { qc, mutations, view } = seeded();
    let meCalls = 0;
    server.use(
      http.get('/v1/staff/me', ({ response }) => {
        meCalls++;
        return response(200).json(meAdmin);
      }),
    );
    try {
      act(() => focusManager.setFocused(false));
      expect(meCalls).toBe(0);
      act(() => focusManager.setFocused(true));
      await waitFor(() => expect(view.result.current).toMatchObject({ me: meAdmin }));
      expect(meCalls).toBe(1);
      expect(qc.getQueryData(LIST_KEY)).toBeUndefined();
      expect(mutations()).not.toContain('комментарий A');
    } finally {
      focusManager.setFocused(undefined);
    }
  });

  test('сессия A истекла, вход B в этой вкладке (401 → mfa_pending → active): почта шага 2 цела', async () => {
    const { view, qc } = seeded();
    mockMe(() => problem(401, 'auth.unauthenticated'));
    await act(() => qc.refetchQueries({ queryKey: ME_KEY, exact: true }));
    await waitFor(() => expect(view.result.current.status).toBe('anonymous'));
    // шаг 1 входа сохранил почту
    sessionStorage.setItem(LOGIN_DRAFT, JSON.stringify({ email: meAdmin.email }));
    mockMe(meMfaPending);
    await act(() => qc.refetchQueries({ queryKey: ME_KEY, exact: true }));
    expect(sessionStorage.getItem(LOGIN_DRAFT)).not.toBeNull();
    mockMe(meAdmin);
    await act(() => qc.refetchQueries({ queryKey: ME_KEY, exact: true }));
    expect(sessionStorage.getItem(LOGIN_DRAFT)).not.toBeNull();
  });
});

// 403 auth.forbidden (ревью ветки, M5): права сняли, пока вкладка открыта, — me перечитывается, и
// меню и разделы следуют новым правам, а не старым.
describe('403 auth.forbidden', () => {
  test('у запроса и у мутации → me перечитан', async () => {
    let meCalls = 0;
    server.use(
      http.get('/v1/staff/me', ({ response }) => {
        meCalls++;
        return response(200).json(meOwner);
      }),
      http.get('/v1/staff/accounts', ({ response }) =>
        response.untyped(problem(403, 'auth.forbidden')),
      ),
    );
    const wrapper = providers({ me: null });
    const { result } = renderHook(() => ({ s: useSession(), api: useApi() }), { wrapper });
    await waitFor(() => expect(result.current.s.status).toBe('active'));
    expect(meCalls).toBe(1);
    await wrapper.queryClient
      .fetchQuery({
        queryKey: ['staff', 'accounts'],
        queryFn: () => unwrap(result.current.api.GET('/v1/staff/accounts')),
      })
      .catch(() => {});
    await waitFor(() => expect(meCalls).toBe(2));

    // мутация с тем же ответом
    await wrapper.queryClient
      .getMutationCache()
      .build(wrapper.queryClient, {
        mutationFn: () => Promise.reject(new ApiError({ status: 403, code: 'auth.forbidden' })),
      })
      .execute(undefined)
      .catch(() => {});
    await waitFor(() => expect(meCalls).toBe(3));

    // другой 403 (не auth.forbidden) me не трогает
    server.use(
      http.get('/v1/staff/accounts', ({ response }) =>
        response.untyped(problem(403, 'auth.ip_not_allowed')),
      ),
    );
    await wrapper.queryClient
      .fetchQuery({
        queryKey: ['staff', 'accounts', 2],
        queryFn: () => unwrap(result.current.api.GET('/v1/staff/accounts')),
      })
      .catch(() => {});
    await act(async () => {});
    expect(meCalls).toBe(3);
  });
});
