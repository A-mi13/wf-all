import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { CommandContext } from '../../app/commands';
import { NAV } from '../../app/nav';
import { PaletteProvider } from '../../app/PaletteProvider';
import type { Me } from '../../app/permissions';
import { pushRecent, RECENT_PREFIX } from '../../app/recent';
import { country } from '../../test/geo';
import { stubMedia } from '../../test/media';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { account, city } from '../../test/staff';
import { Modal } from '../overlay';
import { AppShell } from '../shell/AppShell';
import { SEARCH_DEBOUNCE_MS } from '../table/model';
import { CommandPalette } from './CommandPalette';
import { usePalette } from './context';

// Командная палитра (спека UI-фундамента §4.1, план UF4.5): группы, сущности с сервера по праву, недавнее,
// шаблон combobox + listbox. Ctrl/⌘+K подключает волна 2 (useShortcut) — здесь открытие через usePalette().open().

const ctx = (): CommandContext => ({
  navigate: vi.fn(),
  setTheme: vi.fn(),
  setLocale: vi.fn(),
  setDensity: vi.fn(),
  openShortcuts: vi.fn(),
  logout: vi.fn(),
});

function mount(me: Me = meOwner, context = ctx()) {
  const onClose = vi.fn();
  const r = renderWithProviders(<CommandPalette open onClose={onClose} context={context} />, {
    me,
  });
  return { ...r, onClose, context };
}

const input = () => screen.getByRole('combobox');
const type = (text: string) => fireEvent.change(input(), { target: { value: text } });
const group = (name: string) => screen.getByRole('group', { name });
const optionNames = (name: string) =>
  within(group(name))
    .queryAllByRole('option')
    .map((o) => o.querySelector('[data-label]')?.textContent);
const findGroup = (name: string) => screen.findByRole('group', { name });

