import { createMemoryHistory } from '@tanstack/react-router';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createApi } from '../api/client';
import { CITY_OWN, meModerator, meMfaPending, meOwner, meSetup } from '../test/me';
import { problem } from '../test/problem';
import { http, server, TEST_API } from '../test/server';
import type { Me } from './permissions';
import { AppProviders } from './providers';
import { createQueryClient } from './query';
import { reauthBus } from './reauth';
import { AppRouter } from './router';
import { sectionsServer } from '../test/sections';

// стартовая и возврат приводят в разделы каркаса — их первые запросы описаны (src/test/sections.ts)
beforeEach(() => sectionsServer());

afterEach(() => {
  reauthBus.reject();
  vi.unstubAllGlobals();
});

// Города для карточки сотрудника: id → название (GET /v1/geo/cities?ids=…).
const CITY_NAMES: Record<string, string> = { [CITY_OWN]: 'Ставрополь' };
const citiesHandler = () =>
  http.get('/v1/geo/cities', ({ request, response }) => {
    const ids = new URL(request.url).searchParams.getAll('ids');
    return response(200).json({
      items: ids.map((id) => ({
        id,
        slug: 'city',
        name: CITY_NAMES[id] ?? id,
        status: 'live' as const,
        country_code: 'RU',
        region_name: null,
      })),
      next_cursor: null,
    });
  });

