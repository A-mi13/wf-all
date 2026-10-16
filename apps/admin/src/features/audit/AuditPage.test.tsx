import { screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { auditRecord, auditServer, LOG, MARIA_ID } from '../../test/audit';
import { renderApp } from '../../test/app';
import { meModerator, meOwner } from '../../test/me';
import { http, server } from '../../test/server';

// «Журнал действий» (спека §9.4 п. 7, §5.3; макет 07): вкладки по разделам → action_prefix, фильтры
// (кто, действие, объект, период) — в адресе, период — местные даты → UTC, строка «время · кто · тег ·
// действие · объект · id», клик по имени — фильтр по сотруднику, раскрытие «было/стало» (маскировано
// сервером, показывается как текст), пусто — «Ничего не нашлось… Это не ошибка» со сбросом.

const OLGA_ID = '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d';
const TZ = process.env.TZ;

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.unstubAllGlobals();
  if (TZ === undefined) delete process.env.TZ;
  else process.env.TZ = TZ;
});

const page = () => screen.findByRole('heading', { level: 1, name: 'Журнал действий' });
const table = () => screen.findByRole('table', { name: 'Записи журнала' });
const rows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1);
const last = (seen: URLSearchParams[]) => Object.fromEntries(seen.at(-1) ?? []);

async function openFilters(user: UserEvent) {
  await user.click(screen.getByRole('button', { name: /^Ещё фильтры/ }));
}

describe('вкладки и строки', () => {
  test('строка: время, кто, тег, действие, объект, id; operator и system — подписями', async () => {
    auditServer();
    renderApp('/audit', { me: meOwner });
    await page();
    const [role, approval, expired, operator] = rows(await table());
    expect(role).toHaveTextContent('Ольга В.');
    expect(role).toHaveTextContent('сотрудники');
    expect(role).toHaveTextContent('Роль и города изменены');
    expect(role).toHaveTextContent('сотрудник');
    expect(role).toHaveTextContent('0192f1f0');
    expect(approval).toHaveTextContent('подтверждение');
    expect(approval).toHaveTextContent('Запрос подтверждён и исполнен');
    expect(expired).toHaveTextContent('система');
    expect(within(expired!).queryByRole('button', { name: /Показать действия/ })).toBeNull();
    expect(operator).toHaveTextContent('оператор сервера');
  });

  // Ревью M2: импорт GeoNames оператором пишет только роль (actor_label пуст) — подпись по actor_role.
  test('пустой actor_label — подпись по actor_role (operator, system); вид объекта staff_keys', async () => {
    auditServer({
      records: [
        auditRecord({
          id: '0192f1f0-8a9b-7c0d-9e1f-0000000000a1',
          action: 'geo.import',
          object_type: 'geonames_import',
          actor_staff_id: null,
          actor_role: 'operator',
          actor_label: null,
        }),
        auditRecord({
          id: '0192f1f0-8a9b-7c0d-9e1f-0000000000a2',
          action: 'staff.rekeyed',
          object_type: 'staff_keys',
          object_id: null,
          actor_staff_id: null,
          actor_role: 'system',
          actor_label: null,
        }),
        auditRecord({
          id: '0192f1f0-8a9b-7c0d-9e1f-0000000000a3',
          action: 'staff.login_failed',
          object_id: null,
          actor_staff_id: null,
          actor_role: 'anonymous',
          actor_label: null,
        }),
      ],
    });
    renderApp('/audit', { me: meOwner });
    const [imported, rekeyed, anonymous] = rows(await table());
    expect(anonymous).toHaveTextContent('аноним');
    expect(anonymous).not.toHaveTextContent('anonymous');
    expect(imported).toHaveTextContent('оператор сервера');
    expect(rekeyed).toHaveTextContent('система');
    expect(rekeyed).toHaveTextContent('ключи сотрудников');
  });

  test.each([
    ['Сотрудники', 'staff', 'staff.'],
    ['Подтверждения', 'approvals', 'platform.approval_'],
    ['Справочники', 'directories', 'geo.'],
    ['Флаги', 'flags', 'flags.'],
  ])('вкладка «%s» → action_prefix, в адресе', async (name, tab, prefix) => {
    const seen = auditServer();
    const app = renderApp('/audit', { me: meOwner });
    await table();
    expect(last(seen).action_prefix).toBeUndefined();
    await userEvent.setup().click(screen.getByRole('tab', { name }));
    await waitFor(() => expect(last(seen).action_prefix).toBe(prefix));
    expect(app.at()).toBe(`/audit?tab=${tab}`);
  });

  test('«Показать ещё» — следующая страница', async () => {
    const seen = auditServer({ pageSize: 2 });
    renderApp('/audit', { me: meOwner });
    const t = await table();
    expect(rows(t)).toHaveLength(2);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Показать ещё' }));
    await waitFor(() => expect(rows(t)).toHaveLength(4));
    expect(last(seen).cursor).toBe('2');
  });

  test('модератор открывает журнал — сужает сервер (§5.3), экран фильтров сам не добавляет', async () => {
    server.use(
      http.get('/v1/geo/cities', ({ response }) =>
        response(200).json({ items: [], next_cursor: null }),
      ),
    );
    const seen = auditServer();
    renderApp('/audit', { me: meModerator });
    await table();
    expect(last(seen)).toEqual({});
  });
});

