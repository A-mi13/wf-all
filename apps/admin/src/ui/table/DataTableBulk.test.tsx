import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode, useState } from 'react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { Me } from '../../app/permissions';
import { ME_KEY } from '../../app/query';
import { useSession } from '../../app/session';
import { stubTableLayout } from '../../test/layout';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from '../../test/me';
import { renderWithProviders } from '../../test/render';
import { DataTable } from './DataTable';
import type { BulkAction, BulkOutcome, Column } from './model';
import { forgetTablePositions, rememberRow } from './restore';

// Массовые действия в таблице (спека UI-фундамента §4.5, план UF2b.5; строки «тест в волне 2» отчёта
// uf2b1x): флажки, «выбрать видимые» (загруженные и доступные), флажок без права — недоступен с
// подсказкой, панель при выборе ≥ 1, выбор исчезнувших чистится, итог не переживает смену выбора и
// смену сотрудника.

type Row = { id: string; name: string; perm: string; city?: string };
const row = (id: string, perm = 'x.do', city?: string): Row => ({
  id,
  name: `Строка ${id}`,
  perm,
  city,
});
const rows = (n: number) => Array.from({ length: n }, (_, i) => row(`r${i + 1}`));
const cols: Column<Row>[] = [{ id: 'name', header: 'Имя', cell: (r) => r.name }];
const meBulk: Me = { ...meOwner, permissions: { 'x.do': 'allow' } };

type Call = { row: Row; key: string; resolve(o: BulkOutcome): void; reject(e: unknown): void };
function controlled(concurrency?: number) {
  const calls: Call[] = [];
  const action: BulkAction<Row> = {
    id: 'disable',
    label: 'Выключить',
    permission: (r) => ({ perm: r.perm, cityId: r.city }),
    confirm: { title: (n) => `Выключить ${n}?` },
    concurrency,
    run: (r, key) =>
      new Promise<BulkOutcome>((resolve, reject) => calls.push({ row: r, key, resolve, reject })),
  };
  return { action, calls };
}

let restore: () => void;
beforeEach(() => {
  restore = stubTableLayout();
});
afterEach(() => restore());

function Screen({
  list,
  action,
  onChange,
  onRowClick,
  settle,
  loading = false,
  columns = cols,
  restoreKey,
}: {
  list: Row[];
  action: BulkAction<Row>;
  onChange?: (next: ReadonlySet<string>) => void;
  onRowClick?: (r: Row) => void;
  // onSettled экрана: перечитанный список (выполненные строки ушли из выдачи)
  settle?: (list: Row[]) => Row[];
  loading?: boolean; // перезапрос с новым фильтром: строк нет, скелетон
  columns?: Column<Row>[];
  restoreKey?: string;
}) {
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
  const [settled, setSettled] = useState<Row[] | null>(null);
  return (
    <DataTable
      label="Учётки"
      columns={columns}
      rowKey={(r) => r.id}
      rows={loading ? undefined : (settled ?? list)}
      onRetry={() => {}}
      onRowClick={onRowClick}
      restoreKey={restoreKey}
      bulk={{
        onSettled: settle && (() => setSettled((l) => settle(l ?? list))),
        selected,
        onChange: (next) => {
          onChange?.(next);
          setSelected(next);
        },
        actions: [action],
        rowLabel: (r) => r.name,
        onPending: () => undefined,
      }}
    />
  );
}

// экран под каркасом: key={me.id}, как ActiveShell
function Shell(props: Parameters<typeof Screen>[0]) {
  const s = useSession();
  if (s.status !== 'active') return null;
  return <Screen key={s.me.id} {...props} />;
}

function setup(list: Row[], action: BulkAction<Row>, me: Me = meBulk, extra = {}) {
  const user = userEvent.setup();
  const r = renderWithProviders(
    <StrictMode>
      <Shell list={list} action={action} {...extra} />
    </StrictMode>,
    { me },
  );
  return { ...r, user };
}

const region = () => screen.queryByRole('region', { name: 'Массовые действия' });
const box = (name: string) => screen.getByRole('checkbox', { name: `Выбрать: ${name}` });
const all = () => screen.getByRole('checkbox', { name: 'Выбрать видимые' });

async function runAll(user: ReturnType<typeof userEvent.setup>, n: number) {
  await user.click(screen.getByRole('button', { name: 'Выключить' }));
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: String(n) } });
  await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
}

