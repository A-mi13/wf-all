import {
  MutationObserver,
  onlineManager,
  useQuery,
  type UseQueryResult,
} from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { createQueryClient } from '../../app/query';
import { providers, renderWithIntl, renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import { problem } from '../../test/problem';
import { meOwner } from '../../test/me';
import { session } from '../../test/staff';
import { DataState, SKELETON_DELAY_MS } from './DataState';
import { Skeleton } from './Skeleton';
import { EmptyState, NoNetworkState } from './states';

// Пять состояний одним компонентом (спека UI-фундамента §4.3, план UF2a.8). Запрос — настоящий путь контракта
// (GET /v1/staff/me/sessions) через MSW: ответ удерживается воротами, время — фейковые таймеры.

afterEach(() => {
  onlineManager.setOnline(true);
  vi.useRealTimers();
});

type Sessions = { items: ReturnType<typeof session>[] };

// Ворота ответа: reached — запрос дошёл до мока, open — отпустить ответ.
function gate() {
  let reach!: () => void;
  let release!: () => void;
  const reached = new Promise<void>((r) => (reach = r));
  const opened = new Promise<void>((r) => (release = r));
  return { reached, opened, reach, open: () => release() };
}

function serve(respond: () => Response | Promise<Response>) {
  let calls = 0;
  server.use(
    http.get('/v1/staff/me/sessions', async () => {
      calls++;
      return (await respond()) as never;
    }),
  );
  return { calls: () => calls };
}

const ok = (items = [session()]) => HttpResponse.json({ items });

// Ждать при фейковых таймерах без хода фейкового времени: vi.waitFor и findBy* двигают фейковые часы на свой
// интервал (50 мс) — до задержки скелетона три опроса. Пауза — настоящим таймером, нулевые таймеры — вручную.
const realSetTimeout = globalThis.setTimeout;
async function settle(check: () => unknown) {
  for (let i = 0; ; i++) {
    act(() => vi.advanceTimersByTime(0));
    try {
      return check();
    } catch (e) {
      if (i > 200) throw e;
    }
    await new Promise((r) => realSetTimeout(r, 10));
  }
}

let skeletonRenders = 0;
function Probe() {
  skeletonRenders++;
  return <div data-testid="skeleton" />;
}

function Sessions({ isEmpty }: { isEmpty?: (d: Sessions) => boolean }) {
  const api = useApi();
  const query = useQuery({
    queryKey: ['test', 'sessions'],
    queryFn: () => unwrap(api.GET('/v1/staff/me/sessions')),
  });
  return (
    <DataState query={query} skeleton={<Probe />} isEmpty={isEmpty}>
      {(d) => <p>сеансов: {d.items.length}</p>}
    </DataState>
  );
}

describe('загрузка', () => {
  // Время идёт только руками (без shouldAdvanceTime — иначе реальная задержка под нагрузкой добавлялась бы к
  // фейковой и «через 149 мс» могло оказаться за 150, ревью m3); ожидание — settle().
  test('быстрый ответ: скелетона не было', async () => {
    vi.useFakeTimers();
    skeletonRenders = 0;
    const g = gate();
    serve(async () => {
      g.reach();
      await g.opened;
      return ok();
    });
    renderWithProviders(<Sessions />);
    await g.reached;
    // за миг до задержки удержанного ответа: без задержки скелетон уже стоял бы
    act(() => vi.advanceTimersByTime(SKELETON_DELAY_MS - 1));
    expect(screen.queryByTestId('skeleton')).toBeNull();
    expect(screen.queryByText('Загрузка…')).toBeNull();
    g.open();
    await settle(() => screen.getByText('сеансов: 1'));
    act(() => vi.advanceTimersByTime(500));
    expect(skeletonRenders).toBe(0);
  });

  test('unmount до задержки — таймер скелетона снят', () => {
    vi.useFakeTimers();
    const pending = {
      data: undefined,
      isError: false,
      error: null,
      fetchStatus: 'fetching',
    } as unknown as UseQueryResult<string>;
    const host = (show: boolean) =>
      show ? (
        <DataState query={pending} skeleton={<Probe />}>
          {(d) => d}
        </DataState>
      ) : null;
    const { rerender } = render(host(false), { wrapper: providers() });
    const flush = () => act(() => vi.advanceTimersByTime(0)); // нулевые таймеры уведомлений Query
    flush();
    const base = vi.getTimerCount(); // таймеры провайдеров и кэша
    rerender(host(true));
    flush();
    expect(vi.getTimerCount()).toBe(base + 1);
    rerender(host(false));
    flush();
    expect(vi.getTimerCount()).toBe(base);
  });

  test('медленный ответ: скелетон через 150 мс, aria-busy, «Загрузка…» один раз', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const g = gate();
    serve(async () => {
      g.reach();
      await g.opened;
      return ok();
    });
    const { container } = renderWithProviders(<Sessions />);
    await g.reached;
    act(() => vi.advanceTimersByTime(150));
    const sk = screen.getByTestId('skeleton');
    expect(sk.closest('[aria-busy="true"]')).not.toBeNull();
    expect(screen.getAllByText('Загрузка…')).toHaveLength(1);
    expect(screen.getByRole('status')).toHaveTextContent('Загрузка…');
    g.open();
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
    expect(screen.queryByTestId('skeleton')).toBeNull();
    expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  });
});

