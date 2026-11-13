import { screen, waitFor } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { renderApp } from '../test/app';
import { meOwner } from '../test/me';
import { sectionsServer } from '../test/sections';

// Ошибка загрузки куска раздела (спека UI-фундамента §4.15, план UF2b.9): вышла новая сборка — старого куска нет.
// TanStack Router 1.170 (lazyRouteComponent.tsx:52–68) на такую ошибку один раз перезагружает страницу сам (метка
// tanstack_router_reload:<текст> в sessionStorage); повторная — уходит в errorComponent маршрута: ErrorState с
// «Обновить страницу». Свой файл: подмена модуля (vi.mock) и кэш куска в lazyRouteComponent — на весь файл.
const { CHUNK } = vi.hoisted(() => ({
  CHUNK: 'Failed to fetch dynamically imported module: /assets/MyAccessPage-x.js',
}));

vi.mock('../features/me/MyAccessPage', () => {
  throw new Error(CHUNK);
});

afterEach(() => sessionStorage.clear());

test('кусок раздела не загрузился (перезагрузка уже была) — «Обновить страницу», каркас на месте', async () => {
  sectionsServer();
  // перезагрузка роутером уже была (метка); vitest к тому же оборачивает ошибку фабрики своим текстом — путь
  // самоперезагрузки не срабатывает и в jsdom не исполнится
  sessionStorage.setItem(`tanstack_router_reload:${CHUNK}`, '1');
  renderApp('/me', { me: meOwner });
  // ошибка — после повторной попытки роутера (кадр ошибки пересоздаётся): ждём установившееся состояние
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Не удалось загрузить'));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Обновить страницу' })).toBeInTheDocument(),
  );
  // меню каркаса — в основном куске: можно уйти в другой раздел
  expect(screen.getByRole('navigation', { name: 'Разделы' })).toBeInTheDocument();
});
