import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test, vi } from 'vitest';
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

  // Финальное ревью m1: поиск из одной пунктуации сервер отверг бы (400 query.q) — запроса нет, «Ничего не нашлось».
  test('поиск из одной пунктуации — «Ничего не нашлось», запроса нет', async () => {
    const { seen } = geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), '(');
    expect(await within(drawer).findByText(/Ничего не нашлось/)).toBeInTheDocument();
    expect(within(drawer).queryByRole('alert')).toBeNull();
    expect(seen.places).toEqual([]);
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

// Уборка ревью V1 и G8 (план UF2a.11).
describe('уборка V1 и G8', () => {
  // V1 m1: выбор держится, только пока его строка в результатах — иначе «Выбрать» ушла бы с местом,
  // которого на экране уже нет.
  test('V1 m1: выбрали место → страна мест KZ — выбор снят, «Выбрать» недоступна', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'м');
    await user.click(await within(drawer).findByRole('option', { name: /^Михайловск/ }));
    expect(within(drawer).getByRole('button', { name: 'Выбрать Михайловск' })).toBeEnabled();
    await pick(user, within(drawer).getByRole('combobox', { name: 'Страна места' }), 'KZ');
    await waitFor(() =>
      expect(within(drawer).getByRole('button', { name: 'Выбрать' })).toBeDisabled(),
    );
  });

  test('V1 m1: выбрали место → другой поиск без этой строки — выбор снят', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    const search = within(drawer).getByRole('searchbox', { name: 'Поиск' });
    await user.type(search, 'м');
    await user.click(await within(drawer).findByRole('option', { name: /^Михайловск/ }));
    await user.clear(search);
    await user.type(search, 'ст');
    await within(drawer).findByRole('option', { name: /^Ставрополь/ });
    await waitFor(() =>
      expect(within(drawer).getByRole('button', { name: 'Выбрать' })).toBeDisabled(),
    );
  });

  test('V1 m1, m2: «Другое место» — выбор снят, поиск прежний, фокус на списке', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Другое место' }));
    const list = await within(drawer).findByRole('listbox', { name: 'Места GeoNames' });
    expect(within(drawer).getByRole('searchbox', { name: 'Поиск' })).toHaveValue('м');
    expect(within(drawer).getByRole('button', { name: 'Выбрать' })).toBeDisabled();
    await waitFor(() => expect(list).toHaveFocus());
  });

  // V1 m2: шаг сменился — фокус на первое поле шага 2, а не на body (список шага 1 размонтирован).
  test('V1 m2: ↓, Enter — фокус на «Страна города», не на body', async () => {
    geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'м');
    const list = await within(drawer).findByRole('listbox', { name: 'Места GeoNames' });
    list.focus();
    await user.keyboard('{End}{Enter}');
    const country = await within(drawer).findByRole('combobox', { name: 'Страна города' });
    await waitFor(() => expect(country).toHaveFocus());
  });

  // G8 m5: контракт listGeoPlaces — q не длиннее 100.
  test('G8 m5: поиск длиннее 100 — в запрос уходят первые 100', async () => {
    const { seen } = geoServer();
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await user.click(within(drawer).getByRole('searchbox', { name: 'Поиск' }));
    await user.paste('м'.repeat(150));
    await waitFor(() => expect(seen.places.at(-1)?.get('q')).toHaveLength(100));
    expect(seen.places.every((q) => (q.get('q') ?? '').length <= 100)).toBe(true);
  });

  // G8 m3: город на этом месте уже есть — места перечитаны (метка «уже заведён»), переход — на город
  // по city_id перечитанного места.
  test('G8 m3: 409 city_already_active — места перечитаны, «Открыть город» по city_id', async () => {
    const { state } = geoServer();
    activateServer(() => {
      state.places = state.places.map((p) =>
        p.geoname_id === 526815 ? { ...p, city_id: NEW_CITY } : p,
      );
      return problem(409, 'geo.city_already_active');
    });
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    const open = await within(drawer).findByRole('button', { name: 'Открыть город Михайловск' });
    await user.click(open);
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
  });

  // Раунд 1, minor 3: пока место перечитывается после 409, «Найти город в списке» занята — быстрый клик не
  // уводит в список, когда сейчас появится «Открыть город».
  test('G8 m3: после 409, пока место перечитывается, «Найти город в списке» занята', async () => {
    geoServer();
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/geo/places', async ({ request }) => {
        if (new URL(request.url).searchParams.get('q') === 'Михайловск') await gate;
        return undefined;
      }),
    );
    activateServer(() => problem(409, 'geo.city_already_active'));
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    const find = await within(drawer).findByRole('button', { name: 'Найти город в списке' });
    expect(find).toHaveAttribute('aria-busy', 'true');
    await user.click(find);
    // панель в адресе (UF2b.8): занятая кнопка не увела — адрес списка с открытой активацией
    expect(app.at()).toBe('/geo/cities?activate=1');
    release();
    await waitFor(() => expect(find).not.toHaveAttribute('aria-busy'));
  });

  test('G8 m3: после 409 «Другое место» — место с меткой «уже заведён»', async () => {
    const { state } = geoServer();
    activateServer(() => {
      state.places = state.places.map((p) =>
        p.geoname_id === 526815 ? { ...p, city_id: NEW_CITY } : p,
      );
      return problem(409, 'geo.city_already_active');
    });
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await within(drawer).findByText('На этом месте уже есть город');
    // результаты шага 1 устарели (без этого их показал бы кэш до перечитывания при возврате)
    const step1 = app.queryClient
      .getQueryCache()
      .find({ queryKey: ['geo', 'places', 'ru', { q: 'м' }], exact: true });
    expect(step1).toBeDefined();
    await waitFor(() => expect(step1?.state.isInvalidated).toBe(true));
    await user.click(within(drawer).getByRole('button', { name: 'Другое место' }));
    const row = await within(drawer).findByRole('option', { name: /^Михайловск/ });
    await waitFor(() => expect(row).toHaveTextContent('уже заведён'));
  });

  test('G8 m3: успешная активация — места GeoNames помечены устаревшими', async () => {
    geoServer();
    activateServer();
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    const places = app.queryClient.getQueryCache().findAll({ queryKey: ['geo', 'places'] });
    expect(places.length).toBeGreaterThan(0);
    expect(places.every((q) => q.state.isInvalidated)).toBe(true);
  });

  // G9-C, I-1: open_cities страны считает сервер по городам — после активации список стран устарел.
  test('I-1: успешная активация — список стран помечен устаревшим', async () => {
    geoServer();
    activateServer();
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    const countries = () =>
      app.queryClient.getQueryCache().find({ queryKey: ['geo', 'countries', 'list', 'ru'] });
    // шаг 2 спросил страны (поле «Страна города»)
    await waitFor(() => expect(countries()?.state.data).toBeDefined());
    const updates = countries()?.state.dataUpdateCount ?? 0;
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    // наблюдатель был (поле страны) — перечитан; уже нет — помечен устаревшим
    const q = countries();
    expect(q?.state.isInvalidated || (q?.state.dataUpdateCount ?? 0) > updates).toBe(true);
  });

  // G8 m14: исход неизвестен (обрыв), панель закрыли — город мог появиться: списки перечитаны.
  test('G8 m14: исход неизвестен, панель закрыта — списки городов перечитаны', async () => {
    const { seen } = geoServer();
    activateServer(() => HttpResponse.error());
    renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await within(drawer).findByRole('alert');
    const before = seen.cities.length;
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(seen.cities.length).toBeGreaterThan(before));
  });
});

