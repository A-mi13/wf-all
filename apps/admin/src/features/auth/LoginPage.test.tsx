import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { meMfaPending, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { sectionsServer } from '../../test/sections';

// Экран входа (спека §9.4 п. 1, §4.6): шаг 1 — почта и пароль, шаг 2 — второй фактор. Стадию
// ведёт сессия: 200 staffLogin → me = mfa_pending → шаг 2; 204 staffVerifyMfa → me = active →
// ?return= или стартовая.

// стартовая и возврат приводят в разделы каркаса — их первые запросы описаны (src/test/sections.ts)
beforeEach(() => sectionsServer());

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.useRealTimers();
});

const page = (name: string) => screen.findByRole('heading', { level: 1, name });
const CODE = 'Код из приложения-аутентификатора';

type Seen = {
  login: { body: unknown; key: string | null }[];
  mfa: { body: unknown; key: string | null }[];
};

// Сервер входа: пароль «верный пароль 1» принят → новая сессия mfa_pending; код 492817 → active.
function loginServer(app: ReturnType<typeof renderApp>) {
  const seen: Seen = { login: [], mfa: [] };
  let n = 0;
  server.use(
    http.post('/v1/staff/auth/login', async ({ request, response }) => {
      const body = await request.json();
      seen.login.push({ body, key: request.headers.get('Idempotency-Key') });
      if (body.password !== 'верный пароль 1')
        return response.untyped(problem(400, 'staff.invalid_credentials'));
      // каждый вход — новая сессия (прежняя гасится)
      n++;
      app.me.current = {
        ...meMfaPending,
        session: {
          id: `0192f1d0-0000-7000-8000-00000000000${n}`,
          expires_at: '2026-10-13T17:00:00Z',
        },
      };
      return response(200).json({ stage: 'mfa_pending' });
    }),
    http.post('/v1/staff/auth/mfa', async ({ request, response }) => {
      const body = await request.json();
      seen.mfa.push({ body, key: request.headers.get('Idempotency-Key') });
      if (body.code !== '492817' && body.recovery_code !== 'k7m2q-x9p4t')
        return response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 4 }));
      app.me.current = meOwner;
      return response(204).empty();
    }),
  );
  return seen;
}

async function submitPassword(
  user: UserEvent,
  password = 'верный пароль 1',
  email = 'olga.v@wf.example',
) {
  await user.clear(screen.getByLabelText('Рабочая почта'));
  await user.type(screen.getByLabelText('Рабочая почта'), email);
  await user.type(screen.getByLabelText('Пароль'), password);
  await user.click(screen.getByRole('button', { name: 'Войти' }));
}

async function enterCode(user: UserEvent, code: string) {
  const group = await screen.findByRole('group', { name: CODE });
  await user.click(within(group).getAllByRole('textbox')[0]!);
  await user.keyboard(code);
}

describe('шаг 1 → шаг 2 → раздел', () => {
  test('почта и пароль → код → стартовая; анонимный вход без Idempotency-Key', async () => {
    const app = renderApp('/login');
    const seen = loginServer(app);
    const user = userEvent.setup();
    await page('Вход администратора');
    expect(
      screen.getByText(/Вход только с рабочей почты и со вторым фактором/),
    ).toBeInTheDocument();

    await submitPassword(user);
    await screen.findByRole('group', { name: CODE });
    expect(seen.login).toEqual([
      { body: { email: 'olga.v@wf.example', password: 'верный пароль 1' }, key: null },
    ]);

    await enterCode(user, '492817');
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
    expect(seen.mfa).toHaveLength(1);
    expect(seen.mfa[0]!.body).toEqual({ code: '492817' });
    expect(seen.mfa[0]!.key).toBeTruthy();
    // пароль не попал в черновик, почта — можно
    expect(storageDump()).not.toContain('верный пароль 1');
    expect(storageDump()).toContain('olga.v@wf.example');
    // пароль и код не остаются в кэше мутаций после шагов
    await waitFor(() => {
      const vars = JSON.stringify(
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      );
      expect(vars).not.toContain('верный пароль 1');
      expect(vars).not.toContain('492817');
    });
  });

  test('после входа — возврат на исходный адрес (?return=)', async () => {
    const app = renderApp('/staff?tab=sessions');
    loginServer(app);
    const user = userEvent.setup();
    await page('Вход администратора');
    expect(app.at()).toBe('/login?return=%2Fstaff%3Ftab%3Dsessions');
    await submitPassword(user);
    await enterCode(user, '492817');
    await page('Роли и доступ');
    expect(app.at()).toBe('/staff?tab=sessions');
  });

  test('«Другая почта или пароль» + неверный пароль — ошибка на шаге 1, шаг 2 прежней сессии не открывается', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    const seen = loginServer(app);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Другая почта или пароль' }));
    await submitPassword(user, 'не тот', 'ivan.p@wf.example');
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверная почта или пароль');
    expect(screen.queryByRole('group', { name: CODE })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Пароль')).toBeInTheDocument();
    expect(seen.login).toHaveLength(1);
  });

  test('«Другая почта или пароль» → вход другой почтой → шаг 2 новой сессии', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    loginServer(app);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Другая почта или пароль' }));
    await submitPassword(user, 'верный пароль 1', 'ivan.p@wf.example');
    expect(await screen.findByRole('group', { name: CODE })).toBeInTheDocument();
    expect(screen.getByText('Вход как ivan.p@wf.example')).toBeInTheDocument();
  });

  test('перезагрузка в mfa_pending открывает шаг 2', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    loginServer(app);
    expect(await screen.findByRole('group', { name: CODE })).toBeInTheDocument();
    expect(screen.queryByLabelText('Пароль')).not.toBeInTheDocument();
  });
});

