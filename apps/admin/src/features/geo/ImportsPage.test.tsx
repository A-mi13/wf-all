import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { geoImport, importJob, importsServer, meDirectoriesAdmin } from '../../test/geo';
import { pick } from '../../test/pick';
import { sectionsServer } from '../../test/sections';

// «Импорт GeoNames» (план B, Task 20; спека UI-фундамента §6.2): журнал — страна, статус (чип; незнакомый — «—»),
// кто запустил (имя / «оператор сервера» / «сотрудник»), начало, длительность, итог; фильтр страны в адресе (ссылки
// главной); строка задачи по ?job=; 409 с job_id — строка задачи без памяти клиента; 409 без job_id — текст и
// подсветка строки running; поддержка — только чтение.

afterEach(() => sessionStorage.clear());

const ROWS = [
  geoImport({ id: '0192f1f5-0000-7c3d-8e4f-000000000001', started_by_label: 'Ольга В.' }),
  geoImport({
    id: '0192f1f5-0000-7c3d-8e4f-000000000002',
    country_code: 'KZ',
    status: 'running',
    started_by: null,
    started_by_label: null,
    river_job_id: null,
    finished_at: null,
    places_upserted: null,
    places_removed: null,
    places_missing: null,
    names_upserted: null,
  }),
  geoImport({
    id: '0192f1f5-0000-7c3d-8e4f-000000000003',
    status: 'archived' as 'failed',
    started_by_label: null,
    started_at: '2026-10-19T08:00:00Z',
    finished_at: '2026-10-19T09:30:00Z',
  }),
];

// Поддержка: журнал читает, импорт не запускает и города не привязывает.
const readPerms = Object.fromEntries(
  Object.entries(meDirectoriesAdmin.permissions).filter(
    ([k]) => k !== 'geo.imports.run' && k !== 'geo.cities.manage',
  ),
);
const meReader: Me = { ...meDirectoriesAdmin, permissions: readPerms };

const table = () => screen.findByRole('table', { name: 'Журнал импортов' });
const rowOf = async (text: string) => {
  const t = await table();
  const row = (await within(t).findAllByRole('row')).find((r) => r.textContent?.includes(text));
  if (!row) throw new Error(`нет строки ${text}`);
  return row;
};

function setup(
  path: string,
  me: Me = meDirectoriesAdmin,
  opts: Parameters<typeof importsServer>[0] = {},
) {
  sectionsServer();
  const srv = importsServer({ imports: ROWS, ...opts });
  const app = renderApp(path, { me });
  return { srv, app, user: userEvent.setup() };
}

