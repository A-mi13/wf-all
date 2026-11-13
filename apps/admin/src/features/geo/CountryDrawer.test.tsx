import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import type { Me } from '../../app/permissions';
import { renderApp } from '../../test/app';
import { COUNTRIES, countryEditServer, meDirectoriesAdmin } from '../../test/geo';
import { pick, selectedText } from '../../test/pick';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { geoKeys } from './queries';

type GeoCountryList = components['schemas']['GeoCountryList'];

// Панель страны (план B, Task 21): создание — тело со всеми правилами и default_locale (всегда 202); правка — только
// изменённые поля, без default_locale (R12); правила (валюта, возрасты) — 202, кэш не меняется; прочие поля — 200;
// 409 geo.country_exists — у кода; поля правил помечены «вдвоём» у любой страны.

afterEach(() => sessionStorage.clear());

type User = ReturnType<typeof userEvent.setup>;

async function openEdit(code = 'RU', name = 'Россия', me: Me = meDirectoriesAdmin) {
  const server = countryEditServer();
  const app = renderApp(`/geo/countries?edit=${code}`, { me });
  const drawer = await screen.findByRole('dialog', { name: `${name} (${code})` });
  return { ...server, app, drawer, user: userEvent.setup() };
}

const field = (drawer: HTMLElement, name: string | RegExp) =>
  within(drawer).getByRole('textbox', { name });

async function retype(user: User, el: HTMLElement, value: string) {
  await user.clear(el);
  await user.type(el, value);
}

