import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { meSetup } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';

// Принятие приглашения (спека §9.4 п. 2, §4.3–4.4): почта (не из адреса), пароль дважды, код из
// письма (xxxxx-xxxxx, решение 14.10.2026); «Прислать код ещё раз» — всегда 202, тот же код, отсчёт
// 60 с. Успех → стадия setup → /setup-2fa.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  localStorage.clear(); // wf.locale — выбор языка устройства
  vi.useRealTimers();
  vi.restoreAllMocks(); // navigator.language
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
  await user.type(screen.getByLabelText('Код из письма'), v.code ?? 'k7m2p-x4q6d');
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
      body: {
        email: 'olga.v@wf.example',
        code: 'k7m2p-x4q6d',
        password: 'Жёлтая карточка на 90-й',
        locale: 'ru',
      },
      key: null,
    },
  ]);
});

test('«Прислать код ещё раз» — 202, текст про тот же код и отсчёт 60 с', async () => {
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
  fireEvent.click(screen.getByRole('button', { name: 'Прислать код ещё раз' }));
  expect(await screen.findByText('Заполните поле')).toBeInTheDocument();
  expect(sent).toEqual([]);

  fireEvent.change(screen.getByLabelText('Рабочая почта'), {
    target: { value: 'olga.v@wf.example' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Прислать код ещё раз' }));
  expect(await screen.findByRole('status')).toHaveTextContent(
    'Если приглашение на эту почту живо, письмо с тем же кодом уже в пути',
  );
  expect(sent).toEqual([{ email: 'olga.v@wf.example' }]);
  expect(screen.getByRole('button', { name: 'Прислать ещё раз через 60 с' })).toBeDisabled();
  act(() => vi.advanceTimersByTime(59_000));
  expect(screen.getByRole('button', { name: /^Прислать ещё раз через \d+ с$/ })).toBeDisabled();
  act(() => vi.advanceTimersByTime(1_500));
  const again = screen.getByRole('button', { name: 'Прислать код ещё раз' });
  expect(again).toBeEnabled();
  // повтор — снова отсчёт с 60 с
  fireEvent.click(again);
  expect(await screen.findByRole('button', { name: 'Прислать ещё раз через 60 с' })).toBeDisabled();
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
  expect(storageDump()).not.toContain('k7m2p-x4q6d');
  // пароль и код не остаются в кэше мутаций после экрана
  await waitFor(() => {
    const vars = JSON.stringify(
      app.queryClient
        .getMutationCache()
        .getAll()
        .map((m) => m.state.variables),
    );
    expect(vars).not.toContain('Жёлтая карточка');
    expect(vars).not.toContain('k7m2p-x4q6d');
  });
});

test('повторная отправка — 429: текст и отсчёт; по концу отсчёта текст снят', async () => {
  renderApp('/accept');
  server.use(
    http.post('/v1/staff/auth/invite/resend', ({ response }) =>
      response.untyped(problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '5' })),
    ),
  );
  await page('Принятие приглашения');
  vi.useFakeTimers({ shouldAdvanceTime: true });
  fireEvent.change(screen.getByLabelText('Рабочая почта'), {
    target: { value: 'olga.v@wf.example' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Прислать код ещё раз' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Слишком много запросов');
  expect(screen.getByRole('button', { name: 'Прислать ещё раз через 5 с' })).toBeDisabled();
  act(() => vi.advanceTimersByTime(6_000));
  expect(screen.getByRole('button', { name: 'Прислать код ещё раз' })).toBeEnabled();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('поле кода — текст формата xxxxx-xxxxx: не цифровая клавиатура, без автозамены; уходит как введён', async () => {
  renderApp('/accept');
  const seen: unknown[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/accept', async ({ request, response }) => {
      seen.push(await request.json());
      return response.untyped(problem(400, 'staff.invite_invalid'));
    }),
  );
  const user = userEvent.setup();
  await page('Принятие приглашения');
  const code = screen.getByLabelText('Код из письма');
  expect(code).toHaveAttribute('type', 'text');
  expect(code).not.toHaveAttribute('inputmode', 'numeric');
  expect(code).toHaveAttribute('autocomplete', 'one-time-code');
  expect(code).toHaveAttribute('autocapitalize', 'none');
  expect(code).toHaveAttribute('spellcheck', 'false');
  expect(code).toHaveAccessibleDescription(/xxxxx-xxxxx/);
  // заглавными и с пробелом — как в письме на телефоне; нормализует сервер
  await fill(user, { code: 'K7M2P x4q6d' });
  await submit(user);
  await screen.findByRole('alert');
  expect(seen).toEqual([
    {
      email: 'olga.v@wf.example',
      code: 'K7M2P x4q6d',
      password: 'Жёлтая карточка на 90-й',
      locale: 'ru',
    },
  ]);
});

// Язык с экрана приглашения (решение пользователя 10.11): выбор устройства до входа уходит в теле, сервер
// сохраняет его в учётку — me стадии setup отвечает им же, и /setup-2fa открывается на выбранном языке.
test('EN выбран до принятия → язык в теле → /setup-2fa на EN', async () => {
  const app = renderApp('/accept');
  const seen: unknown[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/accept', async ({ request, response }) => {
      const body = await request.json();
      seen.push(body);
      // как сервер: язык из тела — в учётку, нет поля — прежний (ru)
      app.me.current = { ...meSetup, locale: body.locale ?? meSetup.locale };
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
  await user.click(screen.getByRole('button', { name: 'EN — English' }));
  await page('Accept invitation');
  await user.type(screen.getByLabelText('Work email'), 'olga.v@wf.example');
  await user.type(screen.getByLabelText('New password'), 'Жёлтая карточка на 90-й');
  await user.type(screen.getByLabelText('Repeat password'), 'Жёлтая карточка на 90-й');
  await user.type(screen.getByLabelText('Code from the email'), 'k7m2p-x4q6d');
  await user.click(screen.getByRole('button', { name: 'Accept invitation' }));
  await page('Two-factor setup');
  expect(app.at()).toBe('/setup-2fa');
  expect(seen).toEqual([expect.objectContaining({ locale: 'en' })]);
  expect(document.documentElement.lang).toBe('en');
  expect(screen.queryByText('Настройка двухфакторной защиты')).not.toBeInTheDocument();
});

// Раунд 2 IL: «что видишь, то и сохраняется» — уходит язык, на котором показан экран (выбор устройства или язык
// браузера), всегда. Браузер EN, ничего не выбирал → экран на EN → в учётку en.
test('браузер EN без выбора: экран на EN — в теле locale=en', async () => {
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB');
  renderApp('/accept');
  const seen: unknown[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/accept', async ({ request, response }) => {
      seen.push(await request.json());
      return response.untyped(problem(400, 'staff.invite_invalid'));
    }),
  );
  const user = userEvent.setup();
  await page('Accept invitation');
  await user.type(screen.getByLabelText('Work email'), 'olga.v@wf.example');
  await user.type(screen.getByLabelText('New password'), 'Жёлтая карточка на 90-й');
  await user.type(screen.getByLabelText('Repeat password'), 'Жёлтая карточка на 90-й');
  await user.type(screen.getByLabelText('Code from the email'), 'k7m2p-x4q6d');
  await user.click(screen.getByRole('button', { name: 'Accept invitation' }));
  await screen.findByRole('alert');
  expect(seen).toEqual([expect.objectContaining({ locale: 'en' })]);
});

// Браузер EN, на экране выбран RU (например, сброс доступа — сотрудник выбирает язык сам) → уходит ru.
test('браузер EN, клик RU на экране — уходит ru', async () => {
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB');
  renderApp('/accept');
  const seen: unknown[] = [];
  server.use(
    http.post('/v1/staff/auth/invite/accept', async ({ request, response }) => {
      seen.push(await request.json());
      return response.untyped(problem(400, 'staff.invite_invalid'));
    }),
  );
  const user = userEvent.setup();
  await page('Accept invitation');
  await user.click(screen.getByRole('button', { name: 'RU — Русский' }));
  await page('Принятие приглашения');
  await fill(user);
  await submit(user);
  await screen.findByRole('alert');
  expect(seen).toEqual([expect.objectContaining({ locale: 'ru' })]);
});