// Сервер сущностей: что спрашивали (q по путям) и ворота ответа по q (контролируемые промисы).
function entities(
  opts: {
    cities?: (q: string) => ReturnType<typeof city>[];
    staff?: (q: string) => ReturnType<typeof account>[];
    gate?: (q: string) => Promise<void>;
  } = {},
) {
  const seen = { cities: [] as string[], staff: [] as string[], countries: 0 };
  const signals = new Map<string, AbortSignal>();
  server.use(
    http.get('/v1/geo/cities', async ({ request, response }) => {
      const q = new URL(request.url).searchParams.get('q') ?? '';
      expect(new URL(request.url).searchParams.get('limit')).toBe('5');
      seen.cities.push(q);
      signals.set(q, request.signal);
      await opts.gate?.(q);
      return response(200).json({ items: opts.cities?.(q) ?? [], next_cursor: null });
    }),
    http.get('/v1/staff/accounts', ({ request, response }) => {
      const q = new URL(request.url).searchParams.get('q') ?? '';
      seen.staff.push(q);
      return response(200).json({ items: opts.staff?.(q) ?? [], next_cursor: null });
    }),
    http.get('/v1/geo/countries', ({ response }) => {
      seen.countries++;
      return response(200).json({ items: [country(), country({ code: 'KZ', name: 'Казахстан' })] });
    }),
  );
  return { seen, signals };
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

describe('CommandPalette', () => {
  test('шаблон combobox: поле, список, группы с подписью', () => {
    mount();
    const box = input();
    expect(box).toHaveAttribute('aria-expanded', 'true');
    const list = document.getElementById(box.getAttribute('aria-controls') ?? '');
    expect(list).toHaveAttribute('role', 'listbox');
    expect(box).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[0]?.id);
    expect(group('Разделы')).toBeInTheDocument();
    // пустой запрос — недавнее и разделы, без действий
    expect(screen.queryByRole('group', { name: 'Действия' })).toBeNull();
  });

  test('разделы — только разрешённые (модератор)', () => {
    mount(meModerator);
    expect(optionNames('Разделы')).toEqual([
      'Главная',
      'Журнал действий',
      'Роли и доступ',
      'Подтверждения',
      'Мой доступ',
      'Города и запуск',
    ]);
  });

  test('действие dual — с пометкой «на подтверждение»', () => {
    mount(meOwner);
    type('приг');
    const invite = within(group('Действия')).getByRole('option', {
      name: /Пригласить сотрудника/,
    });
    expect(invite).toHaveTextContent('на подтверждение');
  });

  test('модератор без права выдачи — «Пригласить» нет', () => {
    mount(meModerator);
    type('приг');
    expect(screen.queryByRole('option', { name: /Пригласить сотрудника/ })).toBeNull();
  });

  test('390 + coarse — нет «Пригласить» (только с компьютера)', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    mount(meOwner);
    type('приг');
    expect(screen.queryByRole('option', { name: /Пригласить сотрудника/ })).toBeNull();
    // палитра на телефоне — шторка, но команды без пометки «только с компьютера» есть
    type('тем');
    expect(screen.getByRole('option', { name: 'Тема: светлая' })).toBeInTheDocument();
  });

  test('раскладка и английские слова: «ujhjl» и «city» находят «Города и запуск»', () => {
    mount(meOwner);
    type('ujhjl');
    expect(optionNames('Разделы')).toContain('Города и запуск');
    type('city');
    expect(optionNames('Разделы')).toContain('Города и запуск');
    // совпадение подсвечено <mark>
    type('гор');
    const mark = within(group('Разделы')).getAllByRole('option')[0]?.querySelector('mark');
    expect(mark?.textContent).toBe('Гор');
  });

  test('сущности: от 2 символов и после паузы', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    const { seen } = entities({ cities: () => [city()] });
    mount(meOwner);
    type('с');
    await act(async () => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS * 3));
    expect(seen.cities).toEqual([]);
    type('ст');
    await act(async () => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS - 1));
    expect(seen.cities).toEqual([]);
    expect(seen.staff).toEqual([]);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    await act(async () => vi.advanceTimersByTimeAsync(100));
    expect(seen.cities).toEqual(['ст']);
    expect(seen.staff).toEqual(['ст']);
    expect(within(group('Города')).getByRole('option', { name: /Ставрополь/ })).toBeVisible();
  });

  test('без права запрос не уходит (модератор: сотрудников и стран нет)', async () => {
    const { seen } = entities({ cities: () => [city()] });
    mount(meModerator);
    type('ст');
    await waitFor(() => expect(seen.cities).toEqual(['ст']));
    await within(await findGroup('Города')).findByRole('option', { name: /Ставрополь/ });
    expect(seen.staff).toEqual([]);
    expect(seen.countries).toBe(0);
    expect(screen.queryByRole('group', { name: 'Сотрудники' })).toBeNull();
    expect(screen.queryByRole('group', { name: 'Страны' })).toBeNull();
  });

  test('страны — из кэша списка стран, фильтр на месте', async () => {
    const { seen } = entities();
    mount({ ...meOwner, permissions: { 'geo.countries.read': 'allow' } });
    type('каз');
    expect(
      await within(await screen.findByRole('group', { name: 'Страны' })).findByRole('option', {
        name: /Казахстан/,
      }),
    ).toBeVisible();
    expect(optionNames('Страны')).toEqual(['Казахстан']);
    expect(seen.countries).toBe(1);
  });

  test('последний ответ побеждает, прежний запрос отменён', async () => {
    const gates = new Map<string, { promise: Promise<void>; resolve: () => void }>();
    const gate = (q: string) => {
      if (!gates.has(q)) {
        let resolve = () => {};
        const promise = new Promise<void>((r) => (resolve = r));
        gates.set(q, { promise, resolve });
      }
      return gates.get(q)!.promise;
    };
    const { seen, signals } = entities({
      gate,
      cities: (q) => [city({ id: q === 'ст' ? CITY_OTHER : CITY_OWN, name: `Город по «${q}»` })],
    });
    mount(meOwner);
    type('ст');
    await waitFor(() => expect(seen.cities).toEqual(['ст']));
    expect(within(group('Города')).getByText('Ищем…')).toBeVisible();
    type('ста');
    await waitFor(() => expect(seen.cities).toEqual(['ст', 'ста']));
    gate('ста');
    gates.get('ста')!.resolve();
    expect(
      await within(group('Города')).findByRole('option', { name: /Город по «ста»/ }),
    ).toBeVisible();
    gate('ст');
    gates.get('ст')!.resolve();
    await act(async () => new Promise((r) => setTimeout(r, 30)));
    expect(screen.queryByRole('option', { name: /Город по «ст»/ })).toBeNull();
    expect(signals.get('ст')?.aborted).toBe(true);
  });

  test('ошибка одной группы — строка в группе, остальное живо', async () => {
    server.use(
      http.get('/v1/geo/cities', ({ response }) => response.untyped(problem(503, 'internal'))),
      http.get('/v1/staff/accounts', ({ response }) =>
        response(200).json({ items: [account()], next_cursor: null }),
      ),
    );
    mount(meOwner);
    type('ма');
    expect(await within(await findGroup('Города')).findByText(/Не удалось найти/)).toBeVisible();
    expect(
      await within(group('Сотрудники')).findByRole('option', { name: /Мария К\./ }),
    ).toBeVisible();
    expect(input()).toHaveValue('ма');
  });

  test('выбор сотрудника — переход на панель, в недавнем имя без почты', async () => {
    entities({ staff: () => [account()] });
    const { context, onClose } = mount(meOwner);
    type('ма');
    fireEvent.click(
      await within(await findGroup('Сотрудники')).findByRole('option', { name: /Мария К\./ }),
    );
    expect(onClose).toHaveBeenCalled();
    expect(context.navigate).toHaveBeenCalledWith({
      to: '/staff',
      search: { account: account().id },
    });
    const raw = localStorage.getItem(RECENT_PREFIX + meOwner.id) ?? '';
    expect(raw).toContain('Мария К.');
    expect(raw).not.toContain('@');
  });

  test('недавнее: пункт без доступа не показывается', () => {
    const id = meModerator.id ?? '';
    pushRecent(id, { kind: 'staff', id: 's1', label: 'Пётр', path: '/staff?account=s1' });
    pushRecent(id, {
      kind: 'city',
      id: CITY_OTHER,
      label: 'Чужой',
      path: `/geo/cities/${CITY_OTHER}`,
    });
    pushRecent(id, { kind: 'section', id: 'section.flags', label: 'Флаги', path: '/flags' });
    pushRecent(id, { kind: 'section', id: 'section.audit', label: 'x', path: '/audit' });
    pushRecent(id, { kind: 'city', id: CITY_OWN, label: 'Свой', path: `/geo/cities/${CITY_OWN}` });
    mount(meModerator);
    // раздел — подпись по языку интерфейса, не сохранённая
    expect(optionNames('Недавнее')).toEqual(['Свой', 'Журнал действий']);
  });

  test('недавнее: выбор — переход по сохранённому адресу', () => {
    pushRecent(meOwner.id ?? '', {
      kind: 'staff',
      id: 's1',
      label: 'Пётр',
      path: '/staff?account=s1',
    });
    const { context } = mount(meOwner);
    fireEvent.click(within(group('Недавнее')).getByRole('option', { name: 'Пётр' }));
    expect(context.navigate).toHaveBeenCalledWith({ to: '/staff', search: { account: 's1' } });
  });

  test('клавиатура: стрелки, Home/End, Enter; aria-activedescendant', () => {
    const { context, onClose } = mount(meOwner);
    const options = () => screen.getAllByRole('option');
    const active = () => input().getAttribute('aria-activedescendant');
    expect(active()).toBe(options()[0]?.id);
    expect(options()[0]).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(active()).toBe(options()[1]?.id);
    expect(options()[1]).toHaveAttribute('aria-selected', 'true');
    expect(options()[0]).toHaveAttribute('aria-selected', 'false');
    fireEvent.keyDown(input(), { key: 'ArrowUp' });
    fireEvent.keyDown(input(), { key: 'ArrowUp' });
    // по кругу — на последний
    expect(active()).toBe(options().at(-1)?.id);
    fireEvent.keyDown(input(), { key: 'Home' });
    expect(active()).toBe(options()[0]?.id);
    fireEvent.keyDown(input(), { key: 'End' });
    expect(active()).toBe(options().at(-1)?.id);
    fireEvent.keyDown(input(), { key: 'Home' });
    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onClose).toHaveBeenCalled();
    expect(context.navigate).toHaveBeenCalledWith({ to: '/' });
  });

  test('Esc с текстом — очистка, второй — закрытие', () => {
    const { onClose } = mount(meOwner);
    type('гор');
    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(input()).toHaveValue('');
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test('число результатов — в вежливой живой области после паузы', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    mount({ ...meOwner, permissions: {} });
    const live = document.querySelector('[aria-live="polite"][data-palette-count]');
    type('тема');
    expect(live?.textContent).toBe('');
    await act(async () => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS));
    expect(live?.textContent).toBe('3 результата');
    type('йййй');
    await act(async () => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS));
    expect(live?.textContent).toBe('Ничего не нашлось');
  });
});

