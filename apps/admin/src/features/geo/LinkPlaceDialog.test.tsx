import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { cityDetails, cityEditServer, meGeoAdmin } from '../../test/geo';
import { CITY_OWN } from '../../test/me';
import { problem } from '../../test/problem';

// Ручная привязка города к GeoNames (*; geo.cities.manage): у непривязанного города — «Привязать к
// GeoNames»: поиск места (как при активации, фильтр — страна города), выбор, linkGeoCity
// {geoname_id}; 409 geo.city_already_linked / geo.city_already_active, 404 geo.place_not_found —
// тексты в окне.

afterEach(() => sessionStorage.clear());

async function open(opts: Parameters<typeof cityEditServer>[0] = {}) {
  const srv = cityEditServer({ details: cityDetails({ geoname_id: null }), ...opts });
  renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
  await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: 'Привязать к GeoNames' }));
  const dialog = await screen.findByRole('dialog', { name: 'Привязать к GeoNames' });
  return { ...srv, dialog, user };
}

async function choose(dialog: HTMLElement, user: ReturnType<typeof userEvent.setup>) {
  await user.type(within(dialog).getByRole('searchbox', { name: 'Поиск' }), 'м');
  // V1 п. 9: место — строка списка выбора, «Выбрать» — в подвале окна
  await user.click(await within(dialog).findByRole('option', { name: /^Михайловск/ }));
  await user.click(within(dialog).getByRole('button', { name: 'Выбрать Михайловск' }));
}

describe('привязка', () => {
  test('поиск — по стране города; выбор → {geoname_id}; шапка из ответа', async () => {
    const { dialog, user, sent } = await open();
    expect(within(dialog).getByRole('combobox', { name: 'Страна места' })).toHaveValue('RU');
    await choose(dialog, user);
    await user.click(within(dialog).getByRole('button', { name: 'Привязать' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ geoname_id: 526815 });
    expect(sent[0]?.path).toBe(`/v1/geo/cities/${CITY_OWN}/link`);
    expect(sent[0]?.key).toBeTruthy();
    expect(await screen.findByText('Город привязан к GeoNames')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    expect(facts).toHaveTextContent('526815');
    expect(screen.queryByRole('button', { name: 'Привязать к GeoNames' })).toBeNull();
    // ревью G8, m4: открывателя больше нет — фокус на сведениях о городе, не на body
    await waitFor(() => expect(facts).toHaveFocus());
  });

  test.each([
    [409, 'geo.city_already_linked', 'Город уже привязан к месту GeoNames'],
    [409, 'geo.city_already_active', 'На этом месте уже есть город'],
    [404, 'geo.place_not_found', 'Место не найдено в GeoNames или пропало из источника'],
  ])('%i %s — текст в окне, окно открыто', async (status, code, text) => {
    const { dialog, user } = await open({ answer: () => problem(status, code) });
    await choose(dialog, user);
    await user.click(within(dialog).getByRole('button', { name: 'Привязать' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(text);
    expect(screen.getByRole('dialog', { name: 'Привязать к GeoNames' })).toBeInTheDocument();
  });

  // Ревью G8, m2: город уже привязан (сверкой импорта, пока окно было открыто) — карточка перечитана,
  // страница показывает привязку, «Привязать» исчезает (не ходить по кругу до перезагрузки).
  test('409 geo.city_already_linked — карточка перечитана, «Привязать» исчезает', async () => {
    const box: { srv?: ReturnType<typeof cityEditServer> } = {};
    box.srv = cityEditServer({
      details: cityDetails({ geoname_id: null }),
      answer: () => {
        const srv = box.srv;
        if (srv) srv.state.details = { ...srv.state.details, geoname_id: 487846 };
        return problem(409, 'geo.city_already_linked');
      },
    });
    renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Привязать к GeoNames' }));
    const dialog = await screen.findByRole('dialog', { name: 'Привязать к GeoNames' });
    await choose(dialog, user);
    await user.click(within(dialog).getByRole('button', { name: 'Привязать' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Город уже привязан к месту GeoNames',
    );
    const facts = screen.getByRole('list', { name: 'Сведения о городе' });
    await waitFor(() => expect(facts).toHaveTextContent('487846'));
    expect(facts).not.toHaveTextContent('не привязан');
  });

  test('«Другое место» — снова поиск', async () => {
    const { dialog, user } = await open();
    await choose(dialog, user);
    await user.click(within(dialog).getByRole('button', { name: 'Другое место' }));
    expect(within(dialog).getByRole('searchbox', { name: 'Поиск' })).toBeInTheDocument();
  });
});
