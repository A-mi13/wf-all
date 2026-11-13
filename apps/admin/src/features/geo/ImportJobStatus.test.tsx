import { focusManager } from '@tanstack/react-query';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { components } from '../../api/gen/admin';
import { renderApp } from '../../test/app';
import { geoImport, importJob, importsServer, meDirectoriesAdmin } from '../../test/geo';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { FAST_WINDOW_MS, POLL_CEILING_MS, POLL_FAST_MS, POLL_SLOW_MS } from './importPoll';

type GeoImportJob = components['schemas']['GeoImportJob'];

// Строка задачи импорта (план B, Task 20; бриф G9-I m-7, m-8): опрос getGeoImportJob фоновым запросом — первые
// 2 минуты раз в 5 с, затем раз в 60 с; в queued (и незнакомом состоянии) через 10 минут от монтирования строки —
// стоп с текстом и кнопками; конечное состояние — стоп и перечитывание журнала; уход со страницы — стоп. Окно — по
// Date.now() от монтирования, не по тикам (скрытая вкладка троттлит таймеры). Отмена — ключ идемпотентности,
// 409 — перечитать, неизвестный исход — повтор тем же ключом, 404 — журнал.

const JOB = 2001;

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});
afterEach(() => {
  vi.useRealTimers();
  focusManager.setFocused(undefined);
});

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));

function setup(
  job: Partial<GeoImportJob> = {},
  opts: Parameters<typeof importsServer>[0] = {},
  path = `/geo/imports?job=${JOB}`,
) {
  sectionsServer();
  const srv = importsServer({
    jobs: [importJob({ job_id: JOB, state: 'queued', import_id: null, attempt: 0, ...job })],
    ...opts,
  });
  const app = renderApp(path, { me: meDirectoriesAdmin });
  const count = (op: string) => srv.sent.filter((s) => s.op === op).length;
  return { srv, app, jobs: () => count('job'), lists: () => count('list') };
}

const row = () => screen.findByRole('region', { name: 'Задача импорта' });
// строка с ответом задачи (до него — скелетон)
const ready = async () => {
  const r = await row();
  await within(r).findByText(/^Поставлена/);
  return r;
};