// Ревью UF2b/II-B, m6 (G8 m14 на путях ухода из панели): исход прошлой отправки неизвестен, затем 409 — «Открыть
// город» уводит со списка, панель не закрывается сама (replace), но списки городов всё равно устарели.
describe('G8 m14 при уходе из панели', () => {
  test('обрыв → 409 → «Открыть город»: списки городов помечены устаревшими', async () => {
    const { state } = geoServer();
    activateServer((n) => {
      if (n === 1) return HttpResponse.error();
      state.places = state.places.map((p) =>
        p.geoname_id === 526815 ? { ...p, city_id: NEW_CITY } : p,
      );
      return problem(409, 'geo.city_already_active');
    });
    const app = renderApp('/geo/cities', { me: meActivator });
    const { user, drawer } = await openDrawer();
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await within(drawer).findByRole('alert');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    const open = await within(drawer).findByRole('button', { name: 'Открыть город Михайловск' });
    // списки помечены устаревшими именно уходом (invalidateCityLists: CITIES_KEY без карточек и районов)
    const invalidate = vi.spyOn(app.queryClient, 'invalidateQueries');
    await user.click(open);
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    const lists = invalidate.mock.calls.filter(
      ([f]) => JSON.stringify(f?.queryKey) === JSON.stringify(['geo', 'cities']),
    );
    expect(lists).toHaveLength(1);
  });
});

