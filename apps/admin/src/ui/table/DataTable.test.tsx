import { useInfiniteQuery } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, useState } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { components } from '../../api/gen/admin';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { stubMedia } from '../../test/media';
import { problem } from '../../test/problem';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { Drawer } from '../overlay/Drawer';
import styles from './DataTable.module.css';
import {
  type Column,
  DataTable,
  type DataTableProps,
  infiniteTableProps,
  type SortState,
} from './index';

type Account = components['schemas']['StaffAccount'];

const account = (n: number, role: Account['role'] = 'moderator'): Account => ({
  id: `0192f1c4-7a3e-7b21-9c5d-${String(n).padStart(12, '0')}`,
  email: `staff${n}@wf.example`,
  display_name: `Сотрудник ${n}`,
  locale: 'ru',
  status: 'active',
  role,
  city_ids: [],
  created_at: '2026-10-01T17:00:00Z',
  last_login_at: null,
  locked_until: null,
  version: 1,
  invite: null,
});

const columns: Column<Account>[] = [
  { id: 'name', header: 'Сотрудник', cell: (a) => a.display_name, sortable: true },
  { id: 'email', header: 'Почта', cell: (a) => a.email },
];

// Экран-образец: список сотрудников с поиском, чипами ролей, фильтром статуса и курсором.
function Accounts() {
  const api = useApi();
  const [q, setQ] = useState('');
  const [role, setRole] = useState('all');
  const [blocked, setBlocked] = useState(false);
  const [sort, setSort] = useState<SortState>({ column: 'name', direction: 'asc' });
  const query = useInfiniteQuery({
    queryKey: ['staff', 'accounts', { q, role, blocked }],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/v1/staff/accounts', {
          params: {
            query: {
              q: q || undefined,
              role: role === 'all' ? undefined : (role as Account['role']),
              status: blocked ? 'disabled' : undefined,
              cursor: pageParam,
            },
          },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor ?? undefined,
  });
  const dirty = q !== '' || role !== 'all' || blocked;
  return (
    <DataTable
      label="Сотрудники"
      columns={columns}
      rowKey={(a) => a.id}
      {...infiniteTableProps(query)}
      search={{ value: q, onChange: setQ, placeholder: 'Имя или почта' }}
      chips={{
        label: 'Роль',
        value: role,
        onChange: setRole,
        options: [
          { value: 'all', label: 'Все' },
          { value: 'owner', label: 'Владельцы' },
        ],
      }}
      filters={{
        active: blocked ? 1 : 0,
        content: (
          <label>
            <input
              type="checkbox"
              checked={blocked}
              onChange={(e) => setBlocked(e.target.checked)}
            />
            Выключенные
          </label>
        ),
      }}
      sort={sort}
      onSortChange={setSort}
      dirty={dirty}
      onReset={() => {
        setQ('');
        setRole('all');
        setBlocked(false);
      }}
    />
  );
}

type Seen = { q: string | null; role: string | null; status: string | null; cursor: string | null };