describe('опрос', () => {
  test('5 с → 60 с → стоп через 10 мин в queued: текст, «Проверить ещё раз», «Отменить»', async () => {
    const { jobs } = setup();
    expect(within(await ready()).getByText('в очереди')).toBeInTheDocument();
    expect(jobs()).toBe(1);
    await advance(POLL_FAST_MS);
    expect(jobs()).toBe(2);
    // до конца быстрого окна — раз в 5 с
    await advance(FAST_WINDOW_MS - POLL_FAST_MS);
    const fast = jobs();
    expect(fast).toBeGreaterThanOrEqual(24);
    // последний быстрый тик мог прийти чуть после 2 мин — дальше раз в 60 с
    await advance(POLL_FAST_MS + 100);
    const slow = jobs();
    await advance(POLL_SLOW_MS / 2);
    expect(jobs()).toBe(slow);
    await advance(POLL_SLOW_MS / 2 + 100);
    expect(jobs()).toBe(slow + 1);
    // потолок: 10 мин от монтирования
    await advance(POLL_CEILING_MS);
    const stopped = jobs();
    const r = await row();
    expect(
      within(r).getByText('Задача ждёт воркер — если его нет (dev-стенд), импорт не начнётся.'),
    ).toBeInTheDocument();
    expect(within(r).getByRole('button', { name: 'Проверить ещё раз' })).toBeInTheDocument();
    expect(within(r).getByRole('button', { name: `Отменить задачу ${JOB}` })).toBeInTheDocument();
    await advance(POLL_CEILING_MS);
    expect(jobs()).toBe(stopped);
    // «Проверить ещё раз» — один запрос, опрос не возобновляется (всё ещё queued)
    fireEvent.click(within(r).getByRole('button', { name: 'Проверить ещё раз' }));
    await advance(100);
    expect(jobs()).toBe(stopped + 1);
    await advance(POLL_CEILING_MS);
    expect(jobs()).toBe(stopped + 1);
  });

  test('каждый запрос задачи — с X-WF-Background: 1', async () => {
    const { srv } = setup();
    await ready();
    await advance(POLL_FAST_MS * 3);
    const sent = srv.sent.filter((s) => s.op === 'job');
    expect(sent.length).toBeGreaterThanOrEqual(3);
    expect(sent.every((s) => s.background === '1')).toBe(true);
  });

  test('скрытая вкладка 5 мин → видима → интервал уже 60 с (окно — по часам, не по тикам)', async () => {
    const { jobs } = setup();
    await ready();
    act(() => focusManager.setFocused(false));
    await advance(5 * 60_000);
    expect(jobs()).toBe(1);
    // возврат во вкладку — перечитывание по фокусу, дальше — медленный интервал
    act(() => focusManager.setFocused(true));
    await advance(100);
    const back = jobs();
    expect(back).toBe(2);
    await advance(POLL_FAST_MS * 2);
    expect(jobs()).toBe(back);
    await advance(POLL_SLOW_MS);
    expect(jobs()).toBe(back + 1);
  });

  test('completed → опрос стоп, журнал перечитан, «Открыть отчёт»', async () => {
    const imp = geoImport({ id: '0192f1f5-1a2b-7c3d-8e4f-5a6b7c8d9e77' });
    const { srv, jobs, lists, app } = setup({ state: 'running', attempt: 1 }, { imports: [imp] });
    const r = await ready();
    expect(within(r).getByText('выполняется')).toBeInTheDocument();
    const listed = lists();
    srv.state.jobs.set(JOB, importJob({ job_id: JOB, state: 'completed', import_id: imp.id }));
    await advance(POLL_FAST_MS);
    expect(await within(r).findByText('выполнена')).toBeInTheDocument();
    const done = jobs();
    await advance(POLL_SLOW_MS * 3);
    expect(jobs()).toBe(done);
    expect(lists()).toBeGreaterThan(listed);
    fireEvent.click(within(r).getByRole('button', { name: 'Открыть отчёт' }));
    await advance(50);
    expect(app.at()).toContain(`import=${imp.id}`);
  });

  test('незнакомое состояние — чип «—», опрос до потолка, без «Отменить»', async () => {
    const { jobs } = setup({ state: 'paused' as GeoImportJob['state'] });
    const r = await ready();
    expect(within(r).getByText('—')).toBeInTheDocument();
    await advance(POLL_CEILING_MS + POLL_SLOW_MS);
    const stopped = jobs();
    expect(
      within(r).getByText('Статус задачи давно не менялся — опрос остановлен.'),
    ).toBeInTheDocument();
    expect(within(r).queryByRole('button', { name: /Отменить/ })).toBeNull();
    await advance(POLL_CEILING_MS);
    expect(jobs()).toBe(stopped);
  });

  test('404 при опросе — стоп, текст, журнал перечитан', async () => {
    const { srv, jobs, lists } = setup();
    const r = await ready();
    expect(within(r).getByText('в очереди')).toBeInTheDocument();
    const listed = lists();
    srv.state.jobs.delete(JOB);
    await advance(POLL_FAST_MS);
    expect(
      await within(r).findByText(
        'Задачи нет — она завершилась и удалена очередью. Журнал обновлён.',
      ),
    ).toBeInTheDocument();
    expect(lists()).toBeGreaterThan(listed);
    // ревью m-3, m-4: прежний чип не стоит рядом с «задачи нет»; текст — живая область
    expect(within(r).queryByText('в очереди')).toBeNull();
    expect(
      within(r)
        .getAllByRole('status')
        .some((el) => el.textContent?.startsWith('Задачи нет')),
    ).toBe(true);
    const stopped = jobs();
    await advance(POLL_SLOW_MS * 2);
    expect(jobs()).toBe(stopped);
  });

  // Ревью I-1: временный сбой опроса (деплой admin-api: 502 на запрос и оба повтора) не гасит опрос — иначе строка
  // навсегда «выполняется · Статус обновляется сам», а импорт давно завершён.
  test('502 на опросе (и повторы) — опрос продолжается, конечное состояние приходит', async () => {
    let fails = 0;
    const { srv, jobs } = setup(
      { state: 'running', attempt: 1 },
      { answer: (op) => (op === 'job' && fails-- > 0 ? problem(502, 'internal') : undefined) },
    );
    const r = await ready();
    // renderApp — клиент без повторов (retry: false): один 502 — то же, что запрос и оба повтора в бою
    fails = 1;
    await advance(POLL_FAST_MS + 100);
    expect(fails).toBeLessThanOrEqual(0);
    const failedAt = jobs();
    srv.state.jobs.set(JOB, importJob({ job_id: JOB, state: 'completed', import_id: null }));
    await advance(POLL_SLOW_MS + 100);
    expect(jobs()).toBeGreaterThan(failedAt);
    expect(await within(r).findByText('выполнена')).toBeInTheDocument();
  });

  // 429 (общий NAT офиса) не повторяется клиентом: строка честно говорит «не обновился», есть «Проверить ещё раз»,
  // опрос идёт дальше — следующий успешный ответ снимает текст.
  test('429 на опросе — «Статус не обновился» и «Проверить ещё раз», опрос продолжается', async () => {
    let fails = 0;
    const { jobs } = setup(
      { state: 'running', attempt: 1 },
      { answer: (op) => (op === 'job' && fails-- > 0 ? problem(429, 'rate_limited') : undefined) },
    );
    const r = await ready();
    fails = 1;
    await advance(POLL_FAST_MS + 100);
    expect(
      await within(r).findByText('Статус задачи не обновился — попробуем ещё раз.'),
    ).toBeInTheDocument();
    expect(within(r).queryByText('Статус обновляется сам')).toBeNull();
    expect(within(r).getByRole('button', { name: 'Проверить ещё раз' })).toBeInTheDocument();
    const failedAt = jobs();
    await advance(POLL_FAST_MS + 100);
    expect(jobs()).toBeGreaterThan(failedAt);
    await waitFor(() =>
      expect(within(r).queryByText('Статус задачи не обновился — попробуем ещё раз.')).toBeNull(),
    );
    expect(within(r).getByText('Статус обновляется сам')).toBeInTheDocument();
  });

  test('уход со страницы — запросов задачи больше нет', async () => {
    const { app, jobs } = setup();
    await ready();
    act(() => app.history.push('/geo/cities'));
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    const left = jobs();
    await advance(POLL_SLOW_MS * 3);
    expect(jobs()).toBe(left);
  });
});

