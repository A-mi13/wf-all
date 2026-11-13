import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import {
  COUNTRIES,
  country,
  countryEditServer,
  meDirectoriesAdmin,
  meDirectoriesSupport,
} from '../../test/geo';
import { pick } from '../../test/pick';
import { problem } from '../../test/problem';

// «Страны» (план B, Task 21; спека UI-фундамента §6.2): таблица правил, «Добавить страну» (всегда 202), включение —
// сразу (200), выключение — ConfirmDialog → 202, переключатель не меняется до исполнения; панель страны — ?edit=<код>,
// создание — ?edit=new (I-12: без права rules ключ снимается).

afterEach(() => sessionStorage.clear());

const page = () => screen.findByRole('heading', { level: 1, name: 'Страны' });
const table = () => screen.findByRole('table', { name: 'Страны' });
const rowOf = async (code: string) => {
  const t = await table();
  const row = within(t)
    .getAllByRole('row')
    .find((r) => within(r).queryByText(code, { exact: true }));
  if (!row) throw new Error(`нет строки ${code}`);
  return row;
};

describe('таблица', () => {
  test('код, название, валюта, язык, префикс, неделя, возрасты, включена, открытых городов', async () => {
    countryEditServer({
      countries: [...COUNTRIES, country({ code: 'XX', name: 'Иксия', week_starts_on: 9 })],
    });
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    await page();
    const ru = await rowOf('RU');
    for (const text of ['Россия', 'RUB', 'Русский', '+7', 'понедельник', '14 / 18'])
      expect(ru).toHaveTextContent(text);
    expect(within(ru).getByRole('switch', { name: 'Россия: включена' })).toHaveAttribute(
      'aria-checked',
      'true',
    );
    const kz = await rowOf('KZ');
    expect(within(kz).getByRole('switch', { name: 'Казахстан: включена' })).toHaveAttribute(
      'aria-checked',
      'false',
    );
    // незнакомый день недели (контракт 1–7, ответ шире) — «—», без падения
    expect(await rowOf('XX')).toHaveTextContent('—');
  });

  test('поддержка — только чтение: нет «Добавить страну», переключатели недоступны, панель без «Сохранить»', async () => {
    countryEditServer();
    const app = renderApp('/geo/countries', { me: meDirectoriesSupport });
    await page();
    const ru = await rowOf('RU');
    expect(screen.queryByRole('button', { name: /Добавить страну/ })).toBeNull();
    expect(within(ru).getByRole('switch', { name: 'Россия: включена' })).toBeDisabled();
    await userEvent.setup().click(within(ru).getByRole('button', { name: 'Открыть: Россия' }));
    const drawer = await screen.findByRole('dialog', { name: 'Россия (RU)' });
    expect(app.at()).toBe('/geo/countries?edit=RU');
    expect(within(drawer).queryByRole('button', { name: 'Сохранить' })).toBeNull();
    expect(within(drawer).getByRole('textbox', { name: 'Телефонный код' })).toHaveAttribute(
      'readonly',
    );
  });

  test('I-12: глубокая ссылка ?edit=new у поддержки — панели нет, edit снят', async () => {
    countryEditServer();
    const app = renderApp('/geo/countries?edit=new', { me: meDirectoriesSupport });
    await page();
    await waitFor(() => expect(app.at()).toBe('/geo/countries'));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('?edit=new — форма создания, ?edit=XX — «Страна не найдена», ?edit=ne — панели нет', async () => {
    countryEditServer();
    const a = renderApp('/geo/countries?edit=new', { me: meDirectoriesAdmin });
    expect(await screen.findByRole('dialog', { name: 'Новая страна' })).toBeInTheDocument();
    a.history.push('/geo/countries?edit=XX');
    const nf = await screen.findByRole('dialog', { name: 'Страна не найдена' });
    expect(nf).toHaveTextContent('XX');
    a.history.push('/geo/countries?edit=ne');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
});

describe('добавить страну', () => {
  test('→ 202 → уведомление о запросе, таблица не изменилась', async () => {
    const { sent } = countryEditServer();
    const app = renderApp('/geo/countries', { me: meDirectoriesAdmin });
    await page();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Добавить страну/ }));
    expect(app.at()).toBe('/geo/countries?edit=new');
    const drawer = await screen.findByRole('dialog', { name: 'Новая страна' });
    await fillCreate(user, drawer);
    await user.click(within(drawer).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(drawer).findByText('Отправлено на подтверждение')).toBeInTheDocument();
    expect(sent).toHaveLength(1);
    expect(within(await table()).queryByText('AM', { exact: true })).toBeNull();
  });
});

describe('включение и выключение', () => {
  test('включение → 200 и новое состояние', async () => {
    const { sent } = countryEditServer();
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    const kz = await rowOf('KZ');
    await userEvent.setup().click(within(kz).getByRole('switch', { name: 'Казахстан: включена' }));
    await waitFor(() =>
      expect(within(kz).getByRole('switch', { name: 'Казахстан: включена' })).toHaveAttribute(
        'aria-checked',
        'true',
      ),
    );
    expect(sent[0]).toMatchObject({
      op: 'patch',
      body: { is_enabled: true },
      path: '/v1/geo/countries/KZ',
    });
    expect(sent[0]?.key).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('выключение → подтверждение → 202 и уведомление, переключатель не сменился', async () => {
    const { sent } = countryEditServer();
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    const ru = await rowOf('RU');
    const sw = () => within(ru).getByRole('switch', { name: 'Россия: включена' });
    const user = userEvent.setup();
    await user.click(sw());
    const dialog = await screen.findByRole('dialog', { name: 'Выключить страну Россия?' });
    expect(dialog).toHaveTextContent('Города страны пропадут из приложения');
    // до подтверждения запроса нет, переключатель прежний
    expect(sent).toHaveLength(0);
    expect(sw()).toHaveAttribute('aria-checked', 'true');
    await user.type(within(dialog).getByRole('textbox'), 'RU');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    expect(
      await screen.findByText('Выключение «Россия» отправлено на подтверждение'),
    ).toBeInTheDocument();
    expect(sent[0]).toMatchObject({ op: 'patch', body: { is_enabled: false } });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(sw()).toHaveAttribute('aria-checked', 'true');
  });

  // Ревью G9-C, m-1: таблица устарела (страну уже выключили в другой вкладке) — сервер отвечает 200, а не 202: тост
  // по факту ответа, не «отправлено на подтверждение».
  test('выключение по устаревшей таблице — 200: тост «выключена», не «на подтверждение»', async () => {
    const { state } = countryEditServer();
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    const ru = await rowOf('RU');
    state.countries = state.countries.map((c) =>
      c.code === 'RU' ? { ...c, is_enabled: false } : c,
    );
    const user = userEvent.setup();
    await user.click(within(ru).getByRole('switch', { name: 'Россия: включена' }));
    const dialog = await screen.findByRole('dialog', { name: 'Выключить страну Россия?' });
    await user.type(within(dialog).getByRole('textbox'), 'RU');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    expect(await screen.findByText('Страна «Россия» выключена')).toBeInTheDocument();
    expect(screen.queryByText('Выключение «Россия» отправлено на подтверждение')).toBeNull();
    await waitFor(() =>
      expect(within(ru).getByRole('switch', { name: 'Россия: включена' })).toHaveAttribute(
        'aria-checked',
        'false',
      ),
    );
  });

  test('выключение — отказ сервера: ошибка в окне, переключатель прежний', async () => {
    countryEditServer({ answer: () => problem(403, 'auth.forbidden') });
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    const ru = await rowOf('RU');
    const user = userEvent.setup();
    await user.click(within(ru).getByRole('switch', { name: 'Россия: включена' }));
    const dialog = await screen.findByRole('dialog', { name: 'Выключить страну Россия?' });
    await user.type(within(dialog).getByRole('textbox'), 'RU');
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument();
    expect(within(ru).getByRole('switch', { name: 'Россия: включена' })).toHaveAttribute(
      'aria-checked',
      'true',
    );
  });

  test('включение — обрыв: тост ошибки, переключатель прежний', async () => {
    countryEditServer({ answer: () => HttpResponse.error() as unknown as Response });
    renderApp('/geo/countries', { me: meDirectoriesAdmin });
    const kz = await rowOf('KZ');
    await userEvent.setup().click(within(kz).getByRole('switch', { name: 'Казахстан: включена' }));
    await waitFor(() =>
      expect(within(kz).getByRole('switch', { name: 'Казахстан: включена' })).not.toHaveAttribute(
        'aria-busy',
      ),
    );
    expect(within(kz).getByRole('switch', { name: 'Казахстан: включена' })).toHaveAttribute(
      'aria-checked',
      'false',
    );
  });
});

// Форма создания: код, названия, валюта, язык, префикс, неделя, возрасты.
async function fillCreate(
  user: ReturnType<typeof userEvent.setup>,
  drawer: HTMLElement,
  over: { code?: string } = {},
) {
  await user.type(within(drawer).getByRole('textbox', { name: /Код страны/ }), over.code ?? 'AM');
  await user.type(within(drawer).getByRole('textbox', { name: 'Название на русском' }), 'Армения');
  await pick(user, within(drawer).getByRole('combobox', { name: /Валюта/ }), 'AMD');
  await user.type(within(drawer).getByRole('textbox', { name: /Телефонный код/ }), '+374');
  await user.type(within(drawer).getByRole('textbox', { name: /Минимальный возраст/ }), '14');
  await user.type(within(drawer).getByRole('textbox', { name: /совершеннолетия/ }), '18');
}
