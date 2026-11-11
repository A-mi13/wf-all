import { act, screen, waitFor, within } from '@testing-library/react';
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
  const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoModerator });
  await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
  const card = screen.getByRole('region', { name: 'Названия' });
  return { ...srv, app, card, user: userEvent.setup() };
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
    // ревью G8, m4: кнопки-открывателя больше нет — фокус на поле этого языка, не на body
    await waitFor(() =>
      expect(within(card).getByRole('textbox', { name: /English/ })).toHaveFocus(),
    );
  });

  // Ревью G8, n2: цель фокуса гасится после применения — следующее обновление карточки при фокусе на
  // body не уводит фокус на поле давно удалённого перевода.
  test('после удаления перевода следующее обновление карточки фокус не трогает', async () => {
    const { app, card, user, state } = await open();
    await user.click(within(card).getByRole('button', { name: 'Удалить перевод: English' }));
    const dialog = await screen.findByRole('dialog', { name: 'Удалить перевод «Stavropol»?' });
    await user.type(within(dialog).getByRole('textbox'), 'УДАЛИТЬ');
    await user.click(within(dialog).getByRole('button', { name: 'Удалить' }));
    const en = within(card).getByRole('textbox', { name: /English/ });
    await waitFor(() => expect(en).toHaveFocus());
    act(() => en.blur());
    state.details = {
      ...state.details,
      names: { ...state.details.names, ru: 'Ставрополь-на-Тереке' },
    };
    await act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'card'] }));
    await waitFor(() =>
      expect(within(card).getByRole('textbox', { name: /Русский/ })).toHaveValue(
        'Ставрополь-на-Тереке',
      ),
    );
    expect(document.activeElement).toBe(document.body);
  });

  // Ревью G8, m6: стёртый перевод и «Сохранить» — не удаление: поле возвращается к сохранённому,
  // подсказка «удалить — кнопкой», запроса нет.
  test('стёрли перевод и «Сохранить» — поле вернулось, подсказка про кнопку', async () => {
    const { card, user, sent } = await open();
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(en).toHaveValue('Stavropol'));
    expect(en).toHaveAccessibleDescription(/Удалить перевод — кнопкой «Удалить перевод»/);
    expect(sent).toHaveLength(0);
  });

  // Раунд 1, I1: после ответа форма сброшена к карточке сервера — сохранённое поле не «грязное», и
  // удалённый перевод следующим «Сохранить» не воскресает.
  test('сохранили перевод, удалили, затем «Сохранить» — удалённый не возвращается', async () => {
    const { card, user, sent } = await open();
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.type(en, 'Stavropol City');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    await user.click(await within(card).findByRole('button', { name: 'Удалить перевод: English' }));
    const dialog = await screen.findByRole('dialog', { name: 'Удалить перевод «Stavropol City»?' });
    await user.type(within(dialog).getByRole('textbox'), 'УДАЛИТЬ');
    await user.click(within(dialog).getByRole('button', { name: 'Удалить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(en).toHaveValue(''));
    const ru = within(card).getByRole('textbox', { name: /Русский/ });
    await user.clear(ru);
    await user.type(ru, 'Ставрополь-Кавказский');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(3));
    expect(sent.map((r) => r.body)).toEqual([
      { names: { en: 'Stavropol City' } },
      { names: { en: null } },
      { names: { ru: 'Ставрополь-Кавказский' } },
    ]);
  });

  // Раунд 1, I1: несохранённая правка удаляемого перевода не переживает удаление — иначе следующее
  // «Сохранить» вернуло бы перевод.
  test('правка перевода без сохранения, затем удаление — поле пустое', async () => {
    const { card, user, sent } = await open();
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.type(en, 'Stavropol City');
    await user.click(within(card).getByRole('button', { name: 'Удалить перевод: English' }));
    const dialog = await screen.findByRole('dialog', { name: 'Удалить перевод «Stavropol»?' });
    await user.type(within(dialog).getByRole('textbox'), 'УДАЛИТЬ');
    await user.click(within(dialog).getByRole('button', { name: 'Удалить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(en).toHaveValue(''));
    expect(sent.map((r) => r.body)).toEqual([{ names: { en: null } }]);
  });

  test('сохранили перевод, другая вкладка сменила — поле за сервером', async () => {
    const { app, card, user, sent, state } = await open();
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.type(en, 'Stavropol City');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(await screen.findByText('Названия сохранены')).toBeInTheDocument();
    state.details = { ...state.details, names: { ...state.details.names, en: 'Stavropol Krai' } };
    await act(() => app.queryClient.invalidateQueries({ queryKey: ['geo', 'cities', 'card'] }));
    await waitFor(() => expect(en).toHaveValue('Stavropol Krai'));
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

  // Ревью G8, m7: проверка «запроса нет» сразу после клика прошла бы и раньше запроса. Здесь за
  // пустым «Сохранить» идёт настоящая правка: запросы уходят по порядку, и к ответу на вторую первый
  // уже был бы в sent — после тоста второй правки запрос ровно один.
  test('ничего не изменено — запроса нет', async () => {
    const { card, user, sent } = await open();
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    const en = within(card).getByRole('textbox', { name: /English/ });
    await user.clear(en);
    await user.type(en, 'Stavropol City');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Названия сохранены')).toBeInTheDocument();
    expect(sent.map((r) => r.body)).toEqual([{ names: { en: 'Stavropol City' } }]);
  });
});
