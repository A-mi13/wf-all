import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { cityDetails, cityEditServer, geoServer, meGeoAdmin, meGeoModerator } from '../../test/geo';
import { CITY_OTHER, CITY_OWN, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { pick, selectedText } from '../../test/pick';

// Страница города (*; спека §9.4 п. 8; план B Task 19): шапка (название, статус, страна с пометкой
// «выключена», регион, население, geoname_id или «не привязан»), карточки по правам в городе: статус
// и адрес — geo.cities.manage, названия — geo.cities.rename (модератор своего), районы —
// geo.districts.manage; поддержка — только чтение. Решает сервер: 403 — текстом, страница цела.

const meSupport: Me = { ...meOwner, role: 'support', permissions: { 'geo.cities.read': 'allow' } };

afterEach(() => sessionStorage.clear());

const title = () => screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
const section = (name: string) => screen.getByRole('region', { name });

describe('шапка', () => {
  test('название, статус, страна, регион, население, GeoNames', async () => {
    cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    expect(facts).toHaveTextContent('Россия (RU)');
    expect(facts).toHaveTextContent('Ставропольский край');
    expect(facts).toHaveTextContent('450 680');
    expect(facts).toHaveTextContent('487846');
    expect(facts).not.toHaveTextContent('выключена');
    const header = screen.getByRole('heading', { level: 1 }).closest('header');
    expect(within(header!).getByText('живой')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Привязать к GeoNames' })).toBeNull();
  });

  test('страна выключена, регион не задан, не привязан — пометки и «Привязать» с правом', async () => {
    cityEditServer({
      details: cityDetails({
        geoname_id: null,
        region: null,
        country: { code: 'KZ', name: 'Казахстан', is_enabled: false },
      }),
    });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    expect(facts).toHaveTextContent('выключена');
    expect(facts).toHaveTextContent('не задан');
    expect(facts).toHaveTextContent('не привязан');
    expect(screen.getByRole('button', { name: 'Привязать к GeoNames' })).toBeInTheDocument();
  });
});

describe('права по карточкам', () => {
  test('админ — статус, адрес, названия и районы с правкой', async () => {
    cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    expect(
      within(section('Статус')).getByRole('combobox', { name: 'Статус города' }),
    ).toBeInTheDocument();
    expect(
      within(section('Адрес в вебе')).getByRole('textbox', { name: 'Адрес (slug)' }),
    ).toBeInTheDocument();
    expect(
      within(section('Названия')).getByRole('textbox', { name: /Русский/ }),
    ).toBeInTheDocument();
    expect(
      await within(section('Районы')).findByRole('button', { name: 'Добавить район' }),
    ).toBeInTheDocument();
  });

  test('модератор своего города — названия и районы, без статуса и адреса', async () => {
    cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoModerator });
    await title();
    expect(screen.queryByRole('region', { name: 'Статус' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Адрес в вебе' })).toBeNull();
    expect(screen.queryByRole('combobox', { name: 'Статус города' })).toBeNull();
    expect(
      within(section('Названия')).getByRole('textbox', { name: /English/ }),
    ).toBeInTheDocument();
    expect(
      await within(section('Районы')).findByRole('button', { name: 'Добавить район' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Привязать к GeoNames' })).toBeNull();
  });

  test('поддержка — только чтение', async () => {
    cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meSupport });
    await title();
    expect(screen.queryByRole('region', { name: 'Статус' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Адрес в вебе' })).toBeNull();
    const names = section('Названия');
    expect(within(names).queryByRole('textbox')).toBeNull();
    expect(names).toHaveTextContent('Stavropol');
    const districts = section('Районы');
    expect(await within(districts).findByText('Промышленный')).toBeInTheDocument();
    expect(within(districts).queryByRole('button')).toBeNull();
  });

  test('модератор чужого города по прямому адресу — сервер 403 → «нет доступа»', async () => {
    geoServer();
    server.use(
      http.get('/v1/geo/cities/{cityId}', ({ response }) =>
        response.untyped(problem(403, 'auth.forbidden')),
      ),
      http.get('/v1/geo/cities/{cityId}/districts', ({ response }) =>
        response.untyped(problem(403, 'auth.forbidden')),
      ),
    );
    renderApp(`/geo/cities/${CITY_OTHER}`, { me: meGeoModerator });
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Нет доступа к разделу' }),
    ).toBeInTheDocument();
  });

  test('города нет — «Город не найден» и путь к списку', async () => {
    cityEditServer();
    const app = renderApp(`/geo/cities/${CITY_OTHER}`, { me: meGeoAdmin });
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Город не найден' }),
    ).toBeInTheDocument();
    // возврат к родителю — общий BackLink каркаса (V1 п. 7)
    await userEvent.setup().click(screen.getByRole('link', { name: 'Назад: Города и запуск' }));
    await waitFor(() => expect(app.at()).toBe('/geo/cities'));
  });
});

// UF2a.10: загрузка — DataState со скелетоном по форме страницы; адрес и geoname_id — CopyButton.
describe('загрузка и копирование', () => {
  test('медленный ответ — скелетон страницы города (шапка, сведения, карточки), затем город', async () => {
    cityEditServer();
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/geo/cities/{cityId}', async ({ response }) => {
        await gate;
        return response(200).json(cityDetails());
      }),
    );
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await waitFor(() => expect(document.querySelector('[data-city-skeleton]')).not.toBeNull());
    const skeleton = document.querySelector('[data-city-skeleton]')!;
    expect(skeleton.closest('[aria-busy="true"]')).not.toBeNull();
    expect(skeleton.querySelectorAll('[data-skeleton="card"]').length).toBeGreaterThan(0);
    expect(skeleton.querySelector('[data-skeleton="text"]')).not.toBeNull();
    release();
    await title();
    expect(document.querySelector('[data-city-skeleton]')).toBeNull();
  });

  test('адрес и GeoNames — «Копировать: …» копирует своё значение', async () => {
    cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const user = userEvent.setup();
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    await user.click(within(facts).getByRole('button', { name: 'Копировать: адрес' }));
    expect(await navigator.clipboard.readText()).toBe(cityDetails().slug);
    await user.click(within(facts).getByRole('button', { name: 'Копировать: GeoNames ID' }));
    expect(await navigator.clipboard.readText()).toBe('487846');
  });

  test('не привязан — копировать GeoNames нечего', async () => {
    cityEditServer({ details: cityDetails({ geoname_id: null }) });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    expect(screen.queryByRole('button', { name: 'Копировать: GeoNames ID' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Копировать: адрес' })).toBeInTheDocument();
  });
});

// Уборка ревью G8 (план UF2a.11).
describe('уборка G8', () => {
  test('m1: cityId не uuid — «Город не найден» без запроса карточки', async () => {
    cityEditServer();
    const asked: string[] = [];
    server.use(
      http.get('/v1/geo/cities/{cityId}', ({ params, response }) => {
        asked.push(params.cityId);
        return response.untyped(problem(400, 'validation.failed'));
      }),
      http.get('/v1/geo/cities/{cityId}/districts', ({ params, response }) => {
        asked.push(params.cityId);
        return response.untyped(problem(400, 'validation.failed'));
      }),
    );
    renderApp('/geo/cities/not-a-uuid', { me: meGeoAdmin });
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Город не найден' }),
    ).toBeInTheDocument();
    expect(asked).toEqual([]);
  });

  test('m13: без geo.places.search — «Привязать к GeoNames» нет (поиск места был бы отказом)', async () => {
    cityEditServer({ details: cityDetails({ geoname_id: null }) });
    const permissions = Object.fromEntries(
      Object.entries(meGeoAdmin.permissions).filter(([k]) => k !== 'geo.places.search'),
    );
    renderApp(`/geo/cities/${CITY_OWN}`, { me: { ...meGeoAdmin, permissions } });
    await title();
    expect(screen.getByRole('region', { name: 'Статус' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Привязать к GeoNames' })).toBeNull();
  });

  test('n1: 409 city_already_linked, окно закрыли Esc — фокус на «Сведения о городе»', async () => {
    const srv = cityEditServer({
      details: cityDetails({ geoname_id: null }),
      answer: () => problem(409, 'geo.city_already_linked'),
    });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Привязать к GeoNames' }));
    const dialog = await screen.findByRole('dialog', { name: 'Привязать к GeoNames' });
    // сверка импорта привязала город, пока окно было открыто
    srv.state.details = { ...srv.state.details, geoname_id: 526815 };
    await user.type(within(dialog).getByRole('searchbox', { name: 'Поиск' }), 'м');
    await user.click(await within(dialog).findByRole('option', { name: /^Михайловск/ }));
    await user.click(within(dialog).getByRole('button', { name: 'Выбрать Михайловск' }));
    await user.click(within(dialog).getByRole('button', { name: 'Привязать' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Город уже привязан к месту GeoNames',
    );
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Привязать к GeoNames' })).toBeNull(),
    );
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() =>
      expect(screen.getByRole('list', { name: 'Сведения о городе' })).toHaveFocus(),
    );
  });
  // Раунд 1, minor 1: Esc без привязки — цели фокуса нет; позже город привязал импорт, карточка перечитана
  // при фокусе на body — фокус не уходит на «Сведения о городе».
  test('окно закрыли без привязки — позднее перечитывание фокус не трогает', async () => {
    const srv = cityEditServer({ details: cityDetails({ geoname_id: null }) });
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Привязать к GeoNames' }));
    await screen.findByRole('dialog', { name: 'Привязать к GeoNames' });
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    act(() => (document.activeElement as HTMLElement | null)?.blur());
    srv.state.details = { ...srv.state.details, geoname_id: 526815 };
    await act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'card'] }));
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    await waitFor(() => expect(facts).toHaveTextContent('526815'));
    expect(document.activeElement).toBe(document.body);
  });
});

// Непредусмотренный отказ №1 ревью G8 (m18): язык — в ключе карточки; смена языка не должна размонтировать
// формы и стирать ввод, а «Сохранить» не должна уйти со значениями прежнего ответа.
describe('смена языка и города', () => {
  test('смена языка — ввод на месте, «Сохранить» недоступна до ответа', async () => {
    cityEditServer();
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/geo/cities/{cityId}', async ({ request, response }) => {
        if (request.headers.get('Accept-Language') !== 'en')
          return response(200).json(cityDetails());
        await gate;
        // пока перечитывали, адрес сменили в другой вкладке: введённое не затирается ответом
        return response(200).json(cityDetails({ slug: 'stavropol-city' }));
      }),
    );
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const user = userEvent.setup();
    const slug = within(section('Адрес в вебе')).getByRole('textbox', { name: 'Адрес (slug)' });
    await user.clear(slug);
    await user.type(slug, 'stavropol-new');
    app.me.current = { ...meGeoAdmin, locale: 'en' };
    await app.refetchMe();
    await screen.findByRole('region', { name: 'Web address' });
    const field = screen.getByRole('textbox', { name: 'Address (slug)' });
    expect(field).toHaveValue('stavropol-new');
    for (const save of screen.getAllByRole('button', { name: 'Save' })) expect(save).toBeDisabled();
    release();
    await waitFor(() => {
      for (const save of screen.getAllByRole('button', { name: 'Save' }))
        expect(save).toBeEnabled();
    });
    expect(screen.getByRole('textbox', { name: 'Address (slug)' })).toHaveValue('stavropol-new');
  });

  test('город A → город B — данных A нет: скелетон, затем B', async () => {
    const other = cityDetails({ id: CITY_OTHER, name: 'Пятигорск', slug: 'pyatigorsk' });
    geoServer({ details: [cityDetails(), other], districts: { [CITY_OWN]: [], [CITY_OTHER]: [] } });
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/geo/cities/{cityId}', async ({ params, response }) => {
        if (params.cityId === CITY_OTHER) {
          await gate;
          return response(200).json(other);
        }
        return response(200).json(cityDetails());
      }),
    );
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    await act(() => app.history.push(`/geo/cities/${CITY_OTHER}`));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${CITY_OTHER}`));
    expect(screen.queryByRole('heading', { level: 1, name: 'Ставрополь' })).toBeNull();
    await waitFor(() => expect(document.querySelector('[data-city-skeleton]')).not.toBeNull());
    release();
    expect(await screen.findByRole('heading', { level: 1, name: 'Пятигорск' })).toBeInTheDocument();
  });
});

describe('статус', () => {
  test('выбор и «Сохранить» — {status}, шапка из ответа', async () => {
    const { sent } = cityEditServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const card = section('Статус');
    const user = userEvent.setup();
    await pick(user, within(card).getByRole('combobox', { name: 'Статус города' }), 'pilot');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ status: 'pilot' });
    expect(sent[0]?.key).toBeTruthy();
    expect(await screen.findByText('Статус сохранён')).toBeInTheDocument();
    const header = screen.getByRole('heading', { level: 1 }).closest('header');
    expect(within(header!).getByText('пилот')).toBeInTheDocument();
  });

  // Ревью G8, m12: список статусов в контракте открытый — незнакомый показан «—» и не выбирается;
  // «Сохранить» с ним — ошибка поля, а не молчание.
  test('незнакомый статус — опция «—» недоступна, «Сохранить» — ошибка поля без запроса', async () => {
    const { sent } = cityEditServer({
      details: cityDetails({ status: 'paused' as 'live' }),
    });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const card = section('Статус');
    const user = userEvent.setup();
    const control = within(card).getByRole('combobox', { name: 'Статус города' });
    expect(selectedText(control)).toBe('—');
    await user.click(control);
    expect(await screen.findByRole('option', { name: '—' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    await user.keyboard('{Escape}');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(control).toHaveAccessibleDescription(/Статус неизвестен этой версии админки/),
    );
    expect(sent).toEqual([]);
  });

  // Раунд 1, I1: после сохранения поле не «залипает» — правка другой вкладки, пришедшая с перечитыванием,
  // видна в поле, и следующее «Сохранить» её не откатывает.
  test('сохранили статус, другая вкладка сменила — поле за сервером', async () => {
    const { state } = cityEditServer();
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const card = section('Статус');
    const user = userEvent.setup();
    const control = within(card).getByRole('combobox', { name: 'Статус города' });
    await pick(user, control, 'pilot');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Статус сохранён')).toBeInTheDocument();
    state.details = { ...state.details, status: 'waitlist', version: state.details.version + 1 };
    await act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'card'] }));
    await waitFor(() => expect(selectedText(control)).toBe('ожидание'));
  });

  test('сохранили адрес, другая вкладка сменила — поле за сервером', async () => {
    const { state } = cityEditServer();
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const user = userEvent.setup();
    const slug = within(section('Адрес в вебе')).getByRole('textbox', { name: 'Адрес (slug)' });
    await user.clear(slug);
    await user.type(slug, 'stavropol-new');
    await user.click(within(section('Адрес в вебе')).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Адрес сохранён')).toBeInTheDocument();
    state.details = {
      ...state.details,
      slug: 'stavropol-city',
      version: state.details.version + 1,
    };
    await act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'card'] }));
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'Адрес (slug)' })).toHaveValue('stavropol-city'),
    );
  });

  test('сервер отказал 403 — текст ошибки, страница цела', async () => {
    cityEditServer({ answer: () => problem(403, 'auth.forbidden') });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await title();
    const card = section('Статус');
    const user = userEvent.setup();
    await pick(user, within(card).getByRole('combobox', { name: 'Статус города' }), 'pilot');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    expect(await within(card).findByRole('alert')).toHaveTextContent(
      'Недостаточно прав для этого действия',
    );
    expect(screen.getByRole('heading', { level: 1, name: 'Ставрополь' })).toBeInTheDocument();
  });
});

// Защита от потери (спека UI-фундамента §4.11, план UF2b.10): ввод в формах города (статус, адрес, названия) —
// уход ссылкой «Назад» или браузерной «Назад» спрашивает «Уйти без сохранения?» ровно один раз; «Остаться» —
// адрес и ввод на месте; сохранённое — не потеря, уход без вопроса.
describe('защита от потери', () => {
  const leaveDialogs = () => screen.queryAllByRole('dialog', { name: 'Уйти без сохранения?' });
  const backLink = () => screen.getByRole('link', { name: 'Назад: Города и запуск' });

  const edits = {
    адрес: async (user: ReturnType<typeof userEvent.setup>) => {
      const slug = within(section('Адрес в вебе')).getByRole('textbox', { name: 'Адрес (slug)' });
      await user.type(slug, '-new');
      return {
        card: section('Адрес в вебе'),
        kept: () => expect(slug).toHaveValue('stavropol-new'),
      };
    },
    названия: async (user: ReturnType<typeof userEvent.setup>) => {
      const en = within(section('Названия')).getByRole('textbox', { name: /English/ });
      await user.clear(en);
      await user.type(en, 'Stavropol City');
      return { card: section('Названия'), kept: () => expect(en).toHaveValue('Stavropol City') };
    },
    статус: async (user: ReturnType<typeof userEvent.setup>) => {
      const box = within(section('Статус')).getByRole('combobox', { name: 'Статус города' });
      await pick(user, box, 'pilot');
      return { card: section('Статус'), kept: () => expect(selectedText(box)).toBe('пилот') };
    },
  };

  test.each(Object.keys(edits) as (keyof typeof edits)[])(
    '%s: ввод → ссылка «Назад» — один вопрос, «Остаться» — ввод на месте; после «Сохранить» — без вопроса',
    async (name) => {
      cityEditServer();
      const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
      await title();
      const user = userEvent.setup();
      const { card, kept } = await edits[name](user);
      const at = app.at();
      await user.click(backLink());
      await waitFor(() => expect(leaveDialogs()).toHaveLength(1));
      await user.click(within(leaveDialogs()[0]!).getByRole('button', { name: 'Остаться' }));
      await waitFor(() => expect(leaveDialogs()).toHaveLength(0));
      expect(app.at()).toBe(at);
      kept();
      await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
      await waitFor(() =>
        expect(within(card).getByRole('button', { name: 'Сохранить' })).not.toHaveAttribute(
          'aria-busy',
          'true',
        ),
      );
      await screen.findByText(/сохран[её]н/);
      await user.click(backLink());
      await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
      expect(leaveDialogs()).toHaveLength(0);
    },
  );

  test('ввод в адресе → браузерная «Назад» — ровно один вопрос; «Уйти» — список', async () => {
    cityEditServer();
    const app = renderApp('/geo/cities', { me: meGeoAdmin, history: 'browser' });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('link', { name: 'Открыть: Ставрополь' }));
    await title();
    await edits.адрес(user);
    await app.back();
    await waitFor(() => expect(leaveDialogs()).toHaveLength(1));
    await user.click(within(leaveDialogs()[0]!).getByRole('button', { name: 'Уйти' }));
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    await waitFor(() => expect(leaveDialogs()).toHaveLength(0));
    expect(app.at()).toBe('/geo/cities');
  });
});
