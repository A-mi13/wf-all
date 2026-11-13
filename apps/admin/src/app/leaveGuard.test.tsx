import {
  createBrowserHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
  useNavigate,
} from '@tanstack/react-router';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { describe, expect, onTestFinished, test } from 'vitest';
import { meOwner } from '../test/me';
import { renderWithProviders } from '../test/render';
import { http, server } from '../test/server';
import { Button } from '../ui';
import { useLeaveGuard } from './leaveGuard';
import { useLogout } from './logout';

// Защита от потери (спека UI-фундамента §4.11, Review Focus 9): свой вопрос при переходе внутри приложения,
// стандартный — при F5 и закрытии вкладки (beforeunload), «Выйти» — вопрос до POST. Экранов с useLeaveGuard ещё
// нет (UF2b.10), поэтому стенд — свой маленький роутер на browser history окна jsdom, как renderApp(path,
// { history: 'browser' }) (memory history не опрашивает блокировщики на back — R3-1): адрес — до истории,
// destroy() — после теста, «ровно один вопрос» — на установившемся состоянии (m-m).

function Form({ second = false }: { second?: boolean }) {
  const [email, setEmail] = useState('');
  const [done, setDone] = useState(false);
  const [note, setNote] = useState('');
  const navigate = useNavigate();
  useLeaveGuard(email !== '' && !done);
  useLeaveGuard(second && note !== '');
  // успех: форма сброшена (dirty = false уже применён), переход — следующим эффектом
  useEffect(() => {
    if (done) void navigate({ to: '/audit' });
  }, [done, navigate]);
  const logout = useLogout();
  return (
    <>
      <label>
        Почта
        <input value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      {second && (
        <label>
          Заметка
          <input value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
      )}
      <Button onClick={() => setDone(true)}>Отправить</Button>
      <Button onClick={() => logout.mutate()}>Выйти</Button>
    </>
  );
}

function setup(path = '/form', opts: { second?: boolean } = {}) {
  window.history.replaceState(null, '', path);
  const history = createBrowserHistory();
  onTestFinished(() => history.destroy());
  const root = createRootRoute({
    component: () => (
      <>
        <Link to="/audit">Журнал</Link>
        <Outlet />
      </>
    ),
  });
  const form = createRoute({
    getParentRoute: () => root,
    path: '/form',
    component: () => <Form second={opts.second} />,
  });
  const audit = createRoute({
    getParentRoute: () => root,
    path: '/audit',
    component: () => <h1>Журнал действий</h1>,
  });
  const login = createRoute({
    getParentRoute: () => root,
    path: '/login',
    component: () => <h1>Вход</h1>,
  });
  const router = createRouter({ routeTree: root.addChildren([form, audit, login]), history });
  const r = renderWithProviders(<RouterProvider router={router} />, { me: meOwner });
  return { ...r, router, at: () => window.location.pathname + window.location.search };
}

const QUESTION = 'Уйти без сохранения?';
const questions = () => screen.queryAllByRole('dialog', { name: QUESTION });
const type = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label), { target: { value } });

// «Ровно один» — после того как адрес и вопрос пришли в конечное состояние (второй мог бы прийти тактом позже).
async function exactlyOne() {
  await screen.findByRole('dialog', { name: QUESTION });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  await waitFor(() => expect(questions()).toHaveLength(1));
}

function beforeUnloadPrevented(): boolean {
  const e = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(e);
  return e.defaultPrevented;
}

