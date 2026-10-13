import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { useApi } from '../api/context';
import { CITY_OTHER, CITY_OWN, meModerator, meMfaPending, meOwner, meSetup } from '../test/me';
import { problem } from '../test/problem';
import { providers } from '../test/render';
import { http, server } from '../test/server';
import type { Me } from './permissions';
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