describe('журнал', () => {
  test('страна, чип статуса, кто (три случая), длительность, итог', async () => {
    setup('/geo/imports');
    await screen.findByRole('heading', { level: 1, name: 'Импорт GeoNames' });
    const first = await rowOf('Ольга В.');
    expect(within(first).getByText('завершён')).toBeInTheDocument();
    expect(first).toHaveTextContent('4 мин');
    expect(first).toHaveTextContent(/мест: 1\s520 · названий: 3\s010/);
    const operator = await rowOf('оператор сервера');
    // чип статуса и длительность «идёт»
    expect(within(operator).getAllByText('идёт')).toHaveLength(2);
    expect(operator).toHaveTextContent('KZ');
    const staff = await rowOf('сотрудник');
    // незнакомый статус — «—», не код
    expect(staff).not.toHaveTextContent('archived');
    expect(within(staff).getAllByText('—').length).toBeGreaterThan(0);
    expect(staff).toHaveTextContent('1,5 ч');
  });

  test('?country=RU — запрос журнала с фильтром; фильтр «Страна» меняет адрес', async () => {
    const { srv, app, user } = setup('/geo/imports?country=RU');
    await rowOf('Ольга В.');
    expect(srv.sent.find((s) => s.op === 'list')?.path).toBe('/v1/geo/imports?country=RU');
    expect(screen.queryByText('оператор сервера')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Ещё фильтры, задано: 1' }));
    await pick(user, screen.getByRole('combobox', { name: 'Страна' }), 'Казахстан (KZ)');
    await waitFor(() => expect(app.at()).toBe('/geo/imports?country=KZ'));
    expect(await rowOf('оператор сервера')).toBeInTheDocument();
  });

  test('ссылка главной «Открыть импорт RU» — отфильтрованный журнал (U18)', async () => {
    const failed = geoImport({ status: 'failed', error: 'timeout' });
    const { srv, app, user } = setup('/', meDirectoriesAdmin, { imports: [failed] });
    await user.click(
      await screen.findByRole('link', { name: 'Открыть импорт RU: последний упал' }),
    );
    await screen.findByRole('heading', { level: 1, name: 'Импорт GeoNames' });
    expect(app.at()).toBe('/geo/imports?country=RU');
    await waitFor(() =>
      expect(srv.sent.some((s) => s.path === '/v1/geo/imports?country=RU')).toBe(true),
    );
  });

  test('пусто — иллюстрация и «Запустить импорт»', async () => {
    setup('/geo/imports', meDirectoriesAdmin, { imports: [] });
    expect(await screen.findByText('Импортов ещё не было')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Запустить импорт' }).length).toBeGreaterThan(0);
  });
});

describe('строка задачи', () => {
  test('?job=<id> (F5, пересланная ссылка) — строка со статусом из getGeoImportJob', async () => {
    const { srv } = setup('/geo/imports?job=1842', meDirectoriesAdmin, {
      jobs: [importJob({ job_id: 1842, state: 'running', import_id: null })],
    });
    const row = await screen.findByRole('region', { name: 'Задача импорта' });
    expect(await within(row).findByText('выполняется')).toBeInTheDocument();
    expect(srv.sent.find((s) => s.op === 'job')?.path).toBe('/v1/geo/imports/jobs/1842');
  });

  test('без памяти: 409 с job_id → строка задачи с «Отменить» → отмена → повторный запуск 202', async () => {
    const { srv, app, user } = setup('/geo/imports', meDirectoriesAdmin, {
      jobs: [importJob({ job_id: 1842, state: 'queued', import_id: null, attempt: 0 })],
      busy: { RU: 1842 },
    });
    const start = async () => {
      await user.click(await screen.findByRole('button', { name: 'Запустить импорт' }));
      const d = await screen.findByRole('dialog', { name: 'Запустить импорт GeoNames' });
      await pick(user, within(d).getByRole('combobox', { name: 'Страна' }), 'Россия (RU)');
      await user.click(within(d).getByRole('button', { name: 'Запустить' }));
    };
    await start();
    await waitFor(() => expect(app.at()).toBe('/geo/imports?job=1842'));
    expect(
      await screen.findByText('Импорт RU уже в очереди — показана его задача'),
    ).toBeInTheDocument();
    const row = await screen.findByRole('region', { name: 'Задача импорта' });
    await user.click(await within(row).findByRole('button', { name: 'Отменить задачу 1842' }));
    expect(await within(row).findByText('отменена')).toBeInTheDocument();
    await start();
    await waitFor(() => expect(app.at()).toBe('/geo/imports?job=2001'));
    expect(
      await within(await screen.findByRole('region', { name: 'Задача импорта' })).findByText(
        'в очереди',
      ),
    ).toBeInTheDocument();
    expect(srv.sent.filter((s) => s.op === 'start')).toHaveLength(2);
  });

  test('409 без job_id (импорт оператора) — текст и подсветка строки running', async () => {
    const { app, user } = setup('/geo/imports?start=1', meDirectoriesAdmin, { busy: { KZ: null } });
    const d = await screen.findByRole('dialog', { name: 'Запустить импорт GeoNames' });
    await pick(
      user,
      within(d).getByRole('combobox', { name: 'Страна' }),
      'Казахстан (KZ) (выключена)',
    );
    await user.click(within(d).getByRole('button', { name: 'Запустить' }));
    expect(
      await screen.findByText(
        'Импорт KZ уже идёт — его запустил оператор сервера. Строка подсвечена в журнале.',
      ),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(app.at()).toBe('/geo/imports');
    expect(await rowOf('оператор сервера')).toHaveAttribute('aria-current', 'true');
  });
});

// Ревью m-1, m-2: адрес правится слиянием — открытый отчёт (?import=) не теряется при «Скрыть задачу» и запуске;
// фильтр другой страны при запуске сменяется страной задачи (иначе её строки в журнале не видно).
describe('адрес', () => {
  test('«Скрыть задачу» — ?import= остаётся', async () => {
    const id = ROWS[0]?.id ?? '';
    const { app, user } = setup(`/geo/imports?job=1842&import=${id}`, meDirectoriesAdmin, {
      jobs: [importJob({ job_id: 1842, state: 'completed', import_id: id })],
    });
    const row = await screen.findByRole('region', { name: 'Задача импорта' });
    await user.click(await within(row).findByRole('button', { name: 'Скрыть задачу' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/imports?import=${id}`));
  });

  test('запуск RU при фильтре KZ — фильтр сменяется на RU, отчёт остаётся', async () => {
    const id = ROWS[0]?.id ?? '';
    const { app, user } = setup(`/geo/imports?country=KZ&import=${id}&start=1`);
    const d = await screen.findByRole('dialog', { name: 'Запустить импорт GeoNames' });
    await pick(user, within(d).getByRole('combobox', { name: 'Страна' }), 'Россия (RU)');
    await user.click(within(d).getByRole('button', { name: 'Запустить' }));
    await waitFor(() => expect(app.at()).toContain('job=2001'));
    const q = new URLSearchParams(app.at().split('?')[1]);
    expect(q.get('country')).toBe('RU');
    expect(q.get('import')).toBe(id);
    expect(q.has('start')).toBe(false);
  });
});

describe('поддержка — только чтение', () => {
  test('без «Запустить импорт», «Отменить», «Привязать»; ?start=1 снимается', async () => {
    const id = ROWS[0]?.id ?? '';
    const { app } = setup(`/geo/imports?start=1&job=1842&import=${id}`, meReader, {
      jobs: [importJob({ job_id: 1842, state: 'queued', import_id: null })],
      reports: {
        [id]: {
          linked: [],
          ambiguous: [
            {
              city_id: '7a1d5e9c-2b4f-4c8a-b3e6-1f0d9c8b7a65',
              slug: 'mihaylovsk',
              candidates: [
                { geoname_id: 526815, name: 'Михайловск', region_name: null, population: 9000 },
              ],
            },
          ],
          not_found: [],
          conflict: [],
          missing: [],
          skipped_timezone: 0,
        },
      },
    });
    const row = await screen.findByRole('region', { name: 'Задача импорта' });
    expect(await within(row).findByText('в очереди')).toBeInTheDocument();
    expect(await screen.findByText('Михайловск · регион неизвестен')).toBeInTheDocument();
    await waitFor(() => expect(app.at()).not.toContain('start='));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Запустить импорт' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Отменить/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Привязать/ })).toBeNull();
  });
});
