import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { importsServer, meDirectoriesAdmin } from '../../test/geo';
import { openOptions, pick } from '../../test/pick';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';

// «Запустить импорт» (план B, Task 20): ?start=1 — окно со страной (все страны справочника, выключенные — с
// пометкой) → startGeoImport → 202 {job_id} → строка задачи над журналом (?job=), окно снято replace. Неизвестный
// исход — окно открыто, повтор тем же ключом. Выбранная страна — ввод: закрытие спрашивает.

afterEach(() => sessionStorage.clear());

const dialog = () => screen.findByRole('dialog', { name: 'Запустить импорт GeoNames' });

function setup(opts: Parameters<typeof importsServer>[0] = {}) {
  sectionsServer();
  const srv = importsServer(opts);
  const app = renderApp('/geo/imports?start=1', { me: meDirectoriesAdmin });
  return { srv, app, user: userEvent.setup() };
}

test('страны — включённые и выключенные (с пометкой)', async () => {
  const { user } = setup();
  const d = await dialog();
  const options = await openOptions(user, within(d).getByRole('combobox', { name: 'Страна' }));
  expect(options.map((o) => o.label)).toEqual(['Россия (RU)', 'Казахстан (KZ) (выключена)']);
});

test('202 → строка задачи вверху, ?job= в адресе, окно снято; тело и ключ', async () => {
  const { srv, app, user } = setup();
  const d = await dialog();
  await pick(user, within(d).getByRole('combobox', { name: 'Страна' }), 'Россия (RU)');
  await user.click(within(d).getByRole('button', { name: 'Запустить' }));
  const row = await screen.findByRole('region', { name: 'Задача импорта' });
  expect(await within(row).findByText('в очереди')).toBeInTheDocument();
  expect(within(row).getByText('Задача 2001')).toBeInTheDocument();
  await waitFor(() => expect(app.at()).toBe('/geo/imports?job=2001'));
  expect(screen.queryByRole('dialog')).toBeNull();
  const start = srv.sent.filter((s) => s.op === 'start');
  expect(start).toHaveLength(1);
  expect(start[0]?.body).toEqual({ country: 'RU' });
  expect(start[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
  expect(await screen.findByText('Импорт RU поставлен в очередь')).toBeInTheDocument();
});

test('неизвестный исход (503) — окно открыто с ошибкой, повтор той же страны — тем же ключом', async () => {
  let failed = false;
  const { srv, app, user } = setup({
    answer: (op) => {
      if (op !== 'start' || failed) return undefined;
      failed = true;
      return problem(503, 'internal');
    },
  });
  const d = await dialog();
  await pick(user, within(d).getByRole('combobox', { name: 'Страна' }), 'Россия (RU)');
  await user.click(within(d).getByRole('button', { name: 'Запустить' }));
  expect(await within(d).findByRole('alert')).toBeInTheDocument();
  expect(app.at()).toBe('/geo/imports?start=1');
  await user.click(within(d).getByRole('button', { name: 'Запустить' }));
  await waitFor(() => expect(app.at()).toBe('/geo/imports?job=2001'));
  const keys = srv.sent.filter((s) => s.op === 'start').map((s) => s.key);
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
});

test('выбрана страна — «Закрыть» спрашивает «Уйти без сохранения?»; «Остаться» — окно цело', async () => {
  const { user } = setup();
  const d = await dialog();
  await pick(
    user,
    within(d).getByRole('combobox', { name: 'Страна' }),
    'Казахстан (KZ) (выключена)',
  );
  await user.click(within(d).getByRole('button', { name: 'Закрыть' }));
  const leave = await screen.findByRole('dialog', { name: 'Уйти без сохранения?' });
  await user.click(within(leave).getByRole('button', { name: 'Остаться' }));
  expect(await dialog()).toBeInTheDocument();
});

test('без страны — ошибка у поля, запроса нет', async () => {
  const { srv, user } = setup();
  const d = await dialog();
  await user.click(within(d).getByRole('button', { name: 'Запустить' }));
  await waitFor(() =>
    expect(within(d).getByRole('combobox', { name: 'Страна' })).toHaveAttribute(
      'aria-invalid',
      'true',
    ),
  );
  expect(srv.sent.filter((s) => s.op === 'start')).toEqual([]);
});
