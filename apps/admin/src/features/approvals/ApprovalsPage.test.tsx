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
import { CITY_OTHER, CITY_OWN, meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
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

  // Ревью I1: подтверждающий видит дельту городов — «было → стало», пустой список — «без городов».
  test.each([
    [
      'добавлен город',
      {
        from_role: 'moderator',
        from_city_ids: [CITY_OWN],
        role: 'moderator',
        city_ids: [CITY_OWN, CITY_OTHER],
      },
      'Ставрополь, Ставропольский край → Ставрополь, Ставропольский край; Пятигорск, Ставропольский край',
    ],
    [
      'сняты все города (модератор → поддержка)',
      {
        from_role: 'moderator',
        from_city_ids: [CITY_OWN, CITY_OTHER],
        role: 'support',
        city_ids: [],
      },
      'Ставрополь, Ставропольский край; Пятигорск, Ставропольский край → без городов',
    ],
    [
      'города выданы (поддержка → модератор)',
      { from_role: 'support', from_city_ids: [], role: 'moderator', city_ids: [CITY_OTHER] },
      'без городов → Пятигорск, Ставропольский край',
    ],
  ])('смена роли: города «было → стало» — %s', async (_, change, text) => {
    directory();
    approvalsServer({
      incoming: [
        approval({
          id: '0192f1e2-4c5d-7e6f-a081-0000000000d1',
          action: 'staff.role_change',
          target_id: MARIA.id,
          payload: { v: 1, target_id: MARIA.id, version: 3, ...change },
          requested_by: IGOR,
        }),
      ],
    });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const cities = await within(row!).findByRole('definition', { name: 'Города' });
    await waitFor(() => expect(cities).toHaveTextContent(text));
  });

  test('без staff.accounts.read — цель «сотрудник <id8>», карточка не запрашивается', async () => {
    const dir = directory();
    approvalsServer({ mine: [MINE_PENDING] });
    const permissions = { ...meOwner.permissions };
    delete permissions['staff.accounts.read'];
    renderApp('/approvals?box=mine', { me: { ...meOwner, permissions } });
    const [row] = rows(await table('Мои запросы'));
    expect(row).toHaveTextContent('сотрудник 0192f1c4');
    expect(dir.seen.cards).toEqual([]);
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
    // счётчик перечитан, а строка остаётся на месте с итогом — без кнопок (ревью I2)
    await waitFor(() => expect(seen.lists.length).toBeGreaterThan(lists));
    const [after] = rows(await table('Входящие запросы'));
    expect(after).toHaveTextContent('Иван П.');
    expect(after).toHaveTextContent('исполнен');
    expect(within(after!).queryByRole('button')).toBeNull();
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
    [409, 'platform.approval_closed', 'Запрос уже решён, отменён или просрочен'],
    // ревью M5: запрос уже помечен failed — «подтверждено, но не выполнено» с причиной
    [
      409,
      'platform.approval_stale',
      'Подтверждено, но не выполнено: Данные изменились после запроса — создайте его заново',
    ],
    [
      409,
      'staff.last_owner',
      'Подтверждено, но не выполнено: Нельзя оставить платформу без владельца',
    ],
    [409, 'staff.email_taken', 'Подтверждено, но не выполнено: Сотрудник с такой почтой уже есть'],
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

  // Ревью I2: решённый R1 не уходит из списка сразу — строки не сдвигаются, и клик «туда же» не
  // становится решением по R2.
  test('успех: R1 остаётся на месте с итогом, клик по первой строке — не решение R2', async () => {
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
      requested_by: IGOR,
    });
    const { seen } = approvalsServer({ incoming: [INCOMING, second] });
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    const before = seen.background.length;
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    // счётчик перечитан — инвалидация после решения закончилась
    await waitFor(() => expect(seen.background.length).toBeGreaterThan(before));
    const first = rows(t)[0]!;
    expect(first).toHaveTextContent('Иван П.');
    expect(within(first).queryByRole('button', { name: 'Подтвердить' })).toBeNull();
    fireEvent.click(first);
    expect(rows(t)[1]).toHaveTextContent('Пётр С.');
    expect(seen.decisions).toHaveLength(1);
  });

  test('ошибка: пока список перечитывается, кнопки строк не оживают', async () => {
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
      requested_by: IGOR,
    });
    const { seen } = approvalsServer({
      incoming: [INCOMING, second],
      answer: { approve: () => problem(409, 'platform.approval_closed') },
    });
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    // перечитывание списка после ошибки держим, пока не проверим кнопки
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/approvals', async ({ request, response }) => {
        if (!request.headers.has('X-WF-Background')) await gate;
        return response(200).json({ items: [second], next_cursor: null });
      }),
    );
    const [r1, r2] = rows(t);
    const approve1 = within(r1!).getByRole('button', { name: 'Подтвердить' });
    fireEvent.click(approve1);
    await screen.findByText('Запрос уже решён, отменён или просрочен');
    fireEvent.click(approve1);
    fireEvent.click(within(r2!).getByRole('button', { name: 'Подтвердить' }));
    expect(within(r2!).getByRole('button', { name: 'Подтвердить' })).toBeDisabled();
    expect(seen.decisions).toHaveLength(1);
    release();
    await waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(seen.decisions).toHaveLength(1);
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

  test.each([
    [409, 'platform.approval_self', 'Нельзя решать свой запрос или запрос о себе'],
    [404, 'platform.approval_not_found', 'Запрос на подтверждение не найден'],
  ])('%s %s при отклонении — тост, окно закрыто', async (status, code, text) => {
    directory();
    approvalsServer({ answer: { reject: () => problem(status, code) } });
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'нет');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    expect(await screen.findByText(text)).toBeInTheDocument();
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

describe('отменить — ошибки', () => {
  test.each([
    [404, 'platform.approval_not_found', 'Запрос на подтверждение не найден'],
    [409, 'platform.approval_closed', 'Запрос уже решён, отменён или просрочен'],
  ])('%s %s — тост и перечитывание списка', async (status, code, text) => {
    directory();
    const { seen } = approvalsServer({
      mine: [MINE_PENDING],
      answer: { cancel: () => problem(status, code) },
    });
    renderApp('/approvals?box=mine', { me: meOwner });
    const [row] = rows(await table('Мои запросы'));
    const lists = listCalls(seen).length;
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    await waitFor(() => expect(listCalls(seen).length).toBeGreaterThan(lists));
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

// Общее устройство: сессия A истекла (401 → вход с return), за компьютер сел B. Кэш запросов живёт
// в памяти вкладки — входящие A не должны мелькнуть у B, пока его список грузится.
describe('общее устройство', () => {
  test('сессия истекла → входит другой: данные прошлого сотрудника не видны до ответа', async () => {
    directory();
    approvalsServer();
    const app = renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    expect(row).toHaveTextContent('Иван П.');

    app.me.current = null;
    await app.refetchMe();
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' });

    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/approvals', async ({ request, response }) => {
        if (!request.headers.has('X-WF-Background')) await gate;
        return response(200).json({ items: [], next_cursor: null });
      }),
    );
    app.me.current = meAdmin;
    await app.refetchMe();
    await page();
    expect(screen.queryByText('Иван П.')).toBeNull();
    release();
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
  });
});

// Решённый запрос уходит из «Входящих» вместе с кнопкой, на которой стоял фокус (или куда окно
// «Отклонить» вернуло бы его): фокус не должен падать на body.
describe('общее устройство — кэш мутаций', () => {
  test('сессия истекла — комментарий отклонения не остаётся в кэше мутаций', async () => {
    directory();
    approvalsServer();
    const app = renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'Рано выдавать город');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    await screen.findByText('Запрос отклонён.');
    const dump = () =>
      JSON.stringify(
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      );
    expect(dump()).toContain('Рано выдавать город');

    app.me.current = null;
    await app.refetchMe();
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' });
    await waitFor(() => expect(dump()).not.toContain('Рано выдавать город'));
  });
});

describe('фокус после решения', () => {
  test('подтвердили — кнопки строки нет, фокус на списке, а не на body', async () => {
    directory();
    approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    await within(row!).findByText('исполнен');
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(screen.getByRole('tabpanel').contains(document.activeElement)).toBe(true);
  });

  test('отклонили — окно закрыто, кнопки строки нет, фокус на списке', async () => {
    directory();
    approvalsServer();
    renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'рано');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    await within(row!).findByText('отклонён');
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(screen.getByRole('tabpanel').contains(document.activeElement)).toBe(true);
  });
});
