import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test } from 'vitest';
import { App } from './App';
import { createApi } from './api/client';
import { createQueryClient } from './app/query';
import { reauthBus } from './app/reauth';
import { meOwner } from './test/me';
import { problem } from './test/problem';
import { http, server, TEST_API } from './test/server';
import { sectionsServer } from './test/sections';

// стартовая и возврат приводят в разделы каркаса — их первые запросы описаны (src/test/sections.ts)
beforeEach(() => sectionsServer());

afterEach(() => {
  reauthBus.reject();
  window.history.replaceState(null, '', '/');
});

const mount = () =>
  render(<App api={createApi(TEST_API)} queryClient={createQueryClient({ retry: false })} />);

// R13: связь с admin-api проверяет GET /v1/staff/me (не /v1/health); без сессии — экран входа.
test('App запрашивает me и без сессии показывает экран входа', async () => {
  let asked = 0;
  server.use(
    http.get('/v1/staff/me', ({ response }) => {
      asked++;
      return response.untyped(problem(401, 'auth.unauthenticated'));
    }),
  );
  mount();
  expect(
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText('Рабочая почта')).toBeInTheDocument();
  expect(screen.getByLabelText('Пароль')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Войти' })).toBeInTheDocument();
  expect(asked).toBeGreaterThan(0);
  expect(window.location.pathname).toBe('/login');
});

// P20: me.reauth_required после перезагрузки — окно разблокировки сразу, поверх раздела; после
// unlock me перечитан, окно закрыто.
test('me.reauth_required — окно разблокировки поверх раздела; unlock закрывает его', async () => {
  let locked = true;
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      response(200).json({ ...meOwner, reauth_required: locked }),
    ),
    http.post('/v1/staff/auth/unlock', ({ response }) => {
      locked = false;
      return response(204).empty();
    }),
  );
  window.history.replaceState(null, '', '/staff');
  const user = userEvent.setup();
  mount();
  const dialog = await screen.findByRole('dialog', { name: 'Подтвердите вход' });
  expect(screen.getByRole('heading', { level: 1, name: 'Роли и доступ' })).toBeInTheDocument();
  const group = within(dialog).getByRole('group', { name: 'Код из приложения-аутентификатора' });
  await user.click(within(group).getAllByRole('textbox')[0]!);
  await user.keyboard('492817');
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.getByRole('heading', { level: 1, name: 'Роли и доступ' })).toBeInTheDocument();
  expect(window.location.pathname).toBe('/staff');
});
