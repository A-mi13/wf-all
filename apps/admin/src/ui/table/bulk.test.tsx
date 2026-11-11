import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode, useState } from 'react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { Decision, Me } from '../../app/permissions';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from '../../test/me';
import { renderWithProviders } from '../../test/render';
import { http, server } from '../../test/server';
import {
  BULK_CONCURRENCY,
  BULK_RETRY_DEFAULT_MS,
  type BulkReport,
  retryBulk,
  runBulk,
} from './bulk';
import { BulkBar } from './BulkBar';
import type { BulkAction, BulkOutcome } from './model';

// Ключи — тем же генератором, что у одиночных мутаций (src/api/idempotency.ts): подменяем на счётчик,
// чтобы «тот же ключ» и «новый ключ» были видны в тесте.
let keySeq = 0;
vi.mock('../../api/idempotency', () => ({ newIdempotencyKey: () => `k${++keySeq}` }));

beforeEach(() => {
  keySeq = 0;
});
afterEach(() => {
  vi.useRealTimers();
});

type Row = { id: string; name: string; perm: string; city?: string };
const row = (id: string, perm = 'x.do', city?: string): Row => ({
  id,
  name: `Строка ${id}`,
  perm,
  city,
});
const rows = (n: number, perm?: string) =>
  Array.from({ length: n }, (_, i) => row(`r${i + 1}`, perm));

// Контролируемые промисы: каждый вызов run — запись, тест сам решает исход и порядок.
type Call = {
  row: Row;
  key: string;
  resolve(o: BulkOutcome): void;
  reject(e: unknown): void;
};
function controlled(concurrency?: number) {
  const calls: Call[] = [];
  let inFlight = 0;
  let maxInFlight = 0;
  const action: BulkAction<Row> = {
    id: 'disable',
    label: 'Выключить',
    tone: 'danger',
    permission: (r) => ({ perm: r.perm, cityId: r.city }),
    confirm: { title: (n) => `Выключить ${n}?` },
    concurrency,
    run: (r, key) =>
      new Promise<BulkOutcome>((resolve, reject) => {
        inFlight++;
        maxInFlight = Math.max(maxInFlight, inFlight);
        const done = () => inFlight--;
        calls.push({
          row: r,
          key,
          resolve: (o) => {
            done();
            resolve(o);
          },
          reject: (e) => {
            done();
            reject(e);
          },
        });
      }),
  };
  return {
    action,
    calls,
    max: () => maxInFlight,
    call: (id: string, nth = 0) => calls.filter((c) => c.row.id === id)[nth],
  };
}

const allow = (): Decision => 'allow';
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
const tooMany = (retryAfter?: number) =>
  new ApiError({ status: 429, code: 'rate_limited', retryAfter });
const invalid = () => new ApiError({ status: 422, code: 'validation.failed' });
const ids = (list: Row[]) => list.map((r) => r.id);

