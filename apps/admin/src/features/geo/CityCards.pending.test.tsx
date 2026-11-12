import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, onTestFinished, test, vi } from 'vitest';
import { renderApp } from '../../test/app';
import { geoServer } from '../../test/geo';
import { CITY_OWN, meOwner } from '../../test/me';

// Переход «список → город» при ещё не загруженном куске страницы города (спека UI-фундамента §4.15; ревью
// UF2b/II-B, I2): кусок греется по намерению (наведение, фокус на ссылке карточки); не готов за короткое ожидание —
// переход без View Transition, пока кусок грузится — скелетон раздела; сотрудник ушёл, пока ждали, — переход
// отменён. Один тест на файл: кусок CityPage задерживается воротами (vi.mock), а загруженный кусок кэшируют и vitest,
// и lazyRouteComponent — второй тест файла застал бы его загруженным (первый сценарий — CityCards.lazy.test.tsx).
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

test('кусок грузится долго — переход сразу, без View Transition, скелетон раздела', async () => {
  gate.close();
  geoServer();
  const start = stubViewTransition();
  const app = renderApp('/geo/cities', { me: meOwner });
  fireEvent.click(await openLink());
  await waitFor(() => expect(app.at()).toBe(`/geo/cities/${CITY_OWN}`));
  expect(await screen.findByRole('status', { name: 'Загрузка…' })).toBeInTheDocument();
  gate.open();
  await screen.findByRole('link', { name: 'Назад: Города и запуск' });
  expect(start).not.toHaveBeenCalled();
});
