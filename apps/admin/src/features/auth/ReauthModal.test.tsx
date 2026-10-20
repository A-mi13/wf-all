import { useQuery } from '@tanstack/react-query';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { useState } from 'react';
import { afterEach, describe, expect, test } from 'vitest';
import { background, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { reauthBus } from '../../app/reauth';
import { useSession } from '../../app/session';
import { renderApp } from '../../test/app';
import { meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { ReauthModal } from './ReauthModal';

// Окно разблокировки (спека §9.3): 401 auth.reauth_required у любого запроса — модалка поверх
// страницы, форма под ней цела; unlock → исходный запрос повторяется (тем же ключом). Закрыть без
// разблокировки нельзя; блокировка — экран входа; «Выйти» — выход каркаса.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const CODE = 'Код из приложения-аутентификатора';
const dialog = () => screen.findByRole('dialog', { name: 'Подтвердите вход' });

async function enterCode(user: UserEvent, code: string) {
  const group = within(await dialog()).getByRole('group', { name: CODE });
  await user.click(within(group).getAllByRole('textbox')[0]!);
  await user.keyboard(code);
}

// Экран с формой: «Сохранить» меняет язык; статус сессии — для проверки выхода на вход.
function Probe() {
  const api = useApi();
  const session = useSession();
  const [text, setText] = useState('');
  const save = useAdminMutation((_: void, key: string) =>
    api.PATCH('/v1/staff/me/locale', {
      params: { header: { 'Idempotency-Key': key } },
      body: { locale: 'en' },
    }),
  );
  return (
    <main>
      <p>сессия: {session.status}</p>
      <label>
        Заметка
        <input value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      <button type="button" onClick={() => save.mutate()}>
        Сохранить
      </button>
      {save.isSuccess && <p>Сохранено</p>}
    </main>
  );
}

// Сервер: первый PATCH — 401 reauth_required, после unlock 492817 — 204.
function lockedServer() {
  const ctl = { unlocked: false, patches: [] as (string | null)[], unlocks: 0 };
  server.use(
    http.get('/v1/staff/me', ({ response }) => response(200).json(meOwner)),
    http.patch('/v1/staff/me/locale', ({ request, response }) => {
      ctl.patches.push(request.headers.get('Idempotency-Key'));
      return ctl.unlocked
        ? response(204).empty()
        : response.untyped(problem(401, 'auth.reauth_required'));
    }),
    http.post('/v1/staff/auth/unlock', async ({ request, response }) => {
      ctl.unlocks++;
      const body = await request.json();
      if (body.code !== '492817')
        return response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 2 }));
      ctl.unlocked = true;
      return response(204).empty();
    }),
  );
  return ctl;
}

const mount = () =>
  renderWithProviders(
    <>
      <Probe />
      <ReauthModal />
    </>,
  );

