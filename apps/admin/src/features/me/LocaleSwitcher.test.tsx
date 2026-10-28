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
  expect(screen.getByRole('button', { name: 'RU — Русский' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  expect(group).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: 'EN — English' }));

  await screen.findByRole('group', { name: 'Interface language' });
  expect(screen.getByRole('button', { name: 'EN — English' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  expect(document.documentElement.lang).toBe('en');
  expect(document.title).toBe('Administration');
  expect(calls).toHaveLength(1);
  expect(calls[0]?.body).toEqual({ locale: 'en' });
  expect(calls[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
  expect(calls[0]?.admin).toBe('1');
});

test('компактный сегмент «RU · EN»: на кнопке код языка, полное имя — в доступном имени и подсказке', () => {
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  const ru = screen.getByRole('button', { name: 'RU — Русский' });
  const en = screen.getByRole('button', { name: 'EN — English' });
  // видимый текст — короткий код (имя кнопки начинается с него: label in name, WCAG 2.5.3)
  expect(ru).toHaveTextContent(/^RU$/);
  expect(en).toHaveTextContent(/^EN$/);
  expect(ru).toHaveAttribute('title', 'Русский');
  expect(en).toHaveAttribute('title', 'English');
  expect(ru).toHaveAttribute('lang', 'ru');
  expect(en).toHaveAttribute('lang', 'en');
  // одна строка сегментов: обе кнопки — соседи в одном контейнере
  expect(ru.parentElement).toBe(en.parentElement);
});

test('пока язык сохраняется — сегменты заняты, повторное нажатие не уходит, фокус остаётся', async () => {
  let release = () => {};
  const gate = new Promise<void>((r) => (release = r));
  let calls = 0;
  server.use(
    http.patch('/v1/staff/me/locale', async ({ response }) => {
      calls += 1;
      await gate;
      return response(204).empty();
    }),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  const user = userEvent.setup();
  const en = screen.getByRole('button', { name: 'EN — English' });
  await user.click(en);
  await waitFor(() => expect(en).toHaveAttribute('aria-disabled', 'true'));
  expect(en).toHaveFocus();
  await user.click(en);
  expect(calls).toBe(1);
  release();
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'EN — English' })).toHaveAttribute(
      'aria-pressed',
      'true',
    ),
  );
  expect(calls).toBe(1);
});

test('сервер не сохранил язык — тексты прежние, ошибка показана', async () => {
  server.use(
    http.patch('/v1/staff/me/locale', ({ response }) => response.untyped(problem(503, 'internal'))),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  await userEvent.click(screen.getByRole('button', { name: 'EN — English' }));
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Что-то пошло не так. Попробуйте ещё раз'),
  );
  expect(screen.getByRole('button', { name: 'RU — Русский' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
});

test('код ошибки без текста в локалях — общий текст errors.internal', async () => {
  server.use(
    http.patch('/v1/staff/me/locale', ({ response }) =>
      response.untyped(problem(409, 'future.unknown_code')),
    ),
  );
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'ru' } });
  await userEvent.click(screen.getByRole('button', { name: 'EN — English' }));
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent('Что-то пошло не так. Попробуйте ещё раз'),
  );
});

test('текущий язык повторно не сохраняется', async () => {
  renderWithProviders(<LocaleSwitcher />, { me: { ...meOwner, locale: 'en' } });
  // PATCH не уходит: неописанный запрос роняет тест (request:unhandled → afterEach, src/test/setup.ts)
  await userEvent.click(screen.getByRole('button', { name: 'EN — English' }));
  expect(screen.getByRole('button', { name: 'EN — English' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
});