describe('runBulk', () => {
  test('5 строк: 3 executed, 1 — 202, 1 — 422 → итог по видам, ключ на строку, прогресс', async () => {
    const c = controlled();
    const progress = vi.fn();
    const p = runBulk(rows(5), c.action, { decide: allow, onProgress: progress });
    await Promise.resolve();
    for (const id of ['r1', 'r2', 'r3']) c.call(id)?.resolve('executed');
    await vi.waitFor(() => expect(c.call('r5')).toBeDefined());
    c.call('r4')?.resolve('pending_approval');
    c.call('r5')?.reject(invalid());
    const report = await p;
    expect(ids(report.executed)).toEqual(['r1', 'r2', 'r3']);
    expect(ids(report.pending)).toEqual(['r4']);
    expect(report.failed.map((f) => [f.row.id, f.error.code])).toEqual([
      ['r5', 'validation.failed'],
    ]);
    expect(report.unknown).toEqual([]);
    expect(report.skipped).toEqual([]);
    expect(report.cancelled).toEqual([]);
    expect(new Set(c.calls.map((x) => x.key)).size).toBe(5);
    // ключи — из общего генератора (src/api/idempotency.ts, здесь подменён счётчиком)
    for (const x of c.calls) expect(x.key).toMatch(/^k\d+$/);
    expect(progress).toHaveBeenLastCalledWith(5, 5);
  });

  test('не больше concurrency в полёте (по умолчанию 3)', async () => {
    for (const conc of [undefined, 2]) {
      const c = controlled(conc);
      const p = runBulk(rows(10), c.action, { decide: allow });
      for (let i = 0; i < 10; i++) {
        await vi.waitFor(() => expect(c.calls.length).toBeGreaterThan(i));
        c.calls[i]?.resolve('executed');
      }
      await p;
      expect(c.max()).toBe(conc ?? BULK_CONCURRENCY);
    }
    expect(BULK_CONCURRENCY).toBe(3);
  });

  test('429 с Retry-After: 2 — общая пауза: ни один исполнитель не стартует, строка — тем же ключом', async () => {
    vi.useFakeTimers();
    const c = controlled();
    const p = runBulk(rows(6), c.action, { decide: allow });
    await flush();
    expect(c.calls).toHaveLength(3);
    const key2 = c.call('r2')?.key;
    c.call('r2')?.reject(tooMany(2));
    await flush();
    // два других исполнителя освободились — за паузу всё равно ни одного нового запроса
    c.call('r1')?.resolve('executed');
    c.call('r3')?.resolve('executed');
    await act(() => vi.advanceTimersByTimeAsync(1999));
    expect(c.calls).toHaveLength(3);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(c.call('r2', 1)?.key).toBe(key2);
    expect(c.calls.length).toBeGreaterThan(3);
    expect(c.calls.length).toBeLessThanOrEqual(6);
    for (const id of ['r4', 'r5', 'r6']) {
      await act(() => vi.advanceTimersByTimeAsync(0));
      c.call(id)?.resolve('executed');
    }
    c.call('r2', 1)?.resolve('executed');
    const report = await p;
    expect(ids(report.executed).sort()).toEqual(['r1', 'r2', 'r3', 'r4', 'r5', 'r6']);
    expect(c.max()).toBeLessThanOrEqual(3);
  });

  test('429 без Retry-After — пауза BULK_RETRY_DEFAULT_MS; повторный 429 — снова пауза, строка не теряется', async () => {
    vi.useFakeTimers();
    const c = controlled(1);
    const p = runBulk(rows(2), c.action, { decide: allow });
    await flush();
    c.call('r1')?.reject(tooMany());
    await act(() => vi.advanceTimersByTimeAsync(BULK_RETRY_DEFAULT_MS - 1));
    expect(c.calls).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(c.calls).toHaveLength(2);
    c.call('r1', 1)?.reject(tooMany());
    await act(() => vi.advanceTimersByTimeAsync(BULK_RETRY_DEFAULT_MS));
    expect(c.call('r1', 2)?.key).toBe(c.call('r1')?.key);
    c.call('r1', 2)?.resolve('executed');
    await flush();
    c.call('r2')?.resolve('executed');
    const report = await p;
    expect(ids(report.executed)).toEqual(['r1', 'r2']);
    expect(BULK_RETRY_DEFAULT_MS).toBe(5000);
  });

  test('обрыв и 5xx → unknown с ключом; «Повторить» шлёт тот же ключ', async () => {
    const c = controlled();
    const p = runBulk(rows(2), c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(2));
    c.call('r1')?.reject(new TypeError('Failed to fetch'));
    c.call('r2')?.reject(new ApiError({ status: 503, code: 'internal' }));
    const report = await p;
    expect(report.unknown.map((u) => u.row.id)).toEqual(['r1', 'r2']);
    expect(report.unknown.map((u) => u.key)).toEqual([c.call('r1')?.key, c.call('r2')?.key]);
    expect(report.failed).toEqual([]);
    const again = retryBulk(report, c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(4));
    expect(c.call('r1', 1)?.key).toBe(c.call('r1')?.key);
    expect(c.call('r2', 1)?.key).toBe(c.call('r2')?.key);
    c.call('r1', 1)?.resolve('executed');
    c.call('r2', 1)?.resolve('executed');
    expect(ids((await again).executed)).toEqual(['r1', 'r2']);
  });

  test('422 → failed; «Повторить» — новым ключом', async () => {
    const c = controlled();
    const p = runBulk(rows(1), c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.reject(invalid());
    const report = await p;
    const again = retryBulk(report, c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(2));
    expect(c.call('r1', 1)?.key).not.toBe(c.call('r1')?.key);
    c.call('r1', 1)?.resolve('executed');
    await again;
  });

  test('202 в «Повторить неудавшиеся» не попадает', async () => {
    const c = controlled();
    const p = runBulk(rows(2), c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(2));
    c.call('r1')?.resolve('pending_approval');
    c.call('r2')?.reject(invalid());
    const report = await p;
    const again = retryBulk(report, c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(3));
    c.call('r2', 1)?.resolve('executed');
    const next = await again;
    expect(c.calls.filter((x) => x.row.id === 'r1')).toHaveLength(1);
    // повтор не теряет прежнее: 202 остаётся в итоге
    expect(ids(next.pending)).toEqual(['r1']);
    expect(ids(next.executed)).toEqual(['r2']);
  });

  test('строка с deny не запрашивается — skipped', async () => {
    const c = controlled();
    const decide = (p: string): Decision => (p === 'x.do' ? 'allow' : 'deny');
    const p = runBulk([row('r1'), row('r2', 'x.no')], c.action, { decide });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.resolve('executed');
    const report = await p;
    expect(ids(report.skipped)).toEqual(['r2']);
    expect(c.calls.map((x) => x.row.id)).toEqual(['r1']);
  });

  test('право — по городу строки: чужой город — skipped, свой — исполняется', async () => {
    const c = controlled();
    const decide = (p: string, city?: string): Decision =>
      p === 'x.do' && city === 'A' ? 'allow' : 'deny';
    const p = runBulk([row('r1', 'x.do', 'A'), row('r2', 'x.do', 'B')], c.action, { decide });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.resolve('executed');
    const report = await p;
    expect(ids(report.skipped)).toEqual(['r2']);
    expect(c.calls.map((x) => x.row.id)).toEqual(['r1']);
  });

  test('401 auth.unauthenticated → пачка стоит до перечитывания me, оставшиеся не уходят', async () => {
    const c = controlled(1);
    const ctl = new AbortController();
    let settle!: () => void;
    const onForbidden = vi.fn(() => new Promise<void>((r) => (settle = r)));
    const p = runBulk(rows(3), c.action, { decide: allow, onForbidden, signal: ctl.signal });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.reject(new ApiError({ status: 401, code: 'auth.unauthenticated' }));
    await vi.waitFor(() => expect(onForbidden).toHaveBeenCalledTimes(1));
    await new Promise((r) => setTimeout(r, 0));
    expect(c.calls).toHaveLength(1);
    // me перечитан → anonymous → каркас снят → размонтирование отменяет пачку
    ctl.abort();
    settle();
    const report = await p;
    expect(report.failed.map((f) => f.error.code)).toEqual(['auth.unauthenticated']);
    expect(ids(report.cancelled)).toEqual(['r2', 'r3']);
    expect(c.calls).toHaveLength(1);
  });

  test('unknown при повторе без права остаётся unknown (не «пропущено»)', async () => {
    const c = controlled();
    const p = runBulk(rows(1), c.action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.reject(new TypeError('Failed to fetch'));
    const report = await p;
    const next = await retryBulk(report, c.action, { decide: () => 'deny' });
    expect(next.unknown).toEqual(report.unknown);
    expect(next.skipped).toEqual([]);
    expect(c.calls).toHaveLength(1);
  });

  test('permission(row) бросил — строка пропущена, пачка доходит до конца', async () => {
    const c = controlled(1);
    const err = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const action: BulkAction<Row> = {
      ...c.action,
      permission: (r) => {
        if (r.id === 'r1') throw new Error('неожиданная строка');
        return { perm: r.perm };
      },
    };
    const p = runBulk(rows(2), action, { decide: allow });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r2')?.resolve('executed');
    const report = await p;
    expect(ids(report.skipped)).toEqual(['r1']);
    expect(ids(report.executed)).toEqual(['r2']);
    expect(err).toHaveBeenCalled();
    err.mockRestore();
  });

  test('403 → пауза до перечитывания прав, оставшиеся строки перепроверяются', async () => {
    const c = controlled(1);
    let perms: Decision = 'allow';
    let refreshed!: () => void;
    const onForbidden = vi.fn(
      () =>
        new Promise<void>((r) => {
          refreshed = () => {
            perms = 'deny'; // права сняли
            r();
          };
        }),
    );
    const p = runBulk(rows(3), c.action, { decide: () => perms, onForbidden });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.reject(new ApiError({ status: 403, code: 'auth.forbidden' }));
    await vi.waitFor(() => expect(onForbidden).toHaveBeenCalledTimes(1));
    await new Promise((r) => setTimeout(r, 0));
    expect(c.calls).toHaveLength(1);
    refreshed();
    const report = await p;
    expect(report.failed.map((f) => f.row.id)).toEqual(['r1']);
    expect(ids(report.skipped)).toEqual(['r2', 'r3']);
    expect(c.calls).toHaveLength(1);
  });

  test('отмена signal посреди → в полёте доходит, новые не стартуют (cancelled)', async () => {
    const c = controlled(1);
    const ctl = new AbortController();
    const p = runBulk(rows(3), c.action, { decide: allow, signal: ctl.signal });
    await vi.waitFor(() => expect(c.calls).toHaveLength(1));
    ctl.abort();
    c.call('r1')?.resolve('executed');
    const report = await p;
    expect(ids(report.executed)).toEqual(['r1']);
    expect(ids(report.cancelled)).toEqual(['r2', 'r3']);
    expect(c.calls).toHaveLength(1);
  });

  test('отмена во время паузы 429 — пауза прерывается, остальные cancelled', async () => {
    vi.useFakeTimers();
    const c = controlled(1);
    const ctl = new AbortController();
    const p = runBulk(rows(2), c.action, { decide: allow, signal: ctl.signal });
    await flush();
    c.call('r1')?.reject(tooMany(60));
    await flush();
    ctl.abort();
    const report = await p; // без продвижения таймеров
    expect(ids(report.cancelled)).toEqual(['r1', 'r2']);
    expect(c.calls).toHaveLength(1);
    expect(vi.getTimerCount()).toBe(0);
  });
});

// --- BulkBar ---

const meBulk: Me = { ...meOwner, permissions: { 'x.do': 'allow', 'x.dual': 'dual' } };

function Bar({
  list,
  action,
  onPending = () => undefined,
  onSettled,
}: {
  list: Row[];
  action: BulkAction<Row>;
  onPending?: () => unknown;
  onSettled?: (report: BulkReport<Row>) => unknown;
}) {
  const [shown, setShown] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setShown(false)}>
        уйти
      </button>
      {shown && (
        <BulkBar
          rows={list}
          rowLabel={(r) => r.name}
          actions={[action]}
          onClear={() => undefined}
          onPending={onPending}
          onSettled={onSettled}
        />
      )}
    </>
  );
}

