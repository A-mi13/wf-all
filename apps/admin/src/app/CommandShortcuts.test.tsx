import {
  createBrowserHistory,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useLocation,
} from '@tanstack/react-router';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { type ReactNode, useState } from 'react';
import { afterEach, describe, expect, onTestFinished, test, vi } from 'vitest';
import { meModerator, meOwner } from '../test/me';
import { problem } from '../test/problem';
import { renderWithProviders } from '../test/render';
import { http, server } from '../test/server';
import { Button, Modal } from '../ui';
import { pushOverlay } from '../ui/overlay/stack';
import { usePalette } from '../ui/palette/context';
import { SHORTCUTS_SINGLE_KEY } from '../ui/shortcuts';
import { DataTable } from '../ui/table';
import { findPageSearch } from '../ui/table/model';
import { useLeaveGuard } from './leaveGuard';
import { PaletteProvider } from './PaletteProvider';
import type { Me } from './permissions';

// Связка горячих клавиш с палитрой и реестром команд (спека UI-фундамента §4.1–4.2, UF4 волна 2): Ctrl/⌘+K, g x из
// features/*/commands.ts, «/» одной регистрацией, «?» и команда «Горячие клавиши», CommandContext провайдера.

function Where() {
  const at = useLocation();
  return <p data-testid="at">{at.pathname + at.searchStr}</p>;
}

function Form() {
  const [email, setEmail] = useState('');
  useLeaveGuard(email !== '');
  return (
    <label>
      Почта
      <input value={email} onChange={(e) => setEmail(e.target.value)} />
    </label>
  );
}

function List() {
  const [q, setQ] = useState('');
  return (
    <DataTable
      label="Сотрудники"
      columns={[{ id: 'name', header: 'Имя', cell: (r: { id: string }) => r.id }]}
      rowKey={(r) => r.id}
      rows={[]}
      onRetry={() => {}}
      search={{ value: q, onChange: setQ, placeholder: 'Имя или почта' }}
    />
  );
}

function PaletteButton() {
  const palette = usePalette();
  return <Button onClick={() => palette?.open()}>Открыть палитру</Button>;
}

function ModalPage() {
  return (
    <Modal open onClose={() => {}} title="Другое окно">
      <p>содержимое</p>
    </Modal>
  );
}

function setup(path = '/me', opts: { me?: Me; browser?: boolean } = {}) {
  let history;
  if (opts.browser) {
    window.history.replaceState(null, '', path);
    const browser = createBrowserHistory();
    onTestFinished(() => browser.destroy());
    history = browser;
  } else {
    history = createMemoryHistory({ initialEntries: [path] });
  }
  const root = createRootRoute({
    component: () => (
      <PaletteProvider>
        <Where />
        <PaletteButton />
        <input aria-label="Заметка" />
        <Outlet />
      </PaletteProvider>
    ),
  });
  const child = (p: string, component: () => ReactNode) =>
    createRoute({ getParentRoute: () => root, path: p, component });
  const router = createRouter({
    routeTree: root.addChildren([
      child('/form', Form),
      child('/list', List),
      child('/modal', ModalPage),
      child('$', () => null),
    ]),
    history,
  });
  const r = renderWithProviders(<RouterProvider router={router} />, { me: opts.me ?? meOwner });
  return { ...r, router, at: () => screen.getByTestId('at').textContent };
}

const press = (code: string, key: string, init: KeyboardEventInit = {}) =>
  fireEvent.keyDown(document.activeElement ?? document.body, { code, key, ...init });
const ctrlK = () => press('KeyK', 'k', { ctrlKey: true });
const palette = () => screen.queryByRole('dialog', { name: 'Искать везде' });
const help = () => screen.queryByRole('dialog', { name: 'Горячие клавиши' });
const QUESTION = 'Уйти без сохранения?';

afterEach(() => {
  localStorage.clear();
});

describe('Ctrl/⌘+K', () => {
  test('открывает палитру, и из поля ввода; «k» в поле — нет', async () => {
    setup();
    await screen.findByTestId('at');
    screen.getByLabelText('Заметка').focus();
    press('KeyK', 'k');
    expect(palette()).toBeNull();
    ctrlK();
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
  });

  test('⌘K на Mac', async () => {
    const platform = vi.spyOn(navigator, 'platform', 'get').mockReturnValue('MacIntel');
    try {
      setup();
      await screen.findByTestId('at');
      press('KeyK', 'k', { metaKey: true });
      expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
    } finally {
      platform.mockRestore();
    }
  });

  test('поверх модалки — нет; при открытой подсказке — да (R2-1)', async () => {
    const view = setup('/modal');
    await screen.findByRole('dialog', { name: 'Другое окно' });
    ctrlK();
    expect(palette()).toBeNull();
    view.unmount();

    setup();
    await screen.findByTestId('at');
    const pop = pushOverlay(Symbol('hint'), null, { modal: false });
    try {
      ctrlK();
    } finally {
      pop();
    }
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
  });
});

