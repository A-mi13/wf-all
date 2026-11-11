import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import {
  cityDetails,
  COUNTRIES,
  country,
  geoServer,
  place,
  PLACES,
  REGION_SVERD,
} from '../../test/geo';
import { meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { http, server } from '../../test/server';
import { openOptions, pick, selectedText } from '../../test/pick';

// «Активировать город» (*; спека §9.4 п. 8, geo §5.3): поиск места GeoNames (q ≥ 1 символ, фильтр
// страны) → выбор (заведённое место не выбирается — ссылка на город) → страна (по умолчанию — места),
// регион (по данным GeoNames; при смене страны — пусто, подсказка), статус → activateGeoCity с
// Idempotency-Key. country_code уходит, только если страну переопределили: иначе сервер понял бы
// это как переопределение и снял регион по умолчанию.

type Body = components['schemas']['GeoCityActivateRequest'];

const meActivator: Me = {
  ...meOwner,
  permissions: {
    ...meOwner.permissions,
    'geo.cities.activate': 'allow',
    'geo.places.search': 'allow',
  },
};

const NEW_CITY = '0192f1c4-3333-7b21-9c5d-000000000009';

afterEach(() => sessionStorage.clear());

// Активация: отвечает answer() или карточкой нового города; sent — тела и ключи запросов.
function activateServer(answer?: (n: number) => Response | undefined) {
  const sent: { body: Body; key: string | null }[] = [];
  server.use(
    http.post('/v1/geo/cities', async ({ request, response }) => {
      const body = (await request.json()) as Body;
      sent.push({ body, key: request.headers.get('Idempotency-Key') });
      const own = answer?.(sent.length);
      if (own) return response.untyped(own);
      return response(200).json(
        cityDetails({ id: NEW_CITY, slug: 'mikhaylovsk', name: 'Михайловск', status: 'waitlist' }),
      );
    }),
  );
  return sent;
}

async function openDrawer() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole('button', { name: 'Активировать город' }));
  const drawer = await screen.findByRole('dialog', { name: 'Активировать город' });
  return { user, drawer };
}

// Шаг 1 (V1 п. 9): места — список выбора (listbox), «Выбрать» — в закреплённом подвале панели.
async function choose(user: ReturnType<typeof userEvent.setup>, drawer: HTMLElement, q: string) {
  await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), q);
  await user.click(await within(drawer).findByRole('option', { name: /^Михайловск/ }));
  await user.click(within(drawer).getByRole('button', { name: 'Выбрать Михайловск' }));
}

// Подвал панели — вне прокручиваемого содержимого (Overlay: .body прокручивается, .footer — нет).
const inFooter = (el: HTMLElement) => el.closest('[class*="footer"]') !== null;

