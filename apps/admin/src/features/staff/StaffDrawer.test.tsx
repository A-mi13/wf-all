import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp } from '../../test/app';
import { CITY_OTHER, CITY_OWN, meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { account, approval, session, staffServer, TEAM } from '../../test/staff';

// Строка сотрудника (*; спека §9.4 п. 5, §2.5–2.6, §4.9): переотправка (429 — отсчёт), роль и
// города (200 — сразу, 202 — на подтверждение), сеансы цели, сброс доступа (202), выключение
// (200/202). Разрушающие — ConfirmDialog: почта цели как показана (xn--) и причина.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.useRealTimers();
});

const IDN = account({
  id: '0192f1c4-1111-7b21-9c5d-000000000009',
  email: 'ivan@xn--d1acufc.xn--p1ai',
  display_name: 'Иван П.',
  role: 'support',
  city_ids: [],
});
const MODERATOR2 = account({ city_ids: [CITY_OWN, CITY_OTHER] });

async function manage(user: UserEvent, name: string) {
  const table = await screen.findByRole('table', { name: 'Сотрудники' });
  await user.click(await within(table).findByRole('button', { name: `Управлять: ${name}` }));
  return screen.getByRole('dialog', { name });
}

const region = (root: HTMLElement, name: string) => within(root).getByRole('region', { name });

describe('роль и города', () => {
  function roleServer(status: 200 | 202) {
    const { state, seen } = staffServer({ accounts: [...TEAM.slice(0, 1), MODERATOR2] });
    const sent: unknown[] = [];
    server.use(
      http.put('/v1/staff/accounts/{staffId}/role', async ({ request, response, params }) => {
        const body = await request.json();
        sent.push(body);
        expect(request.headers.get('Idempotency-Key')).toBeTruthy();
        if (status === 202)
          return response(202).json({
            approval: approval({ action: 'staff.role_change', target_id: params.staffId }),
          });
        const next = { ...MODERATOR2, ...body, version: MODERATOR2.version + 1 };
        state.accounts = state.accounts.map((a) => (a.id === next.id ? next : a));
        return response(200).json(next);
      }),
    );
    return { sent, seen };
  }

  test('только снятие города — revoke, сразу: 200, тост, список и карточка перечитаны', async () => {
    const { sent, seen } = roleServer(200);
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Мария К.');
    const block = region(panel, 'Роль и города');
    await user.click(
      await within(block).findByRole('button', { name: 'Убрать «Пятигорск, Ставропольский край»' }),
    );
    const listBefore = seen.list.length;
    const save = within(block).getByRole('button', { name: 'Сохранить' });
    await user.click(save);
    expect(await screen.findByText('Роль и города сохранены.')).toBeInTheDocument();
    expect(sent).toEqual([{ role: 'moderator', city_ids: [CITY_OWN] }]);
    await waitFor(() => expect(seen.list.length).toBeGreaterThan(listBefore));
    expect(seen.cards.length).toBeGreaterThanOrEqual(2);
    expect(within(panel).queryByText('Отправлено на подтверждение')).toBeNull();
  });

  test('модератор → поддержка — grant, вдвоём: 202, «Отправлено на подтверждение»', async () => {
    const { sent } = roleServer(202);
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Мария К.');
    const block = region(panel, 'Роль и города');
    await user.selectOptions(within(block).getByLabelText('Роль'), 'support');
    await user.click(within(block).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(panel).findByRole('status')).toHaveTextContent(
      'Отправлено на подтверждение',
    );
    expect(sent).toEqual([{ role: 'support', city_ids: [] }]);
    // роль пока прежняя — и в форме
    expect(within(block).getByLabelText('Роль')).toHaveValue('moderator');
  });

  test('без изменений — отправить нечего', async () => {
    roleServer(200);
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Мария К.');
    expect(
      within(region(panel, 'Роль и города')).getByRole('button', { name: 'Сохранить' }),
    ).toBeDisabled();
  });
});