describe('PaletteProvider', () => {
  function app() {
    const handles: { open?: () => void } = {};
    function Probe() {
      const palette = usePalette();
      handles.open = palette?.open;
      return null;
    }
    function WithModal() {
      return (
        <Modal open onClose={() => {}} title="Другое окно">
          <p>содержимое</p>
        </Modal>
      );
    }
    const root = createRootRoute({
      component: () => (
        <PaletteProvider>
          <Probe />
          <RouteBody />
        </PaletteProvider>
      ),
    });
    let modal = false;
    function RouteBody() {
      return modal ? <WithModal /> : null;
    }
    const router = createRouter({
      routeTree: root.addChildren([
        createRoute({ getParentRoute: () => root, path: '/', component: () => null }),
      ]),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    });
    return {
      handles,
      router,
      withModal: () => {
        modal = true;
      },
    };
  }

  test('open() открывает палитру; поверх модалки — нет (m15)', async () => {
    const plain = app();
    const first = renderWithProviders(<RouterProvider router={plain.router} />);
    await waitFor(() => expect(plain.handles.open).toBeDefined());
    act(() => plain.handles.open?.());
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
    first.unmount();

    const over = app();
    over.withModal();
    renderWithProviders(<RouterProvider router={over.router} />);
    await screen.findByRole('dialog', { name: 'Другое окно' });
    act(() => over.handles.open?.());
    expect(screen.queryByRole('dialog', { name: 'Искать везде' })).toBeNull();
  });
});

