import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, onTestFinished, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { approvalsServer } from '../../test/approvals';
import { geoImport, geoServer } from '../../test/geo';
import { stubMedia } from '../../test/media';
import { meModerator, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { http, server } from '../../test/server';
import { approval, city } from '../../test/staff';
import { CITIES_KEY } from '../geo/queries';
import { MY_MFA_KEY } from '../me/queries';

// Главная «Что требует внимания» (спека UI-фундамента §4.13, план UF4.2): плитки по правам (без права — ни плитки,
// ни запроса), ошибка одной плитки не роняет остальные, «99+» по ≥ 100 строк или next_cursor, все чтения фоновые
// (X-WF-Background: 1 — открытая главная не продлевает сессию), список дел и «Всё спокойно».

type Approval = components['schemas']['Approval'];

const meAll: Me = {
  ...meOwner,
  permissions: { ...meOwner.permissions, 'geo.imports.read': 'allow' },
};

const MIN = 60_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const rows = (n: number, over: Partial<Approval> = {}) =>
  Array.from({ length: n }, (_, i) =>
    approval({ id: `0192f1e2-4c5d-7e6f-a081-${String(i).padStart(12, '0')}`, ...over }),
  );

// Запросы главной (без me и подписей городов карточки сотрудника — это каркас): путь и фоновый ли.
function recordRequests() {
  const seen: { path: string; background: string | null }[] = [];
  const onStart = ({ request }: { request: Request }) => {
    const url = new URL(request.url);
    if (url.pathname === '/v1/staff/me' || url.searchParams.has('ids')) return;
    seen.push({ path: url.pathname, background: request.headers.get('X-WF-Background') });
  };
  server.events.on('request:start', onStart);
  onTestFinished(() => server.events.removeListener('request:start', onStart));
  return seen;
}

const tiles = async () => within(await screen.findByRole('list', { name: 'Индикаторы' }));
const tileNames = async () =>
  (await tiles()).getAllByRole('listitem').map((li) => li.textContent ?? '');

beforeEach(() => sectionsServer());

describe('плитки по правам', () => {
  test('владелец с импортом: четыре плитки в фиксированном порядке', async () => {
    approvalsServer({ incoming: rows(2), mine: rows(1), pageSize: 100 });
    renderApp('/', { me: meAll });
    expect(await screen.findByRole('link', { name: 'Ждут вашего решения 2' })).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'Мои запросы 1' })).toBeInTheDocument();
    expect(
      await screen.findByRole('link', { name: 'Сбои импорта GeoNames 0' }),
    ).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'Города в ожидании 0' })).toBeInTheDocument();
    const names = await tileNames();
    expect(names.map((n) => n.replace(/[\d+].*$/, ''))).toEqual([
      'Ждут вашего решения',
      'Мои запросы',
      'Сбои импорта GeoNames',
      'Города в ожидании',
    ]);
  });

  test('модератор — без плитки импорта и без её запроса', async () => {
    const seen = recordRequests();
    renderApp('/', { me: meModerator });
    expect(await screen.findByRole('link', { name: 'Города в ожидании 0' })).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'Мои запросы 0' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^Сбои импорта/ })).toBeNull();
    expect(seen.filter((r) => r.path.startsWith('/v1/geo/imports'))).toEqual([]);
  });

  test('без geo.cities.read — без плитки городов и без её запроса', async () => {
    const seen = recordRequests();
    renderApp('/', { me: { ...meOwner, permissions: { 'audit.log.read': 'allow' } } });
    expect(await screen.findByRole('link', { name: 'Мои запросы 0' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^Города в ожидании/ })).toBeNull();
    expect(seen.filter((r) => r.path.startsWith('/v1/geo/'))).toEqual([]);
  });
});

test('ошибка одной плитки не роняет остальные; «Повторить» перечитывает только её', async () => {
  let fail = true;
  approvalsServer({ incoming: rows(1), pageSize: 100 });
  server.use(
    http.get('/v1/geo/cities', ({ request, response }) => {
      if (new URL(request.url).searchParams.has('ids'))
        return response(200).json({ items: [], next_cursor: null });
      if (fail) return response.untyped(problem(500, 'internal'));
      return response(200).json({ items: [city({ status: 'waitlist' })], next_cursor: null });
    }),
  );
  const user = userEvent.setup();
  renderApp('/', { me: meAll });
  const grid = await tiles();
  expect(await grid.findByText('Не загрузилось')).toBeInTheDocument();
  expect(grid.getByRole('link', { name: 'Ждут вашего решения 1' })).toBeInTheDocument();
  expect(grid.getByRole('link', { name: 'Мои запросы 0' })).toBeInTheDocument();
  fail = false;
  await user.click(grid.getByRole('button', { name: 'Повторить' }));
  const tile = await grid.findByRole('link', { name: 'Города в ожидании 1' });
  // ревью UF4-H, m1: кнопка исчезла вместе с ошибкой — фокус на ссылке плитки, а не на body
  expect(tile).toHaveFocus();
  // источник списка дел ожил — строка ошибки в «Списке дел» снята
  expect(screen.queryByText('Не загрузилось')).toBeNull();
});