// Сервер: страницы по курсору; запросы записываются.
function serve(pages: Record<string, { items: Account[]; next: string | null }>) {
  const seen: Seen[] = [];
  server.use(
    http.get('/v1/staff/accounts', ({ request, response }) => {
      const u = new URL(request.url);
      const s = {
        q: u.searchParams.get('q'),
        role: u.searchParams.get('role'),
        status: u.searchParams.get('status'),
        cursor: u.searchParams.get('cursor'),
      };
      seen.push(s);
      const page = pages[s.cursor ?? (s.q || s.role || s.status ? 'filtered' : 'first')] ?? {
        items: [],
        next: null,
      };
      return response(200).json({ items: page.items, next_cursor: page.next });
    }),
  );
  return seen;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const table = () => screen.getByRole('table', { name: 'Сотрудники' });
const bodyRows = () => within(table()).getAllByRole('row').slice(1);

test('скелетон, затем строки и «Показано N»', async () => {
  serve({ first: { items: [account(1), account(2)], next: null } });
  renderWithProviders(<Accounts />);
  expect(screen.getByRole('status')).toHaveTextContent('Загрузка…');
  expect(await screen.findByText('Сотрудник 1')).toBeInTheDocument();
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  expect(bodyRows()).toHaveLength(2);
  expect(screen.getByText('Показано 2')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Показать ещё' })).not.toBeInTheDocument();
});

test('«Показать ещё» запрашивает следующую страницу по курсору и дописывает строки', async () => {
  const seen = serve({
    first: { items: [account(1), account(2)], next: 'c2' },
    c2: { items: [account(3)], next: null },
  });
  const user = userEvent.setup();
  renderWithProviders(<Accounts />);
  await user.click(await screen.findByRole('button', { name: 'Показать ещё' }));
  expect(await screen.findByText('Сотрудник 3')).toBeInTheDocument();
  expect(bodyRows()).toHaveLength(3);
  expect(screen.getByText('Показано 3')).toBeInTheDocument();
  expect(seen.map((s) => s.cursor)).toEqual([null, 'c2']);
  expect(screen.queryByRole('button', { name: 'Показать ещё' })).not.toBeInTheDocument();
});

test('пусто без фильтров — «Ничего не нашлось», без «Сбросить фильтры»', async () => {
  serve({ first: { items: [], next: null } });
  renderWithProviders(<Accounts />);
  expect(await screen.findByText('Ничего не нашлось')).toBeInTheDocument();
  expect(screen.getByText(/Это не ошибка — просто пусто/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Сбросить фильтры' })).not.toBeInTheDocument();
});

test('ошибка загрузки — код и «Повторить»; повтор сразу снимает ошибку и показывает скелетон, затем строки', async () => {
  let fail = true;
  let asked = 0;
  let release = () => {};
  const gate = new Promise<void>((r) => (release = r));
  server.use(
    http.get('/v1/staff/accounts', async ({ response }) => {
      asked++;
      if (!fail) await gate;
      return fail
        ? response.untyped(problem(500, 'internal'))
        : response(200).json({ items: [account(1)], next_cursor: null });
    }),
  );
  const user = userEvent.setup();
  renderWithProviders(<Accounts />);
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('Не удалось загрузить');
  expect(alert).toHaveTextContent('Код ошибки: 500 · internal');
  fail = false;
  await user.click(within(alert).getByRole('button', { name: 'Повторить' }));
  // перезапрос виден сразу: вместо ошибки — скелетон (у запроса без данных статус pending)
  expect(await screen.findByRole('status')).toHaveTextContent('Загрузка…');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  release();
  expect(await screen.findByText('Сотрудник 1')).toBeInTheDocument();
  expect(asked).toBe(2);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('поиск, чип и раскрывающийся фильтр уходят в запрос; «Сбросить фильтры» возвращает всё', async () => {
  const seen = serve({
    first: { items: [account(1), account(2)], next: null },
    filtered: { items: [], next: null },
  });
  const user = userEvent.setup();
  renderWithProviders(<Accounts />);
  await screen.findByText('Сотрудник 1');

  await user.type(screen.getByRole('searchbox', { name: 'Поиск' }), 'zz');
  await screen.findByText('Ничего не нашлось');
  await user.click(screen.getByRole('button', { name: 'Владельцы' }));
  expect(screen.getByRole('button', { name: 'Владельцы' })).toHaveAttribute('aria-pressed', 'true');
  const more = screen.getByRole('button', { name: 'Ещё фильтры' });
  expect(more).toHaveAttribute('aria-expanded', 'false');
  await user.click(more);
  expect(more).toHaveAttribute('aria-expanded', 'true');
  await user.click(screen.getByRole('checkbox', { name: 'Выключенные' }));
  // число заданных фильтров слышно читалке — в имени кнопки
  expect(more).toHaveAccessibleName('Ещё фильтры, задано: 1');
  await waitFor(() =>
    expect(seen.at(-1)).toEqual({ q: 'zz', role: 'owner', status: 'disabled', cursor: null }),
  );
  // поиск ждёт паузы в наборе: по запросу на слово, а не на каждую букву
  expect(seen.filter((s) => s.q === 'z')).toEqual([]);

  // пусто по фильтру: «Сбросить фильтры» и в строке над таблицей, и в пустом состоянии
  const resets = await screen.findAllByRole('button', { name: 'Сбросить фильтры' });
  expect(resets).toHaveLength(2);
  await user.click(resets[1]!);
  expect(await screen.findByText('Сотрудник 1')).toBeInTheDocument();
  expect(screen.getByRole('searchbox', { name: 'Поиск' })).toHaveValue('');
  expect(screen.getByRole('button', { name: 'Все' })).toHaveAttribute('aria-pressed', 'true');
  expect(seen.at(-1)).toEqual({ q: null, role: null, status: null, cursor: null });
  expect(more).toHaveAccessibleName('Ещё фильтры');
  expect(screen.queryByRole('button', { name: 'Сбросить фильтры' })).not.toBeInTheDocument();
});

test('сортировка: столбец с кнопкой, aria-sort и смена направления', async () => {
  serve({ first: { items: [account(1)], next: null } });
  const user = userEvent.setup();
  renderWithProviders(<Accounts />);
  await screen.findByText('Сотрудник 1');
  const header = screen.getByRole('columnheader', { name: /Сотрудник/ });
  expect(header).toHaveAttribute('aria-sort', 'ascending');
  await user.click(within(header).getByRole('button'));
  expect(header).toHaveAttribute('aria-sort', 'descending');
  expect(screen.getByRole('columnheader', { name: 'Почта' })).not.toHaveAttribute('aria-sort');
});

// Экран держит значение поиска с задержкой (например, search-параметр маршрута): эхо прежнего
// значения не должно затирать то, что набрано после отправки.
test('поиск: запоздавшее эхо отправленного значения не затирает новый набор', async () => {
  const ctl: { sent: string[]; apply?: () => void; reset?: () => void } = { sent: [] };
  function Delayed() {
    const [q, setQ] = useState('');
    const [pending, setPending] = useState('');
    useEffect(() => {
      ctl.apply = () => setQ(pending);
      ctl.reset = () => setQ('');
    });
    return (
      <DataTable
        label="Сотрудники"
        columns={columns}
        rowKey={(a) => a.id}
        rows={[]}
        onRetry={() => {}}
        search={{
          value: q,
          onChange: (v) => {
            ctl.sent.push(v);
            setPending(v);
          },
        }}
      />
    );
  }
  const user = userEvent.setup();
  renderWithProviders(<Delayed />);
  const box = screen.getByRole('searchbox', { name: 'Поиск' });
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab']));
  await user.type(box, 'c');
  act(() => ctl.apply?.());
  expect(box).toHaveValue('abc');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'abc']));
  act(() => ctl.apply?.());
  expect(box).toHaveValue('abc');
  // настоящий сброс снаружи («Сбросить фильтры») — в поле сразу
  act(() => ctl.reset?.());
  expect(box).toHaveValue('');
});

test('телефон: строки-карточки сохраняют строки и ячейки, подпись столбца слышна', async () => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: true,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
  serve({ first: { items: [account(1)], next: null } });
  renderWithProviders(<Accounts />);
  await screen.findByText('Сотрудник 1');
  const rows = bodyRows();
  expect(rows).toHaveLength(1);
  const cells = within(rows[0]!).getAllByRole('cell');
  expect(cells).toHaveLength(2);
  expect(cells[1]).toHaveTextContent('Почта');
  expect(within(cells[1]!).getByText('Почта')).not.toHaveAttribute('aria-hidden');
  expect(within(rows[0]!).getAllByRole('cell')[1]).toHaveTextContent('staff1@wf.example');
});

test('поиск: после сброса снаружи тот же текст, набранный снова, уходит в запрос', async () => {
  const ctl: { sent: string[]; reset?: () => void } = { sent: [] };
  function Immediate() {
    const [q, setQ] = useState('');
    useEffect(() => {
      ctl.reset = () => setQ('');
    });
    return (
      <DataTable
        label="Сотрудники"
        columns={columns}
        rowKey={(a) => a.id}
        rows={[]}
        onRetry={() => {}}
        search={{
          value: q,
          onChange: (v) => {
            ctl.sent.push(v);
            setQ(v);
          },
        }}
      />
    );
  }
  const user = userEvent.setup();
  renderWithProviders(<Immediate />);
  const box = screen.getByRole('searchbox', { name: 'Поиск' });
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab']));
  act(() => ctl.reset?.());
  expect(box).toHaveValue('');
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'ab']));
});

