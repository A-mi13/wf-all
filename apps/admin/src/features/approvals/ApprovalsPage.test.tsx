import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import {
  approvalsServer,
  IGOR,
  INCOMING,
  MINE_FAILED,
  MINE_PENDING,
  MINE_SYSTEM,
} from '../../test/approvals';
import { renderApp } from '../../test/app';
import { meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { account, approval, staffServer } from '../../test/staff';

// «Подтверждения» (*; спека §9.4 п. 6, §4.8, D17, D22): вкладки «Входящие» (incoming/pending) и
// «Мои» (mine, все статусы), карточка из payload, «Подтвердить» — сразу, без окна (e2e Task 16),
// итог — по статусу ответа, а не по коду 200; «Отклонить» — с обязательным комментарием; «Отменить» —
// свои ожидающие; ошибки — тост и перечитывание; на телефоне решения по staff.* скрыты.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

// Цели запросов (карточки сотрудников) и подписи городов.
const MARIA = account();
function directory() {
  return staffServer({ accounts: [MARIA] });
}

const page = () => screen.findByRole('heading', { level: 1, name: 'Подтверждения' });
const table = (name: string) => screen.findByRole('table', { name });
const rows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1);
const listCalls = (seen: { lists: URLSearchParams[]; background: (string | null)[] }) =>
  seen.lists.filter((_, i) => seen.background[i] === null);

function phone() {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: query.includes('max-width'),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
}

describe('вкладки', () => {
  test('«Входящие» — incoming/pending (не фоном), число во вкладке; «Мои» — mine без статуса, в адресе', async () => {
    directory();
    const { seen } = approvalsServer({ mine: [MINE_PENDING] });
    const app = renderApp('/approvals', { me: meOwner });
    await page();
    const incoming = screen.getByRole('tab', { name: /^Входящие/ });
    expect(incoming).toHaveAttribute('aria-selected', 'true');
    await waitFor(() => expect(incoming).toHaveAccessibleName('Входящие 1'));
    await table('Входящие запросы');
    const first = listCalls(seen)[0];
    expect(first?.get('box')).toBe('incoming');
    expect(first?.get('status')).toBe('pending');

    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: 'Мои' }));
    await table('Мои запросы');
    const mine = listCalls(seen).at(-1);
    expect(mine?.get('box')).toBe('mine');
    expect(mine?.has('status')).toBe(false);
    expect(app.at()).toBe('/approvals?box=mine');
  });

  test('вкладка из адреса переживает перезагрузку', async () => {
    directory();
    approvalsServer({ mine: [MINE_PENDING] });
    renderApp('/approvals?box=mine', { me: meOwner });
    await page();
    expect(screen.getByRole('tab', { name: 'Мои' })).toHaveAttribute('aria-selected', 'true');
    expect(await table('Мои запросы')).toBeInTheDocument();
  });

  test('пусто — своё состояние вкладки', async () => {
    approvalsServer({ incoming: [] });
    renderApp('/approvals', { me: meOwner });
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
  });

  test('«Показать ещё» — следующая страница по курсору', async () => {
    directory();
    const second = approval({
      id: '0192f1e2-4c5d-7e6f-a081-0000000000b2',
      payload: {
        v: 1,
        email: 'petr@wf.example',
        display_name: 'Пётр С.',
        role: 'support',
        city_ids: [],
      },
    });
    const { seen } = approvalsServer({ incoming: [INCOMING, second], pageSize: 1 });
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    expect(rows(t)).toHaveLength(1);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Показать ещё' }));
    await waitFor(() => expect(rows(t)).toHaveLength(2));
    expect(listCalls(seen).at(-1)?.get('cursor')).toBe('1');
    expect(t).toHaveTextContent('Пётр С.');
  });
});

