import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { HttpResponse } from 'msw';
import { useState } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { components } from './gen/admin';
import { reauthBus } from '../app/reauth';
import { ME_KEY } from '../app/query';
import { district, flag } from '../test/geo';
import { meAdmin, meOwner } from '../test/me';
import { problem } from '../test/problem';
import { renderWithProviders } from '../test/render';
import { http, server } from '../test/server';
import { Button, useToast } from '../ui';
import { unwrap } from './client';
import { useApi } from './context';
import { useAdminMutation } from './mutation';
import { useUndoable } from './undo';

// «Отменить» в тосте (спека UI-фундамента §4.10, I5, Review Focus 8): обратное — новым ключом и с результатом
// прямого (актуальная version, прежнее значение — из переменных экрана); исход неизвестен — перечитать и сказать
// факт, не повторять вслепую.

type FeatureFlag = components['schemas']['FeatureFlag'];
type FlagPatch = components['schemas']['FeatureFlagPatch'];
type DistrictPatch = components['schemas']['GeoDistrictPatch'];
type Call = { key: string | null; body: FlagPatch };
type Answer = (body: FlagPatch) => Response | Promise<Response>;

const CITY = '0192f1c4-1111-7b21-9c5d-000000000001';
const DISTRICT = '0192f1c4-4444-7b21-9c5d-000000000001';

// PATCH /v1/flags/{key}: ответ по очереди answers, иначе — флаг с присланными значениями и version + 1.
// GET /v1/flags — состояние для reread.
function flagServer(start: Partial<FeatureFlag> = {}) {
  const calls: Call[] = [];
  const answers: Answer[] = [];
  const state = { flag: flag({ key: 'ads', version: 7, ...start }), reads: 0 };
  server.use(
    http.patch('/v1/flags/{key}', async ({ request, response }) => {
      const body = (await request.json()) as FlagPatch;
      calls.push({ key: request.headers.get('Idempotency-Key'), body });
      const own = answers.shift();
      if (own) return response.untyped(await own(body));
      state.flag = {
        ...state.flag,
        enabled_globally: body.enabled_globally ?? state.flag.enabled_globally,
        version: body.version + 1,
      };
      return response(200).json(state.flag);
    }),
    http.get('/v1/flags', ({ response }) => {
      state.reads++;
      return response(200).json({ items: [state.flag] });
    }),
  );
  return { calls, answers, state };
}

// Экран флага: прямое — включить с версией экрана; обратное — выключить с version из ОТВЕТА прямого (I5);
// reread — флаг выключен = отменено.
function FlagScreen({ version = 7 }: { version?: number }) {
  const api = useApi();
  const m = useAdminMutation((v: { enabled: boolean; version: number }, key: string) =>
    unwrap(
      api.PATCH('/v1/flags/{key}', {
        params: { path: { key: 'ads' }, header: { 'Idempotency-Key': key } },
        body: { version: v.version, enabled_globally: v.enabled },
      }),
    ),
  );
  const undoable = useUndoable(m, {
    run: async (v, result, key) => {
      await unwrap(
        api.PATCH('/v1/flags/{key}', {
          params: { path: { key: 'ads' }, header: { 'Idempotency-Key': key } },
          body: { version: result.version, enabled_globally: !v.enabled },
        }),
      );
    },
    reread: async (v) => {
      const list = await unwrap(api.GET('/v1/flags'));
      const f = list.items.find((i) => i.key === 'ads');
      return f?.enabled_globally === !v.enabled ? 'undone' : 'not_undone';
    },
  });
  return (
    <>
      <Button
        onClick={() =>
          undoable.mutate({ enabled: true, version }, { successText: `Флаг включён (${version})` })
        }
      >
        Включить {version}
      </Button>
      {version === 7 && (
        <Button
          onClick={() => {
            // одна мутация, два действия подряд (переключатели в строках списка)
            undoable.mutate({ enabled: true, version: 7 }, { successText: 'Флаг включён (7)' });
            undoable.mutate({ enabled: true, version: 30 }, { successText: 'Флаг включён (30)' });
          }}
        >
          Включить два
        </Button>
      )}
    </>
  );
}

// Экран с уходом: экран флага снимается кнопкой «Уйти со страницы» (провайдеры остаются).
function Page() {
  const [shown, setShown] = useState(true);
  return (
    <>
      {shown && <FlagScreen />}
      {shown && <FlagScreen version={20} />}
      <Button onClick={() => setShown(false)}>Уйти со страницы</Button>
    </>
  );
}

const click = (name: string | RegExp) => fireEvent.click(screen.getByRole('button', { name }));
const UNDONE = 'Отменено';
const NOT_UNDONE = 'Не отменено: действие осталось сделанным';

async function done(text = 'Флаг включён (7)') {
  click('Включить 7');
  await screen.findByText(text);
}

afterEach(() => {
  vi.useRealTimers();
  reauthBus.reject();
});

