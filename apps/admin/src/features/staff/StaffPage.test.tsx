import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp } from '../../test/app';
import { meAdmin, meModerator, meOwner } from '../../test/me';
import { account, rolesMatrix, staffServer, TEAM } from '../../test/staff';
import { SEARCH_DEBOUNCE_MS } from '../../ui';

// «Роли и доступ» (спека §9.4 п. 5, §9.3; макет 07): матрица прав из API (подписи — локали),
// «Правило двух рук» — по матрице, «Кто имеет доступ» — только с staff.accounts.read, фильтры —
// search-параметры маршрута, своя строка — «это вы» без действий.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const page = () => screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });

const rowOf = (table: HTMLElement, name: string) => {
  const row = within(table)
    .getAllByRole('row')
    .find((r) => within(r).queryByText(name));
  if (!row) throw new Error(`нет строки ${name}`);
  return row;
};

describe('матрица ролей', () => {
  test('столбцы ролей и клетки — из ответа, подписи — из локалей', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner });
    await page();
    const matrix = await screen.findByRole('table', { name: 'Матрица прав' });
    const headers = within(matrix)
      .getAllByRole('columnheader')
      .map((h) => h.textContent);
    expect(headers).toEqual(['Право', 'владелец', 'админ', 'модератор', 'поддержка']);
    const grant = rowOf(matrix, 'Выдать роль владельца или админа');
    expect(
      within(grant)
        .getAllByRole('cell')
        .slice(1)
        .map((c) => c.textContent),
    ).toEqual(['вдвоём', 'нет', 'нет', 'нет']);
    const audit = rowOf(matrix, 'Читать журнал действий (модератор — свои города и свои записи)');
    expect(
      within(audit)
        .getAllByRole('cell')
        .slice(1)
        .map((c) => c.textContent),
    ).toEqual(['да', 'да', 'свои города', 'да']);
  });

  test('«Правило двух рук» — по dual в матрице, а не готовым текстом', async () => {
    staffServer({
      accounts: TEAM,
      roles: {
        roles: ['owner', 'admin', 'moderator', 'support'],
        permissions: [
          ...rolesMatrix.permissions,
          {
            key: 'geo.cities.activate',
            mutating: true,
            grants: { owner: 'dual', admin: 'deny', moderator: 'dual', support: 'deny' },
          },
        ],
      },
    });
    renderApp('/staff', { me: meOwner });
    const rules = await screen.findByRole('list', { name: 'Правило двух рук' });
    expect(
      within(rules)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual([
      'Выдать роль владельца или админа — владелец',
      'Выдать роль модератора или поддержки, добавить модератору город — владелец, админ',
      'Открыть город — владелец, модератор',
    ]);
  });

  test('незнакомая роль в матрице — столбец «сотрудник», клетка «—»', async () => {
    staffServer({
      accounts: TEAM,
      roles: {
        roles: ['owner', 'future' as never],
        permissions: [rolesMatrix.permissions[0]!],
      },
    });
    renderApp('/staff', { me: meOwner });
    const matrix = await screen.findByRole('table', { name: 'Матрица прав' });
    expect(
      within(matrix)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Право', 'владелец', 'сотрудник']);
    expect(
      within(rowOf(matrix, 'Матрица ролей и прав'))
        .getAllByRole('cell')
        .map((c) => c.textContent),
    ).toEqual(['Матрица ролей и прав', 'да', '—']);
  });
});