describe('поиск места', () => {
  test('пустой поиск — запроса нет, подсказка; набор — места с регионом, страной, населением', async () => {
    const { seen } = geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    expect(within(drawer).getByText('Введите название места')).toBeInTheDocument();
    expect(seen.places).toEqual([]);
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'м');
    const row = await within(drawer).findByRole('option', { name: /^Михайловск/ });
    expect(row).toHaveTextContent('Ставропольский край');
    expect(row).toHaveTextContent('Россия');
    expect(row).toHaveTextContent('56 000');
    expect(seen.places.at(-1)?.get('q')).toBe('м');
  });

  test('уже заведённое место — «уже заведён», ссылка на город, выбрать нельзя', async () => {
    geoServer();
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'с');
    const row = await within(drawer).findByRole('option', { name: /^Ставрополь/ });
    expect(row).toHaveTextContent('уже заведён');
    await user.click(row);
    // выбрано заведённое — в подвале «Открыть город», а не «Выбрать»
    const open = within(drawer).getByRole('button', { name: 'Открыть город Ставрополь' });
    expect(inFooter(open)).toBe(true);
    expect(within(drawer).queryByRole('button', { name: /Выбрать/ })).toBeNull();
    await user.click(open);
    await waitFor(() => expect(app.at()).toMatch(/^\/geo\/cities\/3f2c8e1a/));
  });

  test('список выбора: «Выбрать» в подвале, недоступна до выбора; клик выделяет строку', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    const choose = within(drawer).getByRole('button', { name: 'Выбрать' });
    expect(inFooter(choose)).toBe(true);
    expect(choose).toBeDisabled();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'м');
    const list = await within(drawer).findByRole('listbox', { name: 'Места GeoNames' });
    const option = within(list).getByRole('option', { name: /^Михайловск/ });
    expect(option).toHaveAttribute('aria-selected', 'false');
    await user.click(option);
    expect(option).toHaveAttribute('aria-selected', 'true');
    expect(list).toHaveAttribute('aria-activedescendant', option.id);
    const action = within(drawer).getByRole('button', { name: 'Выбрать Михайловск' });
    expect(inFooter(action)).toBe(true);
    expect(action).toBeEnabled();
  });

  test('с клавиатуры: стрелки двигают выбор, Enter — следующий шаг', async () => {
    geoServer({
      places: [
        ...PLACES,
        place({ geoname_id: 500001, name: 'Сенгилей', ascii_name: 'Sengiley' }),
        place({ geoname_id: 500002, name: 'Светлоград', ascii_name: 'Svetlograd' }),
      ],
    });
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'с');
    const list = await within(drawer).findByRole('listbox', { name: 'Места GeoNames' });
    const options = within(list).getAllByRole('option');
    expect(options.length).toBeGreaterThan(1);
    list.focus();
    await user.keyboard('{ArrowDown}');
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{ArrowDown}');
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    expect(options[0]).toHaveAttribute('aria-selected', 'false');
    await user.keyboard('{Home}');
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{End}');
    expect(options.at(-1)).toHaveAttribute('aria-selected', 'true');
    // последняя — свободное место «Светлоград»: Enter ведёт на шаг 2
    expect(options.at(-1)).toHaveAccessibleName(/^Светлоград/);
    await user.keyboard('{Enter}');
    expect(
      await within(drawer).findByText('Светлоград · Ставропольский край · Россия'),
    ).toBeInTheDocument();
  });

  test('фильтр страны мест — в запрос', async () => {
    const { seen } = geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await pick(user, within(drawer).getByRole('combobox', { name: 'Страна места' }), 'RU');
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'м');
    await waitFor(() => expect(seen.places.at(-1)?.get('country')).toBe('RU'));
  });
});

