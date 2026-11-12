import {
  createBrowserHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, onTestFinished, test, vi } from 'vitest';
import { meOwner } from '../test/me';
import { renderWithProviders } from '../test/render';
import { Button } from '../ui';
import { panelSearch, usePanelParam } from './panels';

// Панель в адресе (спека UI-фундамента §4.6, план UF2b.8): открытие изнутри — push с меткой state.panel, закрытие —
// history.back; глубокая ссылка — navigate без ключа с replace; close идемпотентен в пределах открытия (m13); ключ
// без права снимается replace. Стенд — browser history окна jsdom (memory history вакуумна для back, R3-1).

let allowed = true;

function List() {
  const [ok, setOk] = useState(allowed);
  const panel = usePanelParam('invite', { allowed: ok });
  const account = usePanelParam('account', { allowed: true });
  return (
    <>
      <h1>Список</h1>
      <Button onClick={() => panel.open('1')}>Открыть</Button>
      <Button onClick={() => setOk(false)}>Снять право</Button>
      <Button onClick={() => account.open('0192f1c4-0000-7b21-9c5d-0000000000ad')}>Карточка</Button>
      <Button onClick={() => account.switch('0192f1c4-0000-7b21-9c5d-0000000000be')}>
        Другая карточка
      </Button>
      <Button
        onClick={() => {
          account.close();
          account.close();
        }}
      >
        Закрыть карточку дважды
      </Button>
      <Button
        onClick={() => {
          panel.close();
          panel.close();
        }}
      >
        Закрыть дважды
      </Button>
      {panel.value !== undefined && <p>панель {panel.value}</p>}
      {account.value !== undefined && <p>карточка {account.value}</p>}
    </>
  );
}

function setup(path: string, state?: Record<string, unknown>) {
  window.history.replaceState(null, '', '/start');
  if (state) window.history.pushState(state, '', path);
  else window.history.replaceState(null, '', path);
  const history = createBrowserHistory();
  onTestFinished(() => history.destroy());
  const root = createRootRoute({ component: Outlet });
  const start = createRoute({
    getParentRoute: () => root,
    path: '/start',
    component: () => <h1>Старт</h1>,
  });
  const list = createRoute({
    getParentRoute: () => root,
    path: '/list',
    validateSearch: (s: Record<string, unknown>) => ({
      ...(typeof s.q === 'string' ? { q: s.q } : {}),
      ...panelSearch(['invite', 'account'])(s),
    }),
    component: List,
  });
  const router = createRouter({ routeTree: root.addChildren([start, list]), history });
  renderWithProviders(<RouterProvider router={router} />, { me: meOwner });
  const back = vi.spyOn(window.history, 'back');
  const length = () => window.history.length;
  return {
    router,
    back,
    length,
    at: () => window.location.pathname + window.location.search,
  };
}

afterEach(() => {
  allowed = true;
  vi.restoreAllMocks();
});

const settle = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

