import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
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
