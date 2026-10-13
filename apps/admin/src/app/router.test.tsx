import { createMemoryHistory } from '@tanstack/react-router';
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { createApi } from '../api/client';
import { meModerator, meMfaPending, meOwner, meSetup } from '../test/me';
import { problem } from '../test/problem';
import { http, server, TEST_API } from '../test/server';
import type { Me } from './permissions';
import { AppProviders } from './providers';
import { createQueryClient } from './query';
import { reauthBus } from './reauth';
import { AppRouter } from './router';

afterEach(() => reauthBus.reject());

// me отдаёт текущее значение state.me; null — 401 auth.unauthenticated.
function mount(path: string, me: Me | null) {
  const state = { me };
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      state.me
        ? response(200).json(state.me)
        : response.untyped(problem(401, 'auth.unauthenticated')),
    ),
  );
  const history = createMemoryHistory({ initialEntries: [path] });
  const queryClient = createQueryClient({ retry: false });
  render(
    <AppProviders api={createApi(TEST_API)} queryClient={queryClient}>
      <AppRouter history={history} />
    </AppProviders>,
  );
  const at = () => history.location.pathname + history.location.search;
  const login = async (next: Me) => {
    state.me = next;
    await act(() => queryClient.refetchQueries({ queryKey: ['staff', 'me'] }));
  };
  return { at, login };
}

const page = (name: string) => screen.findByRole('heading', { level: 1, name });

describe('без сессии', () => {
  test('защищённый раздел → /login?return=…, после входа — возврат на исходный адрес', async () => {
    const app = mount('/staff?tab=sessions', null);
    await page('Вход');
    expect(app.at()).toBe('/login?return=%2Fstaff%3Ftab%3Dsessions');

    await app.login(meOwner);
    await page('Роли и доступ');
    expect(app.at()).toBe('/staff?tab=sessions');
  });

  test('/accept открыт без сессии', async () => {
    const app = mount('/accept', null);
    await page('Принятие приглашения');
    expect(app.at()).toBe('/accept');
  });

  test('корень → вход без return', async () => {
    const app = mount('/', null);
    await page('Вход');
    expect(app.at()).toBe('/login');
  });

  test('чужой адрес в return игнорируется — стартовая страница', async () => {
    const app = mount('/login?return=%2F%2Fevil.example%2Fx', null);
    await page('Вход');
    await app.login(meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });
});

describe('стартовая страница — первый разрешённый раздел меню', () => {
  test('с журналом — журнал', async () => {
    const app = mount('/', meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('без права на журнал — следующий раздел', async () => {
    const app = mount('/', { ...meModerator, permissions: { 'staff.roles.read': 'allow' } });
    await page('Роли и доступ');
    expect(app.at()).toBe('/staff');
  });

  test('scoped-право открывает раздел (модератор и журнал своих городов)', async () => {
    const app = mount('/', meModerator);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('вход без return — стартовая', async () => {
    const app = mount('/login', meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });
});

describe('стадии', () => {
  test('setup — только /setup-2fa', async () => {
    const app = mount('/staff', meSetup);
    await page('Настройка двухфакторной защиты');
    expect(app.at()).toBe('/setup-2fa');
  });

  test('setup — /login и /accept тоже ведут на /setup-2fa', async () => {
    const app = mount('/login', meSetup);
    await page('Настройка двухфакторной защиты');
    expect(app.at()).toBe('/setup-2fa');
  });

  test('после настройки 2FA — стартовая', async () => {
    const app = mount('/setup-2fa', meSetup);
    await page('Настройка двухфакторной защиты');
    await app.login(meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('без сессии /setup-2fa — вход', async () => {
    const app = mount('/setup-2fa', null);
    await page('Вход');
    expect(app.at()).toBe('/login');
  });

  test('mfa_pending — шаг 2 входа, возврат сохраняется', async () => {
    const app = mount('/audit', meMfaPending);
    await page('Вход');
    expect(app.at()).toBe('/login?return=%2Faudit');
  });
});

describe('права разделов', () => {
  test('прямой адрес без права — заглушка ForbiddenState, без редиректа', async () => {
    const app = mount('/audit', { ...meOwner, permissions: { 'staff.roles.read': 'allow' } });
    await page('Нет доступа к разделу');
    expect(app.at()).toBe('/audit');
  });

  test('меню — только разрешённые разделы', async () => {
    mount('/me', { ...meOwner, permissions: { 'staff.roles.read': 'allow' } });
    await page('Мой доступ');
    const nav = screen.getByRole('navigation');
    expect(nav).toHaveTextContent('Роли и доступ');
    expect(nav).toHaveTextContent('Подтверждения');
    expect(nav).not.toHaveTextContent('Журнал действий');
  });

  test('неизвестный адрес — «не найдено» из локалей', async () => {
    mount('/nope', meOwner);
    await page('Страница не найдена');
  });
});

test('нет связи с admin-api — экран с повтором', async () => {
  let up = false;
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      up ? response(200).json(meOwner) : response.untyped(problem(503, 'internal')),
    ),
  );
  render(
    <AppProviders api={createApi(TEST_API)} queryClient={createQueryClient({ retry: false })}>
      <AppRouter history={createMemoryHistory({ initialEntries: ['/audit'] })} />
    </AppProviders>,
  );
  const retry = await screen.findByRole('button', { name: 'Повторить' });
  expect(screen.getByRole('alert')).toHaveTextContent('Нет связи с сервером');
  up = true;
  act(() => retry.click());
  await waitFor(() =>
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Журнал действий'),
  );
});
