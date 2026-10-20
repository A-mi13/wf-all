import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { components } from '../../api/gen/admin';
import { reauthBus } from '../../app/reauth';
import {
  approvalsServer,
  IGOR,
  INCOMING,
  MINE_FAILED,
  MINE_PENDING,
  MINE_SYSTEM,
  OLGA,
} from '../../test/approvals';
import { renderApp } from '../../test/app';
import { CITY_OTHER, CITY_OWN, meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { account, approval, staffServer } from '../../test/staff';

type Approval = components['schemas']['Approval'];

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

  // Контракт (d347777): 200 — только executed, неудача исполнения — ошибка с кодом (ниже). 200 failed
  // отвечал сервер до правки контракта — ветка оставлена защитой (скользящий деплой), мок — старый сервер.
  test('старый сервер: 200, но failed — тост-ошибка по error, не «успех»', async () => {
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
    [422, 'staff.city_not_found', 'Подтверждено, но не выполнено: Город не найден'],
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

  // ⚠️ ревью 10b п. 3: исход approve неизвестен — 500 (сбой после коммита, обрыв) или 409
  // idempotency.replay_unavailable (ключ закоммичен, ответа нет). Повтор мутации ошибочен (тем же ключом —
  // снова replay_unavailable, новым — approval_closed): запрос перечитывается (входящие, все состояния),
  // строка и тост — по его итогу.
  test.each([
    {
      name: '500 internal → выполнен',
      answer: () => problem(500, 'internal'),
      outcome: { status: 'executed' as const, error: null },
      toast: 'Подтверждено и выполнено.',
      row: ['исполнен'],
    },
    {
      name: '409 idempotency.replay_unavailable → не выполнен',
      answer: () => problem(409, 'idempotency.replay_unavailable'),
      outcome: { status: 'failed' as const, error: 'staff.email_taken' },
      toast: 'Подтверждено, но не выполнено: Сотрудник с такой почтой уже есть',
      row: ['не исполнен', 'Причина: Сотрудник с такой почтой уже есть'],
    },
    {
      name: 'обрыв сети → выполнен',
      answer: () => HttpResponse.error(),
      outcome: { status: 'executed' as const, error: null },
      toast: 'Подтверждено и выполнено.',
      row: ['исполнен'],
    },
    // уборка №8 m6: 408/499 прокси и 2xx без тела — тоже неизвестный исход
    {
      name: '408 прокси → выполнен',
      answer: () => new HttpResponse('Request Timeout', { status: 408 }),
      outcome: { status: 'executed' as const, error: null },
      toast: 'Подтверждено и выполнено.',
      row: ['исполнен'],
    },
    {
      name: '499 прокси → выполнен',
      answer: () => new HttpResponse(null, { status: 499 }),
      outcome: { status: 'executed' as const, error: null },
      toast: 'Подтверждено и выполнено.',
      row: ['исполнен'],
    },
    {
      name: '200 без тела → выполнен',
      answer: () => new HttpResponse(null, { status: 200 }),
      outcome: { status: 'executed' as const, error: null },
      toast: 'Подтверждено и выполнено.',
      row: ['исполнен'],
    },
  ])('исход неизвестен: $name — без повторного POST, итог из перечитанного запроса', async (c) => {
    directory();
    let record: (a: Approval) => void = () => {};
    const srv = approvalsServer({
      answer: {
        approve: (a) => {
          record({ ...a, ...c.outcome, decided_by: OLGA, decided_at: '2026-10-13T12:00:00Z' });
          return c.answer();
        },
      },
    });
    record = srv.record;
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    expect(await screen.findByText(c.toast)).toBeInTheDocument();
    // перечитан сам запрос: входящие без фильтра состояния
    expect(listCalls(srv.seen).some((q) => q.get('box') === 'incoming' && !q.has('status'))).toBe(
      true,
    );
    const [row] = rows(t);
    for (const text of c.row) expect(row).toHaveTextContent(text);
    expect(within(row!).queryByRole('button', { name: 'Подтвердить' })).toBeNull();
    expect(srv.seen.decisions).toHaveLength(1);
  });

  test('исход неизвестен: запрос не на первой странице всех состояний — перечитываются следующие', async () => {
    directory();
    // новее и уже решён: во «Входящих» его не видно (status=pending), а при перечитывании он первый
    const newer = approval({
      id: '0192f1e2-4c5d-7e6f-a081-0000000000d1',
      status: 'executed',
      requested_at: '2026-10-13T11:00:00Z',
      decided_by: OLGA,
      decided_at: '2026-10-13T11:30:00Z',
      requested_by: IGOR,
    });
    let record: (a: Approval) => void = () => {};
    const srv = approvalsServer({
      incoming: [newer, INCOMING],
      pageSize: 1,
      answer: {
        approve: (a) => {
          record({
            ...a,
            status: 'executed',
            decided_by: OLGA,
            decided_at: '2026-10-13T12:00:00Z',
          });
          return problem(500, 'internal');
        },
      },
    });
    record = srv.record;
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    expect(rows(t)).toHaveLength(1);
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    expect(await screen.findByText('Подтверждено и выполнено.')).toBeInTheDocument();
    expect(rows(t)[0]).toHaveTextContent('исполнен');
    expect(srv.seen.decisions).toHaveLength(1);
  });

  test('исход неизвестен, а запрос всё ещё ждёт (сбой до коммита) — тост ошибки, кнопка снова доступна', async () => {
    directory();
    const { seen } = approvalsServer({ answer: { approve: () => problem(500, 'internal') } });
    renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    expect(await screen.findByText('Что-то пошло не так. Попробуйте ещё раз')).toBeInTheDocument();
    await waitFor(() =>
      expect(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' })).toBeEnabled(),
    );
    expect(rows(t)[0]).toHaveTextContent('ждёт решения');
    expect(seen.decisions).toHaveLength(1);
  });

  // Ревью I2: решённый R1 не уходит из списка сразу — строки не сдвигаются, и под курсором в первой
  // строке нет кнопки, которой второй клик решил бы R2.
  test('успех: R1 остаётся на месте с итогом и без кнопки, R2 не сдвигается', async () => {
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
    expect(rows(t)[1]).toHaveTextContent('Пётр С.');
    expect(seen.decisions).toHaveLength(1);
  });

  // Ревью m1: запрос списка, ушедший до решения («Показать ещё»), по завершении записал бы страницы,
  // взятые до решения, поверх итога — R1 снова pending с кнопками (второй клик → 409). Полёт
  // отменяется до записи итога, а догрузка после итога повторяется (уборка №8: курсор сервера — keyset,
  // страница после последней строки от решения не сдвигается) — «Показать ещё» не теряется молча.
  test('список в полёте («Показать ещё») не возвращает решённую строку с кнопкой, догрузка повторена', async () => {
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
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const { seen } = approvalsServer({
      incoming: [INCOMING, second],
      pageSize: 1,
      hold: (q) => (q.has('cursor') ? gate : undefined),
    });
    const app = renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    expect(rows(t)).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Показать ещё' }));
    await waitFor(() => expect(listCalls(seen).some((q) => q.has('cursor'))).toBe(true));
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    release();
    await waitFor(() => expect(rows(t)).toHaveLength(2));
    await waitFor(() => expect(app.queryClient.isFetching()).toBe(0));
    const first = rows(t)[0]!;
    expect(first).toHaveTextContent('исполнен');
    expect(within(first).queryByRole('button', { name: 'Подтвердить' })).toBeNull();
    expect(rows(t)[1]).toHaveTextContent('Пётр С.');
    // повтор — с тем же курсором (после R1), не с начала
    expect(listCalls(seen).filter((q) => q.get('cursor') === '1')).toHaveLength(2);
    expect(seen.decisions).toHaveLength(1);
  });

  // Уборка №8: повторяется только догрузка; отменённое обновление всего списка (возврат во вкладку) — нет:
  // оно сдвинуло бы строки под курсором (I2), а «Показать ещё» пользователь не нажимал.
  test('обновление всего списка в полёте при решении — не повторяется и страниц не добавляет', async () => {
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
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    let held = false;
    const { seen } = approvalsServer({
      incoming: [INCOMING, second],
      pageSize: 1,
      hold: (_, background) => (held && !background ? gate : undefined),
    });
    const app = renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    held = true;
    const lists = listCalls(seen).length;
    void app.queryClient.refetchQueries({ queryKey: ['approvals', 'list', 'incoming'] });
    await waitFor(() => expect(listCalls(seen).length).toBeGreaterThan(lists));
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    release();
    await waitFor(() => expect(app.queryClient.isFetching()).toBe(0));
    expect(rows(t)).toHaveLength(1);
    expect(rows(t)[0]).toHaveTextContent('исполнен');
    expect(listCalls(seen).some((q) => q.has('cursor'))).toBe(false);
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
    // не найден — строки (и кнопки-открывателя) больше нет: фокус на списке, не на body (ревью m5)
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(screen.getByRole('tabpanel').contains(document.activeElement)).toBe(true);
  });

  // Ревью m5: запрос решён другим — перечитывание убрало строку вместе с «Отклонить», куда окно
  // вернуло бы фокус; окно закрывается после перечитывания — фокус на списке, не на body.
  test('409 approval_closed — тост, окно закрыто, список перечитан, фокус на списке', async () => {
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
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(screen.getByRole('tabpanel').contains(document.activeElement)).toBe(true);
  });
});

// Уборка №8 m4: исход отклонения неизвестен — решение не повторяется, запрос перечитывается; итог
// «отклонён» — успех (тост, окно закрыто); всё ещё ждёт — ошибка в окне, комментарий цел.
describe('отклонить — исход неизвестен', () => {
  async function rejectWith(comment: string) {
    const t = await table('Входящие запросы');
    const user = userEvent.setup();
    await user.click(within(rows(t)[0]!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), comment);
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    return { t, dialog };
  }

  test('500, а запрос отклонён — тост «отклонён», окно закрыто, без повторного POST', async () => {
    directory();
    let record: (a: Approval) => void = () => {};
    const srv = approvalsServer({
      answer: {
        reject: (a) => {
          record({
            ...a,
            status: 'rejected',
            decided_by: OLGA,
            decided_at: '2026-10-13T12:00:00Z',
          });
          return problem(500, 'internal');
        },
      },
    });
    record = srv.record;
    renderApp('/approvals', { me: meOwner });
    const { t } = await rejectWith('Рано');
    expect(await screen.findByText('Запрос отклонён.')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.queryByText('Что-то пошло не так. Попробуйте ещё раз')).toBeNull();
    await waitFor(() => expect(rows(t)[0]).toHaveTextContent('отклонён'));
    expect(srv.seen.decisions).toHaveLength(1);
  });

  test('обрыв, а запрос всё ещё ждёт — ошибка в окне, комментарий цел', async () => {
    directory();
    const { seen } = approvalsServer({ answer: { reject: () => HttpResponse.error() } });
    renderApp('/approvals', { me: meOwner });
    const { dialog } = await rejectWith('Рано');
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Что-то пошло не так. Попробуйте ещё раз',
    );
    expect(within(dialog).getByLabelText('Почему отклоняете')).toHaveValue('Рано');
    expect(seen.decisions).toHaveLength(1);
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

  // Ревью m4: итог отмены — на месте (useSettleDecision('mine')): строка с чипом «отменён» и без
  // кнопки сразу, не дожидаясь перечитывания «Моих» (его держим воротами).
  test('отменили — строка на месте с чипом «отменён» и без кнопки, без перечитывания', async () => {
    directory();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    let held = false;
    approvalsServer({
      mine: [MINE_PENDING],
      hold: (_, background) => (held && !background ? gate : undefined),
    });
    renderApp('/approvals?box=mine', { me: meOwner });
    const t = await table('Мои запросы');
    held = true;
    await userEvent.setup().click(within(rows(t)[0]!).getByRole('button', { name: 'Отменить' }));
    await screen.findByText('Запрос отменён.');
    await waitFor(() => expect(rows(t)[0]).toHaveTextContent('отменён'));
    expect(within(rows(t)[0]!).queryByRole('button')).toBeNull();
    release();
  });
});

// Уборка №8 m4: исход отмены неизвестен (сбой после коммита, обрыв) — как у «Подтвердить»: решение не
// повторяется, запрос перечитывается («Мои», все состояния), тост — по его итогу.
describe('отменить — исход неизвестен', () => {
  test('500, а запрос отменён — «Запрос отменён.», без повторного POST', async () => {
    directory();
    let record: (a: Approval) => void = () => {};
    const srv = approvalsServer({
      mine: [MINE_PENDING],
      answer: {
        cancel: (a) => {
          record({
            ...a,
            status: 'cancelled',
            decided_by: OLGA,
            decided_at: '2026-10-13T12:00:00Z',
          });
          return problem(500, 'internal');
        },
      },
    });
    record = srv.record;
    renderApp('/approvals?box=mine', { me: meOwner });
    const t = await table('Мои запросы');
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Запрос отменён.')).toBeInTheDocument();
    expect(screen.queryByText('Что-то пошло не так. Попробуйте ещё раз')).toBeNull();
    expect(listCalls(srv.seen).some((q) => q.get('box') === 'mine' && !q.has('status'))).toBe(true);
    await waitFor(() => expect(rows(t)[0]).toHaveTextContent('отменён'));
    expect(srv.seen.decisions).toHaveLength(1);
  });

  test('обрыв, а запрос всё ещё ждёт — тост ошибки, кнопка снова доступна', async () => {
    directory();
    const { seen } = approvalsServer({
      mine: [MINE_PENDING],
      answer: { cancel: () => HttpResponse.error() },
    });
    renderApp('/approvals?box=mine', { me: meOwner });
    const t = await table('Мои запросы');
    fireEvent.click(within(rows(t)[0]!).getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Что-то пошло не так. Попробуйте ещё раз')).toBeInTheDocument();
    await waitFor(() =>
      expect(within(rows(t)[0]!).getByRole('button', { name: 'Отменить' })).toBeEnabled(),
    );
    expect(seen.decisions).toHaveLength(1);
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

// Ревью ветки, I1: вкладка A была скрыта, в другой вкладке A вышел и вошёл B — cookie общая, и при
// возврате во вкладку me сразу B, без 401 между. Входящие A и комментарий его отклонения не должны
// мелькнуть у B, пока грузится его список.
describe('общее устройство — сотрудник сменился без выхода', () => {
  test('me A → me B: список A пропал до ответа B, кэш мутаций A стёрт', async () => {
    directory();
    approvalsServer();
    const app = renderApp('/approvals', { me: meOwner });
    const [row] = rows(await table('Входящие запросы'));
    expect(row).toHaveTextContent('Иван П.');
    const user = userEvent.setup();
    await user.click(within(row!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    await user.type(within(dialog).getByLabelText('Почему отклоняете'), 'Рано выдавать город');
    await user.click(within(dialog).getByRole('button', { name: 'Отклонить' }));
    await screen.findByText('Запрос отклонён.');

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
    expect(
      JSON.stringify(
        app.queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables),
      ),
    ).not.toContain('Рано выдавать город');
    release();
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
  });
});

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

// У решённого запроса уходит кнопка, на которой стоял фокус (или куда окно «Отклонить» вернуло бы
// его); строка остаётся с итогом (при ошибке перечитывание убирает и строку): фокус не должен падать
// на body.
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

  // Ревью m5: пока окно «Отклонить» открыто, перечитывание (решил другой, вернулись во вкладку) убрало
  // строку вместе с открывателем; окно закрыли — вернуть фокус некуда, он на списке, а не на body.
  test('окно «Отклонить» закрыли после того, как строку убрало перечитывание, — фокус на списке', async () => {
    directory();
    const { state } = approvalsServer();
    const app = renderApp('/approvals', { me: meOwner });
    const t = await table('Входящие запросы');
    const user = userEvent.setup();
    await user.click(within(rows(t)[0]!).getByRole('button', { name: 'Отклонить' }));
    const dialog = screen.getByRole('dialog', { name: 'Отклонить запрос?' });
    state.incoming = [];
    await app.queryClient.invalidateQueries({ queryKey: ['approvals'] });
    expect(await screen.findByText('Входящих нет')).toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(screen.getByRole('tabpanel').contains(document.activeElement)).toBe(true);
  });
});