describe('«99+»', () => {
  test.each([
    [100, 100, 'Мои запросы 99+'],
    [99, 20, 'Мои запросы 99+'],
    [99, 100, 'Мои запросы 99'],
  ])('%d строк, страница %d — «%s»', async (n, pageSize, name) => {
    approvalsServer({ incoming: [], mine: rows(n), pageSize });
    renderApp('/', { me: meOwner });
    expect(await screen.findByRole('link', { name })).toBeInTheDocument();
  });

  test('99 строк с next_cursor — «99+»', async () => {
    server.use(
      http.get('/v1/approvals', ({ request, response }) =>
        new URL(request.url).searchParams.get('box') === 'mine'
          ? response(200).json({ items: rows(99), next_cursor: 'next' })
          : response(200).json({ items: [], next_cursor: null }),
      ),
    );
    renderApp('/', { me: meOwner });
    expect(await screen.findByRole('link', { name: 'Мои запросы 99+' })).toBeInTheDocument();
  });
});

test('все запросы главной — с X-WF-Background: 1', async () => {
  const seen = recordRequests();
  renderApp('/', { me: meAll });
  expect(await screen.findByRole('link', { name: 'Сбои импорта GeoNames 0' })).toBeInTheDocument();
  expect(await screen.findByRole('link', { name: 'Города в ожидании 0' })).toBeInTheDocument();
  expect(await screen.findByText('Всё спокойно')).toBeInTheDocument();
  const paths = new Set(seen.map((r) => r.path));
  expect([...paths].sort()).toEqual([
    '/v1/approvals',
    '/v1/geo/cities',
    '/v1/geo/imports',
    '/v1/staff/me/mfa',
  ]);
  expect(seen.filter((r) => r.background !== '1')).toEqual([]);
});

// Ревью UF4-H, Important 1: ошибка источника списка дел (у резервных кодов своей плитки нет) видна строкой в «Списке
// дел» — «Не загрузилось · Повторить»; «Всё спокойно», пока есть ошибка, не говорится. Повтор перечитывает упавшие;
// фокус после удачного повтора — не на body.
describe('ошибка источника списка дел', () => {
  const mfaServer = () => {
    const ctl = { fail: true, codes: 2 };
    server.use(
      http.get('/v1/staff/me/mfa', ({ response }) =>
        ctl.fail
          ? response.untyped(problem(500, 'internal'))
          : response(200).json({
              confirmed_at: '2026-10-01T09:12:00Z',
              recovery_codes_left: ctl.codes,
            }),
      ),
    );
    return ctl;
  };
  const todoSection = async () =>
    within(
      (await screen.findByRole('heading', { level: 2, name: 'Список дел' })).closest(
        'section',
      ) as HTMLElement,
    );

  test('коды не загрузились, остальное пусто — «Не загрузилось · Повторить», без «Всё спокойно»; повтор — строка кодов', async () => {
    const ctl = mfaServer();
    const user = userEvent.setup();
    renderApp('/', { me: meAll });
    const todo = await todoSection();
    expect(await todo.findByText('Не загрузилось')).toBeInTheDocument();
    expect(screen.queryByText('Всё спокойно')).toBeNull();
    ctl.fail = false;
    await user.click(todo.getByRole('button', { name: 'Повторить' }));
    const codes = await todo.findByRole('link', { name: 'Осталось 2 резервных кода' });
    expect(todo.queryByText('Не загрузилось')).toBeNull();
    expect(todo.queryByRole('button', { name: 'Повторить' })).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(codes.closest('section')).toContainElement(document.activeElement as HTMLElement);
  });

  test('есть другие дела — они видны, и ошибка кодов строкой под ними', async () => {
    mfaServer();
    approvalsServer({ incoming: rows(3), pageSize: 100 });
    renderApp('/', { me: meAll });
    const todo = await todoSection();
    expect(await todo.findByRole('link', { name: 'Разобрать 3 запроса' })).toBeInTheDocument();
    expect(await todo.findByText('Не загрузилось')).toBeInTheDocument();
    expect(todo.getByRole('button', { name: 'Повторить' })).toBeInTheDocument();
  });

  test('успешный повтор без дел — «Всё спокойно», фокус не на body', async () => {
    const ctl = mfaServer();
    ctl.codes = 10;
    const user = userEvent.setup();
    renderApp('/', { me: meAll });
    const todo = await todoSection();
    await todo.findByText('Не загрузилось');
    ctl.fail = false;
    await user.click(todo.getByRole('button', { name: 'Повторить' }));
    expect(await todo.findByText('Всё спокойно')).toBeInTheDocument();
    expect(document.activeElement).not.toBe(document.body);
  });
});

