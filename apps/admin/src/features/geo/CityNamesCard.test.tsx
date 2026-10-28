import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { cityEditServer, meGeoModerator } from '../../test/geo';
import { CITY_OWN } from '../../test/me';

// Названия города по языкам (*; geo.cities.rename — модератор своего): строка на язык (ru, en);
// язык страны помечен «основное», удалить нельзя; удаление перевода — ConfirmDialog, тело
// names: {"en": null}; сохранение — только изменённые языки.

afterEach(() => sessionStorage.clear());

async function open() {
  const srv = cityEditServer();
  renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoModerator });
  await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
  const card = screen.getByRole('region', { name: 'Названия' });
  return { ...srv, card, user: userEvent.setup() };
}

describe('названия', () => {
  test('язык страны — «основное», без удаления; перевод — с удалением', async () => {
    const { card } = await open();
    const ru = within(card).getByRole('textbox', { name: /Русский/ });
    expect(ru).toHaveAccessibleName(/основное/);
    expect(ru).toHaveValue('Ставрополь');
    expect(within(card).queryByRole('button', { name: 'Удалить перевод: Русский' })).toBeNull();
    expect(
      within(card).getByRole('button', { name: 'Удалить перевод: English' }),
    ).toBeInTheDocument();
  });

  test('удаление перевода — через подтверждение, тело {names: {en: null}}', async () => {
    const { card, user, sent } = await open();
    await user.click(within(card).getByRole('button', { name: 'Удалить перевод: English' }));
    const dialog = await screen.findByRole('dialog', { name: 'Удалить перевод «Stavropol»?' });
    const confirm = within(dialog).getByRole('button', { name: 'Удалить' });
    expect(confirm).toBeDisabled();
    expect(sent).toHaveLength(0);
    await user.type(within(dialog).getByRole('textbox'), 'УДАЛИТЬ');
    await user.click(confirm);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ names: { en: null } });
    expect(await screen.findByText('Перевод удалён')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() =>
      expect(within(card).getByRole('textbox', { name: /English/ })).toHaveValue(''),
    );
    expect(within(card).queryByRole('button', { name: 'Удалить перевод: English' })).toBeNull();
  });

  test('сохранение — только изменённые языки, пробелы по краям сервер обрежет', async () => {
    const { card, user, sent } = await open();
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.type(en, 'Stavropol City');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ names: { en: 'Stavropol City' } });
    expect(await screen.findByText('Названия сохранены')).toBeInTheDocument();
  });

  test('основное название пустым — ошибка поля до запроса', async () => {
    const { card, user, sent } = await open();
    const ru = within(card).getByRole('textbox', { name: /Русский/ });
    await user.clear(ru);
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(ru).toHaveAttribute('aria-invalid', 'true'));
    expect(ru).toHaveAccessibleDescription(/Заполните поле/);
    expect(sent).toHaveLength(0);
  });

  test('ничего не изменено — запроса нет', async () => {
    const { card, user, sent } = await open();
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    expect(sent).toHaveLength(0);
  });
});