describe('useUndoable', () => {
  test('успех → тост с «Отменить» не исчезает через 8 с', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    flagServer();
    renderWithProviders(<Page />);
    await done();
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByText('Флаг включён (7)')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Отменить' })).toBeInTheDocument();
  });

  test('«Отменить» → обратное новым ключом; несёт version из ответа прямого (I5); итог — тост', async () => {
    const { calls } = flagServer();
    renderWithProviders(<Page />);
    await done();
    click('Отменить');
    await screen.findByText(UNDONE);
    expect(calls).toHaveLength(2);
    const [direct, inverse] = calls;
    expect(direct?.body).toEqual({ version: 7, enabled_globally: true });
    // прямой ответ — version 8: обратное несёт её, а не 7 из переменных экрана
    expect(inverse?.body).toEqual({ version: 8, enabled_globally: false });
    expect(inverse?.key).toBeTruthy();
    expect(inverse?.key).not.toBe(direct?.key);
    expect(screen.queryByText('Флаг включён (7)')).not.toBeInTheDocument();
  });

  test('двойной клик «Отменить» — один обратный запрос', async () => {
    const { calls } = flagServer();
    renderWithProviders(<Page />);
    await done();
    const undo = screen.getByRole('button', { name: 'Отменить' });
    fireEvent.click(undo);
    fireEvent.click(undo);
    await screen.findByText(UNDONE);
    expect(calls).toHaveLength(2);
  });

  test('обратное 4xx — тост-ошибка по коду, исходное остаётся сделанным, без повтора', async () => {
    const { calls, answers, state } = flagServer();
    renderWithProviders(<Page />);
    await done();
    answers.push(() => problem(404, 'platform.flag_not_found'));
    click('Отменить');
    await screen.findByText('Флаг не найден');
    expect(calls).toHaveLength(2);
    expect(state.flag.enabled_globally).toBe(true);
    expect(state.reads).toBe(0);
  });

  test('409 platform.flag_stale — тост «изменили», без повтора и без отмены', async () => {
    const { calls, answers } = flagServer();
    renderWithProviders(<Page />);
    await done();
    answers.push(() => problem(409, 'platform.flag_stale'));
    click('Отменить');
    await screen.findByText('Уже изменили — данные обновлены, отмены не было');
    expect(calls).toHaveLength(2);
    expect(screen.queryByText(UNDONE)).not.toBeInTheDocument();
  });

  test.each([
    ['сервер исполнил — «отменено»', true, UNDONE],
    ['сервер не исполнил — «не отменено»', false, NOT_UNDONE],
  ])(
    'обрыв ответа обратного → один запрос, reread, тост по факту: %s',
    async (_, applied, text) => {
      const { calls, answers, state } = flagServer();
      renderWithProviders(<Page />);
      await done();
      answers.push((body) => {
        if (applied)
          state.flag = { ...state.flag, enabled_globally: false, version: body.version + 1 };
        return HttpResponse.error();
      });
      click('Отменить');
      await screen.findByText(text);
      expect(calls).toHaveLength(2);
      expect(state.reads).toBe(1);
    },
  );

  test('новый тост с «Отменить», пока обратное в полёте: прежний заменён, обратное доходит, итог отдельно', async () => {
    const { calls, answers } = flagServer();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    renderWithProviders(<Page />);
    await done();
    answers.push(async (body) => {
      await gate;
      return HttpResponse.json(flag({ key: 'ads', version: body.version + 1 }));
    });
    click('Отменить');
    await waitFor(() => expect(calls).toHaveLength(2));
    click('Включить 20');
    await screen.findByText('Флаг включён (20)');
    release();
    await screen.findByText(UNDONE);
    // тост второго действия на месте, «Отменить» у него одна
    expect(screen.getByText('Флаг включён (20)')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Отменить' })).toHaveLength(1);
  });

  test('ушли со страницы, пока тост висит: тост живёт, обратное доходит', async () => {
    const { calls } = flagServer();
    renderWithProviders(<Page />);
    await done();
    click('Уйти со страницы');
    expect(screen.queryByRole('button', { name: /Включить/ })).not.toBeInTheDocument();
    click('Отменить');
    await screen.findByText(UNDONE);
    expect(calls).toHaveLength(2);
  });

  // Ревью UF2b/II-A, I2: тост успеха и «Отменить» не висят на колбэке одного вызова mutate (TanStack его глушит,
  // когда наблюдателя нет — экран снят — или следующий mutate перезаписал опции).
  test('экран снят до ответа прямого — тост успеха с «Отменить» всё равно есть', async () => {
    const { answers } = flagServer();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    answers.push(async (body) => {
      await gate;
      return HttpResponse.json(
        flag({ key: 'ads', enabled_globally: true, version: body.version + 1 }),
      );
    });
    renderWithProviders(<Page />);
    click('Включить 7');
    click('Уйти со страницы');
    release();
    await screen.findByText('Флаг включён (7)');
    expect(screen.getByRole('button', { name: 'Отменить' })).toBeInTheDocument();
  });

  test('второй вызов до ответа первого — тост успеха на каждое действие', async () => {
    const { answers } = flagServer();
    const gates = [0, 1].map(() => {
      let release!: () => void;
      const gate = new Promise<void>((r) => (release = r));
      return { gate, release };
    });
    for (const g of gates)
      answers.push(async (body) => {
        await g.gate;
        return HttpResponse.json(
          flag({ key: 'ads', enabled_globally: true, version: body.version + 1 }),
        );
      });
    renderWithProviders(<Page />);
    click('Включить два');
    gates[0]?.release();
    await screen.findByText('Флаг включён (7)');
    gates[1]?.release();
    await screen.findByText('Флаг включён (30)');
  });

  // m6: итог обратного, который был в полёте при смене сотрудника, следующему не показывается.
  test('смена сотрудника, пока обратное в полёте, — итог не показан', async () => {
    const { calls, answers } = flagServer();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const { queryClient } = renderWithProviders(<Page />, { me: meOwner });
    await done();
    answers.push(async (body) => {
      await gate;
      return HttpResponse.json(flag({ key: 'ads', version: body.version + 1 }));
    });
    click('Отменить');
    await waitFor(() => expect(calls).toHaveLength(2));
    act(() => queryClient.setQueryData(ME_KEY, meAdmin));
    release();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(screen.queryByText(UNDONE)).not.toBeInTheDocument();
  });

  test('401 reauth_required на обратном — ждёт разблокировку, затем итог', async () => {
    const { calls, answers } = flagServer();
    renderWithProviders(<Page />);
    await done();
    answers.push(() => problem(401, 'auth.reauth_required'));
    click('Отменить');
    await waitFor(() => expect(reauthBus.pending()).toBe(true));
    expect(screen.queryByText(UNDONE)).not.toBeInTheDocument();
    act(() => reauthBus.resolve());
    await screen.findByText(UNDONE);
    // повтор после разблокировки — клон запроса с тем же ключом (клиент API)
    expect(calls).toHaveLength(3);
    expect(calls[2]?.key).toBe(calls[1]?.key);
  });

  test.each([
    ['смена сотрудника', meAdmin],
    ['выход (сессии нет)', null],
  ])('%s — тост с «Отменить» снят', async (_, next) => {
    flagServer();
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        response.untyped(problem(401, 'auth.unauthenticated')),
      ),
    );
    const { queryClient } = renderWithProviders(<Page />, { me: meOwner });
    await done();
    if (next) act(() => queryClient.setQueryData(ME_KEY, next));
    else await act(() => queryClient.refetchQueries({ queryKey: ME_KEY }));
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Отменить' })).not.toBeInTheDocument(),
    );
    expect(document.documentElement).not.toHaveAttribute('data-toast-open');
  });
});

