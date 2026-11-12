import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp } from '../../test/app';
import { meModerator, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { account, staffServer, TEAM } from '../../test/staff';

// Панели «Ролей и доступа» в адресе (спека UI-фундамента §4.6, §4.11; план UF2b.8, Review Focus 9–10): приглашение —
// ?invite=1, карточка сотрудника — ?account=<id>. Один вопрос на один уход (закрытие после «Уйти» — с ignoreBlocker,
// исключений по ключу панели у блокировщика нет), ключ без права снимается, 403 и 404 по id — одно состояние. Все
// тесты — browser history окна jsdom (R3-1): записи живут весь файл, тест не уходит «Назад» дальше своих push.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.restoreAllMocks();
});

const QUESTION = 'Уйти без сохранения?';
const INVITE = 'Приглашение сотрудника';
const questions = () => screen.queryAllByRole('dialog', { name: QUESTION });
const inviteDialog = () => screen.queryByRole('dialog', { name: INVITE });
const settle = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

// «Ровно один» — на установившемся состоянии (m-m): второй вопрос мог бы прийти тактом позже.
async function exactlyOneQuestion() {
  await screen.findByRole('dialog', { name: QUESTION });
  await settle();
  await waitFor(() => expect(questions()).toHaveLength(1));
}

async function openInviteInside() {
  fireEvent.click(await screen.findByRole('button', { name: 'Пригласить сотрудника' }));
  return screen.findByRole('dialog', { name: INVITE });
}

function typeEmail(dialog: HTMLElement) {
  fireEvent.change(within(dialog).getByLabelText('Рабочая почта'), {
    target: { value: 'ivan.p@wf.example' },
  });
}

const search = () => window.location.search;

describe('приглашение в адресе — один вопрос на один уход', () => {
  test('открыто изнутри → ввод → Esc → «Уйти»: ровно один вопрос, панель закрыта, адрес без invite', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    const dialog = await openInviteInside();
    expect(search()).toBe('?invite=1');
    typeEmail(dialog);
    fireEvent.keyDown(document, { key: 'Escape' });
    await exactlyOneQuestion();
    fireEvent.click(screen.getByRole('button', { name: 'Уйти' }));
    await waitFor(() => expect(inviteDialog()).toBeNull());
    await waitFor(() => expect(window.location.pathname + search()).toBe('/staff'));
    await settle();
    expect(questions()).toHaveLength(0);
    expect(inviteDialog()).toBeNull();
  });

  test('глубокая ссылка ?invite=1 → ввод → Esc → «Уйти»: ровно один вопрос', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff?invite=1', { me: meOwner, history: 'browser' });
    const dialog = await screen.findByRole('dialog', { name: INVITE });
    typeEmail(dialog);
    fireEvent.keyDown(document, { key: 'Escape' });
    await exactlyOneQuestion();
    fireEvent.click(screen.getByRole('button', { name: 'Уйти' }));
    await waitFor(() => expect(window.location.pathname + search()).toBe('/staff'));
    await settle();
    expect(questions()).toHaveLength(0);
    expect(inviteDialog()).toBeNull();
  });

  test('открыто изнутри → ввод → браузерная «Назад»: один вопрос, «Остаться» — ?invite=1 и почта на месте', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    const dialog = await openInviteInside();
    typeEmail(dialog);
    await act(async () => window.history.back());
    await exactlyOneQuestion();
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(questions()).toHaveLength(0));
    await waitFor(() => expect(search()).toBe('?invite=1'));
    await settle();
    expect(questions()).toHaveLength(0);
    const still = screen.getByRole('dialog', { name: INVITE });
    expect(within(still).getByLabelText('Рабочая почта')).toHaveValue('ivan.p@wf.example');
  });

  test('грязное приглашение → пункт меню: ровно один вопрос', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    const dialog = await openInviteInside();
    typeEmail(dialog);
    fireEvent.click(screen.getAllByRole('link', { name: 'Журнал действий' })[0] as HTMLElement);
    await exactlyOneQuestion();
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(questions()).toHaveLength(0));
    expect(window.location.pathname + search()).toBe('/staff?invite=1');
  });

  test('открыто изнутри → двойной Esc до popstate: ровно один history.back, адрес — список раздела', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    await openInviteInside();
    const back = vi.spyOn(window.history, 'back');
    fireEvent.keyDown(document, { key: 'Escape' });
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(inviteDialog()).toBeNull());
    await settle();
    expect(back).toHaveBeenCalledTimes(1);
    expect(window.location.pathname + search()).toBe('/staff');
    expect(screen.getByRole('heading', { level: 1, name: 'Роли и доступ' })).toBeInTheDocument();
  });

  // Ревью UF2b/II-B, I1: защита от повтора — на одно появление записи, а не навсегда: «Вперёд» браузера возвращает
  // ту же запись (тот же ключ в history.state), и её панель снова закрывается.
  test('открыто → Esc → «Вперёд» браузера → Esc снова закрывает', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    await openInviteInside();
    const back = vi.spyOn(window.history, 'back');
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(inviteDialog()).toBeNull());
    await waitFor(() => expect(search()).toBe(''));
    await act(async () => {
      const popped = new Promise<void>((r) =>
        window.addEventListener('popstate', () => r(), { once: true }),
      );
      window.history.forward();
      await popped;
    });
    await screen.findByRole('dialog', { name: INVITE });
    expect(search()).toBe('?invite=1');
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(inviteDialog()).toBeNull());
    await waitFor(() => expect(search()).toBe(''));
    expect(back).toHaveBeenCalledTimes(2);
  });

  test('открыто изнутри → «Закрыть» дважды до popstate: ровно один history.back', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner, history: 'browser' });
    const dialog = await openInviteInside();
    const back = vi.spyOn(window.history, 'back');
    const close = within(dialog).getAllByRole('button', { name: 'Закрыть' })[0] as HTMLElement;
    fireEvent.click(close);
    fireEvent.click(close);
    await waitFor(() => expect(inviteDialog()).toBeNull());
    await settle();
    expect(back).toHaveBeenCalledTimes(1);
    expect(window.location.pathname + search()).toBe('/staff');
  });
});

