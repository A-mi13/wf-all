import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { GEO_CITIES, geoServer, REGION_STAV } from '../../test/geo';
import { CITY_OWN, meModerator, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { city } from '../../test/staff';

// «Города и запуск» (спека §9.4 п. 8, макет 07): карточки городов — название, чип статуса, регион,
// население, районы, «страна выключена»; фильтры — search-параметры (q, status[], country,
// region_id; replace); «Показать ещё», «Показано N», «Сбросить фильтры»; пусто, ошибка, скелетон;
// «Активировать город» — только с geo.cities.activate.

const meActivator: Me = {
  ...meOwner,
  permissions: {
    ...meOwner.permissions,
    'geo.cities.activate': 'allow',
    'geo.places.search': 'allow',
  },
};

afterEach(() => sessionStorage.clear());

const page = () => screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
const list = () => screen.findByRole('list', { name: 'Города' });
const cards = async () => within(await list()).getAllByRole('article');
const card = (name: string) => screen.findByRole('article', { name });
const searchOf = (at: string) => new URLSearchParams(at.split('?')[1] ?? '');

describe('карточки', () => {
  test('название, чип статуса, регион, население, районы; страна выключена — пометка', async () => {
    geoServer({
      cities: [
        ...GEO_CITIES,
        city({
          id: '0192f1c4-2222-7b21-9c5d-000000000003',
          slug: 'almaty',
          name: 'Алматы',
          status: 'waitlist',
          country_code: 'KZ',
          region_name: null,
          region_id: null,
          country_enabled: false,
          population: 2000000,
        }),
      ],
    });
    renderApp('/geo/cities', { me: meOwner });
    await page();
    expect((await cards()).map((c) => within(c).getByRole('heading').textContent)).toEqual([
      'Ставрополь',
      'Пятигорск',
      'Алматы',
    ]);
    const stav = await card('Ставрополь');
    expect(within(stav).getByText('живой')).toBeInTheDocument();
    expect(stav).toHaveTextContent('Ставропольский край');
    expect(stav).toHaveTextContent('450 680');
    expect(stav).toHaveTextContent('4района');
    expect(stav).not.toHaveTextContent('страна выключена');
    expect(within(await card('Пятигорск')).getByText('пилот')).toBeInTheDocument();
    const almaty = await card('Алматы');
    expect(within(almaty).getByText('ожидание')).toBeInTheDocument();
    expect(almaty).toHaveTextContent('страна выключена');
    expect(almaty).toHaveTextContent('регион не задан');
    expect(screen.getByText('Показано 3')).toBeInTheDocument();
  });

  test('«Открыть» — страница города', async () => {
    geoServer();
    const app = renderApp('/geo/cities', { me: meOwner });
    const stav = await card('Ставрополь');
    await userEvent.setup().click(within(stav).getByRole('button', { name: 'Открыть' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${CITY_OWN}`));
  });

  // V1 п. 7: у вложенного экрана — возврат к родителю общим BackLink, фильтры родителя — из адреса.
  test('город из отфильтрованного списка → «Назад: Города и запуск» — список с теми же фильтрами', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meOwner });
    const user = userEvent.setup();
    await user.click(within(await card('Ставрополь')).getByRole('button', { name: 'Открыть' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${CITY_OWN}`));
    const back = await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    expect(back).toHaveAttribute('href', '/geo/cities?country=RU');
    await user.click(back);
    await page();
    expect(app.at()).toBe('/geo/cities?country=RU');
    expect(await card('Ставрополь')).toBeInTheDocument();
  });

  test('браузерная «Назад» со страницы города — список с фильтрами', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meOwner });
    await userEvent
      .setup()
      .click(within(await card('Ставрополь')).getByRole('button', { name: 'Открыть' }));
    await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    app.back();
    await page();
    expect(app.at()).toBe('/geo/cities?country=RU');
  });

  test('город по прямой ссылке — возврат на список без фильтров', async () => {
    geoServer();
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meOwner });
    expect(await screen.findByRole('link', { name: 'Назад: Города и запуск' })).toHaveAttribute(
      'href',
      '/geo/cities',
    );
  });
});