test('поиск: эхо пришло не по порядку — набранное не затирается', async () => {
  const ctl: { sent: string[]; set?: (v: string) => void } = { sent: [] };
  function OutOfOrder() {
    const [q, setQ] = useState('');
    useEffect(() => {
      ctl.set = setQ;
    }, []);
    return (
      <DataTable
        label="Сотрудники"
        columns={columns}
        rowKey={(a) => a.id}
        rows={[]}
        onRetry={() => {}}
        search={{ value: q, onChange: (v) => ctl.sent.push(v) }}
      />
    );
  }
  const user = userEvent.setup();
  renderWithProviders(<OutOfOrder />);
  const box = screen.getByRole('searchbox', { name: 'Поиск' });
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab']));
  await user.type(box, 'c');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'abc']));
  await user.type(box, 'd');
  // эхо «ab» запоздало и пришло после отправки «abc»
  act(() => ctl.set?.('ab'));
  expect(box).toHaveValue('abcd');
  act(() => ctl.set?.('abc'));
  expect(box).toHaveValue('abcd');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'abc', 'abcd']));
});

test('поиск: внешнее значение до эха (переход «Назад») снимает отправленное — повтор уходит', async () => {
  const ctl: { sent: string[]; set?: (v: string) => void } = { sent: [] };
  function NoEcho() {
    const [q, setQ] = useState('');
    useEffect(() => {
      ctl.set = setQ;
    }, []);
    return (
      <DataTable
        label="Сотрудники"
        columns={columns}
        rowKey={(a) => a.id}
        rows={[]}
        onRetry={() => {}}
        search={{ value: q, onChange: (v) => ctl.sent.push(v) }}
      />
    );
  }
  const user = userEvent.setup();
  renderWithProviders(<NoEcho />);
  const box = screen.getByRole('searchbox', { name: 'Поиск' });
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab']));
  // эха «ab» не было: экран сам перешёл на другой адрес, затем фильтры сброшены
  act(() => ctl.set?.('zz'));
  expect(box).toHaveValue('zz');
  act(() => ctl.set?.(''));
  expect(box).toHaveValue('');
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'ab']));
});