test('isEmpty → empty (по умолчанию — EmptyState)', async () => {
  serve(() => ok([]));
  renderWithProviders(<Sessions isEmpty={(d) => d.items.length === 0} />);
  expect(await screen.findByText('Ничего не нашлось')).toBeInTheDocument();
  expect(screen.queryByText(/сеансов/)).toBeNull();
});

describe('ошибки', () => {
  test('403 — ForbiddenState', async () => {
    serve(() => problem(403, 'staff.forbidden'));
    renderWithProviders(<Sessions />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Нет доступа к разделу' }),
    ).toBeInTheDocument();
  });

  test('429 — RateLimitedState, повтор — тот же запрос', async () => {
    let n = 0;
    const s = serve(() =>
      n++ === 0 ? problem(429, 'platform.rate_limited', {}, { 'Retry-After': '0' }) : ok(),
    );
    renderWithProviders(<Sessions />);
    expect(
      await screen.findByRole('heading', { name: 'Слишком много запросов' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
    expect(s.calls()).toBe(2);
  });

  test('500 — ErrorState с кодом и «Повторить»', async () => {
    let n = 0;
    serve(() => (n++ === 0 ? problem(500, 'internal') : ok()));
    renderWithProviders(<Sessions />);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Код ошибки: 500 · internal');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
  });

  // 401: пока перечитывается me — скелетон (сессия кончилась — роутер уведёт на вход); me ответил, а экран
  // всё ещё здесь (гонка смены cookie, D9) — не вечная «Загрузка…», а ошибка с «Повторить» (ревью m2).
  test('401 auth.unauthenticated: пока перечитывается me — скелетон, сессия жива — «Повторить»', async () => {
    const me = gate();
    server.use(
      http.get('/v1/staff/me', async () => {
        me.reach();
        await me.opened;
        return HttpResponse.json(meOwner) as never;
      }),
    );
    let n = 0;
    serve(() => (n++ === 0 ? problem(401, 'auth.unauthenticated') : ok()));
    renderWithProviders(<Sessions />);
    await me.reached;
    await vi.waitFor(() => expect(screen.getByTestId('skeleton')).toBeInTheDocument());
    expect(screen.queryByRole('alert')).toBeNull();
    me.open();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Код ошибки: 401 · auth.unauthenticated');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
  });
});

describe('нет сети', () => {
  test('без сети: пауза → NoNetworkState, сеть вернулась → запрос продолжился сам', async () => {
    const s = serve(() => ok());
    onlineManager.setOnline(false);
    renderWithProviders(<Sessions />);
    expect(await screen.findByText('Нет связи')).toBeInTheDocument();
    expect(screen.getByText('Покажем, как только появится.')).toBeInTheDocument();
    expect(s.calls()).toBe(0);
    act(() => onlineManager.setOnline(true));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
    expect(s.calls()).toBe(1);
  });

  test('ошибка без данных, затем «Повторить» без сети — NoNetworkState, а не старая ошибка', async () => {
    let n = 0;
    serve(() => (n++ === 0 ? problem(500, 'internal') : ok()));
    renderWithProviders(<Sessions />);
    expect(await screen.findByRole('alert')).toHaveTextContent('500');
    act(() => onlineManager.setOnline(false));
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('Нет связи')).toBeInTheDocument();
    expect(screen.queryByText(/Код ошибки/)).toBeNull();
    act(() => onlineManager.setOnline(true));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
  });

  test('TypeError fetch (сеть оборвалась при «онлайн») → нет сети и повтор', async () => {
    let n = 0;
    serve(() => (n++ === 0 ? HttpResponse.error() : ok()));
    renderWithProviders(<Sessions />);
    expect(await screen.findByText('Нет связи')).toBeInTheDocument();
    expect(screen.queryByRole('alert', { name: /Не удалось/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
  });

  test('фоновая ошибка перечитывания не прячет данные', async () => {
    let n = 0;
    serve(() => (n++ === 0 ? ok() : problem(500, 'internal')));
    const { queryClient } = renderWithProviders(<Sessions />);
    expect(await screen.findByText('сеансов: 1')).toBeInTheDocument();
    await act(() => queryClient.refetchQueries({ queryKey: ['test', 'sessions'] }));
    expect(queryClient.getQueryState(['test', 'sessions'])?.status).toBe('error');
    expect(screen.getByText('сеансов: 1')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

describe('QueryClient админки', () => {
  test('запросы — networkMode online, мутации — always', () => {
    const client = createQueryClient();
    expect(client.getDefaultOptions().queries?.networkMode).toBe('online');
    expect(client.getDefaultOptions().mutations?.networkMode).toBe('always');
  });

  test('мутации остаются always: без сети исполняются сразу, а не встают на паузу', async () => {
    const client = createQueryClient();
    onlineManager.setOnline(false);
    const fn = vi.fn(async () => 'ok');
    const result = await new MutationObserver(client, { mutationFn: fn }).mutate();
    expect(result).toBe('ok');
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe('части', () => {
  test('Skeleton: формы по пропсам, всё декоративное', () => {
    const { container } = renderWithIntl(
      <div>
        <Skeleton.Text lines={3} />
        <Skeleton.Block height="lg" />
        <Skeleton.Card />
        <Skeleton.Table columns={4} rows={2} />
        <Skeleton.Table columns={2} rows={3} head={false} />
        <Skeleton.Form fields={2} />
      </div>,
    );
    const parts = container.querySelectorAll('[data-skeleton]');
    expect(parts.length).toBeGreaterThan(0);
    for (const p of parts) expect(p.closest('[aria-hidden="true"]')).not.toBeNull();
    const [table, rows] = container.querySelectorAll('[data-skeleton="table"]');
    expect(table!.querySelectorAll('[data-skeleton-row]')).toHaveLength(2);
    expect(table!.children).toHaveLength(3); // шапка + 2 строки
    expect(table!.querySelector('[data-skeleton-row]')!.children).toHaveLength(4);
    expect(rows!.children).toHaveLength(3); // без шапки
    expect(container.querySelector('[data-skeleton="form"]')!.children).toHaveLength(2);
    expect(container.querySelector('[data-skeleton="text"]')!.children).toHaveLength(3);
    expect(container.textContent).toBe('');
  });

  test('EmptyState.illustration — рисунок декоративный, смысл в заголовке', () => {
    const { container } = renderWithIntl(
      <EmptyState illustration="whistle" title="Очередь пуста" />,
    );
    const svg = container.querySelector('svg')!;
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).toHaveAttribute('width', '64');
    expect(screen.getByText('Очередь пуста')).toBeInTheDocument();
  });

  test('NoNetworkState: без onRetry — без кнопки (продолжится сам)', () => {
    renderWithIntl(<NoNetworkState />);
    expect(screen.getByRole('status')).toHaveTextContent('Нет связи');
    expect(screen.queryByRole('button')).toBeNull();
  });
});