describe('разблокировка', () => {
  test('reauth_required посреди работы → окно → unlock → запрос повторён, набранное цело', async () => {
    const ctl = lockedServer();
    const user = userEvent.setup();
    mount();
    await user.type(screen.getByLabelText('Заметка'), 'черновик решения');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await dialog();
    // форма под окном на месте
    expect(screen.getByLabelText('Заметка')).toHaveValue('черновик решения');

    await enterCode(user, '111111');
    expect(await within(await dialog()).findByRole('alert')).toHaveTextContent(
      'Код не подошёл. Осталось 2 попытки',
    );
    await enterCode(user, '492817');
    expect(await screen.findByText('Сохранено')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Заметка')).toHaveValue('черновик решения');
    // исходный запрос повторён тем же ключом идемпотентности
    expect(ctl.patches).toHaveLength(2);
    expect(ctl.patches[1]).toBe(ctl.patches[0]);
    expect(ctl.unlocks).toBe(2);
  });

  test('фоновый опрос (X-WF-Background) тоже открывает окно', async () => {
    let unlocked = false;
    server.use(
      http.get('/v1/staff/me', ({ response }) => response(200).json(meOwner)),
      http.get('/v1/approvals', ({ request, response }) => {
        expect(request.headers.get('X-WF-Background')).toBe('1');
        return unlocked
          ? response(200).json({ items: [], next_cursor: null })
          : response.untyped(problem(401, 'auth.reauth_required'));
      }),
      http.post('/v1/staff/auth/unlock', ({ response }) => {
        unlocked = true;
        return response(204).empty();
      }),
    );
    function Poll() {
      const api = useApi();
      const q = useQuery({
        queryKey: ['approvals', 'count'],
        queryFn: () =>
          unwrap(
            api.GET('/v1/approvals', {
              params: { query: { box: 'incoming', status: 'pending' } },
              ...background(),
            }),
          ),
      });
      return <p>{q.isSuccess ? 'счётчик обновлён' : 'ждём'}</p>;
    }
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Poll />
        <ReauthModal />
      </>,
    );
    await dialog();
    await enterCode(user, '492817');
    expect(await screen.findByText('счётчик обновлён')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // Уборка №6: ячейки кода на время запроса были disabled — после автоотправки шестой цифры фокус
  // падал на body (читалка теряла окно).
  test('пока unlock идёт, фокус остаётся в ячейке кода, а не на body', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    lockedServer();
    server.use(
      http.post('/v1/staff/auth/unlock', async ({ response }) => {
        await gate;
        return response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 3 }));
      }),
    );
    const user = userEvent.setup();
    mount();
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    const d = await dialog();
    await enterCode(user, '492817');
    const group = within(d).getByRole('group', { name: CODE });
    await waitFor(() => expect(group).toHaveAttribute('aria-busy', 'true'));
    // jsdom не снимает фокус с disabled-поля сам — включённость ячеек проверяется явно (ревью M4)
    for (const cell of within(group).getAllByRole('textbox')) expect(cell).toBeEnabled();
    expect(document.activeElement).not.toBe(document.body);
    expect(group.contains(document.activeElement)).toBe(true);
    release();
    expect(await within(d).findByRole('alert')).toHaveTextContent('Осталось 3 попытки');
  });

  test('закрыть без разблокировки нельзя: Esc не закрывает, «Закрыть» недоступна', async () => {
    lockedServer();
    const user = userEvent.setup();
    mount();
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    const d = await dialog();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Подтвердите вход' })).toBe(d);
    expect(within(d).getByRole('button', { name: 'Закрыть' })).toBeDisabled();
  });

  test('блокировка в окне (attempts_left = 0) — текст, «Ко входу» → запрос падает, сессия — вход', async () => {
    let gone = false;
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        gone ? response.untyped(problem(401, 'auth.unauthenticated')) : response(200).json(meOwner),
      ),
      http.patch('/v1/staff/me/locale', ({ response }) =>
        response.untyped(problem(401, 'auth.reauth_required')),
      ),
      http.post('/v1/staff/auth/unlock', ({ response }) => {
        gone = true;
        return response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 0 }));
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Probe />
        <ReauthModal />
      </>,
      { me: null },
    );
    await screen.findByText('сессия: active');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await enterCode(user, '111111');
    const d = await dialog();
    expect(await within(d).findByRole('alert')).toHaveTextContent(
      'доступ закрыт на час, мы сообщили администраторам',
    );
    await user.click(within(d).getByRole('button', { name: 'Ко входу' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(await screen.findByText('сессия: anonymous')).toBeInTheDocument();
    expect(screen.queryByText('Сохранено')).not.toBeInTheDocument();
  });
});