test('поиск: одно значение отправлено дважды — эхо снимает по последней отправке', async () => {
  const ctl: { sent: string[]; set?: (v: string) => void } = { sent: [] };
  function Twice() {
    const [q, setQ] = useState('');
    useEffect(() => {
      ctl.set = setQ;
    }, []);
    return (
      <DataTable
        label="Сотрудники"
        columns={columns}
        rowKey={(a) => a.id}
        rows={[]}
        onRetry={() => {}}
        search={{ value: q, onChange: (v) => ctl.sent.push(v) }}
      />
    );
  }
  const user = userEvent.setup();
  renderWithProviders(<Twice />);
  const box = screen.getByRole('searchbox', { name: 'Поиск' });
  await user.type(box, 'ab');
  await waitFor(() => expect(ctl.sent).toEqual(['ab']));
  await user.type(box, 'c');
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'abc']));
  await user.type(box, '{Backspace}');
  // навигации схлопнулись: экран применил только последнюю отправку «ab»
  await waitFor(() => expect(ctl.sent).toEqual(['ab', 'abc', 'ab']));
  act(() => ctl.set?.('ab'));
  expect(box).toHaveValue('ab');
  // «abc» после эха последней отправки — внешнее значение («Назад»), а не запоздавшее эхо
  act(() => ctl.set?.('abc'));
  expect(box).toHaveValue('abc');
});

// Статичная таблица (матрица прав, Task 14): число строк ничего не говорит — сводку можно убрать.
test('summary={false} — без «Показано N», строки на месте', () => {
  renderWithProviders(
    <DataTable
      label="Матрица"
      columns={columns}
      rowKey={(a) => a.id}
      rows={[account(1), account(2)]}
      onRetry={() => {}}
      summary={false}
    />,
  );
  expect(screen.getByRole('table', { name: 'Матрица' })).toBeInTheDocument();
  expect(screen.getAllByRole('row')).toHaveLength(3);
  expect(screen.queryByText(/Показано/)).toBeNull();
});

