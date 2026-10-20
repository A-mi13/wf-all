import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { components } from '../../api/gen/admin';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { session } from '../../test/staff';

// «Мой доступ» (спека §9.4 п. 4, §4.6–4.7; макет 07): второй фактор и перевыпуск кодов (только
// TOTP, M9), резервные коды «N из 10», активные сеансы, смена пароля (*). Неудачи пароля и второго
// фактора здесь считаются в блокировку: attempts_left = 0 — доступ закрыт на час, сессия перечитана.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.useRealTimers();
});

type MfaStatus = components['schemas']['StaffMfaStatus'];
type Session = components['schemas']['StaffSession'];

const NEW_CODES = [
  'k7m2q-x9p4t',
  'a3b5c-d7e2f',
  'm4n6p-q2r8s',
  't5v7w-x3y9z',
  'b2c4d-f6g8h',
  'j3k5m-n7p2q',
  'r4s6t-v8w3x',
  'y5z7a-b2c9d',
  'e3f5g-h7j4k',
  'p6q8r-s2t5v',
];
const CODE = 'Код из приложения-аутентификатора';
const OTHER = '0192f1d0-1b2c-7d3e-8f4a-5b6c7d8e9f02';
const PHONE = '0192f1d0-1b2c-7d3e-8f4a-5b6c7d8e9f03';

const SESSIONS: Session[] = [
  session(),
  session({
    id: OTHER,
    current: false,
    ip: '198.51.100.0/24',
    created_at: '2026-10-12T08:00:00Z',
    user_agent:
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
  }),
  session({
    id: PHONE,
    current: false,
    ip: '2001:db8:1::/48',
    created_at: '2026-10-11T20:40:00Z',
    user_agent: null,
  }),
];

type Seen = {
  mfa: number;
  sessions: number;
  regen: unknown[];
  password: unknown[];
  keys: string[];
};

// Сервер «Моего доступа»: статус 2FA и сеансы читаются заново при каждом запросе.
function accessServer(opts: { mfa?: MfaStatus; sessions?: Session[] } = {}) {
  const state = {
    mfa: opts.mfa ?? { confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 8 },
    sessions: opts.sessions ?? SESSIONS,
  };
  const seen: Seen = { mfa: 0, sessions: 0, regen: [], password: [], keys: [] };
  server.use(
    http.get('/v1/staff/me/mfa', ({ response }) => {
      seen.mfa++;
      return response(200).json(state.mfa);
    }),
    http.get('/v1/staff/me/sessions', ({ response }) => {
      seen.sessions++;
      return response(200).json({ items: state.sessions });
    }),
  );
  return { state, seen };
}

const page = () => screen.findByRole('heading', { level: 1, name: 'Мой доступ' });
const card = (name: string) => screen.getByRole('region', { name });

async function enterCode(user: UserEvent, root: HTMLElement, code: string) {
  const group = await within(root).findByRole('group', { name: CODE });
  await user.click(within(group).getAllByRole('textbox')[0]!);
  await user.keyboard(code);
}

const digits = (root: HTMLElement) =>
  within(within(root).getByRole('group', { name: CODE }))
    .getAllByRole('textbox')
    .map((c) => (c as HTMLInputElement).value)
    .join('');

