import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { cityDetails, cityEditServer, meGeoAdmin } from '../../test/geo';
import { CITY_OWN } from '../../test/me';

// Адрес города в вебе (*; geo.cities.manage): поле с форматом и подсказкой «старый адрес продолжит
// работать», история (адрес и когда заменён); 409 geo.slug_taken — у поля; формат — проверка до запроса.

afterEach(() => sessionStorage.clear());

async function open(opts: Parameters<typeof cityEditServer>[0] = {}) {
  const srv = cityEditServer(opts);
  renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
  await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
  const card = screen.getByRole('region', { name: 'Адрес в вебе' });
  const field = within(card).getByRole('textbox', { name: 'Адрес (slug)' });
  return { ...srv, card, field, user: userEvent.setup() };
}

describe('адрес', () => {
  test('подсказка и история: адрес не менялся — так и сказано', async () => {
    const { card, field } = await open();
    expect(field).toHaveValue('stavropol');
    expect(field).toHaveAccessibleDescription(/Старый адрес продолжит работать/);
    expect(card).toHaveTextContent('Адрес не менялся');
  });

  test('история из ответа: прежние адреса и когда заменены', async () => {
    const { card } = await open({
      details: cityDetails({
        slug_history: [{ slug: 'stavropol-old', replaced_at: '2026-10-20T08:30:00Z' }],
      }),
    });
    const history = within(card).getByRole('list', { name: 'Прежние адреса' });
    expect(history).toHaveTextContent('stavropol-old');
    expect(history).toHaveTextContent(/заменён .*2026/);
  });

  test('новый адрес — {slug}; прежний уходит в историю', async () => {
    const { card, field, user, sent } = await open();
    await user.clear(field);
    await user.type(field, 'stavropol-city');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ slug: 'stavropol-city' });
    expect(await screen.findByText('Адрес сохранён')).toBeInTheDocument();
    await waitFor(() =>
      expect(within(card).getByRole('list', { name: 'Прежние адреса' })).toHaveTextContent(
        'stavropol',
      ),
    );
    expect(field).toHaveValue('stavropol-city');
  });

  // Ревью G8, m7: тот же адрес — запроса нет (за ним — настоящая правка, см. CityNamesCard.test).
  test('тот же адрес — запроса нет', async () => {
    const { card, field, user, sent } = await open();
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await user.clear(field);
    await user.type(field, 'stavropol-city');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Адрес сохранён')).toBeInTheDocument();
    expect(sent.map((r) => r.body)).toEqual([{ slug: 'stavropol-city' }]);
  });

  test('409 geo.slug_taken — у поля', async () => {
    const { card, field, user } = await open({ taken: ['pyatigorsk'] });
    await user.clear(field);
    await user.type(field, 'pyatigorsk');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(field).toHaveAccessibleDescription(/Этот адрес уже занят другим городом/),
    );
    expect(field).toHaveAttribute('aria-invalid', 'true');
  });

  test.each(['Stavropol', 'ставрополь', '-stav', 'stav--ropol', 'a'.repeat(101)])(
    'формат «%s» — ошибка поля, запроса нет',
    async (bad) => {
      const { card, field, user, sent } = await open();
      await user.clear(field);
      await user.type(field, bad);
      await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
      await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'));
      expect(sent).toHaveLength(0);
    },
  );
});
