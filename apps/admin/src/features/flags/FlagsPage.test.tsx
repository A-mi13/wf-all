import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { renderApp } from '../../test/app';
import { flagsServer } from '../../test/flags';
import { flag, meDirectoriesAdmin, meDirectoriesSupport } from '../../test/geo';
import { stubMedia } from '../../test/media';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { http, server } from '../../test/server';
import { city } from '../../test/staff';

type FeatureFlagKey = components['schemas']['FeatureFlagKey'];

// «Флаги функций» (план B, Task 22; спека UI-фундамента §6.2, §3.5): раздел открыт; подписи из локалей, незнакомый
// ключ — сам ключ; без platform.flags.manage — только чтение; пусто, ошибка городов, потолок; на телефоне — список
// по флагам; на грубом указателе — подтверждение шторкой.

afterEach(() => sessionStorage.clear());

const heading = () => screen.findByRole('heading', { level: 1, name: 'Флаги функций' });
const sw = (name: string) => screen.findByRole('switch', { name });

function setup(
  flags = [flag(), flag({ key: 'economy' })],
  { path = '/flags', me = meDirectoriesAdmin } = {},
) {
  sectionsServer();
  const srv = flagsServer(flags);
  const app = renderApp(path, { me });
  return { ...srv, app, user: userEvent.setup() };
}

describe('матрица', () => {
  test('строки — флаги с подписью и пояснением, столбцы — «Везде» и города; текст под матрицей', async () => {
    setup();
    await heading();
    const table = await screen.findByRole('table', { name: 'Флаги функций по городам' });
    const heads = within(table)
      .getAllByRole('columnheader')
      .map((h) => h.textContent);
    expect(heads).toEqual(['Функция', 'Везде', 'Ставрополь', 'Пятигорск']);
    const row = within(table).getByRole('rowheader', { name: /Подписки/ });
    expect(row).toHaveTextContent('Платные тарифы игрока и капитана');
    expect(
      screen.getByText(/Флаг включает функцию городу, а не проценту пользователей/),
    ).toBeInTheDocument();
    expect(screen.getByText('Городов: 2')).toBeInTheDocument();
  });

  test('незнакомый ключ — сам ключ и «нет подписи», без падения', async () => {
    setup([flag({ key: 'night_mode' as FeatureFlagKey })]);
    const row = await screen.findByRole('rowheader', { name: /night_mode/ });
    expect(row).toHaveTextContent('нет подписи');
    expect(await sw('night_mode: Ставрополь')).toBeEnabled();
  });

  test('без platform.flags.manage — все переключатели недоступны', async () => {
    setup(undefined, { me: meDirectoriesSupport });
    await sw('Подписки: Ставрополь');
    for (const s of screen.getAllByRole('switch')) expect(s).toBeDisabled();
  });
});

describe('состояния', () => {
  test('флагов нет — пустое состояние', async () => {
    setup([]);
    expect(await screen.findByText('Флагов нет')).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
  });

  test('городов по фильтру нет — строка и «Сбросить фильтры» снимает фильтр', async () => {
    const { app, user } = setup(undefined, { path: '/flags?q=Zzz' });
    expect(await screen.findByText('По фильтру городов нет')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Сбросить фильтры' }));
    await waitFor(() => expect(app.at()).toBe('/flags'));
    expect(await sw('Подписки: Ставрополь')).toBeInTheDocument();
  });

  test('статус города — в адресе и в запросе', async () => {
    const seen: URLSearchParams[] = [];
    const { app, user } = setup();
    server.use(
      http.get('/v1/geo/cities', ({ request }) => {
        seen.push(new URL(request.url).searchParams);
        return undefined;
      }),
    );
    await sw('Подписки: Ставрополь');
    await user.click(screen.getByRole('button', { name: 'Пилот' }));
    await waitFor(() => expect(app.at()).toBe('/flags?status=pilot'));
    await waitFor(() =>
      expect(screen.queryByRole('switch', { name: 'Подписки: Ставрополь' })).toBeNull(),
    );
    expect(seen.some((q) => q.getAll('status').join() === 'pilot')).toBe(true);
    expect(await sw('Подписки: Пятигорск')).toBeInTheDocument();
  });

  test('города не загрузились — ошибка с «Повторить»', async () => {
    setup();
    server.use(
      http.get('/v1/geo/cities', ({ response }) => response.untyped(problem(500, 'internal'))),
    );
    expect(await screen.findByRole('button', { name: 'Повторить' })).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
  });

  test('больше 2000 городов — предупреждение о потолке', async () => {
    sectionsServer();
    flagsServer([flag()]);
    const all = Array.from({ length: 2050 }, (_, i) =>
      city({ id: `0192f1c4-0000-7000-8000-${String(i).padStart(12, '0')}`, name: `Город ${i}` }),
    );
    server.use(
      http.get('/v1/geo/cities', ({ request, response }) => {
        const q = new URL(request.url).searchParams;
        const from = Number(q.get('cursor') ?? 0);
        const limit = Number(q.get('limit') ?? 20);
        return response(200).json({
          items: all.slice(from, from + limit),
          next_cursor: from + limit < all.length ? String(from + limit) : null,
        });
      }),
    );
    renderApp('/flags', { me: meDirectoriesAdmin });
    expect(
      await screen.findByText('Показаны первые 2000 городов — уточните поиском', undefined, {
        timeout: 5000,
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '2002');
  });
});

describe('устройство', () => {
  test('грубый указатель — подтверждение шторкой: «Отмена» — без запроса, «Включить» — запрос', async () => {
    stubMedia({ width: 1440, pointer: 'coarse' });
    const { calls, user } = setup();
    const cell = await sw('Подписки: Ставрополь');
    await user.click(cell);
    const sheet = await screen.findByRole('dialog', { name: 'Включить «Подписки»: Ставрополь?' });
    await user.click(within(sheet).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(calls).toHaveLength(0);
    await user.click(cell);
    const again = await screen.findByRole('dialog', { name: 'Включить «Подписки»: Ставрополь?' });
    await user.click(within(again).getByRole('button', { name: 'Включить' }));
    await waitFor(() => expect(cell.getAttribute('aria-checked')).toBe('true'));
    expect(calls).toHaveLength(1);
  });

  test('компьютер — без окна', async () => {
    const { calls, user } = setup();
    await user.click(await sw('Подписки: Ставрополь'));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('телефон — список по флагам; раскрытый флаг в адресе, его города с переключателями', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { app, calls, user } = setup([flag(), flag({ key: 'economy', enabled_city_ids: [] })]);
    await heading();
    expect(screen.queryByRole('table')).toBeNull();
    const item = await screen.findByRole('listitem', { name: 'Подписки' });
    expect(within(item).getByText('Выключен')).toBeInTheDocument();
    expect(within(item).getByRole('switch', { name: 'Подписки: везде' })).toBeEnabled();
    await user.click(within(item).getByRole('button', { name: 'Города: Подписки' }));
    await waitFor(() => expect(app.at()).toBe('/flags?flag=subscriptions'));
    const cities = await screen.findByRole('list', { name: 'Города: Подписки' });
    await user.click(within(cities).getByRole('switch', { name: 'Подписки: Ставрополь' }));
    const sheet = await screen.findByRole('dialog', { name: 'Включить «Подписки»: Ставрополь?' });
    await user.click(within(sheet).getByRole('button', { name: 'Включить' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(await within(item).findByText('В 1 городе')).toBeInTheDocument();
  });
});
