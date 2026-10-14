import { useInfiniteQuery } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, useState } from 'react';
import { afterEach, expect, test, vi } from 'vitest';
import type { components } from '../../api/gen/admin';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { problem } from '../../test/problem';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { type Column, DataTable, infiniteTableProps, type SortState } from './index';

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
