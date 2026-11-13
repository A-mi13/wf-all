import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { reauthBus } from '../app/reauth';
import { renderApp } from './app';
import { meOwner } from './me';
import { sectionsServer } from './sections';

// Стенд renderApp на browser history (спека UI-фундамента §4.11, R3-1, m-l, m10): memory history
// back() блокировщики не опрашивает — тесты «Назад» с блокировщиком и закрытия панели идут на настоящей
// истории окна jsdom. Стенд ставит адрес до createBrowserHistory и в cleanup делает destroy():
// @tanstack/history 1.162.5 подменяет window.history.pushState/replaceState и вешает popstate и
// beforeunload — destroy возвращает исходные функции и снимает слушатели (проверено по исходникам).
// Записи истории окна живут весь файл: тест «Назад» не уходит дальше своих push.

beforeEach(() => sectionsServer());
afterEach(() => {
  reauthBus.reject();
});

// Исходные функции истории окна — до любого renderApp в файле.
const pushState = window.history.pushState;
const replaceState = window.history.replaceState;
// Слушатели окна по типу: + при addEventListener, − при removeEventListener. Уборка стенда
// (onTestFinished) идёт после afterEach, поэтому шпионы живут до следующего теста и снимаются в нём.
const count = new Map<string, number>();
const listeners = (type: string) => count.get(type) ?? 0;
function countWindowListeners() {
  const bump = (type: string, d: number) => count.set(type, (count.get(type) ?? 0) + d);
  const add = window.addEventListener.bind(window);
  const remove = window.removeEventListener.bind(window);
  vi.spyOn(window, 'addEventListener').mockImplementation((type, fn, opts) => {
    // одноразовый (ожидание popstate в back() стенда) снимается сам, без removeEventListener
    if (!(typeof opts === 'object' && opts.once)) bump(type, 1);
    add(type, fn, opts);
  });
  vi.spyOn(window, 'removeEventListener').mockImplementation((type, fn, opts) => {
    bump(type, -1);
    remove(type, fn, opts);
  });
}

test('browser: back() опрашивает блокировщик один раз, адрес возвращается', async () => {
  countWindowListeners();

  // не стартовая страница: адрес из opts, а не прежний адрес окна и не редирект на стартовую
  const app = renderApp('/staff', { me: meOwner, history: 'browser' });
  await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
  expect(window.location.pathname).toBe('/staff');
  expect(window.history.pushState).not.toBe(pushState); // история подменила — destroy обязан вернуть

  await userEvent.setup().click(screen.getByRole('link', { name: 'Журнал действий' }));
  await screen.findByRole('heading', { level: 1, name: 'Журнал действий' });
  expect(app.at()).toBe('/audit');

  // блокировщик как у useBlocker(shouldBlockFn → true): уход «Назад» запрещён
  const blockerFn = vi.fn(() => true);
  const unblock = app.history.block({ blockerFn, enableBeforeUnload: false });
  await app.back();
  await waitFor(() => expect(blockerFn).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(app.at()).toBe('/audit'));
  expect(screen.getByRole('heading', { level: 1, name: 'Журнал действий' })).toBeInTheDocument();
  unblock();

  // без блокировщика «Назад» уходит
  await app.back();
  await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
  expect(app.at()).toBe('/staff');
  expect(listeners('popstate')).toBeGreaterThan(0);
});

// Уборка стенда после предыдущего теста: destroy() вернул исходные функции и снял слушатели.
test('после теста на browser — исходные pushState/replaceState, слушателей окна столько же, сколько до', () => {
  // проверка уборки предыдущего теста: запущенная одна (-t) или после пропуска первого — красная, а не
  // вакуумно зелёная (ревью m7)
  expect(count.get('popstate'), 'первый тест не шёл — нечего проверять').toBeDefined();
  expect(window.history.pushState).toBe(pushState);
  expect(window.history.replaceState).toBe(replaceState);
  expect(listeners('popstate')).toBe(0);
  expect(listeners('beforeunload')).toBe(0);
  vi.restoreAllMocks();
});

test('по умолчанию — memory: адрес окна не трогается', async () => {
  const before = window.location.pathname + window.location.search;
  const app = renderApp('/staff', { me: meOwner });
  await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
  expect(app.at()).toBe('/staff');
  expect(window.location.pathname + window.location.search).toBe(before);
});
