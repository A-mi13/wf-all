import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { renderApp } from '../../test/app';
import { geoServer } from '../../test/geo';
import { meOwner } from '../../test/me';

// Переход «список → город» при ещё не загруженном куске страницы города (спека UI-фундамента §4.15; ревью
// UF2b/II-B, I2): кусок греется по намерению (наведение, фокус на ссылке карточки); не готов за короткое ожидание —
// переход без View Transition, пока кусок грузится — скелетон раздела; сотрудник ушёл, пока ждали, — переход
// отменён. Один тест на файл: кусок CityPage задерживается воротами (vi.mock), а загруженный кусок кэшируют и vitest,
// и lazyRouteComponent — второй тест файла застал бы его загруженным (остальные — CityCards.lazy.test.tsx, CityCards.pending.test.tsx).
const gate = vi.hoisted(() => {
  let release = () => {};
  let wait = Promise.resolve();
  return {
    started: false,
    close() {
      wait = new Promise<void>((r) => (release = r));
    },
    open: () => release(),
    wait: () => wait,
  };
});

vi.mock('./CityPage', async (original) => {
  gate.started = true;
  await gate.wait();
  return original();
});

afterEach(() => sessionStorage.clear());

const openLink = async () =>
  within(await screen.findByRole('article', { name: 'Ставрополь' })).getByRole('link', {
    name: 'Открыть: Ставрополь',
  });

test('наведение на «Открыть: <город>» — кусок страницы города начинает грузиться до клика', async () => {
  gate.close();
  geoServer();
  renderApp('/geo/cities', { me: meOwner });
  const link = await openLink();
  expect(gate.started).toBe(false);
  fireEvent.pointerEnter(link);
  await waitFor(() => expect(gate.started).toBe(true));
  gate.open();
});