// Ревью UF4-H, Minor 2: общий кэш с разделами — перевыпуск кодов (MY_MFA_KEY) и активация города (CITIES_KEY)
// задевают и главную.
test('ключи главной: коды — MY_MFA_KEY «Моего доступа», города в ожидании — под CITIES_KEY', async () => {
  const app = renderApp('/', { me: meAll });
  expect(await screen.findByText('Всё спокойно')).toBeInTheDocument();
  expect(app.queryClient.getQueryData(MY_MFA_KEY)).toMatchObject({ recovery_codes_left: 10 });
  const cities = app.queryClient.getQueryCache().findAll({ queryKey: CITIES_KEY });
  expect(cities.some((q) => q.queryKey.includes('waitlist'))).toBe(true);
});

test('пусто — «Всё спокойно»', async () => {
  renderApp('/', { me: meAll });
  expect(await screen.findByText('Всё спокойно')).toBeInTheDocument();
  expect(screen.queryByRole('list', { name: 'Список дел' })).toBeNull();
});

describe('список дел', () => {
  test('строки со ссылками через search роутера; резервные коды ≤ 3 — «Осталось»', async () => {
    approvalsServer({ incoming: rows(3), mine: rows(2), pageSize: 100 });
    geoServer({ cities: [city({ status: 'waitlist' })] });
    server.use(
      http.get('/v1/geo/imports', ({ response }) =>
        response(200).json({
          items: [
            geoImport({
              id: '0192f1f5-1a2b-7c3d-8e4f-000000000001',
              country_code: 'RU',
              status: 'failed',
              started_at: ago(130 * MIN),
              finished_at: ago(120 * MIN),
            }),
            geoImport({
              id: '0192f1f5-1a2b-7c3d-8e4f-000000000002',
              country_code: 'RU',
              status: 'succeeded',
            }),
            geoImport({
              id: '0192f1f5-1a2b-7c3d-8e4f-000000000003',
              country_code: 'KZ',
              status: 'running',
              started_at: ago(40 * MIN),
              finished_at: null,
            }),
            geoImport({
              id: '0192f1f5-1a2b-7c3d-8e4f-000000000004',
              country_code: 'BY',
              status: 'running',
              started_at: ago(20 * MIN),
              finished_at: null,
            }),
          ],
          next_cursor: null,
        }),
      ),
      http.get('/v1/staff/me/mfa', ({ response }) =>
        response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 2 }),
      ),
    );
    renderApp('/', { me: meAll });
    const todo = within(await screen.findByRole('list', { name: 'Список дел' }));
    expect(
      await screen.findByRole('link', { name: 'Сбои импорта GeoNames 2' }),
    ).toBeInTheDocument();
    const links = todo.getAllByRole('link').map((l) => [l.textContent, l.getAttribute('href')]);
    expect(links).toEqual([
      ['Разобрать 3 запроса', '/approvals'],
      ['2 запроса ждут второго сотрудника', '/approvals?box=mine'],
      ['Открыть импорт RU: последний упал', '/geo/imports?country=RU'],
      ['Открыть импорт KZ: идёт дольше 30 мин', '/geo/imports?country=KZ'],
      ['Посмотреть города в ожидании', expect.stringMatching(/^\/geo\/cities\?status=/)],
      ['Осталось 2 резервных кода', '/me'],
    ]);
    // фильтр городов — массив (контракт: повторённый параметр), роутер держит его JSON-ом
    const cities = new URLSearchParams(String(links[4]?.[1]).split('?')[1]);
    expect(JSON.parse(cities.get('status') ?? '')).toEqual(['waitlist']);
  });

  test('резервных кодов 4 — строки нет', async () => {
    server.use(
      http.get('/v1/staff/me/mfa', ({ response }) =>
        response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 4 }),
      ),
    );
    renderApp('/', { me: meOwner });
    expect(await screen.findByText('Всё спокойно')).toBeInTheDocument();
  });
});

describe('карманное устройство', () => {
  test('телефон (390 + палец): входящие staff.* — «Решать с компьютера»', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    approvalsServer({ incoming: rows(1), pageSize: 100 });
    renderApp('/', { me: meOwner });
    const link = await screen.findByRole('link', { name: 'Ждут вашего решения 1' });
    expect(link).toHaveAccessibleDescription('Решать с компьютера');
  });

  test('компьютер — без пометки', async () => {
    approvalsServer({ incoming: rows(1), pageSize: 100 });
    renderApp('/', { me: meOwner });
    const link = await screen.findByRole('link', { name: 'Ждут вашего решения 1' });
    expect(link).not.toHaveAccessibleDescription('Решать с компьютера');
  });

  test('телефон: входящие не staff.* — без пометки', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    approvalsServer({
      incoming: rows(1, { action: 'geo.country_rules', permission: 'geo.countries.manage' }),
      pageSize: 100,
    });
    renderApp('/', { me: meOwner });
    const link = await screen.findByRole('link', { name: 'Ждут вашего решения 1' });
    expect(link).not.toHaveAccessibleDescription('Решать с компьютера');
  });
});