describe('g x из реестра команд', () => {
  test('g c → «Города и запуск» в русской раскладке, g h → главная', async () => {
    const { at } = setup();
    await screen.findByTestId('at');
    press('KeyG', 'п');
    press('KeyC', 'с');
    await waitFor(() => expect(at()).toBe('/geo/cities'));
    press('KeyG', 'g');
    press('KeyH', 'h');
    await waitFor(() => expect(at()).toBe('/'));
  });

  test('раздел без права — клавиша не зарегистрирована и не в шпаргалке (модератор: g o)', async () => {
    const { at } = setup('/me', { me: meModerator });
    await screen.findByTestId('at');
    press('KeyG', 'g');
    press('KeyO', 'o');
    press('KeyG', 'g');
    press('KeyC', 'c');
    await waitFor(() => expect(at()).toBe('/geo/cities'));
    press('Slash', '?', { shiftKey: true });
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    expect(within(dialog).getByText('Города и запуск')).toBeInTheDocument();
    expect(within(dialog).queryByText('Страны')).toBeNull();
    expect(within(dialog).getByText('Искать везде')).toBeInTheDocument();
  });

  test('g x с грязной формой → ровно один вопрос; «Остаться» — адрес и ввод на месте', async () => {
    const { at } = setup('/form', { browser: true });
    fireEvent.change(await screen.findByLabelText('Почта'), {
      target: { value: 'maria@example.com' },
    });
    (document.activeElement as HTMLElement).blur();
    press('KeyG', 'g');
    press('KeyA', 'a');
    await screen.findByRole('dialog', { name: QUESTION });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(screen.queryAllByRole('dialog', { name: QUESTION })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() =>
      expect(screen.queryAllByRole('dialog', { name: QUESTION })).toHaveLength(0),
    );
    expect(at()).toBe('/form');
    expect(window.location.pathname).toBe('/form');
    expect(screen.getByLabelText('Почта')).toHaveValue('maria@example.com');
  });
});

describe('«/» и «?»', () => {
  test('«/» — фокус в поиск таблицы страницы, одна регистрация без конфликта', async () => {
    const error = vi.spyOn(console, 'error');
    try {
      setup('/list');
      const search = await screen.findByRole('searchbox', { name: 'Поиск' });
      press('Slash', '/');
      expect(search).toHaveFocus();
      expect(palette()).toBeNull();
      expect(error).not.toHaveBeenCalledWith(expect.stringContaining('useShortcut'));
    } finally {
      error.mockRestore();
    }
  });

  test('поиск страницы — первый не под inert и hidden', () => {
    const root = document.createElement('div');
    root.innerHTML =
      '<div inert><input data-page-search id="a"></div><div hidden><input data-page-search id="b"></div>' +
      '<input data-page-search id="c">';
    expect(findPageSearch(root)?.id).toBe('c');
    expect(findPageSearch(document.createElement('div'))).toBeNull();
  });

  test('«/» без поиска на странице — палитра', async () => {
    setup('/me');
    await screen.findByTestId('at');
    press('Slash', '/');
    expect(await screen.findByRole('dialog', { name: 'Искать везде' })).toBeVisible();
  });

  test('односимвольные выключены: Ctrl+K → «Горячие клавиши» → шпаргалка с переключателем (WCAG 2.1.4)', async () => {
    localStorage.setItem(SHORTCUTS_SINGLE_KEY, '0');
    setup();
    await screen.findByTestId('at');
    press('Slash', '?', { shiftKey: true });
    expect(help()).toBeNull();
    ctrlK();
    const input = await screen.findByRole('combobox');
    fireEvent.change(input, { target: { value: 'горячие' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    const single = within(dialog).getByRole('switch', { name: 'Односимвольные клавиши' });
    expect(single).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(single);
    expect(localStorage.getItem(SHORTCUTS_SINGLE_KEY)).toBe('1');
  });

  test('шпаргалка из палитры: Esc — фокус на том, кто открыл палитру', async () => {
    setup();
    const button = await screen.findByRole('button', { name: 'Открыть палитру' });
    button.focus();
    fireEvent.click(button);
    const input = await screen.findByRole('combobox');
    fireEvent.change(input, { target: { value: 'горячие' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    expect(palette()).toBeNull();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(help()).toBeNull());
    expect(button).toHaveFocus();
  });
});

describe('CommandContext провайдера', () => {
  test('«Язык: English» — PATCH языка с Idempotency-Key, интерфейс на английском', async () => {
    const seen: { key: string | null; body: unknown }[] = [];
    server.use(
      http.patch('/v1/staff/me/locale', async ({ request, response }) => {
        seen.push({ key: request.headers.get('Idempotency-Key'), body: await request.json() });
        return response(204).empty();
      }),
    );
    setup();
    await screen.findByTestId('at');
    ctrlK();
    const input = await screen.findByRole('combobox');
    fireEvent.change(input, { target: { value: 'english' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(seen).toHaveLength(1));
    expect(seen[0]?.key).toMatch(/\S/);
    expect(seen[0]?.body).toEqual({ locale: 'en' });
    await waitFor(() => expect(document.documentElement.lang).toBe('en'));
  });

  test('«Язык» — ошибка сервера: тост с текстом ошибки, язык прежний', async () => {
    server.use(
      http.patch('/v1/staff/me/locale', ({ response }) =>
        response.untyped(problem(503, 'internal')),
      ),
    );
    setup();
    await screen.findByTestId('at');
    ctrlK();
    const input = await screen.findByRole('combobox');
    fireEvent.change(input, { target: { value: 'english' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByText(/Что-то пошло не так/)).toBeInTheDocument();
    expect(document.documentElement.lang).toBe('ru');
  });
});