describe('фильтры', () => {
  test('чип статуса — в адрес (replace) и в запрос повторённым параметром', async () => {
    const { seen } = geoServer();
    const app = renderApp('/geo/cities', { me: meOwner });
    await cards();
    expect(seen.cities.at(-1)?.getAll('status')).toEqual([]);
    await userEvent.setup().click(
      within(screen.getByRole('group', { name: 'Статус' })).getByRole('button', {
        name: 'Пилот',
      }),
    );
    await waitFor(() => expect(seen.cities.at(-1)?.getAll('status')).toEqual(['pilot']));
    expect(JSON.parse(searchOf(app.at()).get('status') ?? 'null')).toEqual(['pilot']);
    expect((await cards()).map((c) => within(c).getByRole('heading').textContent)).toEqual([
      'Пятигорск',
    ]);
    expect(screen.getByRole('button', { name: 'Пилот' })).toHaveAttribute('aria-pressed', 'true');
  });

  test('из адреса: статусы, страна, регион, поиск — в запрос; мусор отброшен', async () => {
    const { seen } = geoServer();
    const status = encodeURIComponent(JSON.stringify(['pilot', 'live', 'archived']));
    renderApp(`/geo/cities?status=${status}&country=RU&region_id=${REGION_STAV}&q=%D0%BF`, {
      me: meOwner,
    });
    await page();
    await waitFor(() => expect(seen.cities.length).toBeGreaterThan(0));
    const q = seen.cities.at(-1);
    expect(q?.getAll('status')).toEqual(['pilot', 'live']);
    expect(q?.get('country')).toBe('RU');
    expect(q?.get('region_id')).toBe(REGION_STAV);
    expect(q?.get('q')).toBe('п');
    expect(screen.getByRole('searchbox', { name: 'Поиск' })).toHaveValue('п');
  });

  test('мусор в адресе — в запрос не уходит', async () => {
    const { seen } = geoServer();
    renderApp('/geo/cities?country=ru&region_id=x&status=%22gone%22', { me: meOwner });
    await cards();
    const q = seen.cities.at(-1);
    expect(q?.has('country')).toBe(false);
    expect(q?.has('region_id')).toBe(false);
    expect(q?.has('status')).toBe(false);
  });

  test('страна — в «Ещё фильтры», выбор — в адрес; «Сбросить фильтры» — всё сначала', async () => {
    const { seen } = geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meOwner });
    await cards();
    const more = screen.getByRole('button', { name: 'Ещё фильтры, задано: 1' });
    const user = userEvent.setup();
    await user.click(more);
    const country = screen.getByRole('combobox', { name: 'Страна' });
    expect(country).toHaveValue('RU');
    await user.selectOptions(country, 'KZ');
    await waitFor(() => expect(seen.cities.at(-1)?.get('country')).toBe('KZ'));
    expect(searchOf(app.at()).get('country')).toBe('KZ');
    await user.click(screen.getAllByRole('button', { name: 'Сбросить фильтры' })[0]!);
    await waitFor(() => expect(app.at()).toBe('/geo/cities'));
    await waitFor(() => expect(seen.cities.at(-1)?.has('country')).toBe(false));
  });

  test('пусто с фильтрами — «Ничего не нашлось» и сброс', async () => {
    geoServer();
    renderApp('/geo/cities?q=%D1%8F', { me: meOwner });
    await page();
    expect(await screen.findByText(/Ничего не нашлось/)).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Сбросить фильтры' }).length).toBeGreaterThan(0);
  });
});

describe('состояния', () => {
  test('пусто без фильтров — своё пустое', async () => {
    geoServer({ cities: [] });
    renderApp('/geo/cities', { me: meOwner });
    expect(await screen.findByText('Городов пока нет')).toBeInTheDocument();
  });

  test('ошибка — код и «Повторить», повтор загружает', async () => {
    geoServer();
    let fail = true;
    server.use(
      http.get('/v1/geo/cities', ({ response }) =>
        fail
          ? response.untyped(problem(503, 'internal'))
          : response(200).json({ items: [city()], next_cursor: null }),
      ),
    );
    renderApp('/geo/cities', { me: meOwner });
    const retry = await screen.findByRole('button', { name: 'Повторить' });
    expect(screen.getByText(/503/)).toBeInTheDocument();
    fail = false;
    await userEvent.setup().click(retry);
    expect(await card('Ставрополь')).toBeInTheDocument();
  });

  test('«Показать ещё» — следующая страница по курсору', async () => {
    const { seen } = geoServer({ pageSize: 1 });
    renderApp('/geo/cities', { me: meOwner });
    expect(await cards()).toHaveLength(1);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Показать ещё' }));
    await waitFor(async () => expect(await cards()).toHaveLength(2));
    expect(seen.cities.at(-1)?.get('cursor')).toBe('1');
  });
});

describe('права', () => {
  test('с geo.cities.activate — «Активировать город»', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    await page();
    expect(screen.getByRole('button', { name: 'Активировать город' })).toBeInTheDocument();
  });

  test('модератор — без «Активировать город», город — свой (сужает сервер)', async () => {
    geoServer({ cities: [city()] });
    renderApp('/geo/cities', { me: meModerator });
    await card('Ставрополь');
    expect(screen.queryByRole('button', { name: 'Активировать город' })).toBeNull();
    expect(screen.queryByRole('article', { name: 'Пятигорск' })).toBeNull();
  });
});