// Карточки вместо строк (план B, Task 18: «Города и запуск», макет 07) — та же панель фильтров,
// «Показано N», «Показать ещё» и состояния; данные — список именованный label, карточка — card(row).
test('card — список карточек вместо таблицы, «Показать ещё» на месте', async () => {
  const onLoadMore = vi.fn();
  const { user } = { user: userEvent.setup() };
  renderWithProviders(
    <DataTable
      label="Города"
      columns={[]}
      rowKey={(a) => a.id}
      rows={[account(1), account(2)]}
      onRetry={() => {}}
      hasMore
      onLoadMore={onLoadMore}
      card={(a) => <article aria-label={a.display_name}>{a.email}</article>}
    />,
  );
  expect(screen.queryByRole('table')).toBeNull();
  const list = screen.getByRole('list', { name: 'Города' });
  expect(within(list).getAllByRole('listitem')).toHaveLength(2);
  expect(within(list).getByRole('article', { name: 'Сотрудник 2' })).toHaveTextContent(
    'staff2@wf.example',
  );
  expect(screen.getByText('Показано 2')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Показать ещё' }));
  expect(onLoadMore).toHaveBeenCalledOnce();
});

// Матрица прав на телефоне (спека UI-фундамента §3.1, §3.5; I6): layout 'table' — таблица на любой
// ширине со своей горизонтальной прокруткой; 'auto' (по умолчанию) — карточки на узком экране.
test.each([
  ['auto', true],
  ['table', false],
] as const)('layout %s на узком экране — карточки: %s', (layout, stacked) => {
  stubMedia({ width: 390, pointer: 'coarse' });
  renderWithProviders(
    <DataTable
      label="Сотрудники"
      layout={layout}
      columns={columns}
      rowKey={(a) => a.id}
      rows={[account(1), account(2)]}
      onRetry={() => {}}
      summary={false}
    />,
  );
  const matrix = table();
  expect(within(matrix).getAllByRole('columnheader')).toHaveLength(columns.length);
  expect(within(matrix).getAllByRole('row')).toHaveLength(3);
  // карточка повторяет подпись столбца в ячейке; строка таблицы — нет
  const labels = within(bodyRows()[0]!).queryAllByText(columns[1]!.header as string);
  expect(labels.length > 0).toBe(stacked);
  expect(matrix.closest('[data-table-scroll]')).not.toBeNull();
});

// Режим select (список выбора, ревью V1 m6): общий компонент проверен сам, а не только через экран поиска места.
describe('select — список выбора', () => {
  function Picker({
    onActivate,
    onLoadMore,
  }: {
    onActivate?: (a: Account) => void;
    onLoadMore?: () => void;
  }) {
    const [value, setValue] = useState<string | null>(account(2).id);
    return (
      <DataTable
        label="Места"
        columns={[]}
        rowKey={(a) => a.id}
        rows={[account(1), account(2), account(3)]}
        onRetry={() => {}}
        hasMore={onLoadMore !== undefined}
        onLoadMore={onLoadMore}
        select={{
          value,
          onChange: (a) => setValue(a.id),
          onActivate,
          option: (a) => a.display_name,
        }}
      />
    );
  }
  const selected = () =>
    within(screen.getByRole('listbox', { name: 'Места' }))
      .getAllByRole('option')
      .filter((o) => o.getAttribute('aria-selected') === 'true')
      .map((o) => o.textContent);

  test('↑ выбирает предыдущую строку, на первой — остаётся', async () => {
    renderWithProviders(<Picker />);
    const user = userEvent.setup();
    screen.getByRole('listbox', { name: 'Места' }).focus();
    await user.keyboard('{ArrowUp}');
    expect(selected()).toEqual(['Сотрудник 1']);
    await user.keyboard('{ArrowUp}');
    expect(selected()).toEqual(['Сотрудник 1']);
  });

  test('двойной клик — onActivate этой строки', async () => {
    const onActivate = vi.fn();
    renderWithProviders(<Picker onActivate={onActivate} />);
    await userEvent.setup().dblClick(screen.getByRole('option', { name: 'Сотрудник 3' }));
    expect(onActivate).toHaveBeenCalledExactlyOnceWith(account(3));
    expect(selected()).toEqual(['Сотрудник 3']);
  });

  test('«Показать ещё» под списком выбора', async () => {
    const onLoadMore = vi.fn();
    renderWithProviders(<Picker onLoadMore={onLoadMore} />);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Показать ещё' }));
    expect(onLoadMore).toHaveBeenCalledOnce();
  });
});

// ---------- UF2b.2: закрепление шапки и первого столбца ----------

const many = (n: number) => Array.from({ length: n }, (_, i) => account(i + 1));
function Plain(props: Partial<DataTableProps<Account>>) {
  return (
    <DataTable
      label="Сотрудники"
      columns={columns}
      rowKey={(a) => a.id}
      rows={[account(1)]}
      onRetry={() => {}}
      {...props}
    />
  );
}

describe('закрепление', () => {
  test('stickyHeader по умолчанию — при 21 строке, при 20 — нет; явный проп важнее', () => {
    const { rerender } = renderWithProviders(<Plain rows={many(20)} />);
    expect(table()).not.toHaveClass(styles.stickyHeader!);
    rerender(<Plain rows={many(21)} />);
    expect(table()).toHaveClass(styles.stickyHeader!);
    rerender(<Plain rows={many(21)} stickyHeader={false} />);
    expect(table()).not.toHaveClass(styles.stickyHeader!);
    rerender(<Plain rows={many(2)} stickyHeader />);
    expect(table()).toHaveClass(styles.stickyHeader!);
  });

  test('stickyFirstColumn — класс у первой ячейки шапки и строк, в том числе layout table на 390', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithProviders(<Plain layout="table" stickyFirstColumn rows={many(2)} />);
    const [head, ...rest] = within(table()).getAllByRole('row');
    const heads = within(head!).getAllByRole('columnheader');
    expect(heads[0]).toHaveClass(styles.pinned!);
    expect(heads[1]).not.toHaveClass(styles.pinned!);
    for (const row of rest) {
      const cells = within(row).getAllByRole('cell');
      expect(cells[0]).toHaveClass(styles.pinned!);
      expect(cells[1]).not.toHaveClass(styles.pinned!);
    }
  });

  test('stickyFirstColumn в карточках телефона не закрепляет (строка — карточка)', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithProviders(<Plain stickyFirstColumn />);
    expect(within(table()).getAllByRole('cell')[0]).not.toHaveClass(styles.pinned!);
  });
});