describe('карточка запроса', () => {
  test('приглашение: действие, кто и когда, кому — имя, почта как есть (xn--), роль, город, срок', async () => {
    directory();
    approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    expect(row).toHaveTextContent('Приглашение сотрудника');
    expect(row).toHaveTextContent(IGOR.label);
    expect(row).toHaveTextContent('Иван П.');
    expect(within(row!).getByText('ivan@xn--d1acufc.xn--p1ai')).toBeInTheDocument();
    expect(row).toHaveTextContent('модератор');
    expect(await within(row!).findByText(/Ставрополь/)).toBeInTheDocument();
    expect(row).toHaveTextContent('ждёт решения');
    expect(row).toHaveTextContent(/до /);
  });

  test('смена роли: цель по id (карточка сотрудника), было → стало', async () => {
    directory();
    approvalsServer({ mine: [MINE_FAILED] });
    renderApp('/approvals?box=mine', { me: meOwner });
    const [row] = rows(await table('Мои запросы'));
    expect(row).toHaveTextContent('Смена роли или городов');
    expect(await within(row!).findByText(/Мария К\./)).toBeInTheDocument();
    expect(row).toHaveTextContent('модератор → поддержка');
  });

  test('решённые: статус, кто решил, комментарий, причина неудачи; системная отмена — «система»', async () => {
    directory();
    approvalsServer({ mine: [MINE_FAILED, MINE_SYSTEM] });
    renderApp('/approvals?box=mine', { me: meOwner });
    const [failed, system] = rows(await table('Мои запросы'));
    expect(failed).toHaveTextContent('не исполнен');
    expect(failed).toHaveTextContent(IGOR.label);
    expect(failed).toHaveTextContent('Комментарий: ок');
    expect(failed).toHaveTextContent('Причина: Нельзя оставить платформу без владельца');
    expect(system).toHaveTextContent('отменён');
    expect(system).toHaveTextContent('система');
    expect(system).toHaveTextContent('Причина: Инициатор изменился после запроса');
    // решённые — без кнопок
    expect(within(failed!).queryByRole('button')).toBeNull();
  });

  test('неизвестный код неудачи — общий текст, не сырой код и не пусто', async () => {
    directory();
    approvalsServer({ mine: [{ ...MINE_FAILED, error: 'staff.brand_new_reason' }] });
    renderApp('/approvals?box=mine', { me: meOwner });
    const [row] = rows(await table('Мои запросы'));
    expect(row).toHaveTextContent('Причина: Действие не выполнено');
    expect(row).not.toHaveTextContent('brand_new_reason');
  });
});