test('сотрудник появился (me загрузился) — тост с «Отменить» не снят', async () => {
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  server.use(
    http.get('/v1/staff/me', async ({ response }) => {
      await gate;
      return response(200).json(meOwner);
    }),
  );
  function Early() {
    const toast = useToast();
    return (
      <Button onClick={() => toast.success('Район в архиве', { undo: async () => {} })}>
        Тост
      </Button>
    );
  }
  renderWithProviders(<Early />, { me: null });
  click('Тост');
  release();
  await waitFor(() => expect(document.documentElement).toHaveAttribute('data-toast-open'));
  await act(async () => {
    await new Promise((r) => setTimeout(r, 50));
  });
  expect(screen.getByRole('button', { name: 'Отменить' })).toBeInTheDocument();
});

// Переименование района: прежнее имя — в переменных экрана (у района нет version в контракте, m11).
function DistrictScreen() {
  const api = useApi();
  const m = useAdminMutation((v: { name: string; previous: string }, key: string) =>
    unwrap(
      api.PATCH('/v1/geo/cities/{cityId}/districts/{districtId}', {
        params: {
          path: { cityId: CITY, districtId: DISTRICT },
          header: { 'Idempotency-Key': key },
        },
        body: { name: v.name },
      }),
    ),
  );
  const undoable = useUndoable(m, {
    run: async (v, _r, key) => {
      await unwrap(
        api.PATCH('/v1/geo/cities/{cityId}/districts/{districtId}', {
          params: {
            path: { cityId: CITY, districtId: DISTRICT },
            header: { 'Idempotency-Key': key },
          },
          body: { name: v.previous },
        }),
      );
    },
    reread: async () => 'not_undone',
  });
  return (
    <Button
      onClick={() =>
        undoable.mutate({ name: 'Б', previous: 'А' }, { successText: 'Переименовано' })
      }
    >
      Переименовать
    </Button>
  );
}

test('переименование «А» → «Б»: «Отменить» шлёт имя «А»', async () => {
  const bodies: DistrictPatch[] = [];
  server.use(
    http.patch('/v1/geo/cities/{cityId}/districts/{districtId}', async ({ request, response }) => {
      const body = (await request.json()) as DistrictPatch;
      bodies.push(body);
      return response(200).json(district({ id: DISTRICT, name: body.name ?? '' }));
    }),
  );
  renderWithProviders(<DistrictScreen />);
  click('Переименовать');
  await screen.findByText('Переименовано');
  click('Отменить');
  await screen.findByText(UNDONE);
  expect(bodies).toEqual([{ name: 'Б' }, { name: 'А' }]);
});
