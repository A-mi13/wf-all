import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { COUNT_KEY } from './queries';
import { approvalsServer, INCOMING } from '../../test/approvals';
import { renderApp } from '../../test/app';
import { meOwner } from '../../test/me';
import { http, server } from '../../test/server';
import { account, approval, staffServer, TEAM } from '../../test/staff';

// Счётчик «Подтверждения» в меню (спека §9.3, D10): GET /v1/approvals?box=incoming&status=pending
// фоном (X-WF-Background: 1 — опрос не продлевает бездействие), раз в минуту и при возврате во
// вкладку; любой 202 {approval} и решения — перечитывание сразу.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.useRealTimers();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
});

// «Мой доступ» — экран без подтверждений: запросы /v1/approvals идут только от счётчика.
function meServer() {
  server.use(
    http.get('/v1/staff/me/mfa', ({ response }) =>
      response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 8 }),
    ),
    http.get('/v1/staff/me/sessions', ({ response }) => response(200).json({ items: [] })),
  );
}

const counter = () => screen.getByRole('link', { name: /^Подтверждения/ });
const counts = (seen: { lists: URLSearchParams[] }) => seen.lists.length;

describe('счётчик в меню', () => {
  test('число у пункта; запрос — incoming/pending с X-WF-Background: 1', async () => {
    meServer();
    const { seen } = approvalsServer({
      incoming: [INCOMING, approval({ id: '0192f1e2-4c5d-7e6f-a081-0000000000b2' })],
    });
    renderApp('/me', { me: meOwner });
    await waitFor(() =>
      expect(counter()).toHaveAccessibleName('Подтверждения 2 запроса ждут решения'),
    );
    expect(seen.lists[0]?.get('box')).toBe('incoming');
    expect(seen.lists[0]?.get('status')).toBe('pending');
    expect(seen.background).toEqual(['1']);
  });

  test('больше 99 — «99+»', async () => {
    meServer();
    const many = Array.from({ length: 120 }, (_, i) =>
      approval({ id: `0192f1e2-4c5d-7e6f-a081-${String(i).padStart(12, '0')}` }),
    );
    approvalsServer({ incoming: many, pageSize: 100 });
    renderApp('/me', { me: meOwner });
    await waitFor(() => expect(within(counter()).getByText('99+')).toBeInTheDocument());
  });

  test('опрос раз в 60 с; в скрытой вкладке не идёт, по возвращении — сразу', async () => {
    meServer();
    const { seen } = approvalsServer();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const app = renderApp('/me', { me: meOwner });
    const fetching = () => app.queryClient.getQueryState(COUNT_KEY)?.fetchStatus;
    await waitFor(() => expect(counts(seen)).toBe(1));
    act(() => vi.advanceTimersByTime(59_000));
    expect(counts(seen)).toBe(1);
    act(() => vi.advanceTimersByTime(1_500));
    await waitFor(() => expect(counts(seen)).toBe(2));
    expect(seen.background).toEqual(['1', '1']);

    // вкладка скрыта: таймер не опрашивает
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    fireEvent(window, new Event('visibilitychange'));
    await waitFor(() => expect(fetching()).toBe('idle'));
    act(() => vi.advanceTimersByTime(180_000));
    // запрос интервала ставит fetching синхронно — здесь видно, что его не было
    expect(fetching()).toBe('idle');
    expect(counts(seen)).toBe(2);

    // вернулись во вкладку — опрос сразу
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    fireEvent(window, new Event('visibilitychange'));
    await waitFor(() => expect(counts(seen)).toBe(3));
  });

  test('202 приглашения — счётчик перечитан сразу', async () => {
    staffServer({ accounts: TEAM });
    const { seen } = approvalsServer({ incoming: [] });
    server.use(
      http.post('/v1/staff/invites', ({ response }) =>
        response(202).json({ approval: approval() }),
      ),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Пригласить сотрудника' }));
    const dialog = screen.getByRole('dialog', { name: 'Приглашение сотрудника' });
    await user.type(within(dialog).getByLabelText('Рабочая почта'), 'ivan.p@wf.example');
    await user.type(within(dialog).getByLabelText('Имя'), 'Иван П.');
    await user.selectOptions(within(dialog).getByLabelText('Роль'), 'support');
    const before = counts(seen);
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(dialog).findByText('Отправлено на подтверждение');
    await waitFor(() => expect(counts(seen)).toBeGreaterThan(before));
  });

  test('202 смены роли в панели сотрудника — счётчик перечитан сразу', async () => {
    staffServer({ accounts: [...TEAM.slice(0, 1), account()] });
    const { seen } = approvalsServer({ incoming: [] });
    server.use(
      http.get('/v1/staff/accounts/{staffId}/sessions', ({ response }) =>
        response(200).json({ items: [] }),
      ),
      http.put('/v1/staff/accounts/{staffId}/role', ({ response, params }) =>
        response(202).json({
          approval: approval({ action: 'staff.role_change', target_id: params.staffId }),
        }),
      ),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await user.click(await within(table).findByRole('button', { name: 'Управлять: Мария К.' }));
    const panel = screen.getByRole('dialog', { name: 'Мария К.' });
    const block = within(panel).getByRole('region', { name: 'Роль и города' });
    await user.selectOptions(within(block).getByLabelText('Роль'), 'support');
    const before = counts(seen);
    await user.click(within(block).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(panel).findByText('Отправлено на подтверждение');
    await waitFor(() => expect(counts(seen)).toBeGreaterThan(before));
  });
});