describe('подтвердить', () => {
  test('сразу, без окна: executed — тост успеха, списки и счётчик перечитаны', async () => {
    directory();
    const { seen } = approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const lists = seen.lists.length;
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(await screen.findByText('Подтверждено и выполнено.')).toBeInTheDocument();
    expect(seen.decisions).toHaveLength(1);
    expect(seen.decisions[0]).toMatchObject({ kind: 'approve', id: INCOMING.id });
    expect(seen.decisions[0]?.key).toBeTruthy();
    await waitFor(() => expect(seen.lists.length).toBeGreaterThan(lists));
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
  });

  test('executed — учётки перечитаны (карточка цели)', async () => {
    const dir = directory();
    const roleChange = approval({
      id: '0192f1e2-4c5d-7e6f-a081-0000000000c1',
      action: 'staff.role_change',
      target_id: MARIA.id,
      payload: { v: 1, target_id: MARIA.id, version: 3, from_role: 'moderator', role: 'support' },
      requested_by: IGOR,
    });
    approvalsServer({ incoming: [roleChange] });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    await within(row!).findByText(/Мария К\./);
    const cards = dir.seen.cards.length;
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    await waitFor(() => expect(dir.seen.cards.length).toBeGreaterThan(cards));
  });

  test('200, но failed — тост-ошибка по error, не «успех»', async () => {
    directory();
    approvalsServer({
      answer: {
        approve: (a) => ({ ...a, status: 'failed', decided_by: null, error: 'staff.last_owner' }),
      },
    });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    expect(
      await screen.findByText(
        'Подтверждено, но не выполнено: Нельзя оставить платформу без владельца',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText('Подтверждено и выполнено.')).toBeNull();
  });

  test.each([
    [404, 'platform.approval_not_found', 'Запрос на подтверждение не найден'],
    [409, 'platform.approval_self', 'Нельзя решать свой запрос или запрос о себе'],
    [409, 'platform.approval_stale', 'Данные изменились после запроса — создайте его заново'],
    [409, 'platform.approval_closed', 'Запрос уже решён, отменён или просрочен'],
    [409, 'staff.last_owner', 'Нельзя оставить платформу без владельца'],
    [409, 'staff.email_taken', 'Сотрудник с такой почтой уже есть'],
  ])('%s %s — тост с текстом и перечитывание списка', async (status, code, text) => {
    directory();
    const { seen } = approvalsServer({ answer: { approve: () => problem(status, code) } });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const lists = listCalls(seen).length;
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    await waitFor(() => expect(listCalls(seen).length).toBeGreaterThan(lists));
  });

  test('двойной клик — один запрос', async () => {
    directory();
    const { seen } = approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const approve = within(row!).getByRole('button', { name: 'Подтвердить' });
    // два клика в одном такте — до перерисовки
    fireEvent.click(approve);
    fireEvent.click(approve);
    await screen.findByText('Подтверждено и выполнено.');
    expect(seen.decisions).toHaveLength(1);
  });
});

describe('отклонить', () => {
  test('без комментария (и из одних пробелов) — не уходит; с комментарием — reject {comment}', async () => {
    directory();
    const { seen } = approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    expect(await within(dialog).findByText('Заполните поле')).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), '   ');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    expect(within(dialog).getByText('Заполните поле')).toBeInTheDocument();
    expect(seen.decisions).toHaveLength(0);

    await user.clear(within(dialog).getByLabelText('Почему отклоняете'));
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'Рано выдавать город');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    expect(await screen.findByText('Запрос отклонён.')).toBeInTheDocument();
    expect(seen.decisions).toEqual([
      expect.objectContaining({ kind: 'reject', body: { comment: 'Рано выдавать город' } }),
    ]);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('409 approval_closed — тост, окно закрыто, список перечитан', async () => {
    directory();
    const { seen } = approvalsServer({
      answer: { reject: () => problem(409, 'platform.approval_closed') },
    });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'нет');
    const lists = listCalls(seen).length;
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    expect(await screen.findByText('Запрос уже решён, отменён или просрочен')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(listCalls(seen).length).toBeGreaterThan(lists));
  });
});

describe('отменить', () => {
  test('у своих ожидающих — «Отменить» → cancel, тост; у решённых кнопки нет', async () => {
    directory();
    const { seen } = approvalsServer({ mine: [MINE_PENDING, MINE_FAILED] });
    renderApp('/approvals?box=mine', { me: meOwner });
    const [pending, failed] = rows(await table('Мои запросы'));
    expect(within(failed!).queryByRole('button', { name: 'Отменить' })).toBeNull();
    expect(within(pending!).queryByRole('button', { name: 'Подтвердить' })).toBeNull();
    await userEvent.setup().click(within(pending!).getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Запрос отменён.')).toBeInTheDocument();
    expect(seen.decisions).toEqual([
      expect.objectContaining({ kind: 'cancel', id: MINE_PENDING.id }),
    ]);
  });
});

describe('телефон', () => {
  test('решения по staff.* скрыты — «Выдача прав — только с компьютера», чтение есть', async () => {
    phone();
    directory();
    approvalsServer();
    renderApp('/approvals', { me: meOwner });
    await page();
    const [row] = rows(await table('Входящие запросы'));
    expect(row).toHaveTextContent('Иван П.');
    expect(within(row!).queryByRole('button', { name: 'Подтвердить' })).toBeNull();
    expect(within(row!).queryByRole('button', { name: 'Отклонить' })).toBeNull();
    expect(row).toHaveTextContent('Выдача прав — только с компьютера');
  });
});
