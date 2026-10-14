import { act, fireEvent, screen } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { meSetup } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';

// Принятие приглашения (спека §9.4 п. 2, §4.3–4.4): почта (не из адреса), пароль дважды, код из
// письма; «Отправить код ещё раз» — всегда 202, отсчёт 60 с. Успех → стадия setup → /setup-2fa.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.useRealTimers();
});

const page = (name: string) => screen.findByRole('heading', { level: 1, name });

async function fill(
  user: UserEvent,
  v: { password?: string; repeat?: string; code?: string } = {},
) {
  await user.type(screen.getByLabelText('Рабочая почта'), 'olga.v@wf.example');
  await user.type(screen.getByLabelText('Новый пароль'), v.password ?? 'Жёлтая карточка на 90-й');
  await user.type(
    screen.getByLabelText('Пароль ещё раз'),
    v.repeat ?? v.password ?? 'Жёлтая карточка на 90-й',
  );
  await user.type(screen.getByLabelText('Код из письма'), v.code ?? '305718');
}

const submit = (user: UserEvent) =>
  user.click(screen.getByRole('button', { name: 'Принять приглашение' }));

test('почта не читается из адреса', async () => {
  renderApp('/accept?email=evil%40x.example');
  await page('Принятие приглашения');
  expect(screen.getByLabelText('Рабочая почта')).toHaveValue('');
});

test('пароли не совпали — ошибка поля на клиенте, запроса нет', async () => {
  renderApp('/accept');
  let asked = 0;
  server.use(
    http.post('/v1/staff/auth/invite/accept', ({ response }) => {
      asked++;
      return response(200).json({ stage: 'setup' });
    }),
  );
  const user = userEvent.setup();
  await page('Принятие приглашения');
  await fill(user, { repeat: 'другой пароль 123' });
  await submit(user);
  expect(await screen.findByText('Не совпадает')).toBeInTheDocument();
  expect(screen.getByLabelText('Пароль ещё раз')).toHaveAttribute('aria-invalid', 'true');
  expect(asked).toBe(0);
});

test('validation.failed по body.password — ошибка у поля пароля', async () => {
  renderApp('/accept');
  server.use(
    http.post('/v1/staff/auth/invite/accept', ({ response }) =>
      response.untyped(
        problem(400, 'validation.failed', { errors: [{ field: 'body.password', code: 'common' }] }),
      ),
    ),
  );
  const user = userEvent.setup();
  await page('Принятие приглашения');
  await fill(user, { password: '1234567890' });
  await submit(user);
  expect(
    await screen.findByText('Этот пароль слишком распространён — придумайте другой'),
  ).toBeInTheDocument();
  expect(screen.getByLabelText('Новый пароль')).toHaveAttribute('aria-invalid', 'true');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('staff.invite_invalid — общий текст; анонимный запрос без Idempotency-Key', async () => {
  renderApp('/accept');
  const seen: { body: unknown; key: string | null }[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/accept', async ({ request, response }) => {
      seen.push({ body: await request.json(), key: request.headers.get('Idempotency-Key') });
      return response.untyped(problem(400, 'staff.invite_invalid'));
    }),
  );
  const user = userEvent.setup();
  await page('Принятие приглашения');
  await fill(user);
  await submit(user);
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Код приглашения не подошёл или истёк',
  );
  expect(seen).toEqual([
    {
      body: { email: 'olga.v@wf.example', code: '305718', password: 'Жёлтая карточка на 90-й' },
      key: null,
    },
  ]);
});

test('«Отправить код ещё раз» — 202, текст и отсчёт 60 с', async () => {
  renderApp('/accept');
  const sent: unknown[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/resend', async ({ request, response }) => {
      sent.push(await request.json());
      return response(202).empty();
    }),
  );
  await page('Принятие приглашения');
  // без почты — ошибка поля, запроса нет
  vi.useFakeTimers({ shouldAdvanceTime: true });
  fireEvent.click(screen.getByRole('button', { name: 'Отправить код ещё раз' }));
  expect(await screen.findByText('Заполните поле')).toBeInTheDocument();
  expect(sent).toEqual([]);

  fireEvent.change(screen.getByLabelText('Рабочая почта'), {
    target: { value: 'olga.v@wf.example' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Отправить код ещё раз' }));
  expect(await screen.findByText(/Если приглашение на эту почту живо/)).toBeInTheDocument();
  expect(sent).toEqual([{ email: 'olga.v@wf.example' }]);
  expect(screen.getByRole('button', { name: 'Отправить ещё раз через 60 с' })).toBeDisabled();
  act(() => vi.advanceTimersByTime(59_000));
  expect(screen.getByRole('button', { name: /^Отправить ещё раз через \d+ с$/ })).toBeDisabled();
  act(() => vi.advanceTimersByTime(1_500));
  const again = screen.getByRole('button', { name: 'Отправить код ещё раз' });
  expect(again).toBeEnabled();
  // повтор — снова отсчёт с 60 с
  fireEvent.click(again);
  expect(
    await screen.findByRole('button', { name: 'Отправить ещё раз через 60 с' }),
  ).toBeDisabled();
  expect(sent).toHaveLength(2);
});

test('успех → стадия setup → /setup-2fa; пароль и код не в черновике', async () => {
  const app = renderApp('/accept');
  server.use(
    http.post('/v1/staff/auth/invite/accept', ({ response }) => {
      app.me.current = meSetup;
      return response(200).json({ stage: 'setup' });
    }),
    http.post('/v1/staff/me/mfa/setup', ({ response }) =>
      response(200).json({
        secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
        otpauth_uri: 'otpauth://totp/WF:olga?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
      }),
    ),
  );
  const user = userEvent.setup();
  await page('Принятие приглашения');
  await fill(user);
  await submit(user);
  await page('Настройка двухфакторной защиты');
  expect(app.at()).toBe('/setup-2fa');
  expect(storageDump()).not.toContain('Жёлтая карточка');
  expect(storageDump()).not.toContain('305718');
});
