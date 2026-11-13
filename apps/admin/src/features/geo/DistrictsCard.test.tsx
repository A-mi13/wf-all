import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { problem } from '../../test/problem';
import { renderApp } from '../../test/app';
import { cityEditServer, district, meGeoModerator } from '../../test/geo';
import { CITY_OWN } from '../../test/me';
import { http, server } from '../../test/server';

// Ответ правки района ждёт release(): потеря фокуса (blur) изображается, пока запрос в полёте, — иначе
// быстрый ответ успевал до blur, и тест зависел от скорости мока (флак при запуске в паре с соседним).
function holdDistrictPatch() {
  let release = () => {};
  const gate = new Promise<void>((r) => (release = r));
  server.use(
    http.patch('/v1/geo/cities/{cityId}/districts/{districtId}', async () => {
      await gate;
      return undefined;
    }),
  );
  return () => release();
}

// Районы города (*; geo.districts.manage в городе): таблица — действующие, затем архивные с чипом;
// «Добавить район», «Переименовать» (окно), «В архив» / «Вернуть»; 409 geo.district_name_taken — у
// поля (добавление, переименование) или тостом (возврат из архива).

afterEach(() => sessionStorage.clear());

const ARCHIVED = district({
  id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e16',
  name: 'Старый центр',
  archived_at: '2026-10-01T09:00:00Z',
});

async function open(opts: Parameters<typeof cityEditServer>[0] = {}) {
  const srv = cityEditServer(opts);
  const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoModerator });
  await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
  const card = screen.getByRole('region', { name: 'Районы' });
  const table = await within(card).findByRole('table', { name: 'Районы города' });
  // районы перечитаны (сервер изменил список сам — другая вкладка, импорт)
  const refetch = () =>
    act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'districts'] }));
  return { ...srv, app, refetch, card, table, user: userEvent.setup() };
}

const rows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1);