// ---------- UF2b.3: выбор столбцов и плотность ----------

const cols3: Column<Account>[] = [
  { id: 'name', header: 'Сотрудник', cell: (a) => a.display_name },
  { id: 'email', header: 'Почта', cell: (a) => a.email },
  { id: 'role', header: 'Роль', cell: (a) => a.role, priority: 'low' },
];
function Pref(props: Partial<DataTableProps<Account>>) {
  return <Plain tableId="staff" columns={cols3} {...props} />;
}
const headers = () =>
  within(table())
    .getAllByRole('columnheader')
    .map((h) => h.textContent);
const columnsButton = () => screen.getByRole('button', { name: 'Столбцы' });
async function openColumns(user: ReturnType<typeof userEvent.setup>) {
  await user.click(columnsButton());
  return screen.getByRole('menu', { name: 'Столбцы' });
}
const item = (name: string) => screen.getByRole('menuitemcheckbox', { name });

describe('столбцы', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  test('без tableId кнопки «Столбцы» нет; с tableId — есть', () => {
    const { rerender } = renderWithProviders(<Plain columns={cols3} />);
    expect(screen.queryByRole('button', { name: 'Столбцы' })).toBeNull();
    rerender(<Pref />);
    expect(columnsButton()).toHaveAttribute('aria-haspopup', 'menu');
  });

  test('скрыть столбец → нет в таблице и после перемонтирования', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
    await openColumns(user);
    expect(item('Почта')).toHaveAttribute('aria-checked', 'true');
    await user.click(item('Почта'));
    expect(item('Почта')).toHaveAttribute('aria-checked', 'false');
    expect(headers()).toEqual(['Сотрудник', 'Роль']);
    unmount();
    renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Роль']);
  });

  test('первый столбец по умолчанию не скрывается — и когда storage просит', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    localStorage.setItem('wf.admin.table.staff', JSON.stringify({ hidden: ['name'], shown: [] }));
    const user = userEvent.setup();
    renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
    await openColumns(user);
    expect(item('Сотрудник')).toHaveAttribute('aria-disabled', 'true');
    await user.click(item('Сотрудник'));
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
  });

  test('скрыть последний видимый нельзя', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const all = cols3.map((c) => ({ ...c, hideable: true }));
    const user = userEvent.setup();
    renderWithProviders(<Pref columns={all} />);
    await openColumns(user);
    await user.click(item('Сотрудник'));
    await user.click(item('Почта'));
    expect(headers()).toEqual(['Роль']);
    expect(item('Роль')).toHaveAttribute('aria-disabled', 'true');
    await user.click(item('Роль'));
    expect(headers()).toEqual(['Роль']);
    expect(item('Роль')).toHaveAttribute('aria-checked', 'true');
  });

  test('столбец из старой версии в storage — отброшен, остальные на месте', () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    localStorage.setItem(
      'wf.admin.table.staff',
      JSON.stringify({ hidden: ['legacy', 'email'], shown: ['gone'] }),
    );
    renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Роль']);
  });

  test('900 + fine: low скрыт, в меню — «скрыт по ширине», флажок снят; 1280 — виден', async () => {
    const media = stubMedia({ width: 900, pointer: 'fine' });
    const user = userEvent.setup();
    renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Почта']);
    await openColumns(user);
    expect(item('Роль')).toHaveAttribute('aria-checked', 'false');
    expect(item('Роль')).toHaveAccessibleDescription('скрыт по ширине');
    act(() => media.set({ width: 1280, pointer: 'fine' }));
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
    expect(item('Роль')).toHaveAttribute('aria-checked', 'true');
    expect(item('Роль')).not.toHaveAccessibleDescription('скрыт по ширине');
  });

  test('900 + явно включённый low → виден и после перемонтирования', async () => {
    stubMedia({ width: 900, pointer: 'fine' });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<Pref />);
    await openColumns(user);
    await user.click(item('Роль'));
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
    expect(item('Роль')).toHaveAttribute('aria-checked', 'true');
    unmount();
    renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
  });

  test('ширина не прячет единственный оставшийся столбец', () => {
    stubMedia({ width: 900, pointer: 'fine' });
    const all = cols3.map((c) => ({ ...c, hideable: true }));
    localStorage.setItem(
      'wf.admin.table.staff',
      JSON.stringify({ hidden: ['name', 'email'], shown: [] }),
    );
    renderWithProviders(<Pref columns={all} />);
    expect(headers()).toEqual(['Роль']);
  });

  test('storage бросает — таблица работает, скрытие держится до перемонтирования', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    const user = userEvent.setup();
    renderWithProviders(<Pref />);
    await openColumns(user);
    await user.click(item('Почта'));
    expect(headers()).toEqual(['Сотрудник', 'Роль']);
  });

  test('меню: стрелки по пунктам по кругу, Esc закрывает только меню (таблица в Drawer), фокус — на «Столбцы»', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    const onClose = vi.fn();
    renderWithProviders(
      <Drawer open onClose={onClose} title="Панель">
        <Pref />
      </Drawer>,
    );
    await openColumns(user);
    expect(item('Сотрудник')).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(item('Почта')).toHaveFocus();
    await user.keyboard('{End}');
    expect(screen.getByRole('menuitemradio', { name: 'Обычная' })).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(item('Сотрудник')).toHaveFocus();
    await user.keyboard('{ArrowUp}');
    expect(screen.getByRole('menuitemradio', { name: 'Обычная' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Панель' })).toBeInTheDocument();
    expect(columnsButton()).toHaveFocus();
  });

  test('Tab из меню закрывает его и уходит дальше', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Pref />
        <a href="#next">Дальше</a>
      </>,
    );
    await openColumns(user);
    await user.keyboard('{ArrowDown}');
    await user.tab();
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.getByRole('link', { name: 'Дальше' })).toHaveFocus();
  });

  test('Shift+Tab из меню закрывает его, фокус — перед «Столбцы»', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <a href="#prev">Раньше</a>
        <Pref />
      </>,
    );
    await openColumns(user);
    await user.tab({ shift: true });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.getByRole('link', { name: 'Раньше' })).toHaveFocus();
  });

  test('Tab из меню в Drawer — меню закрыто, фокус на следующем элементе панели, панель открыта', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    const onClose = vi.fn();
    renderWithProviders(
      <Drawer open onClose={onClose} title="Панель">
        <Pref />
        <a href="#next">Дальше</a>
      </Drawer>,
    );
    await openColumns(user);
    await user.keyboard('{ArrowDown}');
    await user.tab();
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.getByRole('link', { name: 'Дальше' })).toHaveFocus();
    expect(onClose).not.toHaveBeenCalled();
  });

  test('смена tableId у смонтированной таблицы — выбор второй таблицы, запись — в её ключ', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    localStorage.setItem('wf.admin.table.staff', JSON.stringify({ hidden: ['email'], shown: [] }));
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(<Pref />);
    expect(headers()).toEqual(['Сотрудник', 'Роль']);
    rerender(<Pref tableId="audit" />);
    expect(headers()).toEqual(['Сотрудник', 'Почта', 'Роль']);
    await openColumns(user);
    await user.click(item('Роль'));
    expect(JSON.parse(localStorage.getItem('wf.admin.table.audit') ?? '')).toEqual({
      hidden: ['role'],
      shown: [],
    });
    expect(JSON.parse(localStorage.getItem('wf.admin.table.staff') ?? '')).toEqual({
      hidden: ['email'],
      shown: [],
    });
  });

  test('столбец пришёл после монтирования (право) — его сохранённое скрытие применяется и не стирается записью', async () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    localStorage.setItem('wf.admin.table.staff', JSON.stringify({ hidden: ['role'], shown: [] }));
    const user = userEvent.setup();
    const two = cols3.slice(0, 2);
    const { rerender } = renderWithProviders(<Pref columns={two} />);
    await openColumns(user);
    await user.click(item('Почта'));
    expect(headers()).toEqual(['Сотрудник']);
    rerender(<Pref columns={cols3} />);
    expect(headers()).toEqual(['Сотрудник']);
  });

  test('плотность: compact — у всех таблиц и после перемонтирования; на coarse — normal', async () => {
    const media = stubMedia({ width: 1440, pointer: 'fine' });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(
      <>
        <Pref />
        <Plain label="Другая" />
      </>,
    );
    const other = () => screen.getByRole('table', { name: 'Другая' });
    expect(table()).toHaveAttribute('data-density', 'normal');
    await openColumns(user);
    const compact = screen.getByRole('menuitemradio', { name: 'Компактная' });
    expect(compact).toHaveAttribute('aria-checked', 'false');
    await user.click(compact);
    expect(compact).toHaveAttribute('aria-checked', 'true');
    expect(table()).toHaveAttribute('data-density', 'compact');
    expect(other()).toHaveAttribute('data-density', 'compact');
    unmount();
    renderWithProviders(<Pref />);
    expect(table()).toHaveAttribute('data-density', 'compact');
    act(() => media.set({ width: 1440, pointer: 'coarse' }));
    expect(table()).toHaveAttribute('data-density', 'normal');
  });

  test('телефон: кнопки «Столбцы» нет, low — под «Подробнее»', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const user = userEvent.setup();
    renderWithProviders(<Pref />);
    expect(screen.queryByRole('button', { name: 'Столбцы' })).toBeNull();
    const row = bodyRows()[0]!;
    expect(within(row).queryByRole('cell', { name: /Роль/ })).toBeNull();
    expect(within(row).getByText('moderator')).not.toBeVisible();
    const more = within(row).getByRole('button', { name: 'Подробнее' });
    expect(more).toHaveAttribute('aria-expanded', 'false');
    await user.click(more);
    expect(more).toHaveAttribute('aria-expanded', 'true');
    expect(within(row).getByText('moderator')).toBeVisible();
  });
});