describe('фильтры', () => {
  test('клик по имени — фильтр по сотруднику (в адресе и запросе)', async () => {
    const seen = auditServer();
    const app = renderApp('/audit', { me: meOwner });
    const [role] = rows(await table());
    await userEvent
      .setup()
      .click(within(role!).getByRole('button', { name: 'Показать действия: Ольга В.' }));
    await waitFor(() => expect(last(seen).actor_staff_id).toBe(OLGA_ID));
    expect(app.at()).toBe(`/audit?actor=${OLGA_ID}`);
  });

  test('фильтры из адреса переживают перезагрузку: запрос, поля и число заданных', async () => {
    const seen = auditServer();
    process.env.TZ = 'UTC';
    renderApp(
      `/audit?tab=staff&actor=${OLGA_ID}&object_type=staff_account&object_id=${MARIA_ID}&from=2026-10-01&to=2026-10-13`,
      { me: meOwner },
    );
    await table();
    expect(last(seen)).toEqual({
      action_prefix: 'staff.',
      actor_staff_id: OLGA_ID,
      object_type: 'staff_account',
      object_id: MARIA_ID,
      from: '2026-10-01T00:00:00.000Z',
      to: '2026-10-14T00:00:00.000Z',
    });
    expect(screen.getByRole('tab', { name: 'Сотрудники' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    const user = userEvent.setup();
    expect(screen.getByRole('button', { name: 'Ещё фильтры, задано: 5' })).toBeInTheDocument();
    await openFilters(user);
    expect(screen.getByLabelText('Вид объекта')).toHaveValue('staff_account');
    expect(screen.getByLabelText('Id объекта')).toHaveValue(MARIA_ID);
    expect(screen.getByLabelText('Id сотрудника')).toHaveValue(OLGA_ID);
    expect(screen.getByLabelText('С')).toHaveValue('2026-10-01');
    expect(screen.getByLabelText('По')).toHaveValue('2026-10-13');
  });

  test.each([
    ['Europe/Moscow', '2026-09-30T21:00:00.000Z', '2026-10-13T21:00:00.000Z'],
    ['America/New_York', '2026-10-01T04:00:00.000Z', '2026-10-14T04:00:00.000Z'],
  ])('период в поясе %s → from/to в UTC, «по» — включительно', async (tz, from, to) => {
    process.env.TZ = tz;
    const seen = auditServer();
    const app = renderApp('/audit', { me: meOwner });
    await table();
    const user = userEvent.setup();
    await openFilters(user);
    await user.type(screen.getByLabelText('С'), '2026-10-01');
    await user.type(screen.getByLabelText('По'), '2026-10-13');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(last(seen).from).toBe(from));
    expect(last(seen).to).toBe(to);
    expect(app.at()).toBe('/audit?from=2026-10-01&to=2026-10-13');
  });

  test('негодный id и «по» раньше «с» — ошибки полей, запрос не уходит', async () => {
    const seen = auditServer();
    const app = renderApp('/audit', { me: meOwner });
    await table();
    const user = userEvent.setup();
    await openFilters(user);
    await user.type(screen.getByLabelText('Id объекта'), 'не-uuid');
    await user.type(screen.getByLabelText('С'), '2026-10-13');
    await user.type(screen.getByLabelText('По'), '2026-10-01');
    const before = seen.length;
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    expect(await screen.findByText('Неверный формат')).toBeInTheDocument();
    expect(screen.getByText('Неверный период')).toBeInTheDocument();
    expect(seen.length).toBe(before);
    expect(app.at()).toBe('/audit');
  });

  test('действие в фильтре — точнее вкладки', async () => {
    const seen = auditServer();
    renderApp('/audit?tab=staff', { me: meOwner });
    await table();
    const user = userEvent.setup();
    await openFilters(user);
    await user.selectOptions(screen.getByLabelText('Действие'), 'staff.role_changed');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(last(seen).action_prefix).toBe('staff.role_changed'));
  });

  test('смена вкладки сбрасывает действие (оно внутри раздела), остальные фильтры остаются', async () => {
    const seen = auditServer();
    const app = renderApp(`/audit?tab=staff&action=staff.login&actor=${OLGA_ID}`, { me: meOwner });
    await page();
    await waitFor(() => expect(last(seen).action_prefix).toBe('staff.login'));
    await userEvent.setup().click(screen.getByRole('tab', { name: 'Подтверждения' }));
    await waitFor(() => expect(last(seen).action_prefix).toBe('platform.approval_'));
    expect(last(seen).actor_staff_id).toBe(OLGA_ID);
    expect(app.at()).toBe(`/audit?tab=approvals&actor=${OLGA_ID}`);
  });

  test('пусто с фильтрами — «Ничего не нашлось… Это не ошибка» и «Сбросить фильтры»', async () => {
    const seen = auditServer({ records: [] });
    const app = renderApp(`/audit?tab=staff&actor=${OLGA_ID}`, { me: meOwner });
    await page();
    expect(await screen.findByText('Ничего не нашлось')).toBeInTheDocument();
    expect(screen.getByText(/Это не ошибка — просто пусто/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getAllByRole('button', { name: 'Сбросить фильтры' })[0]!);
    await waitFor(() => expect(app.at()).toBe('/audit?tab=staff'));
    expect(last(seen)).toEqual({ action_prefix: 'staff.' });
  });
});

describe('было и стало', () => {
  test('раскрытие: было/стало как текст (без HTML), причина, сеть, браузер', async () => {
    auditServer({
      records: [
        auditRecord({
          after: { role: 'moderator', note: '<img src=x onerror="alert(1)">' },
        }),
      ],
    });
    renderApp('/audit', { me: meOwner });
    const [row] = rows(await table());
    const toggle = within(row!).getByRole('button', { name: 'Подробнее' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await userEvent.setup().click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const details = within(row!).getByRole('region', { name: /Подробности записи/ });
    expect(details).toHaveTextContent('"role": "support"');
    expect(details).toHaveTextContent('"role": "moderator"');
    // строка JSON как текст: разметка не исполняется
    expect(details).toHaveTextContent('"note": "<img src=x onerror=');
    expect(details.querySelector('img')).toBeNull();
    expect(details).toHaveTextContent('role_change: новые обязанности');
    expect(details).toHaveTextContent('203.0.113.0/24');
    expect(details).toHaveTextContent('Safari');
    // повторный клик сворачивает
    await userEvent.setup().click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(within(row!).queryByRole('region', { name: /Подробности записи/ })).toBeNull();
  });

  test('объекта не было (before = null) — «—»', async () => {
    auditServer({ records: [LOG[1]!] });
    renderApp('/audit', { me: meOwner });
    const [row] = rows(await table());
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подробнее' }));
    const details = within(row!).getByRole('region', { name: /Подробности записи/ });
    expect(within(details).getByRole('group', { name: 'Было' })).toHaveTextContent('—');
  });
});