const searchQ = (at: string) => new URLSearchParams(at.split('?')[1] ?? '').get('q');

// Панель в адресе (спека UI-фундамента §4.6, план UF2b.8): ?activate=1; успех — уход на страницу города с replace
// (leavePanelTo): ни BackLink, ни браузерная «Назад» не возвращают на список с открытой активацией. Browser history
// (R3-1): записи окна живут весь файл, тест не уходит «Назад» дальше своих push.
describe('активация в адресе', () => {
  async function activateFromList() {
    geoServer();
    activateServer();
    const app = renderApp('/geo/cities?country=RU', { me: meActivator, history: 'browser' });
    const { user, drawer } = await openDrawer();
    expect(window.location.search).toBe('?country=RU&activate=1');
    await choose(user, drawer, 'м');
    await user.click(within(drawer).getByRole('button', { name: 'Активировать' }));
    await waitFor(() => expect(app.at()).toBe(`/geo/cities/${NEW_CITY}`));
    return { app, user };
  }

  test('активация → город → BackLink — список без открытой активации', async () => {
    const { app, user } = await activateFromList();
    const back = await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    expect(back).toHaveAttribute('href', '/geo/cities?country=RU');
    await user.click(back);
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    expect(app.at()).toBe('/geo/cities?country=RU');
    expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull();
  });

  // Ревью UF2b/II-B раунд 1 (рядом с m6): «Открыть город» шага 1 закрывал панель (history.back) и тут же делал push —
  // две навигации наперегонки. Теперь — уход из панели replace, как у успеха активации.
  test('шаг 1: заведённое место → «Открыть город» → «Назад» — список без активации', async () => {
    geoServer();
    const app = renderApp('/geo/cities', { me: meActivator, history: 'browser' });
    const { user, drawer } = await openDrawer();
    await user.type(within(drawer).getByRole('searchbox', { name: 'Поиск' }), 'с');
    await user.click(await within(drawer).findByRole('option', { name: /^Ставрополь/ }));
    await user.click(within(drawer).getByRole('button', { name: 'Открыть город Ставрополь' }));
    await waitFor(() => expect(app.at()).toMatch(/^\/geo\/cities\/3f2c8e1a/));
    await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    await app.back();
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    expect(app.at()).toBe('/geo/cities');
    expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull();
  });

  test('активация → город → браузерная «Назад» — список без открытой активации', async () => {
    const { app } = await activateFromList();
    await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    await app.back();
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    expect(app.at()).toBe('/geo/cities?country=RU');
    expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull();
  });

  // F5 на открытой панели (глубокая ссылка, метки state.panel нет): панель открыта; «Закрыть» — replace без ключа,
  // фильтры на месте, из приложения не уводит (запись истории не добавлена).
  test('F5 на ?activate=1 — панель открыта; «Закрыть» — адрес без activate, фильтры на месте', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU&activate=1', {
      me: meActivator,
      history: 'browser',
    });
    const drawer = await screen.findByRole('dialog', { name: 'Активировать город' });
    const length = window.history.length;
    await userEvent.setup().click(within(drawer).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull(),
    );
    expect(app.at()).toBe('/geo/cities?country=RU');
    expect(window.history.length).toBe(length);
  });

  test('открыта из списка → браузерная «Назад» — панель закрыта, фильтры на месте', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meActivator, history: 'browser' });
    await openDrawer();
    expect(window.location.search).toBe('?country=RU&activate=1');
    await app.back();
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull(),
    );
    expect(app.at()).toBe('/geo/cities?country=RU');
  });

  test('?activate=1 без права активации — ключ снят, панели нет', async () => {
    geoServer();
    const app = renderApp('/geo/cities?activate=1', { me: meNoActivate });
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    await waitFor(() => expect(app.at()).toBe('/geo/cities'));
    expect(screen.queryByRole('dialog', { name: 'Активировать город' })).toBeNull();
  });
});

const meNoActivate: Me = {
  ...meOwner,
  permissions: Object.fromEntries(
    Object.entries(meActivator.permissions).filter(([k]) => k !== 'geo.cities.activate'),
  ),
};