// Ревью ветки, I1 (сценарий 2): запрос A ждёт разблокировку, окно открыто; в другой вкладке вошёл B
// (cookie общая). Вкладка узнаёт об этом по me — окно закрывается, запрос A падает исходной ошибкой и
// не повторяется под cookie B, даже если B потом разблокирует свою сессию.
describe('сменился сотрудник', () => {
  test('me стал другим, пока окно открыто — окно закрыто, запрос A не повторён', async () => {
    const ctl = lockedServer();
    const user = userEvent.setup();
    const { queryClient } = mount();
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await dialog();
    expect(ctl.patches).toHaveLength(1);

    server.use(http.get('/v1/staff/me', ({ response }) => response(200).json(meAdmin)));
    await act(() => queryClient.refetchQueries({ queryKey: ['staff', 'me'], exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    // B разблокировал бы свою сессию — запрос A уже не ждёт
    ctl.unlocked = true;
    await act(async () => {});
    expect(ctl.patches).toHaveLength(1);
    expect(ctl.unlocks).toBe(0);
    expect(screen.queryByText('Сохранено')).not.toBeInTheDocument();
  });
});

describe('в приложении', () => {
  test('«Выйти» в окне — выход каркаса: вход без return; черновики и мутации стёрты', async () => {
    const app = renderApp('/staff', { me: { ...meOwner, reauth_required: true } });
    sessionStorage.setItem('wf-admin:draft:/staff', '{"email":"ivan.p@wf.example"}');
    // переменные прежних мутаций (пароль, код) в кэше дольше экрана
    app.queryClient.getMutationCache().build(
      app.queryClient,
      { gcTime: Infinity },
      {
        context: undefined,
        data: undefined,
        error: null,
        failureCount: 0,
        failureReason: null,
        isPaused: false,
        status: 'success',
        variables: { password: 'пароль прежнего' },
        submittedAt: 0,
      },
    );
    let key: string | null = null;
    server.use(
      http.post('/v1/staff/auth/logout', ({ request, response }) => {
        key = request.headers.get('Idempotency-Key');
        app.me.current = null;
        return response(204).empty();
      }),
    );
    const user = userEvent.setup();
    const d = await dialog();
    await user.click(within(d).getByRole('button', { name: 'Выйти' }));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Вход администратора' }),
    ).toBeInTheDocument();
    await waitFor(() => expect(app.at()).toBe('/login'));
    expect(key).toBeTruthy();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(sessionStorage.getItem('wf-admin:draft:/staff')).toBeNull();
    expect(
      JSON.stringify(
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      ),
    ).not.toContain('пароль прежнего');
  });

  test('сессия стала anonymous, пока окно открыто (401 другого запроса) — окно закрыто, вход с return', async () => {
    const app = renderApp('/staff', { me: { ...meOwner, reauth_required: true } });
    await dialog();
    app.me.current = null;
    await app.refetchMe();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Вход администратора' }),
    ).toBeInTheDocument();
    expect(app.at()).toBe('/login?return=%2Fstaff');
  });

  test('сессия уже не ждёт разблокировки (409 staff.wrong_stage: другая вкладка успела) — окно закрыто, запрос повторён', async () => {
    const ctl = { patches: 0 };
    let unlocked = false;
    server.use(
      http.get('/v1/staff/me', ({ response }) => response(200).json(meOwner)),
      http.patch('/v1/staff/me/locale', ({ response }) => {
        ctl.patches++;
        return unlocked
          ? response(204).empty()
          : response.untyped(problem(401, 'auth.reauth_required'));
      }),
      http.post('/v1/staff/auth/unlock', ({ response }) => {
        unlocked = true; // разблокировала другая вкладка — cookie общая
        return response.untyped(problem(409, 'staff.wrong_stage'));
      }),
    );
    const user = userEvent.setup();
    mount();
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await enterCode(user, '492817');
    expect(await screen.findByText('Сохранено')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(ctl.patches).toBe(2);
  });

  test('сессия кончилась, пока окно открыто (401 auth.unauthenticated) — окно закрывается, вход', async () => {
    const app = renderApp('/staff', { me: { ...meOwner, reauth_required: true } });
    server.use(
      http.post('/v1/staff/auth/unlock', ({ response }) => {
        app.me.current = null;
        return response.untyped(problem(401, 'auth.unauthenticated'));
      }),
    );
    const user = userEvent.setup();
    await dialog();
    await enterCode(user, '492817');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Вход администратора' }),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await act(async () => {});
    expect(app.at()).toBe('/login?return=%2Fstaff');
  });
});