describe('второй фактор и резервные коды', () => {
  test('статус 2FA и «8 из 10 не использованы» без предупреждения', async () => {
    accessServer();
    renderApp('/me', { me: meOwner });
    await page();
    const mfa = card('Второй фактор');
    expect(await within(mfa).findByText('Второй фактор включён')).toBeInTheDocument();
    expect(screen.getByText('2ФА включена')).toBeInTheDocument();
    expect(within(mfa).getByText('8 из 10 не использованы')).toBeInTheDocument();
    expect(within(mfa).queryByText(/Кодов почти не осталось/)).not.toBeInTheDocument();
    // сами коды после выдачи не показываются
    expect(within(mfa).queryByRole('list', { name: 'Резервные коды' })).not.toBeInTheDocument();
  });

  test('≤ 3 кодов — предупреждение', async () => {
    accessServer({ mfa: { confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 3 } });
    renderApp('/me', { me: meOwner });
    const mfa = await screen.findByRole('region', { name: 'Второй фактор' });
    expect(await within(mfa).findByText('3 из 10 не использованы')).toBeInTheDocument();
    expect(within(mfa).getByText(/Кодов почти не осталось/)).toBeInTheDocument();
  });

  test('статус 2FA не загрузился — ошибка с кодом и «Повторить»', async () => {
    accessServer();
    let calls = 0;
    server.use(
      http.get('/v1/staff/me/mfa', ({ response }) => {
        calls++;
        return calls === 1
          ? response.untyped(problem(500, 'internal'))
          : response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 8 });
      }),
    );
    renderApp('/me', { me: meOwner });
    const mfa = await screen.findByRole('region', { name: 'Второй фактор' });
    expect(await within(mfa).findByText('Код ошибки: 500 · internal')).toBeInTheDocument();
    await userEvent.setup().click(within(mfa).getByRole('button', { name: 'Повторить' }));
    expect(await within(mfa).findByText('8 из 10 не использованы')).toBeInTheDocument();
  });

  test('«Потерял доступ» — раскрывающийся текст: восстанавливают владелец или админ вдвоём', async () => {
    accessServer();
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    const lost = await screen.findByRole('button', { name: 'Потерял доступ' });
    expect(lost).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText(/восстанавливает владелец или админ/)).not.toBeVisible();
    await user.click(lost);
    expect(lost).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText(/восстанавливает владелец или админ/)).toBeVisible();
  });
});