async function confirm(word: string) {
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: word } });
  return dialog;
}

describe('BulkBar', () => {
  test('«Выбрано N»; подтверждение называет обе части', async () => {
    const c = controlled();
    const list = [...rows(3), row('d1', 'x.no'), row('d2', 'x.no')];
    const { user } = renderBar(list, c.action);
    expect(screen.getByRole('region', { name: 'Массовые действия' })).toHaveTextContent(
      'Выбрано 5',
    );
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await screen.findByRole('dialog', { name: 'Выключить 3?' });
    expect(dialog).toHaveTextContent('Ещё 2 — без вашего права на это действие: пропустим.');
  });

  test('действие недоступно, если нельзя ни для одной выбранной', () => {
    const c = controlled();
    renderBar([row('d1', 'x.no')], c.action);
    expect(screen.getByRole('button', { name: 'Выключить' })).toBeDisabled();
  });

  test('двойное подтверждение — одна пачка', async () => {
    const c = controlled();
    const { user } = renderBar(rows(2), c.action);
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await confirm('2');
    const form = dialog.querySelector('form') as HTMLFormElement;
    // два подтверждения в одном такте, до перерисовки с loading (двойной Enter): диалог их не гасит
    act(() => {
      fireEvent.submit(form);
      fireEvent.submit(form);
    });
    await waitFor(() => expect(c.calls).toHaveLength(2));
    c.calls.forEach((x) => x.resolve('executed'));
    expect(await screen.findByText('Выполнено 2')).toBeInTheDocument();
    expect(c.calls).toHaveLength(2);
  });

  test('итог по видам, тексты ошибок; после 202 — onPending; «Повторить неудавшиеся»: 4xx новым ключом, неизвестный — тем же', async () => {
    const c = controlled();
    const onPending = vi.fn();
    const onSettled = vi.fn();
    const { user } = renderBar(rows(4), c.action, { onPending, onSettled });
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await confirm('4');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(c.calls).toHaveLength(3));
    c.call('r1')?.resolve('executed');
    c.call('r2')?.resolve('pending_approval');
    c.call('r3')?.reject(invalid());
    await waitFor(() => expect(c.calls).toHaveLength(4));
    c.call('r4')?.reject(new TypeError('Failed to fetch'));
    const status = await screen.findByRole('status');
    await waitFor(() =>
      expect(status).toHaveTextContent(
        'Выполнено 1 · отправлено на подтверждение 1 · ошибка 1 · исход неизвестен 1',
      ),
    );
    expect(onPending).toHaveBeenCalledTimes(1);
    expect(onSettled).toHaveBeenCalledTimes(1);
    expect(ids((onSettled.mock.calls[0]?.[0] as BulkReport<Row>).executed)).toEqual(['r1']);
    expect(screen.getByText(/Строка r3: Проверьте/)).toBeInTheDocument();
    expect(screen.getByText(/Строка r4: исход неизвестен/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Повторить неудавшиеся' }));
    await waitFor(() => expect(c.calls).toHaveLength(6));
    expect(c.call('r3', 1)?.key).not.toBe(c.call('r3')?.key);
    expect(c.call('r4', 1)?.key).toBe(c.call('r4')?.key);
    expect(c.calls.filter((x) => x.row.id === 'r2')).toHaveLength(1);
    c.call('r3', 1)?.resolve('executed');
    c.call('r4', 1)?.resolve('executed');
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        'Выполнено 3 · отправлено на подтверждение 1',
      ),
    );
    // повтор без новых 202 подтверждения не перечитывает
    expect(onPending).toHaveBeenCalledTimes(1);
    expect(onSettled).toHaveBeenCalledTimes(2);
  });

  test('модератор: строки чужого города — вторая часть подтверждения и «пропущено», своего — исполняются', async () => {
    const c = controlled();
    const me: Me = { ...meModerator, city_ids: [CITY_OWN], permissions: { 'x.city': 'scoped' } };
    const own = [1, 2, 3].map((i) => row(`o${i}`, 'x.city', CITY_OWN));
    const other = [1, 2].map((i) => row(`b${i}`, 'x.city', CITY_OTHER));
    const { user } = renderBar([...own, ...other], c.action, { me });
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await screen.findByRole('dialog', { name: 'Выключить 3?' });
    expect(dialog).toHaveTextContent('Ещё 2 — без вашего права на это действие: пропустим.');
    await confirm('3');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(c.calls).toHaveLength(3));
    c.calls.forEach((x) => x.resolve('executed'));
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('Выполнено 3 · пропущено 2'),
    );
    expect(c.calls.map((x) => x.row.id)).toEqual(['o1', 'o2', 'o3']);
  });

  test('403 посреди: me перечитан, оставшиеся строки перепроверены по свежим правам', async () => {
    const c = controlled(1);
    let meReads = 0;
    server.use(
      http.get('/v1/staff/me', ({ response }) => {
        meReads++;
        return response(200).json({ ...meBulk, permissions: {} }); // право сняли
      }),
    );
    const { user } = renderBar(rows(3), c.action);
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await confirm('3');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(c.calls).toHaveLength(1));
    c.call('r1')?.reject(new ApiError({ status: 403, code: 'auth.forbidden' }));
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('ошибка 1 · пропущено 2'),
    );
    expect(c.calls).toHaveLength(1);
    expect(meReads).toBeGreaterThan(0);
  });

  test('размонтирование посреди пачки: в полёте доходит, новые не стартуют, итог — тостом', async () => {
    const c = controlled(1);
    const { user } = renderBar(rows(3), c.action);
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    const dialog = await confirm('3');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(c.calls).toHaveLength(1));
    await user.click(screen.getByRole('button', { name: 'уйти' }));
    expect(screen.queryByRole('region', { name: 'Массовые действия' })).toBeNull();
    c.call('r1')?.resolve('executed');
    expect(await screen.findByText('Выполнено 1 · не начато 2')).toBeInTheDocument();
    expect(c.calls).toHaveLength(1);
  });
});

function renderBar(
  list: Row[],
  action: BulkAction<Row>,
  {
    onPending,
    onSettled,
    me = meBulk,
  }: {
    onPending?: () => unknown;
    onSettled?: (report: BulkReport<Row>) => unknown;
    me?: Me;
  } = {},
) {
  const user = userEvent.setup();
  const r = renderWithProviders(
    <StrictMode>
      <Bar list={list} action={action} onPending={onPending} onSettled={onSettled} />
    </StrictMode>,
    { me },
  );
  return { ...r, user };
}