describe('DataTable bulk', () => {
  test('0 выбранных — панели нет; 1 — «Выбрано 1»; снять — панели снова нет', async () => {
    const { user } = setup(rows(3), controlled().action);
    expect(region()).toBeNull();
    await user.click(box('Строка r1'));
    expect(region()).toHaveTextContent('Выбрано 1');
    await user.click(box('Строка r1'));
    expect(region()).toBeNull();
  });

  test('строка с deny для всех действий — флажок недоступен, причина — подсказкой; по городу строки', async () => {
    const me: Me = { ...meModerator, city_ids: [CITY_OWN], permissions: { 'x.city': 'scoped' } };
    const list = [row('own', 'x.city', CITY_OWN), row('other', 'x.city', CITY_OTHER)];
    const { user } = setup(list, controlled().action, me);
    const denied = box('Строка other');
    expect(denied).toHaveAttribute('aria-disabled', 'true');
    expect(denied).toHaveAccessibleDescription(
      'Нет вашего права ни на одно действие с этой строкой',
    );
    await user.click(denied);
    expect(denied).not.toBeChecked();
    expect(region()).toBeNull();
    await user.click(box('Строка own'));
    expect(box('Строка own')).toBeChecked();
    expect(region()).toHaveTextContent('Выбрано 1');
  });

  test('«выбрать видимые» — все загруженные (300, виртуально), кроме deny; повторно — снять', async () => {
    const list = [...rows(298), row('d1', 'x.no'), row('d2', 'x.no')];
    const onChange = vi.fn();
    const { user } = setup(list, controlled().action, meBulk, { onChange });
    // виртуально: в DOM не все строки
    expect(screen.getAllByRole('checkbox').length).toBeLessThan(60);
    await user.click(all());
    // сам выбор — без недоступных (не их снятие задним числом)
    const first = onChange.mock.calls[0]?.[0] as ReadonlySet<string>;
    expect(first.size).toBe(298);
    expect(first.has('d1')).toBe(false);
    expect(region()).toHaveTextContent('Выбрано 298');
    expect(all()).toBeChecked();
    await user.click(box('Строка r1'));
    expect(all()).toBePartiallyChecked();
    await user.click(all());
    expect(region()).toHaveTextContent('Выбрано 298');
    await user.click(all());
    expect(region()).toBeNull();
  });

  test('выбор исчезнувших после перечитывания строк чистится', async () => {
    const onChange = vi.fn();
    const action = controlled().action;
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <Screen list={rows(3)} action={action} onChange={onChange} />,
      { me: meBulk },
    );
    await user.click(box('Строка r1'));
    await user.click(box('Строка r2'));
    rerender(<Screen list={[row('r1'), row('r3')]} action={action} onChange={onChange} />);
    await waitFor(() => expect(region()).toHaveTextContent('Выбрано 1'));
    expect([...(onChange.mock.lastCall?.[0] as ReadonlySet<string>)]).toEqual(['r1']);
  });

  test('итог не переживает смену выбора: «Повторить неудавшиеся» не шлёт снятые строки', async () => {
    const c = controlled();
    const { user } = setup(rows(3), c.action);
    await user.click(all());
    await runAll(user, 3);
    await waitFor(() => expect(c.calls).toHaveLength(3));
    c.calls[0]?.resolve('executed');
    c.calls[1]?.reject(new ApiError({ status: 422, code: 'validation.failed' }));
    c.calls[2]?.resolve('executed');
    expect(
      await screen.findByRole('button', { name: 'Повторить неудавшиеся' }),
    ).toBeInTheDocument();
    await user.click(box('Строка r2'));
    expect(screen.queryByRole('button', { name: 'Повторить неудавшиеся' })).toBeNull();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
    // вернули тот же выбор — прежний итог не оживает
    await user.click(box('Строка r2'));
    expect(screen.queryByRole('button', { name: 'Повторить неудавшиеся' })).toBeNull();
    expect(c.calls).toHaveLength(3);
  });

  test('выбор сменили посреди пачки — итог тостом, в панели его нет', async () => {
    const c = controlled(1);
    const { user } = setup(rows(2), c.action);
    await user.click(all());
    await runAll(user, 2);
    await waitFor(() => expect(c.calls).toHaveLength(1));
    await user.click(box('Строка r1'));
    c.calls[0]?.resolve('executed');
    await waitFor(() => expect(c.calls).toHaveLength(2));
    c.calls[1]?.resolve('executed');
    expect(await screen.findByText('Выполнено 2')).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
  });

  test('смена сотрудника — выбор и итог не переживают key={me.id}', async () => {
    const c = controlled();
    const { user, queryClient } = setup(rows(2), c.action);
    await user.click(all());
    await runAll(user, 2);
    await waitFor(() => expect(c.calls).toHaveLength(2));
    c.calls.forEach((x) => x.resolve('executed'));
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Выполнено 2'));
    act(() => queryClient.setQueryData(ME_KEY, { ...meBulk, id: 'другой' }));
    await waitFor(() => expect(region()).toBeNull());
    expect(box('Строка r1')).not.toBeChecked();
  });

  test('права сняли (me перечитан) — строка снята с выбора и недоступна', async () => {
    const { user, queryClient } = setup(rows(2), controlled().action);
    await user.click(box('Строка r1'));
    expect(region()).toHaveTextContent('Выбрано 1');
    act(() => queryClient.setQueryData(ME_KEY, { ...meBulk, permissions: {} }));
    await waitFor(() => expect(region()).toBeNull());
    expect(box('Строка r1')).toHaveAttribute('aria-disabled', 'true');
    expect(box('Строка r1')).not.toBeChecked();
  });

  test('флажок не открывает строку (onRowClick)', async () => {
    const onRowClick = vi.fn();
    const { user } = setup(rows(2), controlled().action, meBulk, { onRowClick });
    await user.click(box('Строка r1'));
    expect(onRowClick).not.toHaveBeenCalled();
    expect(region()).toHaveTextContent('Выбрано 1');
  });

  test('перечитывание после пачки убрало выполненные — итог, тексты и «Повторить» остаются (I1)', async () => {
    const c = controlled();
    const { user } = setup(rows(3), c.action, meBulk, {
      settle: (l: Row[]) => l.filter((r) => r.id === 'r2'),
    });
    await user.click(all());
    await runAll(user, 3);
    await waitFor(() => expect(c.calls).toHaveLength(3));
    c.calls[0]?.resolve('executed');
    c.calls[1]?.reject(new ApiError({ status: 422, code: 'validation.failed' }));
    c.calls[2]?.resolve('executed');
    await waitFor(() => expect(screen.queryByText('Строка r1')).toBeNull());
    expect(screen.getByRole('status')).toHaveTextContent('Выполнено 2 · ошибка 1');
    expect(screen.getByText(/Строка r2: Проверьте/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Повторить неудавшиеся' }));
    await waitFor(() => expect(c.calls).toHaveLength(4));
    expect(c.calls[3]?.row.id).toBe('r2');
  });

  test('перечитывание убрало все строки — итог виден до «Закрыть»; unknown — тем же ключом (I1)', async () => {
    const c = controlled();
    const { user } = setup(rows(3), c.action, meBulk, { settle: () => [] });
    await user.click(all());
    await runAll(user, 3);
    await waitFor(() => expect(c.calls).toHaveLength(3));
    c.calls[0]?.resolve('executed');
    c.calls[1]?.reject(new TypeError('Failed to fetch'));
    c.calls[2]?.resolve('executed');
    await waitFor(() => expect(screen.queryByRole('table')).toBeNull());
    expect(screen.getByRole('status')).toHaveTextContent('Выполнено 2 · исход неизвестен 1');
    await user.click(screen.getByRole('button', { name: 'Повторить неудавшиеся' }));
    await waitFor(() => expect(c.calls).toHaveLength(4));
    expect(c.calls[3]?.key).toBe(c.calls[1]?.key);
    c.calls[3]?.resolve('executed');
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Выполнено 3'));
    await user.click(screen.getByRole('button', { name: 'Закрыть' }));
    expect(region()).toBeNull();
  });

  test('поиск или чип посреди пачки (строк нет — скелетон) пачку не отменяют (m1)', async () => {
    const c = controlled(1);
    const user = userEvent.setup();
    const props = { list: rows(2), action: c.action };
    const { rerender } = renderWithProviders(<Screen {...props} />, { me: meBulk });
    await user.click(all());
    await runAll(user, 2);
    await waitFor(() => expect(c.calls).toHaveLength(1));
    rerender(<Screen {...props} loading />);
    c.calls[0]?.resolve('executed');
    await waitFor(() => expect(c.calls).toHaveLength(2));
    c.calls[1]?.resolve('executed');
    rerender(<Screen {...props} />);
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Выполнено 2'));
    expect(screen.queryByText(/не начато/)).toBeNull();
  });

  test('«Выбрать видимые» недоступна, когда выбирать нечего (m5)', async () => {
    const { user } = setup([row('d1', 'x.no'), row('d2', 'x.no')], controlled().action);
    expect(all()).toHaveAttribute('aria-disabled', 'true');
    expect(all()).toHaveAccessibleDescription('Нет строк, доступных для выбора');
    await user.click(all());
    expect(region()).toBeNull();
  });

  test('недоступный флажок по касанию (без фокуса) — получает фокус, причина видна (m3)', async () => {
    setup([row('d1', 'x.no')], controlled().action);
    const denied = box('Строка d1');
    fireEvent.click(denied);
    expect(document.activeElement).toBe(denied);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Нет вашего права ни на одно действие с этой строкой',
    );
  });

  test('возврат на позицию с bulk: фокус на действии строки, не на флажке (m6)', async () => {
    const columns: Column<Row>[] = [
      ...cols,
      {
        id: 'act',
        header: 'Действия',
        cell: (r) => <button type="button">Управлять {r.id}</button>,
      },
    ];
    rememberRow('k', 'r2');
    setup(rows(3), controlled().action, meBulk, { columns, restoreKey: 'k' });
    await waitFor(() => expect(document.activeElement).toHaveAccessibleName('Управлять r2'));
    forgetTablePositions();
  });
});