describe('AppShell: «Искать везде»', () => {
  function shell(path = '/me') {
    const root = createRootRoute({
      component: () => (
        <PaletteProvider>
          <AppShell
            groups={NAV}
            account={{ name: 'Ольга В.', details: 'владелец' }}
            onLogout={() => {}}
          >
            <p>страница</p>
          </AppShell>
        </PaletteProvider>
      ),
    });
    const router = createRouter({
      routeTree: root.addChildren([
        createRoute({ getParentRoute: () => root, path: '$', component: () => null }),
      ]),
      history: createMemoryHistory({ initialEntries: [path] }),
    });
    return renderWithProviders(<RouterProvider router={router} />);
  }

  test('кнопка над меню открывает палитру, Esc возвращает фокус на кнопку', async () => {
    shell();
    const button = await screen.findByRole('button', { name: /Искать везде/ });
    expect(button).toHaveAttribute('aria-keyshortcuts', 'Control+K');
    button.focus(); // fireEvent.click фокус не ставит (браузер — ставит)
    fireEvent.click(button);
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
    expect(input()).toHaveFocus();
    fireEvent.keyDown(input(), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(button).toHaveFocus();
  });

  test('телефон: первый пункт шторки «Ещё» закрывает её и открывает палитру', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    shell();
    fireEvent.click(await screen.findByRole('button', { name: 'Ещё' }));
    const sheet = await screen.findByRole('dialog', { name: 'Разделы и доступ' });
    const first = within(sheet).getAllByRole('button')[1]; // [0] — «Закрыть» в шапке
    expect(first).toHaveAccessibleName('Искать везде');
    fireEvent.click(first!);
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
    expect(screen.queryByRole('dialog', { name: 'Разделы и доступ' })).toBeNull();
  });
});
