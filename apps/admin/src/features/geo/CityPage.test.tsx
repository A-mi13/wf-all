import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { cityDetails, cityEditServer, geoServer, meGeoAdmin, meGeoModerator } from '../../test/geo';
import { CITY_OTHER, CITY_OWN, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { pick } from '../../test/pick';

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