describe('ключ без права и чужой id', () => {
  test('модератор: ?invite=1 без права выдачи — панели нет, ключ снят', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff?invite=1', { me: meModerator, history: 'browser' });
    await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
    await waitFor(() => expect(window.location.pathname + search()).toBe('/staff'));
    await settle();
    expect(inviteDialog()).toBeNull();
  });

  test('модератор: ?account=<id> без staff.accounts.read — панели нет, карточку не запрашивает', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    const id = account().id;
    renderApp(`/staff?account=${id}`, { me: meModerator, history: 'browser' });
    await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
    await waitFor(() => expect(window.location.pathname + search()).toBe('/staff'));
    await settle();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(seen.cards).toEqual([]);
  });

  // Ревью UF2b/II-B, m4: оба ответа — в одном тесте (сравнение не зависит от порядка и фильтра -t). Глубокая ссылка
  // открывает панель и на memory history — стенд по умолчанию, два рендера подряд без общей истории окна.
  test('?account=<id> → 404 и 403 — одинаковое «Сотрудник не найден», без кода ответа', async () => {
    const missing = '0192f1c4-ffff-7b21-9c5d-00000000ffff';
    const shown: string[] = [];
    for (const status of [404, 403] as const) {
      staffServer({ accounts: TEAM });
      server.use(
        http.get('/v1/staff/accounts/{staffId}', ({ response }) =>
          response.untyped(problem(status, status === 404 ? 'staff.not_found' : 'auth.forbidden')),
        ),
      );
      renderApp(`/staff?account=${missing}`, { me: meOwner });
      const dialog = await screen.findByRole('dialog', { name: 'Сотрудник' });
      await within(dialog).findByText('Сотрудник не найден');
      expect(dialog.textContent).not.toMatch(/40[34]|auth\.|staff\./);
      shown.push(dialog.textContent ?? '');
      cleanup();
    }
    expect(shown[1]).toBe(shown[0]);
  });

  test('?account=<id> по глубокой ссылке — карточка открыта (F5 на открытой панели)', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    const target = account();
    renderApp(`/staff?account=${target.id}`, { me: meOwner, history: 'browser' });
    await screen.findByRole('dialog', { name: target.display_name ?? '' });
    expect(seen.cards).toContain(target.id);
  });
});
