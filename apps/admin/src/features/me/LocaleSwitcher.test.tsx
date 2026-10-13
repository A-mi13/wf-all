import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { LocaleSwitcher } from './LocaleSwitcher';

test('выбор языка сохраняется через updateStaffMeLocale и меняет тексты без перезагрузки', async () => {
  const calls: { body: unknown; key: string | null; admin: string | null }[] = [];
  server.use(
    http.patch('/v1/staff/me/locale', async ({ request, response }) => {
      calls.push({
        body: await request.json(),
        key: request.headers.get('Idempotency-Key'),
        admin: request.headers.get('X-WF-Admin'),
      });
      return response(204).empty();
    }),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });

  const group = screen.getByRole('group', { name: 'Язык интерфейса' });
  expect(screen.getByRole('button', { name: 'Русский' })).toHaveAttribute('aria-pressed', 'true');
  expect(group).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: 'English' }));

  await screen.findByRole('group', { name: 'Interface language' });
  expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'true');
  expect(document.documentElement.lang).toBe('en');
  expect(document.title).toBe('Administration');
  expect(calls).toHaveLength(1);
  expect(calls[0]?.body).toEqual({ locale: 'en' });
  expect(calls[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
  expect(calls[0]?.admin).toBe('1');
});

test('сервер не сохранил язык — тексты прежние, ошибка показана', async () => {
  server.use(
    http.patch('/v1/staff/me/locale', ({ response }) => response.untyped(problem(503, 'internal'))),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  await userEvent.click(screen.getByRole('button', { name: 'English' }));
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Что-то пошло не так. Попробуйте ещё раз'),
  );
  expect(screen.getByRole('button', { name: 'Русский' })).toHaveAttribute('aria-pressed', 'true');
});

test('код ошибки без текста в локалях — общий текст errors.internal', async () => {
  server.use(
    http.patch('/v1/staff/me/locale', ({ response }) =>
      response.untyped(problem(409, 'future.unknown_code')),
    ),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  await userEvent.click(screen.getByRole('button', { name: 'English' }));
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Что-то пошло не так. Попробуйте ещё раз'),
  );
});

test('текущий язык повторно не сохраняется', async () => {
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'en' } });
  // неописанный запрос уронил бы тест (onUnhandledRequest: error)
  await userEvent.click(screen.getByRole('button', { name: 'English' }));
  expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'true');
});
