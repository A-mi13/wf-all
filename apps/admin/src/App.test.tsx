import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { App } from './App';
import { createApi } from './api/client';
import { createQueryClient } from './app/query';
import { problem } from './test/problem';
import { http, server, TEST_API } from './test/server';

// R13: связь с admin-api проверяет GET /v1/staff/me (не /v1/health); без сессии — экран входа.
test('App запрашивает me и без сессии показывает вход', async () => {
  let asked = 0;
  server.use(
    http.get('/v1/staff/me', ({ response }) => {
      asked++;
      return response.untyped(problem(401, 'auth.unauthenticated'));
    }),
  );
  render(<App api={createApi(TEST_API)} queryClient={createQueryClient({ retry: false })} />);
  expect(await screen.findByRole('heading', { level: 1, name: 'Вход' })).toBeInTheDocument();
  expect(asked).toBeGreaterThan(0);
  expect(window.location.pathname).toBe('/login');
});