describe('«Отменить»', () => {
  const cancel = async () =>
    fireEvent.click(
      await within(await row()).findByRole('button', { name: `Отменить задачу ${JOB}` }),
    );

  test('ключ идемпотентности, без тела → «отменена», журнал перечитан', async () => {
    const { srv, lists } = setup();
    await ready();
    const listed = lists();
    await cancel();
    expect(await within(await row()).findByText('отменена')).toBeInTheDocument();
    const sent = srv.sent.filter((s) => s.op === 'cancel');
    expect(sent).toHaveLength(1);
    expect(sent[0]?.path).toBe(`/v1/geo/imports/jobs/${JOB}/cancel`);
    expect(sent[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
    expect(sent[0]?.body).toBeNull();
    expect(lists()).toBeGreaterThan(listed);
    expect(within(await row()).queryByRole('button', { name: /Отменить/ })).toBeNull();
    // ревью m-4 (WCAG 2.4.3): кнопка исчезла — фокус на заголовке строки, не на body
    const title = within(await row()).getByText(`Задача ${JOB}`);
    await waitFor(() => expect(document.activeElement).toBe(title));
  });

  test('409 not_cancellable — текст и перечитанное состояние', async () => {
    const { srv, jobs } = setup();
    await ready();
    // задача успела начаться
    srv.state.jobs.set(JOB, importJob({ job_id: JOB, state: 'running', import_id: null }));
    const before = jobs();
    await cancel();
    const r = await row();
    expect(
      await within(r).findByText(
        'Задача уже выполняется или завершена — отменить нельзя. Состояние обновлено.',
      ),
    ).toBeInTheDocument();
    expect(await within(r).findByText('выполняется')).toBeInTheDocument();
    expect(jobs()).toBeGreaterThan(before);
  });

  test('неизвестный исход (500) → повтор тем же ключом → 200 cancelled (K6) → «отменена»', async () => {
    let failed = false;
    const { srv } = setup(
      {},
      {
        answer: (op) => {
          if (op !== 'cancel' || failed) return undefined;
          failed = true;
          return problem(500, 'internal');
        },
      },
    );
    await cancel();
    expect(await within(await row()).findByRole('alert')).toBeInTheDocument();
    await cancel();
    expect(await within(await row()).findByText('отменена')).toBeInTheDocument();
    const keys = srv.sent.filter((s) => s.op === 'cancel').map((s) => s.key);
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  test('уже отменена другой вкладкой — 200 cancelled, не ошибка', async () => {
    const { srv } = setup();
    await ready();
    srv.state.jobs.set(
      JOB,
      importJob({ job_id: JOB, state: 'cancelled', import_id: null, attempt: 0 }),
    );
    await cancel();
    const r = await row();
    expect(await within(r).findByText('отменена')).toBeInTheDocument();
    expect(within(r).queryByRole('alert')).toBeNull();
  });

  test('404 после «Отменить» — текст, журнал перечитан, не тупик («Скрыть задачу»)', async () => {
    const { srv, lists, app } = setup();
    await ready();
    const listed = lists();
    srv.state.jobs.delete(JOB);
    await cancel();
    const r = await row();
    expect(
      await within(r).findByText(
        'Задачи нет — она завершилась и удалена очередью. Журнал обновлён.',
      ),
    ).toBeInTheDocument();
    expect(lists()).toBeGreaterThan(listed);
    fireEvent.click(within(r).getByRole('button', { name: 'Скрыть задачу' }));
    await advance(50);
    expect(app.at()).toBe('/geo/imports');
  });
});