describe('отправка', () => {
  test('без переопределения: тело {geoname_id, status} — без country_code и region_id; успех — тост и город', async () => {
    geoServer();
    const sent = activateServer();
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    expect(selectedText(within(drawer).getByRole('combobox', { name: 'Страна города' }))).toBe(
      'Россия',
    );
    // пустое значение региона — опция «по данным GeoNames»
    expect(selectedText(within(drawer).getByRole('combobox', { name: 'Регион' }))).toMatch(
      /^По данным GeoNames/,
    );
    expect(selectedText(within(drawer).getByRole('combobox', { name: 'Статус' }))).toBe('ожидание');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    expect(sent).toHaveLength(1);
    expect(sent[0]?.body).toEqual({ geoname_id: 526815, status: 'waitlist' });
    expect(sent[0]?.key).toBeTruthy();
    expect(await screen.findByText('Город «Михайловск» активирован')).toBeInTheDocument();
  });

  test('регион своей страны и статус — в теле; страна не уходит', async () => {
    geoServer();
    const sent = activateServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    const region = within(drawer).getByRole('combobox', { name: 'Регион' });
    await waitFor(async () =>
      expect((await openOptions(user, region)).map((o) => o.label)).toContain(
        'Свердловская область',
      ),
    );
    await pick(user, region, REGION_SVERD);
    await pick(user, within(drawer).getByRole('combobox', { name: 'Статус' }), 'pilot');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ geoname_id: 526815, status: 'pilot', region_id: REGION_SVERD });
  });

  test('страна переопределена: выбранный регион сброшен, подсказка «без региона», country_code в теле', async () => {
    geoServer({
      countries: [
        ...COUNTRIES.filter((c) => c.code !== 'KZ'),
        country({ code: 'KZ', name: 'Казахстан', currency: 'KZT' }),
      ],
    });
    const sent = activateServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    const region = within(drawer).getByRole('combobox', { name: 'Регион' });
    await waitFor(async () =>
      expect((await openOptions(user, region)).map((o) => o.label)).toContain(
        'Свердловская область',
      ),
    );
    await pick(user, region, REGION_SVERD);
    expect(selectedText(region)).toBe('Свердловская область');
    await pick(user, within(drawer).getByRole('combobox', { name: 'Страна города' }), 'KZ');
    expect(selectedText(region)).toBe('Без региона');
    expect((await openOptions(user, region)).map((o) => o.label)).toContain('Без региона');
    await user.keyboard('{Escape}');
    expect(within(drawer).getByText(/Страна изменена/)).toBeInTheDocument();
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ geoname_id: 526815, status: 'waitlist', country_code: 'KZ' });
  });

  test('в списке стран — только включённые (и страна места)', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    const options = (
      await openOptions(user, within(drawer).getByRole('combobox', { name: 'Страна города' }))
    ).map((o) => o.value);
    expect(options).toEqual(['RU']);
  });

  test('409 geo.city_already_active — текст и «Найти город» (поиск в списке)', async () => {
    geoServer();
    activateServer(() => problem(409, 'geo.city_already_active'));
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    expect(await within(drawer).findByText('На этом месте уже есть город')).toBeInTheDocument();
    await user.click(within(drawer).getByRole('button', { name: 'Найти город в списке' }));
    await waitFor(() => expect(searchQ(app.at())).toBe('Михайловск'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test.each([
    ['geo.country_not_enabled', 'Страна города', 'Страны нет в справочнике или она выключена'],
    ['geo.region_not_found', 'Регион', 'Регион не из этой страны'],
  ])('422 %s — у поля «%s»', async (code, field, text) => {
    geoServer();
    activateServer(() => problem(422, code));
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    const control = within(drawer).getByRole('combobox', { name: field });
    await waitFor(() => expect(control).toHaveAccessibleDescription(new RegExp(text)));
    expect(control).toHaveAttribute('aria-invalid', 'true');
  });

  // Ответ пришёл после ухода со страницы (меню): город заведён — тост и кэш, но сотрудника не уводит.
  test('ответ после ухода со страницы — без перехода на город, тост есть', async () => {
    sectionsServer();
    geoServer();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const sent = activateServer();
    server.use(
      http.post('/v1/geo/cities', async ({ response }) => {
        sent.push({ body: { geoname_id: 0 }, key: null });
        await gate;
        return response(200).json(cityDetails({ id: NEW_CITY, name: 'Михайловск' }));
      }),
    );
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    await user.click(screen.getByRole('link', { name: 'Журнал действий' }));
    await waitFor(() => expect(app.at()).toBe('/audit'));
    release();
    expect(await screen.findByText('Город «Михайловск» активирован')).toBeInTheDocument();
    expect(app.at()).toBe('/audit');
  });

  test('обрыв сети — повтор отправки с тем же Idempotency-Key', async () => {
    geoServer();
    const sent = activateServer((n) => (n === 1 ? HttpResponse.error() : undefined));
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    await within(drawer).findByRole('alert');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    expect(sent).toHaveLength(2);
    expect(sent[1]?.key).toBe(sent[0]?.key);
  });
});

const searchQ = (at: string) => new URLSearchParams(at.split('?')[1] ?? '').get('q');