describe('районы', () => {
  test('действующие, затем архивные с чипом «в архиве»', async () => {
    const { table } = await open({ districts: [district(), ARCHIVED] });
    const [active, archived] = rows(table);
    expect(active).toHaveTextContent('Промышленный');
    expect(active).toHaveTextContent('действует');
    expect(archived).toHaveTextContent('Старый центр');
    expect(archived).toHaveTextContent('в архиве');
    expect(
      within(archived!).getByRole('button', { name: 'Вернуть: Старый центр' }),
    ).toBeInTheDocument();
    expect(
      within(active!).getByRole('button', { name: 'В архив: Промышленный' }),
    ).toBeInTheDocument();
  });

  test('«Добавить район» — {name}, строка появляется', async () => {
    const { card, table, user, sent } = await open();
    await user.type(within(card).getByRole('textbox', { name: 'Новый район' }), 'Октябрьский');
    await user.click(within(card).getByRole('button', { name: 'Добавить район' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ name: 'Октябрьский' });
    expect(sent[0]?.path).toBe(`/v1/geo/cities/${CITY_OWN}/districts`);
    await waitFor(() => expect(rows(table)).toHaveLength(2));
    expect(within(card).getByRole('textbox', { name: 'Новый район' })).toHaveValue('');
  });

  test('409 geo.district_name_taken при добавлении — у поля', async () => {
    const { card, user } = await open();
    const field = within(card).getByRole('textbox', { name: 'Новый район' });
    await user.type(field, 'промышленный');
    await user.click(within(card).getByRole('button', { name: 'Добавить район' }));
    await waitFor(() =>
      expect(field).toHaveAccessibleDescription(/Район с таким названием уже есть/),
    );
  });

  test('«В архив» и «Вернуть» — {archived: true|false}', async () => {
    const { table, user, sent } = await open();
    await user.click(within(table).getByRole('button', { name: 'В архив: Промышленный' }));
    await waitFor(() => expect(rows(table)[0]).toHaveTextContent('в архиве'));
    expect(sent[0]?.body).toEqual({ archived: true });
    // ревью G8, m4: «В архив» сменилась на «Вернуть» — фокус на новой кнопке строки, не на body
    await waitFor(() =>
      expect(within(table).getByRole('button', { name: 'Вернуть: Промышленный' })).toHaveFocus(),
    );
    expect(await screen.findByText('Район «Промышленный» в архиве')).toBeInTheDocument();
    await user.click(within(table).getByRole('button', { name: 'Вернуть: Промышленный' }));
    await waitFor(() => expect(rows(table)[0]).toHaveTextContent('действует'));
    expect(sent[1]?.body).toEqual({ archived: false });
    await waitFor(() =>
      expect(within(table).getByRole('button', { name: 'В архив: Промышленный' })).toHaveFocus(),
    );
    expect(sent[1]?.path).toMatch(/\/districts\/c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e15$/);
  });

  // Ревью G8, m4: архивный район уходит вниз списка — React переставляет строку в DOM, и в браузере
  // фокус с её кнопки слетает на body (jsdom при перестановке фокус не снимает — потерю фокуса
  // изображает blur, пока запрос в полёте). После ответа фокус — на «Вернуть» этой строки.
  test('фокус потерян, пока строка переезжала, — после ответа на «Вернуть» этой строки', async () => {
    const { table, user } = await open({
      districts: [
        district(),
        district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e17', name: 'Юбилейный' }),
      ],
    });
    expect(rows(table)[0]).toHaveTextContent('Промышленный');
    const release = holdDistrictPatch();
    const archive = within(table).getByRole('button', { name: 'В архив: Промышленный' });
    await user.click(archive);
    act(() => archive.blur());
    expect(document.activeElement).toBe(document.body);
    release();
    await waitFor(() => expect(rows(table)[1]).toHaveTextContent('Промышленный'));
    await waitFor(() =>
      expect(within(table).getByRole('button', { name: 'Вернуть: Промышленный' })).toHaveFocus(),
    );
  });

  // Ревью G8, n2: цель фокуса гасится после применения — следующее обновление списка при фокусе на body
  // не уводит фокус на кнопку давно заархивированной строки.
  test('после архива следующее обновление списка фокус не трогает', async () => {
    const { table, user, state, refetch } = await open({
      districts: [
        district(),
        district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e17', name: 'Юбилейный' }),
      ],
    });
    const release = holdDistrictPatch();
    const archive = within(table).getByRole('button', { name: 'В архив: Промышленный' });
    await user.click(archive);
    act(() => archive.blur());
    release();
    const restore = await within(table).findByRole('button', { name: 'Вернуть: Промышленный' });
    await waitFor(() => expect(restore).toHaveFocus());
    act(() => restore.blur());
    state.districts = [
      ...state.districts,
      district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e18', name: 'Октябрьский' }),
    ];
    await refetch();
    await waitFor(() => expect(table).toHaveTextContent('Октябрьский'));
    expect(document.activeElement).toBe(document.body);
  });

  // Ревью G8, n3: переименование, меняющее порядок, — строка переезжает в DOM; фокус после закрытия окна —
  // на «Переименовать» этой строки, не на body. В jsdom (и в браузере) его возвращает и Overlay: районы
  // перечитаны до закрытия окна (мутация ждёт инвалидацию), открыватель на месте; setRefocus — запас.
  test('переименование, меняющее порядок, — фокус на «Переименовать» этой строки', async () => {
    const { table, user } = await open({
      districts: [
        district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e17', name: 'Ленинский' }),
        district(),
      ],
    });
    await user.click(within(table).getByRole('button', { name: 'Переименовать: Ленинский' }));
    const dialog = await screen.findByRole('dialog', { name: 'Переименовать район' });
    const field = within(dialog).getByRole('textbox', { name: 'Название' });
    await user.clear(field);
    await user.type(field, 'Юго-Западный');
    await user.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(rows(table)[1]).toHaveTextContent('Юго-Западный');
    await waitFor(() =>
      expect(
        within(table).getByRole('button', { name: 'Переименовать: Юго-Западный' }),
      ).toHaveFocus(),
    );
  });

  // Раунд 1, minor 1: цель фокуса одноразовая — не применённая сразу (фокус вернул Overlay) не ждёт и не
  // уводит фокус при позднем перечитывании списка.
  test('после переименования позднее обновление списка фокус не трогает', async () => {
    const { table, user, state, refetch } = await open({
      districts: [
        district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e17', name: 'Ленинский' }),
        district(),
      ],
    });
    await user.click(within(table).getByRole('button', { name: 'Переименовать: Ленинский' }));
    const dialog = await screen.findByRole('dialog', { name: 'Переименовать район' });
    const field = within(dialog).getByRole('textbox', { name: 'Название' });
    await user.clear(field);
    await user.type(field, 'Юго-Западный');
    await user.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    act(() => (document.activeElement as HTMLElement | null)?.blur());
    state.districts = [
      ...state.districts,
      district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e18', name: 'Октябрьский' }),
    ];
    await refetch();
    await waitFor(() => expect(table).toHaveTextContent('Октябрьский'));
    expect(document.activeElement).toBe(document.body);
  });

  test('возврат из архива: имя занято действующим — тост ошибки, строка в архиве', async () => {
    const { table, user } = await open({
      districts: [district(), { ...ARCHIVED, name: 'Промышленный' }],
    });
    const [, archived] = rows(table);
    await user.click(within(archived!).getByRole('button', { name: 'Вернуть: Промышленный' }));
    expect(await screen.findByText('Район с таким названием уже есть')).toBeInTheDocument();
    expect(rows(table)[1]).toHaveTextContent('в архиве');
  });

  test('переименование — окно, {name}; 409 — у поля окна', async () => {
    const { table, user, sent } = await open({
      districts: [
        district(),
        district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e17', name: 'Ленинский' }),
      ],
    });
    await user.click(within(table).getByRole('button', { name: 'Переименовать: Промышленный' }));
    const dialog = await screen.findByRole('dialog', { name: 'Переименовать район' });
    const field = within(dialog).getByRole('textbox', { name: 'Название' });
    expect(field).toHaveValue('Промышленный');
    await user.clear(field);
    await user.type(field, 'Ленинский');
    await user.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(field).toHaveAccessibleDescription(/Район с таким названием уже есть/),
    );
    await user.clear(field);
    await user.type(field, 'Юго-Западный');
    await user.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(sent.at(-1)?.body).toEqual({ name: 'Юго-Западный' });
    await waitFor(() => expect(table).toHaveTextContent('Юго-Западный'));
    expect(table).not.toHaveTextContent('Промышленный');
  });

  test('пусто — своё пустое и «Добавить район»', async () => {
    cityEditServer({ districts: [] });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoModerator });
    await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
    const card = screen.getByRole('region', { name: 'Районы' });
    expect(await within(card).findByText('Районов нет')).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Добавить район' })).toBeInTheDocument();
  });
});