// me отдаёт текущее значение state.me; null — 401 auth.unauthenticated.
function mount(path: string, me: Me | null) {
  const state = { me };
  server.use(
    citiesHandler(),
    // экран настройки 2FA сам выпускает секрет (Task 13)
    http.post('/v1/staff/me/mfa/setup', ({ response }) =>
      response(200).json({ secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/WF:x' }),
    ),
    http.get('/v1/staff/me', ({ response }) =>
      state.me
        ? response(200).json(state.me)
        : response.untyped(problem(401, 'auth.unauthenticated')),
    ),
  );
  const history = createMemoryHistory({ initialEntries: [path] });
  const queryClient = createQueryClient({ retry: false });
  render(
    <AppProviders api={createApi(TEST_API)} queryClient={queryClient}>
      <AppRouter history={history} />
    </AppProviders>,
  );
  const at = () => history.location.pathname + history.location.search;
  // me перечитывается (как после входа, разблокировки или 401 любого запроса)
  const refetchMe = () => act(() => queryClient.refetchQueries({ queryKey: ['staff', 'me'] }));
  const login = async (next: Me) => {
    state.me = next;
    await refetchMe();
  };
  return { at, login, refetchMe };
}

const page = (name: string) => screen.findByRole('heading', { level: 1, name });

describe('без сессии', () => {
  test('защищённый раздел → /login?return=…, после входа — возврат на исходный адрес', async () => {
    const app = mount('/staff?tab=sessions', null);
    await page('Вход администратора');
    expect(app.at()).toBe('/login?return=%2Fstaff%3Ftab%3Dsessions');

    await app.login(meOwner);
    await page('Роли и доступ');
    expect(app.at()).toBe('/staff?tab=sessions');
  });

  test('/accept открыт без сессии', async () => {
    const app = mount('/accept', null);
    await page('Принятие приглашения');
    expect(app.at()).toBe('/accept');
  });

  test('корень → вход без return', async () => {
    const app = mount('/', null);
    await page('Вход администратора');
    expect(app.at()).toBe('/login');
  });

  test('чужой адрес в return игнорируется — стартовая страница', async () => {
    const app = mount('/login?return=%2F%2Fevil.example%2Fx', null);
    await page('Вход администратора');
    await app.login(meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });
});

describe('стартовая страница — первый разрешённый раздел меню', () => {
  test('с журналом — журнал', async () => {
    const app = mount('/', meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('без права на журнал — следующий раздел', async () => {
    const app = mount('/', { ...meModerator, permissions: { 'staff.roles.read': 'allow' } });
    await page('Роли и доступ');
    expect(app.at()).toBe('/staff');
  });

  test('scoped-право открывает раздел (модератор и журнал своих городов)', async () => {
    const app = mount('/', meModerator);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('вход без return — стартовая', async () => {
    const app = mount('/login', meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });
});

describe('стадии', () => {
  test('setup — только /setup-2fa', async () => {
    const app = mount('/staff', meSetup);
    await page('Настройка двухфакторной защиты');
    expect(app.at()).toBe('/setup-2fa');
  });

  test('setup — /login и /accept тоже ведут на /setup-2fa', async () => {
    const app = mount('/login', meSetup);
    await page('Настройка двухфакторной защиты');
    expect(app.at()).toBe('/setup-2fa');
  });

  test('после настройки 2FA — стартовая', async () => {
    const app = mount('/setup-2fa', meSetup);
    await page('Настройка двухфакторной защиты');
    await app.login(meOwner);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
  });

  test('без сессии /setup-2fa — вход', async () => {
    const app = mount('/setup-2fa', null);
    await page('Вход администратора');
    expect(app.at()).toBe('/login');
  });

  test('mfa_pending — шаг 2 входа, возврат сохраняется', async () => {
    const app = mount('/audit', meMfaPending);
    await page('Вход администратора');
    expect(app.at()).toBe('/login?return=%2Faudit');
  });
});

describe('права разделов', () => {
  test('прямой адрес без права — заглушка ForbiddenState, без редиректа', async () => {
    const app = mount('/audit', { ...meOwner, permissions: { 'staff.roles.read': 'allow' } });
    await page('Нет доступа к разделу');
    expect(app.at()).toBe('/audit');
  });

  test('меню — только разрешённые разделы', async () => {
    mount('/me', { ...meOwner, permissions: { 'staff.roles.read': 'allow' } });
    await page('Мой доступ');
    const nav = screen.getByRole('navigation');
    expect(nav).toHaveTextContent('Роли и доступ');
    expect(nav).toHaveTextContent('Подтверждения');
    expect(nav).not.toHaveTextContent('Журнал действий');
  });

  test('неизвестный адрес — «не найдено» из локалей', async () => {
    mount('/nope', meOwner);
    await page('Страница не найдена');
  });
});

test('стадия error (нет связи с admin-api) — экран с понятным текстом и «Повторить»', async () => {
  let up = false;
  server.use(
    http.get('/v1/staff/me', ({ response }) =>
      up ? response(200).json(meOwner) : response.untyped(problem(503, 'internal')),
    ),
  );
  render(
    <AppProviders api={createApi(TEST_API)} queryClient={createQueryClient({ retry: false })}>
      <AppRouter history={createMemoryHistory({ initialEntries: ['/audit'] })} />
    </AppProviders>,
  );
  const retry = await screen.findByRole('button', { name: 'Повторить' });
  const alert = screen.getByRole('alert');
  expect(alert).toHaveTextContent('Нет связи с сервером');
  expect(alert).toHaveTextContent('Админка не получила ответ от сервера');
  up = true;
  act(() => retry.click());
  await waitFor(() =>
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Журнал действий'),
  );
});

// Ревью ветки, M1: me при загрузке ответил отказом сервера, а не молчанием — «Нет связи» вводит в
// заблуждение. 429 (лимит staff_auth по общему NAT) — отсчёт по Retry-After и «Повторить» по его концу;
// прочие 4xx (403 auth.ip_not_allowed) — текст по коду и код для поддержки. Сеть и 5xx — «нет связи».
describe('ошибка me при загрузке', () => {
  function mountFailing(answer: () => Response) {
    let up = false;
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        up ? response(200).json(meOwner) : response.untyped(answer()),
      ),
    );
    render(
      <AppProviders api={createApi(TEST_API)} queryClient={createQueryClient({ retry: false })}>
        <AppRouter history={createMemoryHistory({ initialEntries: ['/audit'] })} />
      </AppProviders>,
    );
    return { up: () => (up = true) };
  }

  test('429 — «Слишком много запросов» с отсчётом, не «нет связи»; по концу отсчёта — «Повторить»', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const srv = mountFailing(() =>
        problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '3' }),
      );
      expect(
        await screen.findByRole('heading', { level: 1, name: 'Слишком много запросов' }),
      ).toBeInTheDocument();
      expect(screen.queryByText('Нет связи с сервером')).toBeNull();
      expect(screen.getByRole('button', { name: 'Подождать 3 с' })).toBeDisabled();
      act(() => vi.advanceTimersByTime(3000));
      const retry = await screen.findByRole('button', { name: 'Повторить' });
      srv.up();
      act(() => retry.click());
      await waitFor(() =>
        expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Журнал действий'),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  test('403 auth.ip_not_allowed — текст по коду и код для поддержки, не «нет связи»', async () => {
    mountFailing(() => problem(403, 'auth.ip_not_allowed'));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Вход в админку с этого адреса закрыт');
    expect(alert).toHaveTextContent('Код ошибки: 403 · auth.ip_not_allowed');
    expect(alert).not.toHaveTextContent('Нет связи с сервером');
    expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeInTheDocument();
  });
});

describe('каркас AppShell', () => {
  // Модератор без права на «Роли и доступ»: в каталоге оно у всех ролей, здесь проверяется
  // механизм меню по правам.
  const meNoRoles: Me = { ...meModerator, permissions: { 'audit.log.read': 'scoped' } };

  test('модератор без права не видит «Роли и доступ», прямой адрес — ForbiddenState', async () => {
    const app = mount('/staff', meNoRoles);
    await page('Нет доступа к разделу');
    expect(app.at()).toBe('/staff');
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    expect(nav).toHaveTextContent('Журнал действий');
    expect(nav).not.toHaveTextContent('Роли и доступ');
  });

  test('карточка сотрудника: имя, роль · города (названия из geo)', async () => {
    mount('/audit', meModerator);
    await page('Журнал действий');
    expect(screen.getByText('Ольга В.')).toBeInTheDocument();
    expect(await screen.findByText('модератор · Ставрополь')).toBeInTheDocument();
  });

  test('без права на города — их число вместо названий', async () => {
    mount('/audit', { ...meModerator, permissions: { 'audit.log.read': 'scoped' } });
    await page('Журнал действий');
    expect(screen.getByText('модератор · 1 город')).toBeInTheDocument();
  });

  test('владелец без городов — только роль', async () => {
    mount('/audit', meOwner);
    await page('Журнал действий');
    expect(screen.getByText('владелец')).toBeInTheDocument();
  });

  test('переключатель языка — в меню профиля', async () => {
    mount('/audit', meOwner);
    await page('Журнал действий');
    expect(screen.getByRole('group', { name: 'Язык интерфейса' })).toBeInTheDocument();
  });

  // После выхода me — 401; signedIn — вход другим сотрудником (новая сессия).
  function logoutServer(logout: 204 | 401) {
    const ctl = { key: null as string | null, signedIn: false };
    const next: Me = {
      ...meOwner,
      session: { id: 'другая-сессия', expires_at: '2026-10-13T18:00:00Z' },
    };
    server.use(
      http.post('/v1/staff/auth/logout', ({ request, response }) => {
        ctl.key = request.headers.get('Idempotency-Key');
        server.use(
          http.get('/v1/staff/me', ({ response: r }) =>
            ctl.signedIn ? r(200).json(next) : r.untyped(problem(401, 'auth.unauthenticated')),
          ),
        );
        return logout === 204
          ? response(204).empty()
          : response.untyped(problem(401, 'auth.unauthenticated'));
      }),
    );
    return ctl;
  }

  test('«Выйти» из раздела — вход без return; новый вход — стартовая; истечение — с возвратом', async () => {
    // раздел не стартовый: возврат в него после нового входа отличим от стартовой страницы
    const app = mount('/staff', meOwner);
    const ctl = logoutServer(204);
    await page('Роли и доступ');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Выйти' }));
    await page('Вход администратора');
    expect(ctl.key).toBeTruthy();
    // явный выход — без return: следующий сотрудник не попадёт в чужой раздел
    await waitFor(() => expect(app.at()).toBe('/login'));

    // новый вход — стартовая страница, а не раздел прежнего сотрудника
    ctl.signedIn = true;
    await app.refetchMe();
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');

    // сессия нового входа истекла — вход с возвратом (флаг выхода не залипает)
    ctl.signedIn = false;
    await app.refetchMe();
    await page('Вход администратора');
    expect(app.at()).toBe('/login?return=%2Faudit');
  });

  test('выход ответил 401 (сессии уже нет) — тоже вход без return и без тоста', async () => {
    const app = mount('/staff', meOwner);
    logoutServer(401);
    await page('Роли и доступ');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Выйти' }));
    await page('Вход администратора');
    await waitFor(() => expect(app.at()).toBe('/login'));
    expect(screen.queryByText('Войдите в аккаунт заново')).not.toBeInTheDocument();
  });

  test('телефон: desktopOnly-раздел — «только с компьютера», таббар с «Ещё»', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => ({
        matches: true,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      })),
    );
    mount('/me', meOwner);
    await page('Только с компьютера');
    const tabbar = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(tabbar).getByRole('button', { name: 'Ещё' })).toBeInTheDocument();
    expect(within(tabbar).queryByRole('link', { name: 'Мой доступ' })).not.toBeInTheDocument();
  });
});