describe('создание', () => {
  test('тело — все правила, названия и default_locale; 202 → уведомление, ссылка на «Подтверждения»', async () => {
    const { sent } = countryEditServer();
    const app = renderApp('/geo/countries?edit=new', { me: meDirectoriesAdmin });
    const drawer = await screen.findByRole('dialog', { name: 'Новая страна' });
    const user = userEvent.setup();
    await user.type(field(drawer, /Код страны/), 'AM');
    await user.type(field(drawer, 'Название на русском'), 'Армения');
    await user.type(field(drawer, 'Название на английском'), 'Armenia');
    await pick(user, within(drawer).getByRole('combobox', { name: /Валюта/ }), 'AMD');
    await pick(user, within(drawer).getByRole('combobox', { name: 'Язык страны' }), 'English');
    await user.type(field(drawer, /Телефонный код/), '+374');
    await pick(
      user,
      within(drawer).getByRole('combobox', { name: 'Первый день недели' }),
      'воскресенье',
    );
    await user.type(field(drawer, /Минимальный возраст/), '16');
    await user.type(field(drawer, /совершеннолетия/), '18');
    await user.click(within(drawer).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(drawer).findByText('Отправлено на подтверждение');
    expect(sent).toHaveLength(1);
    expect(sent[0]?.body).toEqual({
      code: 'AM',
      currency: 'AMD',
      default_locale: 'en',
      phone_prefix: '+374',
      week_starts_on: 7,
      min_signup_age: 16,
      age_of_majority: 18,
      names: { ru: 'Армения', en: 'Armenia' },
      is_enabled: false,
    });
    await user.click(within(drawer).getByRole('button', { name: 'Подтверждения' }));
    await waitFor(() => expect(app.at()).toBe('/approvals?box=mine'));
  });

  // Ревью G9-C, m-6: autoCapitalize на компьютере не работает — код приводится к заглавным, а не «Неверный формат».
  test('код строчными — уходит заглавными', async () => {
    const { sent } = countryEditServer();
    renderApp('/geo/countries?edit=new', { me: meDirectoriesAdmin });
    const drawer = await screen.findByRole('dialog', { name: 'Новая страна' });
    const user = userEvent.setup();
    await user.type(field(drawer, /Код страны/), 'am');
    await user.type(field(drawer, 'Название на русском'), 'Армения');
    await pick(user, within(drawer).getByRole('combobox', { name: /Валюта/ }), 'AMD');
    await user.type(field(drawer, /Телефонный код/), '+374');
    await user.type(field(drawer, /Минимальный возраст/), '16');
    await user.type(field(drawer, /совершеннолетия/), '18');
    await user.click(within(drawer).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(drawer).findByText('Отправлено на подтверждение');
    expect(sent[0]?.body).toMatchObject({ code: 'AM' });
  });

  test('409 geo.country_exists — у поля кода; совершеннолетие ниже регистрации — до запроса', async () => {
    const { sent } = countryEditServer();
    renderApp('/geo/countries?edit=new', { me: meDirectoriesAdmin });
    const drawer = await screen.findByRole('dialog', { name: 'Новая страна' });
    const user = userEvent.setup();
    await user.type(field(drawer, /Код страны/), 'KZ');
    await user.type(field(drawer, 'Название на русском'), 'Казахстан');
    await pick(user, within(drawer).getByRole('combobox', { name: /Валюта/ }), 'KZT');
    await user.type(field(drawer, /Телефонный код/), '+7');
    await user.type(field(drawer, /Минимальный возраст/), '18');
    await user.type(field(drawer, /совершеннолетия/), '16');
    const submit = within(drawer).getByRole('button', { name: 'Отправить на подтверждение' });
    await user.click(submit);
    await waitFor(() =>
      expect(field(drawer, /совершеннолетия/)).toHaveAttribute('aria-invalid', 'true'),
    );
    expect(sent).toHaveLength(0);
    await retype(user, field(drawer, /совершеннолетия/), '18');
    await user.click(submit);
    await waitFor(() =>
      expect(field(drawer, /Код страны/)).toHaveAccessibleDescription(/Такая страна уже есть/),
    );
    expect(sent).toHaveLength(1);
  });
});

describe('правка', () => {
  test('тело — только изменённые поля, без default_locale и code; 200 → таблица с новым значением', async () => {
    const { sent, drawer, user } = await openEdit();
    expect(within(drawer).queryByRole('combobox', { name: 'Язык страны' })).toBeNull();
    expect(drawer).toHaveTextContent('Язык страны: Русский');
    await retype(user, field(drawer, /Телефонный код/), '+8');
    await user.click(within(drawer).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({ op: 'patch', path: '/v1/geo/countries/RU' });
    expect(sent[0]?.body).toEqual({ phone_prefix: '+8' });
    const table = await screen.findByRole('table', { name: 'Страны' });
    await waitFor(() => expect(table).toHaveTextContent('+8'));
    expect(await screen.findByText('Страна «Россия» сохранена')).toBeInTheDocument();
  });

  test('смена валюты → 202 → уведомление, кэш страны не изменён', async () => {
    const { sent, drawer, user, app } = await openEdit();
    await pick(user, within(drawer).getByRole('combobox', { name: /Валюта/ }), 'KZT');
    await retype(user, field(drawer, /Телефонный код/), '+8');
    await user.click(within(drawer).getByRole('button', { name: 'Сохранить' }));
    await within(drawer).findByText('Отправлено на подтверждение');
    // весь patch — в запрос, страна не изменилась
    expect(sent[0]?.body).toEqual({ currency: 'KZT', phone_prefix: '+8' });
    const cached = app.queryClient.getQueryData<GeoCountryList>(geoKeys.countries('ru'));
    expect(cached?.items.find((c) => c.code === 'RU')).toEqual(COUNTRIES[0]);
    // форма закрыта (ввод отправлен): закрытие без вопроса
    // «Закрыть» подвала (у панели есть и своя «Закрыть» в шапке)
    const closers = within(drawer).getAllByRole('button', { name: 'Закрыть' });
    await user.click(closers[closers.length - 1]!);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  // Ревью G9-C, I-1: список перечитан под открытой формой (фокус, инвалидация) — основа patch — снимок, из которого
  // собрана форма: поле, которого сотрудник не трогал, не уходит (иначе откатило бы чужую правку).
  test('список перечитан под открытой формой — уходит только тронутое поле', async () => {
    const { sent, drawer, user, app } = await openEdit();
    app.queryClient.setQueryData(geoKeys.countries('ru'), {
      items: [
        {
          ...COUNTRIES[0]!,
          week_starts_on: 7,
          currency: 'KZT',
          names: { ru: 'Россия' },
          version: 4,
        },
        COUNTRIES[1]!,
      ],
    });
    await retype(user, field(drawer, /Телефонный код/), '+8');
    await user.click(within(drawer).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toEqual({ phone_prefix: '+8' });
  });

  // Финальное ревью m2: язык сменили в другой вкладке (me перечитан при возврате) — список стран на новом языке ещё
  // грузится, а форма не размонтируется: ввод цел, как на странице города (G8 m18).
  test('смена языка под открытой формой — ввод не стирается', async () => {
    const { drawer, user, app } = await openEdit();
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.get('/v1/geo/countries', async ({ request, response }) => {
        if (request.headers.get('Accept-Language') === 'en') await gate;
        return response(200).json({ items: COUNTRIES });
      }),
    );
    const prefix = field(drawer, /Телефонный код/);
    await retype(user, prefix, '+77');
    app.me.current = { ...meDirectoriesAdmin, locale: 'en' };
    await app.refetchMe();
    await screen.findByRole('button', { name: 'Save' });
    expect(screen.getByRole('textbox', { name: /Phone code/i })).toBe(prefix);
    expect(prefix).toHaveValue('+77');
    release();
    await waitFor(() =>
      expect(app.queryClient.getQueryData(geoKeys.countries('en'))).toBeDefined(),
    );
    expect(screen.getByRole('textbox', { name: /Phone code/i })).toHaveValue('+77');
  });

  test('ничего не изменилось — запроса нет, панель закрыта', async () => {
    const { sent, drawer, user } = await openEdit();
    await user.click(within(drawer).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(sent).toHaveLength(0);
  });

  test('validation.failed — у поля', async () => {
    countryEditServer({
      answer: () =>
        problem(400, 'validation.failed', {
          errors: [{ field: 'body.phone_prefix', code: 'pattern' }],
        }),
    });
    renderApp('/geo/countries?edit=RU', { me: meDirectoriesAdmin });
    const drawer = await screen.findByRole('dialog', { name: 'Россия (RU)' });
    const user = userEvent.setup();
    await retype(user, field(drawer, /Телефонный код/), '+9');
    await user.click(within(drawer).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(field(drawer, /Телефонный код/)).toHaveAccessibleDescription(/Неверный формат/),
    );
  });

  test.each([
    ['RU', 'Россия', 'RUB'],
    ['KZ', 'Казахстан', 'KZT'],
  ] as const)('поля правил помечены «вдвоём» у любой страны: %s', async (code, name, currency) => {
    const { drawer } = await openEdit(code, name);
    const select = within(drawer).getByRole('combobox', { name: /Валюта/ });
    expect(select).toHaveAccessibleDescription(/Изменение — вдвоём/);
    for (const n of [/Минимальный возраст/, /совершеннолетия/])
      expect(field(drawer, n)).toHaveAccessibleDescription(/Изменение — вдвоём/);
    expect(selectedText(select)).toBe(currency);
  });

  test('ввод → Esc — вопрос «Уйти без сохранения?»; «Уйти» — панель закрыта, адрес без edit', async () => {
    const { drawer, user, app } = await openEdit();
    await retype(user, field(drawer, /Телефонный код/), '+9');
    await user.keyboard('{Escape}');
    const leave = await screen.findByRole('dialog', { name: 'Уйти без сохранения?' });
    await user.click(within(leave).getByRole('button', { name: 'Уйти' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(app.at()).toBe('/geo/countries');
  });
});