// «Отменить» в тосте (спека UI-фундамента §4.10, план UF2b.10): «В архив»/«Вернуть» и переименование района —
// useUndoable. Обратное — новым ключом (новое действие, своя запись в журнале), прежнее значение — из переменных
// прямого (имя до переименования), а не из перечитанного списка. У района нет version в GeoDistrictPatch — кода
// *_stale нет («последняя запись побеждает», m11); исход обратного неизвестен — перечитать районы и сказать факт.
describe('«Отменить»', () => {
  const undoButton = () => screen.getByRole('button', { name: 'Отменить' });

  test('«В архив» → «Отменить»: {archived: false} новым ключом, район снова действует', async () => {
    const { table, user, sent } = await open();
    await user.click(within(table).getByRole('button', { name: 'В архив: Промышленный' }));
    await screen.findByText('Район «Промышленный» в архиве');
    await user.click(undoButton());
    expect(await screen.findByText('Отменено')).toBeInTheDocument();
    await waitFor(() => expect(rows(table)[0]).toHaveTextContent('действует'));
    expect(sent.map((s) => s.body)).toEqual([{ archived: true }, { archived: false }]);
    expect(sent[1]?.path).toBe(sent[0]?.path);
    expect(sent[1]?.key).toBeTruthy();
    expect(sent[1]?.key).not.toBe(sent[0]?.key);
  });

  test('«Вернуть» → «Отменить»: {archived: true}, район снова в архиве', async () => {
    const { table, user, sent } = await open({ districts: [ARCHIVED] });
    await user.click(within(table).getByRole('button', { name: 'Вернуть: Старый центр' }));
    await screen.findByText('Район «Старый центр» возвращён');
    await user.click(undoButton());
    expect(await screen.findByText('Отменено')).toBeInTheDocument();
    await waitFor(() => expect(rows(table)[0]).toHaveTextContent('в архиве'));
    expect(sent.map((s) => s.body)).toEqual([{ archived: false }, { archived: true }]);
  });

  test('переименование → «Отменить»: прежнее имя новым ключом', async () => {
    const { table, user, sent } = await open();
    await user.click(within(table).getByRole('button', { name: 'Переименовать: Промышленный' }));
    const dialog = await screen.findByRole('dialog', { name: 'Переименовать район' });
    const field = within(dialog).getByRole('textbox', { name: 'Название' });
    await user.clear(field);
    await user.type(field, 'Юго-Западный');
    await user.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await screen.findByText('Район переименован');
    await waitFor(() => expect(table).toHaveTextContent('Юго-Западный'));
    await user.click(undoButton());
    expect(await screen.findByText('Отменено')).toBeInTheDocument();
    await waitFor(() => expect(table).toHaveTextContent('Промышленный'));
    expect(sent.map((s) => s.body)).toEqual([{ name: 'Юго-Западный' }, { name: 'Промышленный' }]);
    expect(sent[1]?.key).not.toBe(sent[0]?.key);
  });

  test('обратное 409 geo.district_name_taken — тост по коду, район остался в архиве, повтора нет', async () => {
    const { table, user, sent, state } = await open();
    await user.click(within(table).getByRole('button', { name: 'В архив: Промышленный' }));
    await screen.findByText('Район «Промышленный» в архиве');
    // за эти секунды другой сотрудник завёл действующий район с тем же именем
    state.districts = [
      ...state.districts,
      district({ id: 'c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e18', name: 'Промышленный' }),
    ];
    await user.click(undoButton());
    expect(await screen.findByText('Район с таким названием уже есть')).toBeInTheDocument();
    expect(screen.queryByText('Отменено')).toBeNull();
    expect(sent).toHaveLength(2);
    expect(state.districts[0]?.archived_at).not.toBeNull();
  });

  test('обратное оборвалось, сервер его исполнил — перечитать районы: «Отменено», без второго запроса', async () => {
    // ответ потерян, а на сервере обратное прошло: обработчик исполняет его и отвечает 502
    const held: { state?: { districts: ReturnType<typeof district>[] } } = {};
    const srv = await open({
      answer: (op, body) => {
        if (op !== 'patchDistrict' || (body as { archived?: boolean }).archived !== false) return;
        held.state!.districts = held.state!.districts.map((d) => ({ ...d, archived_at: null }));
        return problem(502, 'internal');
      },
    });
    held.state = srv.state;
    const { table, user, sent } = srv;
    await user.click(within(table).getByRole('button', { name: 'В архив: Промышленный' }));
    await screen.findByText('Район «Промышленный» в архиве');
    await user.click(undoButton());
    expect(await screen.findByText('Отменено')).toBeInTheDocument();
    await waitFor(() => expect(rows(table)[0]).toHaveTextContent('действует'));
    expect(sent).toHaveLength(2);
  });

  test('обратное оборвалось и не исполнено — «Не отменено», без второго запроса', async () => {
    const { table, user, sent } = await open({
      answer: (op, body) =>
        op === 'patchDistrict' && (body as { archived?: boolean }).archived === false
          ? problem(502, 'internal')
          : undefined,
    });
    await user.click(within(table).getByRole('button', { name: 'В архив: Промышленный' }));
    await screen.findByText('Район «Промышленный» в архиве');
    await user.click(undoButton());
    expect(await screen.findByText('Не отменено: действие осталось сделанным')).toBeInTheDocument();
    expect(rows(table)[0]).toHaveTextContent('в архиве');
    expect(sent).toHaveLength(2);
  });
});