describe('кто имеет доступ', () => {
  test('без staff.accounts.read — ни блока, ни запроса списка; без прав выдачи — нет «Пригласить»', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meModerator });
    await screen.findByRole('table', { name: 'Матрица прав' });
    expect(screen.queryByRole('heading', { name: 'Кто имеет доступ' })).not.toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'Сотрудники' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Пригласить сотрудника' })).toBeNull();
    expect(seen.list).toHaveLength(0);
  });

  test('статусы из invite и status; своя строка — «это вы» без действий', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner });
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Мария К.');
    expect(rowOf(table, 'Настя Д.')).toHaveTextContent('приглашение не принято');
    expect(rowOf(table, 'Пётр С.')).toHaveTextContent('приглашение просрочено');
    expect(rowOf(table, 'Олег Б.')).toHaveTextContent('выключен');
    expect(rowOf(table, 'Мария К.')).toHaveTextContent('активен');
    expect(rowOf(table, 'Мария К.')).toHaveTextContent('maria.k@wf.example');

    const mine = rowOf(table, 'Ольга В.');
    expect(mine).toHaveTextContent('это вы');
    expect(within(mine).queryByRole('button')).toBeNull();
    expect(
      within(rowOf(table, 'Мария К.')).getByRole('button', { name: 'Управлять: Мария К.' }),
    ).toBeInTheDocument();
  });

  test('вход закрыт после пяти неверных кодов — пока locked_until в будущем', async () => {
    staffServer({
      accounts: [
        account({ locked_until: '2099-01-01T00:00:00Z' }),
        account({
          id: '0192f1c4-1111-7b21-9c5d-0000000000b2',
          display_name: 'Борис Т.',
          locked_until: '2000-01-01T00:00:00Z',
        }),
      ],
    });
    renderApp('/staff', { me: meOwner });
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Мария К.');
    expect(rowOf(table, 'Мария К.')).toHaveTextContent('вход закрыт');
    expect(rowOf(table, 'Борис Т.')).toHaveTextContent('активен');
  });

  test('админ: у владельца и админа «Управлять» нет, у модератора — есть', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meAdmin });
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Мария К.');
    expect(within(rowOf(table, 'Ольга В.')).queryByRole('button')).toBeNull();
    expect(within(rowOf(table, 'Игорь Л.')).queryByRole('button')).toBeNull();
    expect(
      within(rowOf(table, 'Мария К.')).getByRole('button', { name: 'Управлять: Мария К.' }),
    ).toBeInTheDocument();
  });

  test('поиск, статус и роль — search-параметры маршрута и запроса', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    const app = renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Мария К.');

    await user.type(screen.getByRole('searchbox', { name: 'Поиск' }), 'мар');
    await waitFor(() => expect(app.at()).toBe('/staff?q=%D0%BC%D0%B0%D1%80'), {
      timeout: SEARCH_DEBOUNCE_MS + 1000,
    });
    await waitFor(() => expect(seen.list.at(-1)?.get('q')).toBe('мар'));
    await waitFor(() => expect(within(table).queryByText('Игорь Л.')).not.toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Приглашены' }));
    await waitFor(() => expect(seen.list.at(-1)?.get('status')).toBe('invited'));
    expect(app.at()).toContain('status=invited');

    await user.click(screen.getByRole('button', { name: 'Ещё фильтры' }));
    await user.selectOptions(screen.getByLabelText('Роль'), 'support');
    await waitFor(() => expect(seen.list.at(-1)?.get('role')).toBe('support'));
    expect(app.at()).toContain('role=support');

    await user.click(screen.getAllByRole('button', { name: 'Сбросить фильтры' })[0]!);
    await waitFor(() => expect(app.at()).toBe('/staff'));
    expect(screen.getByLabelText('Роль')).toHaveValue('');
  });

  test('фильтры из адреса применяются сразу (перезагрузка, «Назад»)', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    renderApp('/staff?status=disabled&role=support&q=%D0%BE', { me: meOwner });
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Олег Б.');
    const q = seen.list[0]!;
    expect([q.get('status'), q.get('role'), q.get('q')]).toEqual(['disabled', 'support', 'о']);
    expect(screen.getByRole('searchbox', { name: 'Поиск' })).toHaveValue('о');
  });

  test('незнакомые значения фильтров в адресе отбрасываются', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    renderApp('/staff?status=hacked&role=root', { me: meOwner });
    await screen.findByRole('table', { name: 'Сотрудники' });
    await waitFor(() => expect(seen.list).toHaveLength(1));
    expect([seen.list[0]!.get('status'), seen.list[0]!.get('role')]).toEqual([null, null]);
  });

  test('курсор: «Показать ещё» запрашивает следующую страницу', async () => {
    const { seen } = staffServer({ accounts: TEAM, pageSize: 4 });
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const table = await screen.findByRole('table', { name: 'Сотрудники' });
    await within(table).findByText('Мария К.');
    expect(within(table).queryByText('Олег Б.')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Показать ещё' }));
    expect(await within(table).findByText('Олег Б.')).toBeInTheDocument();
    expect(seen.list.at(-1)?.get('cursor')).toBe('4');
  });
});
