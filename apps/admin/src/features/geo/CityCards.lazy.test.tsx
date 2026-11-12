import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, expect, onTestFinished, test, vi } from 'vitest';
import { renderApp } from '../../test/app';
import { auditServer } from '../../test/audit';
import { geoServer } from '../../test/geo';
import { meOwner } from '../../test/me';

// Переход «список → город» при ещё не загруженном куске страницы города (спека UI-фундамента §4.15; ревью
// UF2b/II-B, I2): кусок греется по намерению (наведение, фокус на ссылке карточки); не готов за короткое ожидание —
// переход без View Transition, пока кусок грузится — скелетон раздела; сотрудник ушёл, пока ждали, — переход
// отменён. Один тест на файл: кусок CityPage задерживается воротами (vi.mock), а загруженный кусок кэшируют и vitest,
// и lazyRouteComponent — второй тест файла застал бы его загруженным (второй сценарий — CityCards.pending.test.tsx).
const gate = vi.hoisted(() => {
  let release = () => {};
  let wait = Promise.resolve();
  return {
    close() {
      wait = new Promise<void>((r) => (release = r));
    },
    open: () => release(),
    wait: () => wait,
  };
});

vi.mock('./CityPage', async (original) => {
  await gate.wait();
  return original();
});

afterEach(() => sessionStorage.clear());

const settle = (ms = 50) =>
  act(async () => {
    await new Promise((r) => setTimeout(r, ms));
  });

function stubViewTransition() {
  const start = vi.fn((fn: () => Promise<void>) => {
    const done = Promise.resolve(fn());
    return { updateCallbackDone: done, finished: done, ready: done, skipTransition() {} };
  });
  Object.defineProperty(document, 'startViewTransition', { configurable: true, value: start });
  onTestFinished(() => {
    Reflect.deleteProperty(document, 'startViewTransition');
  });
  return start;
}

const openLink = async () =>
  within(await screen.findByRole('article', { name: 'Ставрополь' })).getByRole('link', {
    name: 'Открыть: Ставрополь',
  });

test('кусок не загружен, сотрудник ушёл в «Журнал» до загрузки — остался в «Журнале»', async () => {
  gate.close();
  geoServer();
  auditServer({ records: [] });
  const start = stubViewTransition();
  const app = renderApp('/geo/cities', { me: meOwner });
  fireEvent.click(await openLink());
  fireEvent.click(screen.getAllByRole('link', { name: 'Журнал действий' })[0] as HTMLElement);
  await screen.findByRole('heading', { level: 1, name: 'Журнал действий' });
  await settle(400);
  gate.open();
  await settle(400);
  expect(app.at()).toBe('/audit');
  expect(start).not.toHaveBeenCalled();
});