// Защита от потери (спека UI-фундамента §4.11): окно переименования с изменённым именем — Esc спрашивает
// (Modal.dirty), браузерная «Назад» — ровно один вопрос (useLeaveGuard формы); после «Сохранить» вопроса нет.
describe('переименование: защита от потери', () => {
  const leaveDialogs = () => screen.queryAllByRole('dialog', { name: 'Уйти без сохранения?' });

  async function typed(opts: { history?: 'browser' } = {}) {
    cityEditServer();
    // browser history: «Назад» — только на свою запись (город открыт из списка), записи окна живут весь файл
    const app = renderApp('/geo/cities', { me: meGeoModerator, ...opts });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('link', { name: 'Открыть: Ставрополь' }));
    await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
    const table = await screen.findByRole('table', { name: 'Районы города' });
    await user.click(within(table).getByRole('button', { name: 'Переименовать: Промышленный' }));
    const dialog = await screen.findByRole('dialog', { name: 'Переименовать район' });
    const field = within(dialog).getByRole('textbox', { name: 'Название' });
    await user.type(field, '-2');
    return { app, user, dialog, field };
  }

  test('изменённое имя → Esc — «Уйти без сохранения?»; «Остаться» — ввод на месте', async () => {
    const { user, field } = await typed();
    await user.keyboard('{Escape}');
    const ask = await screen.findByRole('dialog', { name: 'Уйти без сохранения?' });
    await user.click(within(ask).getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(leaveDialogs()).toHaveLength(0));
    expect(screen.getByRole('dialog', { name: 'Переименовать район' })).toBeInTheDocument();
    expect(field).toHaveValue('Промышленный-2');
  });

  test('изменённое имя → браузерная «Назад» — ровно один вопрос, «Остаться» — адрес и ввод на месте', async () => {
    const { app, user, field } = await typed({ history: 'browser' });
    const at = app.at();
    act(() => window.history.back());
    await waitFor(() => expect(leaveDialogs()).toHaveLength(1));
    await user.click(within(leaveDialogs()[0]!).getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(leaveDialogs()).toHaveLength(0));
    expect(app.at()).toBe(at);
    expect(field).toHaveValue('Промышленный-2');
  });
});

describe('добавление района: защита от потери', () => {
  test('набранное имя → «Назад: Города и запуск» — вопрос; после «Добавить» — без вопроса', async () => {
    const { user, card, app } = await open();
    const field = within(card).getByRole('textbox', { name: 'Новый район' });
    await user.type(field, 'Северный');
    await user.click(screen.getByRole('link', { name: 'Назад: Города и запуск' }));
    const ask = await screen.findByRole('dialog', { name: 'Уйти без сохранения?' });
    await user.click(within(ask).getByRole('button', { name: 'Остаться' }));
    expect(field).toHaveValue('Северный');
    await user.click(within(card).getByRole('button', { name: 'Добавить район' }));
    await screen.findByText('Район «Северный» добавлен');
    await user.click(screen.getByRole('link', { name: 'Назад: Города и запуск' }));
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    expect(app.at()).toBe('/geo/cities');
    expect(screen.queryByRole('dialog', { name: 'Уйти без сохранения?' })).toBeNull();
  });
});