describe('перевыпуск резервных кодов', () => {
  function regenServer(answer: (code: string) => Response | null) {
    const { state, seen } = accessServer();
    server.use(
      http.post('/v1/staff/me/mfa/recovery-codes', async ({ request, response }) => {
        const body = await request.json();
        seen.regen.push(body);
        seen.keys.push(request.headers.get('Idempotency-Key') ?? '');
        const r = answer(body.code);
        if (r) return response.untyped(r);
        state.mfa = { ...state.mfa, recovery_codes_left: 10 };
        return response(200).json({ recovery_codes: NEW_CODES });
      }),
    );
    return seen;
  }

  // Ревью ветки, I1: коды A на экране, в другой вкладке вошёл B — вкладка узнаёт об этом по me.
  // Окно с кодами живёт в состоянии экрана, а не в кэше: экраны A размонтируются (каркас по me.id).
  test('сотрудник сменился, пока коды на экране — окна и кодов A у B нет', async () => {
    regenServer(() => null);
    const app = renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '492817');
    await within(dialog).findByRole('list', { name: 'Резервные коды' });

    app.me.current = meAdmin;
    await app.refetchMe();
    await page();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    for (const c of NEW_CODES) expect(screen.queryByText(c)).not.toBeInTheDocument();
  });

  test('только TOTP: 10 новых кодов; после «Готово» их нет ни в DOM, ни в storage, ни в кэше', async () => {
    const seen = regenServer(() => null);
    const app = renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    // резервный код здесь не принимается (M9): ни переключателя, ни поля
    expect(within(dialog).queryByRole('button', { name: /резервный код/i })).toBeNull();
    expect(within(dialog).queryByLabelText(/Резервный код/)).toBeNull();

    await enterCode(user, dialog, '492817');
    const list = await within(dialog).findByRole('list', { name: 'Резервные коды' });
    expect(
      within(list)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(NEW_CODES);
    expect(seen.regen).toEqual([{ code: '492817' }]);
    expect(seen.keys[0]).toBeTruthy();
    expect(within(dialog).getByRole('button', { name: 'Скопировать коды' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Скачать .txt' })).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Готово' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    for (const c of NEW_CODES) expect(screen.queryByText(c)).not.toBeInTheDocument();
    // счётчик перечитан
    expect(await screen.findByText('10 из 10 не использованы')).toBeInTheDocument();
    for (const c of NEW_CODES) expect(storageDump()).not.toContain(c);
    await waitFor(() => {
      const cached = JSON.stringify([
        app.queryClient
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data),
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => [m.state.data, m.state.variables]),
      ]);
      expect(cached).not.toContain(NEW_CODES[0]);
      expect(cached).not.toContain('492817');
    });
  });

  test('код не подошёл — «Осталось N попыток», ячейки очищены', async () => {
    const seen = regenServer(() => problem(400, 'staff.mfa_invalid', { attempts_left: 3 }));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '111111');
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Код не подошёл. Осталось 3 попытки',
    );
    await waitFor(() => expect(digits(dialog)).toBe(''));
    expect(within(dialog).getByRole('button', { name: 'Выпустить коды' })).toBeDisabled();
    expect(seen.regen).toHaveLength(1);
  });

  test('attempts_left = 0 — тост «доступ закрыт на час», сессия перечитана → вход', async () => {
    let app: ReturnType<typeof renderApp> | null = null;
    regenServer(() => {
      if (app) app.me.current = null; // пятая неудача гасит все сессии
      return problem(400, 'staff.mfa_invalid', { attempts_left: 0 });
    });
    app = renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    await enterCode(user, screen.getByRole('dialog'), '111111');
    expect(await screen.findByText(/Доступ закрыт на час/)).toBeInTheDocument();
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' });
    // тост пережил переход на вход
    expect(screen.getByText(/Доступ закрыт на час/)).toBeInTheDocument();
  });

  // Сервер мог уже выпустить новые коды (старые сгорели): окно, закрытое посреди запроса, потеряло
  // бы их навсегда.
  // Ревью I1: разблокировка кодом из приложения сдвигает last_used_step — повтор того же кода сервер
  // отверг бы как staff.mfa_invalid и засчитал неудачу. Запрос не повторяется, код стёрт, просим новый.
  test('после разблокировки запрос с тем же кодом не повторяется — ячейки пусты, «введите новый код»', async () => {
    const seen = regenServer(() =>
      seen.regen.length === 1 ? problem(401, 'auth.reauth_required') : null,
    );
    let unlocks = 0;
    server.use(
      http.post('/v1/staff/auth/unlock', ({ response }) => {
        unlocks++;
        return response(204).empty();
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '492817');
    const reauth = await screen.findByRole('dialog', { name: 'Подтвердите вход' });
    await enterCode(user, reauth, '730415');
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Подтвердите вход' })).toBeNull(),
    );
    expect(unlocks).toBe(1);
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/введите новый код/);
    await waitFor(() => expect(digits(dialog)).toBe(''));
    expect(seen.regen).toEqual([{ code: '492817' }]);

    // новый код — новый запрос
    await enterCode(user, dialog, '118274');
    expect(await within(dialog).findByRole('list', { name: 'Резервные коды' })).toBeInTheDocument();
    expect(seen.regen).toEqual([{ code: '492817' }, { code: '118274' }]);
  });

  // Уборка №6, R3: разблокировку отклонили (выход из окна, блокировка) — экран уходит на вход, и
  // «Вход подтверждён…» на нём не должен мелькнуть: вход как раз НЕ подтверждён.
  test('разблокировка отклонена — без «Вход подтверждён», запрос не повторён', async () => {
    const seen = regenServer(() => problem(401, 'auth.reauth_required'));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '492817');
    await screen.findByRole('dialog', { name: 'Подтвердите вход' });
    act(() => reauthBus.reject());
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Подтвердите вход' })).toBeNull(),
    );
    await waitFor(() =>
      expect(within(dialog).getByRole('group', { name: CODE })).not.toHaveAttribute('aria-busy'),
    );
    expect(screen.queryByText(/Вход подтверждён/)).toBeNull();
    expect(seen.regen).toEqual([{ code: '492817' }]);
  });

  // Ревью ветки, M2: исход перевыпуска неизвестен (500 после коммита, обрыв, replay_unavailable — ответ с
  // кодами не хранится) — старые коды могли сгореть. Окно говорит об этом прямо, счётчик перечитан.
  test.each([
    ['500 после коммита', () => problem(500, 'internal')],
    ['обрыв сети', () => HttpResponse.error()],
    ['409 idempotency.replay_unavailable', () => problem(409, 'idempotency.replay_unavailable')],
  ])(
    'исход неизвестен (%s) — «коды могли быть перевыпущены», счётчик перечитан',
    async (_, answer) => {
      const { state, seen } = accessServer();
      server.use(
        http.post('/v1/staff/me/mfa/recovery-codes', ({ response }) => {
          seen.regen.push(1);
          state.mfa = { ...state.mfa, recovery_codes_left: 10 };
          return response.untyped(answer());
        }),
      );
      renderApp('/me', { me: meOwner });
      const user = userEvent.setup();
      await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
      const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
      const before = seen.mfa;
      await enterCode(user, dialog, '492817');
      expect(await within(dialog).findByRole('alert')).toHaveTextContent(
        'коды могли быть перевыпущены',
      );
      await waitFor(() => expect(seen.mfa).toBeGreaterThan(before));
      expect(await screen.findByText('10 из 10 не использованы')).toBeInTheDocument();
      expect(seen.regen).toHaveLength(1);
    },
  );

  test('коды на экране — Esc, фон и «×» окно не закрывают, только «Готово»', async () => {
    regenServer(() => null);
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '492817');
    await within(dialog).findByRole('list', { name: 'Резервные коды' });
    await user.keyboard('{Escape}');
    expect(within(dialog).getByRole('list', { name: 'Резервные коды' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Закрыть' })).toBeDisabled();
    await user.click(within(dialog).getByRole('button', { name: 'Готово' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  test('пока запрос идёт, окно не закрывается; ответ показывает коды', async () => {
    accessServer();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.post('/v1/staff/me/mfa/recovery-codes', async ({ response }) => {
        await gate;
        return response(200).json({ recovery_codes: NEW_CODES });
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog', { name: 'Новые резервные коды' });
    await enterCode(user, dialog, '492817');
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Закрыть' })).toBeDisabled(),
    );
    // ячейки на время запроса — busy, не disabled (ревью M4)
    const group = within(dialog).getByRole('group', { name: CODE });
    expect(group).toHaveAttribute('aria-busy', 'true');
    for (const cell of within(group).getAllByRole('textbox')) expect(cell).toBeEnabled();
    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog', { name: 'Новые резервные коды' })).toBeInTheDocument();
    release();
    expect(await within(dialog).findByRole('list', { name: 'Резервные коды' })).toBeInTheDocument();
  });

  test('429 — отсчёт; набранный за время отсчёта код по его концу стёрт', async () => {
    const seen = regenServer(() => problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '20' }));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Перевыпустить коды' }));
    const dialog = screen.getByRole('dialog');
    const group = await within(dialog).findByRole('group', { name: CODE });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const paste = (code: string) =>
      fireEvent.paste(within(group).getAllByRole('textbox')[0]!, {
        clipboardData: { getData: () => code },
      });
    paste('492817');
    expect(await within(dialog).findByRole('button', { name: 'Подождать 20 с' })).toBeDisabled();
    paste('135790');
    expect(seen.regen).toHaveLength(1);
    act(() => vi.advanceTimersByTime(21_000));
    await waitFor(() => expect(digits(dialog)).toBe(''));
    expect(within(dialog).getByRole('button', { name: 'Выпустить коды' })).toBeDisabled();
    expect(seen.regen).toHaveLength(1);
  });
});

describe('активные сеансы', () => {
  test('текущий — «Текущий сеанс» без кнопки; устройство, сеть; без UA — «Устройство не определено»', async () => {
    accessServer();
    renderApp('/me', { me: meOwner });
    const list = await screen.findByRole('list', { name: 'Сеансы' });
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent('Chrome · Windows');
    expect(items[0]).toHaveTextContent('Текущий сеанс');
    expect(items[0]).toHaveTextContent('203.0.113.0/24');
    expect(within(items[0]!).queryByRole('button')).toBeNull();
    expect(items[1]).toHaveTextContent('Safari · macOS');
    expect(
      within(items[1]!).getByRole('button', { name: /Завершить сеанс: Safari · macOS/ }),
    ).toBeEnabled();
    expect(items[2]).toHaveTextContent('Устройство не определено');
    expect(items[2]).toHaveTextContent('2001:db8:1::/48');
  });

  test('«Завершить» → 204 — тост и перезапрос; 404 (уже завершён) — тост ошибки и перезапрос', async () => {
    const { state, seen } = accessServer();
    const deleted: string[] = [];
    server.use(
      http.delete('/v1/staff/me/sessions/{sessionId}', ({ params, request, response }) => {
        deleted.push(params.sessionId);
        expect(request.headers.get('Idempotency-Key')).toBeTruthy();
        if (params.sessionId === PHONE)
          return response.untyped(problem(404, 'staff.session_not_found'));
        state.sessions = state.sessions.filter((s) => s.id !== params.sessionId);
        return response(204).empty();
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    const list = await screen.findByRole('list', { name: 'Сеансы' });
    const before = seen.sessions;

    await user.click(within(list).getByRole('button', { name: /Safari · macOS/ }));
    expect(await screen.findByText(/Сеанс завершён/)).toBeInTheDocument();
    await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(2));
    expect(seen.sessions).toBeGreaterThan(before);

    const mid = seen.sessions;
    await user.click(within(list).getByRole('button', { name: /Устройство не определено/ }));
    expect(await screen.findByText('Сессия не найдена или уже завершена')).toBeInTheDocument();
    await waitFor(() => expect(seen.sessions).toBeGreaterThan(mid));
    expect(deleted).toEqual([OTHER, PHONE]);
  });

  // Уборка №6, R2: у нажатой «Завершить» loading вместе с disabled — побеждал disabled, и фокус с
  // кнопки падал на body. Недоступны только кнопки других строк.
  test('пока «Завершить» идёт, фокус остаётся на нажатой кнопке; другие строки недоступны', async () => {
    accessServer();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.delete('/v1/staff/me/sessions/{sessionId}', async ({ response }) => {
        await gate;
        return response(204).empty();
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    const list = await screen.findByRole('list', { name: 'Сеансы' });
    const pressed = within(list).getByRole('button', { name: /Safari · macOS/ });
    await user.click(pressed);
    await waitFor(() => expect(pressed).toHaveAttribute('aria-busy', 'true'));
    expect(pressed).toBeEnabled();
    expect(document.activeElement).toBe(pressed);
    expect(within(list).getByRole('button', { name: /Устройство не определено/ })).toBeDisabled();
    release();
    expect(await screen.findByText(/Сеанс завершён/)).toBeInTheDocument();
  });

  test('«Завершить все другие сеансы» — число завершённых и перезапрос', async () => {
    const { state, seen } = accessServer();
    let calls = 0;
    server.use(
      http.post('/v1/staff/me/sessions/revoke-others', ({ request, response }) => {
        calls++;
        expect(request.headers.get('Idempotency-Key')).toBeTruthy();
        state.sessions = state.sessions.filter((s) => s.current);
        return response(200).json({ revoked: 2 });
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    const list = await screen.findByRole('list', { name: 'Сеансы' });
    const before = seen.sessions;
    await user.click(screen.getByRole('button', { name: 'Завершить все другие сеансы' }));
    expect(await screen.findByText('Завершено 2 сеанса')).toBeInTheDocument();
    await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(1));
    expect(seen.sessions).toBeGreaterThan(before);
    expect(calls).toBe(1);
    // других сеансов нет — кнопка недоступна
    expect(screen.getByRole('button', { name: 'Завершить все другие сеансы' })).toBeDisabled();
  });
});

describe('смена пароля', () => {
  function passwordServer(answer: (body: unknown) => Response | null) {
    const { seen } = accessServer();
    server.use(
      http.post('/v1/staff/me/password', async ({ request, response }) => {
        const body = await request.json();
        seen.password.push(body);
        seen.keys.push(request.headers.get('Idempotency-Key') ?? '');
        const r = answer(body);
        return r ? response.untyped(r) : response(204).empty();
      }),
    );
    return seen;
  }

  async function fill(user: UserEvent, root: HTMLElement, repeat = 'Свисток на 46-й минуте') {
    await user.type(within(root).getByLabelText('Текущий пароль'), 'Жёлтая карточка на 90-й');
    await user.type(within(root).getByLabelText('Новый пароль'), 'Свисток на 46-й минуте');
    await user.type(within(root).getByLabelText('Новый пароль ещё раз'), repeat);
    await enterCode(user, root, '492817');
  }

  // Ревью M4: на время смены пароля ячейки кода busy, а не disabled.
  test('пока смена пароля идёт, ячейки кода включены и заняты (aria-busy)', async () => {
    accessServer();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.post('/v1/staff/me/password', async ({ response }) => {
        await gate;
        return response(204).empty();
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    const group = within(form).getByRole('group', { name: CODE });
    await waitFor(() => expect(group).toHaveAttribute('aria-busy', 'true'));
    for (const cell of within(group).getAllByRole('textbox')) expect(cell).toBeEnabled();
    release();
    expect(await screen.findByText(/Пароль изменён|Пароль сменён/)).toBeInTheDocument();
  });

  // Ревью ветки, M4: исход смены пароля неизвестен (500 после коммита, обрыв) — пароль мог смениться.
  // Повтор со старым паролем дал бы staff.password_invalid в блокировку: экран говорит «результат
  // неизвестен — входите новым паролем», форма стёрта, список сеансов перечитан (другие могли кончиться).
  test.each([
    ['500 после коммита', () => problem(500, 'internal')],
    ['обрыв сети', () => HttpResponse.error()],
  ])(
    'исход неизвестен (%s) — «входите новым паролем», форма стёрта, повтора нет',
    async (_, answer) => {
      const seen = passwordServer(() => answer() as Response);
      renderApp('/me', { me: meOwner });
      const user = userEvent.setup();
      await page();
      const form = card('Смена пароля');
      const before = seen.sessions;
      await fill(user, form);
      await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
      expect(await within(form).findByRole('alert')).toHaveTextContent('Результат неизвестен');
      expect(within(form).getByRole('alert')).toHaveTextContent('входите новым паролем');
      expect(within(form).getByLabelText('Текущий пароль')).toHaveValue('');
      expect(within(form).getByLabelText('Новый пароль')).toHaveValue('');
      expect(within(form).getByLabelText('Новый пароль ещё раз')).toHaveValue('');
      expect(digits(form)).toBe('');
      await waitFor(() => expect(seen.sessions).toBeGreaterThan(before));
      expect(seen.password).toHaveLength(1);
    },
  );

  test('новый пароль и повтор не совпали — ошибка поля, запрос не уходит', async () => {
    const seen = passwordServer(() => null);
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form, 'Свисток на 47-й минуте');
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(await within(form).findByText('Не совпадает')).toBeInTheDocument();
    expect(seen.password).toHaveLength(0);
  });

  test('успех: тело запроса, тост, поля пусты; пароль и код — ни в storage, ни в кэше мутаций', async () => {
    const seen = passwordServer(() => null);
    const app = renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    const before = seen.sessions;
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(await screen.findByText(/Пароль изменён/)).toBeInTheDocument();
    expect(seen.password).toEqual([
      {
        current_password: 'Жёлтая карточка на 90-й',
        new_password: 'Свисток на 46-й минуте',
        code: '492817',
      },
    ]);
    expect(seen.keys[0]).toBeTruthy();
    expect(within(form).getByLabelText('Текущий пароль')).toHaveValue('');
    expect(within(form).getByLabelText('Новый пароль')).toHaveValue('');
    expect(digits(form)).toBe('');
    // другие сеансы погашены — список перечитан
    await waitFor(() => expect(seen.sessions).toBeGreaterThan(before));
    expect(storageDump()).not.toContain('Жёлтая карточка');
    expect(storageDump()).not.toContain('Свисток');
    await waitFor(() => {
      const vars = JSON.stringify(
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      );
      expect(vars).not.toContain('Свисток');
      expect(vars).not.toContain('492817');
    });
  });

  test('резервный код вместо кода приложения — уходит recovery_code', async () => {
    const seen = passwordServer(() => null);
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await user.type(within(form).getByLabelText('Текущий пароль'), 'Жёлтая карточка на 90-й');
    await user.type(within(form).getByLabelText('Новый пароль'), 'Свисток на 46-й минуте');
    await user.type(within(form).getByLabelText('Новый пароль ещё раз'), 'Свисток на 46-й минуте');
    await user.click(within(form).getByRole('button', { name: 'Использовать резервный код' }));
    await user.type(within(form).getByLabelText('Резервный код'), 'k7m2q-x9p4t');
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    await screen.findByText(/Пароль изменён/);
    expect(seen.password).toEqual([
      {
        current_password: 'Жёлтая карточка на 90-й',
        new_password: 'Свисток на 46-й минуте',
        recovery_code: 'k7m2q-x9p4t',
      },
    ]);
  });

  test('staff.password_invalid с attempts_left — «Текущий пароль неверный. Осталось 2 попытки», фактор стёрт', async () => {
    passwordServer(() => problem(400, 'staff.password_invalid', { attempts_left: 2 }));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(await within(form).findByRole('alert')).toHaveTextContent(
      'Текущий пароль неверный. Осталось 2 попытки',
    );
    await waitFor(() => expect(digits(form)).toBe(''));
    expect(within(form).getByLabelText('Текущий пароль')).toHaveValue('');
    await waitFor(() => expect(within(form).getByLabelText('Текущий пароль')).toHaveFocus());
  });

  test('staff.mfa_invalid с attempts_left — «Код не подошёл. Осталась 1 попытка»', async () => {
    passwordServer(() => problem(400, 'staff.mfa_invalid', { attempts_left: 1 }));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(await within(form).findByRole('alert')).toHaveTextContent(
      'Код не подошёл. Осталась 1 попытка',
    );
    await waitFor(() => expect(digits(form)).toBe(''));
    // пароли остались — ошибся только код
    expect(within(form).getByLabelText('Текущий пароль')).toHaveValue('Жёлтая карточка на 90-й');
  });

  test('password_invalid без attempts_left — общий текст по коду, а не немая ошибка', async () => {
    passwordServer(() => problem(400, 'staff.password_invalid'));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(await within(form).findByRole('alert')).toHaveTextContent('Текущий пароль неверный');
  });

  test('после разблокировки смена пароля не повторяется: код стёрт, пароли на месте, «введите новый код»', async () => {
    const seen = passwordServer(() =>
      seen.password.length === 1 ? problem(401, 'auth.reauth_required') : null,
    );
    server.use(http.post('/v1/staff/auth/unlock', ({ response }) => response(204).empty()));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    const reauth = await screen.findByRole('dialog', { name: 'Подтвердите вход' });
    await enterCode(user, reauth, '730415');
    expect(await within(form).findByRole('alert')).toHaveTextContent(/введите новый код/);
    await waitFor(() => expect(digits(form)).toBe(''));
    expect(within(form).getByLabelText('Текущий пароль')).toHaveValue('Жёлтая карточка на 90-й');
    expect(within(form).getByLabelText('Новый пароль')).toHaveValue('Свисток на 46-й минуте');
    expect(seen.password).toHaveLength(1);
  });

  // Уборка №6, R3: отклонённая разблокировка — не «Вход подтверждён» и не FormError с «Подтвердите
  // вход…»: сессия уходит на вход, экран молчит.
  test('разблокировка отклонена — без «Вход подтверждён» и без текста ошибки', async () => {
    const seen = passwordServer(() => problem(401, 'auth.reauth_required'));
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    await screen.findByRole('dialog', { name: 'Подтвердите вход' });
    act(() => reauthBus.reject());
    await waitFor(() =>
      expect(within(form).getByRole('button', { name: 'Сменить пароль' })).not.toHaveAttribute(
        'aria-busy',
      ),
    );
    expect(screen.queryByText(/Вход подтверждён/)).toBeNull();
    expect(within(form).queryByRole('alert')).toBeNull();
    expect(seen.password).toHaveLength(1);
  });

  test('attempts_left = 0 — тост «доступ закрыт на час» и refresh() → вход', async () => {
    let app: ReturnType<typeof renderApp> | null = null;
    passwordServer(() => {
      if (app) app.me.current = null;
      return problem(400, 'staff.password_invalid', { attempts_left: 0 });
    });
    app = renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' });
    expect(screen.getByText(/Доступ закрыт на час/)).toBeInTheDocument();
  });

  test('новый пароль не подошёл серверу — ошибка по полю', async () => {
    passwordServer(() =>
      problem(400, 'validation.failed', {
        errors: [{ field: 'body.new_password', code: 'common' }],
      }),
    );
    renderApp('/me', { me: meOwner });
    const user = userEvent.setup();
    await page();
    const form = card('Смена пароля');
    await fill(user, form);
    await user.click(within(form).getByRole('button', { name: 'Сменить пароль' }));
    expect(
      await within(form).findByText('Этот пароль слишком распространён — придумайте другой'),
    ).toBeInTheDocument();
  });
});

test('StrictMode: статус и сеансы — по одному запросу на экран', async () => {
  const { seen } = accessServer();
  renderApp('/me', { me: meOwner, strict: true });
  await screen.findByText('8 из 10 не использованы');
  await screen.findByRole('list', { name: 'Сеансы' });
  expect([seen.mfa, seen.sessions]).toEqual([1, 1]);
});