describe('выключение и сброс доступа', () => {
  function destructiveServer(answer: { disable?: () => Response | null } = {}) {
    const { state } = staffServer({ accounts: [...TEAM.slice(0, 1), IDN] });
    const sent: { path: string; body: unknown }[] = [];
    server.use(
      http.get('/v1/staff/accounts/{staffId}/sessions', ({ response }) =>
        response(200).json({ items: [] }),
      ),
      http.post('/v1/staff/accounts/{staffId}/disable', async ({ request, response }) => {
        const body = await request.json();
        sent.push({ path: 'disable', body });
        const r = answer.disable?.();
        if (r) return response.untyped(r);
        const off = { ...IDN, status: 'disabled' as const, version: IDN.version + 1 };
        state.accounts = state.accounts.map((a) => (a.id === off.id ? off : a));
        return response(200).json(off);
      }),
      http.post('/v1/staff/accounts/{staffId}/access-reset', async ({ request, response }) => {
        sent.push({ path: 'reset', body: await request.json() });
        return response(202).json({ approval: approval({ action: 'staff.access_reset' }) });
      }),
    );
    return sent;
  }

  test('выключение: без верной почты кнопка недоступна; с xn-- почтой (как показана) — 200', async () => {
    const sent = destructiveServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Иван П.');
    // почта показана в A-форме, как хранится
    expect(within(panel).getByText('ivan@xn--d1acufc.xn--p1ai')).toBeInTheDocument();
    await user.click(
      within(region(panel, 'Выключить учётку')).getByRole('button', { name: 'Выключить' }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Выключить — Иван П.?' });
    const confirm = within(dialog).getByRole('button', { name: 'Выключить' });
    const input = within(dialog).getByLabelText(/Введите «ivan@xn--d1acufc.xn--p1ai»/);
    expect(confirm).toBeDisabled();
    // U-форма домена — не та почта, что показана
    await user.type(input, 'ivan@пример.рф');
    expect(confirm).toBeDisabled();
    await user.clear(input);
    await user.type(input, 'ivan@xn--d1acufc.xn--p1ai');
    expect(confirm).toBeEnabled();

    // причина обязательна
    await user.click(confirm);
    expect(await within(dialog).findByText('Заполните поле')).toBeInTheDocument();
    expect(sent).toHaveLength(0);

    await user.selectOptions(within(dialog).getByLabelText('Причина'), 'left_team');
    await user.type(within(dialog).getByLabelText('Комментарий'), 'Перешёл в другой проект');
    await user.click(confirm);
    expect(await screen.findByText('Учётка выключена.')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Выключить — Иван П.?' })).not.toBeInTheDocument();
    // учётка выключена — блока «Выключить» больше нет, фокус не потерян на body, а в панели
    await waitFor(() =>
      expect(within(panel).queryByRole('region', { name: 'Выключить учётку' })).toBeNull(),
    );
    await waitFor(() => expect(panel.contains(document.activeElement)).toBe(true));
    expect(sent).toEqual([
      {
        path: 'disable',
        body: {
          reason: 'left_team',
          comment: 'Перешёл в другой проект',
          confirm: 'ivan@xn--d1acufc.xn--p1ai',
        },
      },
    ]);
  });

  test('«Другое» без комментария — ошибка поля, запрос не уходит', async () => {
    const sent = destructiveServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Иван П.');
    await user.click(
      within(region(panel, 'Выключить учётку')).getByRole('button', { name: 'Выключить' }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Выключить — Иван П.?' });
    await user.selectOptions(within(dialog).getByLabelText('Причина'), 'other');
    await user.type(within(dialog).getByLabelText(/Введите/), 'ivan@xn--d1acufc.xn--p1ai');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    expect(await within(dialog).findByText('Заполните поле')).toBeInTheDocument();
    expect(sent).toHaveLength(0);
  });

  test('ошибка выключения — текст в диалоге, диалог открыт, почта и причина целы', async () => {
    const sent = destructiveServer({ disable: () => problem(409, 'staff.last_owner') });
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Иван П.');
    await user.click(
      within(region(panel, 'Выключить учётку')).getByRole('button', { name: 'Выключить' }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Выключить — Иван П.?' });
    await user.selectOptions(within(dialog).getByLabelText('Причина'), 'security');
    const input = within(dialog).getByLabelText(/Введите/);
    await user.type(input, 'ivan@xn--d1acufc.xn--p1ai');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Нельзя оставить платформу без владельца',
    );
    expect(input).toHaveValue('ivan@xn--d1acufc.xn--p1ai');
    expect(within(dialog).getByLabelText('Причина')).toHaveValue('security');
    expect(sent).toHaveLength(1);
  });

  test('сброс доступа — всегда вдвоём: xn-- почта → 202 → «Отправлено на подтверждение»', async () => {
    const sent = destructiveServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Иван П.');
    await user.click(
      within(region(panel, 'Сброс доступа')).getByRole('button', {
        name: 'Запросить сброс доступа',
      }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Сбросить доступ — Иван П.?' });
    const confirm = within(dialog).getByRole('button', { name: 'Отправить на подтверждение' });
    await user.selectOptions(within(dialog).getByLabelText('Причина'), 'security');
    expect(confirm).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/Введите/), 'ivan@xn--d1acufc.xn--p1ai');
    await user.click(confirm);
    expect(await within(panel).findByRole('status')).toHaveTextContent(
      'Отправлено на подтверждение',
    );
    expect(sent).toEqual([
      { path: 'reset', body: { reason: 'security', confirm: 'ivan@xn--d1acufc.xn--p1ai' } },
    ]);
  });

  test('выключение владельца — вдвоём: кнопка «Запросить выключение», 202 → уведомление', async () => {
    const owner2 = account({
      id: '0192f1c4-1111-7b21-9c5d-0000000000f2',
      email: 'am@wf.example',
      display_name: 'Алексей М.',
      role: 'owner',
      city_ids: [],
    });
    staffServer({ accounts: [...TEAM.slice(0, 1), owner2] });
    server.use(
      http.post('/v1/staff/accounts/{staffId}/disable', ({ response }) =>
        response(202).json({ approval: approval({ action: 'staff.revoke_owner' }) }),
      ),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Алексей М.');
    await user.click(
      within(region(panel, 'Выключить учётку')).getByRole('button', {
        name: 'Запросить выключение',
      }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Выключить — Алексей М.?' });
    await user.selectOptions(within(dialog).getByLabelText('Причина'), 'role_change');
    await user.type(within(dialog).getByLabelText(/Введите/), 'am@wf.example');
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(panel).findByRole('status')).toHaveTextContent(
      'Отправлено на подтверждение',
    );
  });
});

describe('приглашение и сеансы цели', () => {
  const INVITED = TEAM.find((a) => a.display_name === 'Настя Д.')!;

  test('переотправка: 204 — тост; 429 — отсчёт по Retry-After, кнопка недоступна до конца', async () => {
    staffServer({ accounts: [...TEAM.slice(0, 1), INVITED] });
    let calls = 0;
    server.use(
      http.post('/v1/staff/accounts/{staffId}/invite/resend', ({ request, response }) => {
        calls++;
        expect(request.headers.get('Idempotency-Key')).toBeTruthy();
        return calls === 1
          ? response(204).empty()
          : response.untyped(problem(429, 'ratelimit.exceeded', { retry_after: 42 }));
      }),
    );
    renderApp('/staff', { me: meAdmin });
    const user = userEvent.setup();
    const panel = await manage(user, 'Настя Д.');
    const block = region(panel, 'Приглашение');
    await user.click(within(block).getByRole('button', { name: 'Отправить приглашение повторно' }));
    expect(await screen.findByText(/Новый код приглашения ушёл на почту/)).toBeInTheDocument();

    vi.useFakeTimers({ shouldAdvanceTime: true });
    fireEvent.click(within(block).getByRole('button', { name: 'Отправить приглашение повторно' }));
    const wait = await within(block).findByRole('button', { name: 'Повторить через 42 с' });
    expect(wait).toBeDisabled();
    fireEvent.click(wait);
    expect(calls).toBe(2);
    act(() => vi.advanceTimersByTime(43_000));
    expect(
      within(block).getByRole('button', { name: 'Отправить приглашение повторно' }),
    ).toBeEnabled();
  });

  test('сеансы цели: список и «Завершить все» через подтверждение почтой', async () => {
    staffServer({ accounts: [...TEAM.slice(0, 1), account()] });
    let sessions = [
      session({ current: false }),
      session({ id: 'x2', current: false, user_agent: null }),
    ];
    let revoked = 0;
    server.use(
      http.get('/v1/staff/accounts/{staffId}/sessions', ({ response }) =>
        response(200).json({ items: sessions }),
      ),
      http.post('/v1/staff/accounts/{staffId}/sessions/revoke-all', ({ response }) => {
        revoked++;
        sessions = [];
        return response(200).json({ revoked: 2 });
      }),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const panel = await manage(user, 'Мария К.');
    const block = region(panel, 'Сеансы');
    const list = await within(block).findByRole('list', { name: 'Сеансы сотрудника' });
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
    expect(list).toHaveTextContent('Chrome · Windows');
    expect(list).toHaveTextContent('Устройство не определено');

    await user.click(within(block).getByRole('button', { name: 'Завершить все сеансы' }));
    const dialog = screen.getByRole('dialog', { name: 'Завершить все сеансы — Мария К.?' });
    await user.type(within(dialog).getByLabelText(/Введите/), 'maria.k@wf.example');
    await user.click(within(dialog).getByRole('button', { name: 'Завершить все' }));
    expect(await screen.findByText('Завершено 2 сеанса')).toBeInTheDocument();
    expect(revoked).toBe(1);
    expect(await within(block).findByText('Активных сеансов нет.')).toBeInTheDocument();
  });

  test('админ над модератором: сеансы есть; над приглашённым владельцем — только переотправка', async () => {
    const invitedOwner = account({
      id: '0192f1c4-1111-7b21-9c5d-0000000000f3',
      email: 'new.owner@wf.example',
      display_name: 'Новый В.',
      role: 'owner',
      city_ids: [],
      status: 'invited',
      invite: { purpose: 'invite', expires_at: '2026-10-14T09:00:00Z', expired: false },
    });
    staffServer({ accounts: [account(), invitedOwner] });
    server.use(
      http.get('/v1/staff/accounts/{staffId}/sessions', ({ response }) =>
        response(200).json({ items: [] }),
      ),
    );
    renderApp('/staff', { me: meAdmin });
    const user = userEvent.setup();
    const panel = await manage(user, 'Новый В.');
    expect(within(panel).getByRole('region', { name: 'Приглашение' })).toBeInTheDocument();
    for (const name of ['Роль и города', 'Сеансы', 'Сброс доступа', 'Выключить учётку'])
      expect(within(panel).queryByRole('region', { name })).toBeNull();
  });
});