describe('usePanelParam', () => {
  test('открыта изнутри — push с меткой; закрытие — back, адрес без ключа', async () => {
    const { back, at, length } = setup('/list?q=a');
    await screen.findByRole('heading', { name: 'Список' });
    const before = length();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    await screen.findByText('панель 1');
    expect(at()).toBe('/list?q=a&invite=1');
    expect(length()).toBe(before + 1);
    expect(window.history.state).toMatchObject({ panel: true });
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть дважды' }));
    await waitFor(() => expect(at()).toBe('/list?q=a'));
    await settle();
    expect(back).toHaveBeenCalledTimes(1);
    expect(at()).toBe('/list?q=a');
    expect(screen.queryByText(/^панель/)).toBeNull();
  });

  // Ревью UF2b.10-S, m1: смена значения открытой панели (раскрыта другая запись журнала) — replace с прежним state:
  // одна запись истории на панель, метка panel цела — закрытие по-прежнему back на список; повтор close — один back.
  test('switch: открыта изнутри → другое значение replace, метка цела; close дважды — один back на список', async () => {
    const { back, at, length } = setup('/list?q=a');
    await screen.findByRole('heading', { name: 'Список' });
    fireEvent.click(screen.getByRole('button', { name: 'Карточка' }));
    await screen.findByText('карточка 0192f1c4-0000-7b21-9c5d-0000000000ad');
    // записи истории окна живут весь файл (push усекает «вперёд») — сравнение с длиной после открытия
    const opened = length();
    fireEvent.click(screen.getByRole('button', { name: 'Другая карточка' }));
    await screen.findByText('карточка 0192f1c4-0000-7b21-9c5d-0000000000be');
    expect(at()).toBe('/list?q=a&account=0192f1c4-0000-7b21-9c5d-0000000000be');
    expect(length()).toBe(opened);
    expect(window.history.state).toMatchObject({ panel: true });
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть карточку дважды' }));
    await waitFor(() => expect(at()).toBe('/list?q=a'));
    await settle();
    expect(back).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/^карточка/)).toBeNull();
  });

  test('switch без открытой панели — как open (push с меткой)', async () => {
    const { at, back } = setup('/list');
    await screen.findByRole('heading', { name: 'Список' });
    fireEvent.click(screen.getByRole('button', { name: 'Другая карточка' }));
    await screen.findByText('карточка 0192f1c4-0000-7b21-9c5d-0000000000be');
    expect(at()).toBe('/list?account=0192f1c4-0000-7b21-9c5d-0000000000be');
    expect(window.history.state).toMatchObject({ panel: true });
    // push: закрытие уходит back на запись списка
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть карточку дважды' }));
    await waitFor(() => expect(at()).toBe('/list'));
    expect(back).toHaveBeenCalledTimes(1);
  });

  test('глубокая ссылка — закрытие replace без back, «Назад» дальше не ведёт в панель', async () => {
    const { back, at, length } = setup('/list?invite=1&q=a');
    await screen.findByText('панель 1');
    const before = length();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть дважды' }));
    await waitFor(() => expect(at()).toBe('/list?q=a'));
    await settle();
    expect(back).not.toHaveBeenCalled();
    expect(length()).toBe(before);
    expect(at()).toBe('/list?q=a');
  });

  test('F5 на панели, открытой изнутри: метка в history.state пережила — закрытие back', async () => {
    const { back, at } = setup('/list?invite=1', {
      panel: true,
      __TSR_index: 1,
      key: 'f5',
      __TSR_key: 'f5',
    });
    await screen.findByText('панель 1');
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть дважды' }));
    await waitFor(() => expect(at()).toBe('/start'));
    expect(back).toHaveBeenCalledTimes(1);
  });

  test('без права — панели нет, ключ снят replace', async () => {
    allowed = false;
    const { at, length } = setup('/list?invite=1');
    const before = length();
    await screen.findByRole('heading', { name: 'Список' });
    await waitFor(() => expect(at()).toBe('/list'));
    expect(screen.queryByText(/^панель/)).toBeNull();
    expect(length()).toBe(before);
  });

  test('право сняли, пока панель открыта, — ключ снят', async () => {
    const { at } = setup('/list?invite=1');
    await screen.findByText('панель 1');
    fireEvent.click(screen.getByRole('button', { name: 'Снять право' }));
    await waitFor(() => expect(at()).toBe('/list'));
    expect(screen.queryByText(/^панель/)).toBeNull();
  });

  test.each(['/list?invite=2', '/list?account=x', '/list?account=%D1%8F', '/list?invite='])(
    'негодное значение %s — панели нет',
    async (path) => {
      setup(path);
      await screen.findByRole('heading', { name: 'Список' });
      await settle();
      expect(screen.queryByText(/^панель|^карточка/)).toBeNull();
    },
  );

  test('id карточки — строкой в адресе как есть', async () => {
    const { at } = setup('/list');
    await screen.findByRole('heading', { name: 'Список' });
    fireEvent.click(screen.getByRole('button', { name: 'Карточка' }));
    await screen.findByText('карточка 0192f1c4-0000-7b21-9c5d-0000000000ad');
    expect(at()).toBe('/list?account=0192f1c4-0000-7b21-9c5d-0000000000ad');
  });
});

// Ревью UF2b/II-B, m1: открытие панели и закрытие глубокой ссылки (replace) не прокручивают окно наверх
// (resetScroll: false) — список под затемнением на месте. Закрытие back — popstate: позицию записи списка
// восстанавливает scrollRestoration роутера (в приложении включён). jsdom не прокручивает — проверяется вызов scrollTo.
test('открытие панели — без scrollTo окна', async () => {
  setup('/list');
  await screen.findByRole('heading', { name: 'Список' });
  await settle();
  const scroll = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
  await screen.findByText('панель 1');
  await settle();
  expect(scroll.mock.calls).toEqual([]);
});

test('закрытие глубокой ссылки (replace) — без scrollTo окна', async () => {
  const { at } = setup('/list?invite=1');
  await screen.findByText('панель 1');
  await settle();
  const scroll = vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  fireEvent.click(screen.getByRole('button', { name: 'Закрыть дважды' }));
  await waitFor(() => expect(at()).toBe('/list'));
  await settle();
  expect(scroll.mock.calls).toEqual([]);
});