describe('ошибки шага 1', () => {
  test('staff.invalid_credentials — общий текст под формой, не по полю', async () => {
    const app = renderApp('/login');
    loginServer(app);
    const user = userEvent.setup();
    await page('Вход администратора');
    await submitPassword(user, 'не тот');
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверная почта или пароль');
    expect(screen.getByLabelText('Рабочая почта')).not.toHaveAttribute('aria-invalid');
    expect(screen.getByLabelText('Пароль')).not.toHaveAttribute('aria-invalid');
  });

  test('429 — отсчёт по Retry-After, «Войти» недоступна до конца', async () => {
    renderApp('/login');
    let asked = 0;
    server.use(
      http.post('/v1/staff/auth/login', ({ response }) => {
        asked++;
        return response.untyped(problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '30' }));
      }),
    );
    await page('Вход администратора');
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fireEvent.change(screen.getByLabelText('Рабочая почта'), {
      target: { value: 'olga.v@wf.example' },
    });
    fireEvent.change(screen.getByLabelText('Пароль'), { target: { value: 'пароль' } });
    fireEvent.click(screen.getByRole('button', { name: 'Войти' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Слишком много запросов');
    const wait = await screen.findByRole('button', { name: /^Подождать 30 с$/ });
    expect(wait).toBeDisabled();
    // Enter в поле тоже не уходит, пока идёт отсчёт
    fireEvent.submit(wait.closest('form')!);
    // отправка формы асинхронна (валидация react-hook-form) — дать ей закончиться
    await act(async () => {});
    expect(screen.getByRole('alert')).toHaveTextContent('Слишком много запросов');
    act(() => vi.advanceTimersByTime(29_000));
    expect(screen.getByRole('button', { name: /^Подождать \d+ с$/ })).toBeDisabled();
    act(() => vi.advanceTimersByTime(1_500));
    const again = screen.getByRole('button', { name: 'Войти' });
    expect(again).toBeEnabled();
    // следующий запрос после отсчёта; дождались его ответа (снова 429) — Enter выше не ушёл
    fireEvent.click(again);
    expect(await screen.findByRole('button', { name: 'Подождать 30 с' })).toBeDisabled();
    expect(asked).toBe(2);
  });
});

describe('шаг 2', () => {
  test('staff.mfa_invalid — «Осталось N попыток», ячейки очищены, стёртый код не уходит', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    const seen = loginServer(app);
    const user = userEvent.setup();
    await enterCode(user, '111111');
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Код не подошёл. Осталось 4 попытки, потом доступ закроется на час',
    );
    const cells = within(screen.getByRole('group', { name: CODE })).getAllByRole('textbox');
    await waitFor(() => expect(cells.map((c) => (c as HTMLInputElement).value).join('')).toBe(''));
    // стёртый код не уходит повторно: «Войти» недоступна, клик — без запроса
    const submit = screen.getByRole('button', { name: 'Войти' });
    expect(submit).toBeDisabled();
    await user.click(submit);
    expect(seen.mfa).toHaveLength(1);
  });

  test('429 на шаге 2 — отсчёт, фокус в ячейках; после отсчёта текст снят, код снова уходит', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    let asked = 0;
    server.use(
      http.post('/v1/staff/auth/mfa', ({ response }) => {
        asked++;
        if (asked > 1) {
          app.me.current = meOwner;
          return response(204).empty();
        }
        return response.untyped(problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '30' }));
      }),
    );
    const group = await screen.findByRole('group', { name: CODE });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const paste = (code: string) =>
      fireEvent.paste(within(group).getAllByRole('textbox')[0]!, {
        clipboardData: { getData: () => code },
      });
    paste('492817');
    expect(await screen.findByRole('alert')).toHaveTextContent('Слишком много запросов');
    expect(screen.getByRole('button', { name: 'Подождать 30 с' })).toBeDisabled();
    // фокус не потерян на body — в первой ячейке
    await waitFor(() => expect(within(group).getAllByRole('textbox')[0]).toHaveFocus());
    act(() => vi.advanceTimersByTime(31_000));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    paste('492817');
    await page('Журнал действий');
    expect(asked).toBe(2);
  });

  // N1 (уборка №5): код, набранный во время отсчёта 429, не уходит сам и не остаётся в ячейках —
  // иначе после отсчёта «Войти» отправила бы устаревший код и сожгла попытку.
  test('код, набранный во время отсчёта 429, по концу отсчёта стёрт и не уходит', async () => {
    renderApp('/login', { me: meMfaPending });
    let asked = 0;
    server.use(
      http.post('/v1/staff/auth/mfa', ({ response }) => {
        asked++;
        return response.untyped(problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '30' }));
      }),
    );
    const group = await screen.findByRole('group', { name: CODE });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const paste = (code: string) =>
      fireEvent.paste(within(group).getAllByRole('textbox')[0]!, {
        clipboardData: { getData: () => code },
      });
    const digits = () =>
      within(group)
        .getAllByRole('textbox')
        .map((c) => (c as HTMLInputElement).value)
        .join('');
    paste('492817');
    expect(await screen.findByRole('button', { name: 'Подождать 30 с' })).toBeDisabled();
    // во время отсчёта набран новый код: он не уходит, но виден в ячейках
    paste('135790');
    expect(digits()).toBe('135790');
    expect(asked).toBe(1);
    act(() => vi.advanceTimersByTime(31_000));
    // отсчёт кончился: ячейки пусты, «Войти» недоступна — устаревший код не уйдёт
    await waitFor(() => expect(digits()).toBe(''));
    const submit = screen.getByRole('button', { name: 'Войти' });
    expect(submit).toBeDisabled();
    fireEvent.click(submit);
    expect(asked).toBe(1);
  });

  test('attempts_left = 0 — доступ закрыт на час, ввод недоступен, «Ко входу» → шаг 1', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    server.use(
      http.post('/v1/staff/auth/mfa', ({ response }) => {
        app.me.current = null; // пятая неудача гасит все сессии сотрудника
        return response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 0 }));
      }),
    );
    const user = userEvent.setup();
    await enterCode(user, '111111');
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('доступ закрыт на час, мы сообщили администраторам');
    for (const cell of within(screen.getByRole('group', { name: CODE })).getAllByRole('textbox'))
      expect(cell).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Использовать резервный код' })).toBeDisabled();
    // фокус — на единственном действии
    await waitFor(() => expect(screen.getByRole('button', { name: 'Ко входу' })).toHaveFocus());

    await user.click(screen.getByRole('button', { name: 'Ко входу' }));
    expect(await screen.findByLabelText('Пароль')).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: CODE })).not.toBeInTheDocument();
  });

  test('резервный код — поле вместо ячеек, уходит recovery_code', async () => {
    const app = renderApp('/login', { me: meMfaPending });
    const seen = loginServer(app);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Использовать резервный код' }));
    expect(screen.queryByRole('group', { name: CODE })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Резервный код'), 'k7m2q-x9p4t');
    await user.click(screen.getByRole('button', { name: 'Войти' }));
    await page('Журнал действий');
    expect(seen.mfa.map((m) => m.body)).toEqual([{ recovery_code: 'k7m2q-x9p4t' }]);
    expect(storageDump()).not.toContain('k7m2q-x9p4t');
  });

  test('5 минут на шаг 2 истекли (401 auth.unauthenticated) — шаг 1 с понятным текстом', async () => {
    const app = renderApp('/login?return=%2Faudit', { me: meMfaPending });
    server.use(
      http.post('/v1/staff/auth/mfa', ({ response }) => {
        app.me.current = null;
        return response.untyped(problem(401, 'auth.unauthenticated'));
      }),
    );
    const user = userEvent.setup();
    await enterCode(user, '492817');
    expect(await screen.findByLabelText('Пароль')).toBeInTheDocument();
    expect(screen.getByText(/Время на код из приложения вышло/)).toBeInTheDocument();
    expect(app.at()).toBe('/login?return=%2Faudit');
  });
});