describe('useLeaveGuard', () => {
  test('ссылка меню при вводе — ровно один вопрос; «Остаться» — адрес и ввод на месте', async () => {
    const { at } = setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('link', { name: 'Журнал' }));
    await exactlyOne();
    expect(screen.getByRole('button', { name: 'Остаться' })).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(questions()).toHaveLength(0));
    expect(at()).toBe('/form');
    expect(screen.getByLabelText('Почта')).toHaveValue('maria@example.com');
  });

  test('«Уйти» — переход состоялся', async () => {
    const { at } = setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('link', { name: 'Журнал' }));
    await exactlyOne();
    fireEvent.click(screen.getByRole('button', { name: 'Уйти' }));
    await screen.findByRole('heading', { name: 'Журнал действий' });
    expect(at()).toBe('/audit');
    expect(questions()).toHaveLength(0);
  });

  test('чистая форма — переход без вопроса', async () => {
    const { at } = setup();
    await screen.findByLabelText('Почта');
    fireEvent.click(screen.getByRole('link', { name: 'Журнал' }));
    await screen.findByRole('heading', { name: 'Журнал действий' });
    expect(at()).toBe('/audit');
    expect(questions()).toHaveLength(0);
  });

  test('«Назад» браузера при вводе — один вопрос; «Остаться» — адрес вернулся, ввод на месте', async () => {
    const { router, at } = setup('/audit');
    await screen.findByRole('heading', { name: 'Журнал действий' });
    await act(() => router.navigate({ to: '/form' }));
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    await act(async () => window.history.back());
    await exactlyOne();
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(questions()).toHaveLength(0));
    await waitFor(() => expect(at()).toBe('/form'));
    expect(screen.getByLabelText('Почта')).toHaveValue('maria@example.com');
  });

  test('две грязные формы — один вопрос на уход', async () => {
    setup('/form', { second: true });
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    type('Заметка', 'позвонить');
    fireEvent.click(screen.getByRole('link', { name: 'Журнал' }));
    await exactlyOne();
    fireEvent.click(screen.getByRole('button', { name: 'Уйти' }));
    await screen.findByRole('heading', { name: 'Журнал действий' });
    expect(questions()).toHaveLength(0);
  });

  test('успех отправки → переход без вопроса', async () => {
    const { at } = setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Отправить' }));
    await screen.findByRole('heading', { name: 'Журнал действий' });
    expect(at()).toBe('/audit');
    expect(questions()).toHaveLength(0);
  });

  test('чистая форма не вешает beforeunload; грязная — стандартный вопрос браузера', async () => {
    setup();
    await screen.findByLabelText('Почта');
    expect(beforeUnloadPrevented()).toBe(false);
    type('Почта', 'maria@example.com');
    await waitFor(() => expect(beforeUnloadPrevented()).toBe(true));
    type('Почта', '');
    await waitFor(() => expect(beforeUnloadPrevented()).toBe(false));
  });

  // Ревью UF2b/II-A, m1 (вместо прежнего «сессия кончилась — без вопроса»: тот проверял ignoreBlocker роутера, а не
  // наш код, — m5). Вопрос открыт, а форма снята мимо блокировщика (конец сессии — редирект на вход, смена
  // сотрудника — каркас с key={me.id}): вопрос снимается, отложенный переход отменяется.
  test('форма снята, пока открыт вопрос, — вопрос снят, отложенный переход не исполнен', async () => {
    const { router, at } = setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('link', { name: 'Журнал' }));
    await exactlyOne();
    await act(() => router.navigate({ to: '/login', ignoreBlocker: true }));
    await screen.findByRole('heading', { name: 'Вход' });
    await waitFor(() => expect(questions()).toHaveLength(0));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(at()).toBe('/login');
  });
});

describe('«Выйти» при несохранённом (m8: вопрос в useLogout до POST)', () => {
  function logoutServer() {
    const posts: string[] = [];
    server.use(
      http.post('/v1/staff/auth/logout', ({ request, response }) => {
        posts.push(request.headers.get('Idempotency-Key') ?? '');
        return response(204).empty();
      }),
      http.get('/v1/staff/me', ({ response }) => response(200).json(meOwner)),
    );
    return posts;
  }

  test('«Остаться» — POST не ушёл', async () => {
    const posts = logoutServer();
    setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Выйти' }));
    await exactlyOne();
    expect(posts).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(questions()).toHaveLength(0));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(posts).toHaveLength(0);
    expect(screen.getByLabelText('Почта')).toHaveValue('maria@example.com');
  });

  test('«Уйти» — POST ушёл один раз', async () => {
    const posts = logoutServer();
    setup();
    await screen.findByLabelText('Почта');
    type('Почта', 'maria@example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Выйти' }));
    await exactlyOne();
    fireEvent.click(screen.getByRole('button', { name: 'Уйти' }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(questions()).toHaveLength(0);
  });

  test('чистая форма — POST сразу, без вопроса', async () => {
    const posts = logoutServer();
    setup();
    await screen.findByLabelText('Почта');
    fireEvent.click(screen.getByRole('button', { name: 'Выйти' }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(questions()).toHaveLength(0);
  });
});
