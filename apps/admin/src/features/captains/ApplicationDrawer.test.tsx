import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import {
  captainApplication,
  CONTACT,
  identityServer,
  meCaptains,
  meModCaptains,
  OTHER_CITY_APPLICATION,
  UNAVAILABLE,
} from '../../test/identity';
import { pick } from '../../test/pick';
import { problem } from '../../test/problem';
import { staffServer } from '../../test/staff';
import { contactKey } from './queries';

// Карточка заявки (план I-4, Task 8; спека identity §7.4, R21, R22): панель ?application=<id> — поля формы,
// условия, история; «Одобрить» недоступна, пока хоть одно условие не ok; отказ — код и комментарий (обязателен
// при other); ошибки решения — тост и перечитывание; контакты — по причине, только в памяти диалога (R19).

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const APP = captainApplication();

// Владелец раздела без одного права.
function without(permission: string): Me {
  const permissions = { ...meCaptains.permissions };
  delete permissions[permission];
  return { ...meCaptains, permissions };
}
const PATH = `/captains?application=${APP.id}`;

const drawer = () => screen.findByRole('dialog', { name: 'kosta_10' });
const footerButton = async (name: string) => within(await drawer()).findByRole('button', { name });
const modal = (name: string) => screen.findByRole('dialog', { name });

describe('карточка', () => {
  test('поля формы, телефон, условия, перевод строки в тексте игрока', async () => {
    staffServer();
    identityServer();
    renderApp(PATH, { me: meCaptains });
    const d = await drawer();
    await within(d).findByText('Условия сейчас');
    expect(within(d).getByText('6×6 по средам')).toBeInTheDocument();
    expect(within(d).getByText('Телефон указан')).toBeInTheDocument();
    // \n в тексте игрока — как есть (pre-wrap), без разметки
    const note = within(d).getByText(
      (_, el) => el?.textContent === 'Собираю ребят с работы\nтретий год',
    );
    expect(note.tagName).not.toBe('BR');
    expect(within(d).getByText('Возраст не ниже порога страны')).toBeInTheDocument();
  });

  test('условие unavailable — «Одобрить» недоступна, подсказка видна', async () => {
    staffServer();
    const { seen } = identityServer({ checks: UNAVAILABLE });
    renderApp(PATH, { me: meCaptains });
    const approve = await footerButton('Одобрить');
    expect(approve).toBeDisabled();
    const d = await drawer();
    expect(
      within(d).getAllByText('Проверка появится с модулями профиля и команд — одобрить нельзя'),
    ).not.toHaveLength(0);
    expect(within(d).getByText('Одобрить можно, когда все условия выполнены')).toBeInTheDocument();
    await userEvent.setup().click(approve);
    expect(screen.queryByRole('dialog', { name: 'Одобрить заявку' })).toBeNull();
    expect(seen.decisions).toEqual([]);
  });

  test('все ok — одобрение с комментарием: тост, строка ушла из очереди, итог в карточке', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Одобрить'));
    const m = await modal('Одобрить заявку');
    await user.type(
      within(m).getByRole('textbox', { name: 'Комментарий для игрока' }),
      '  знаю его ',
    );
    await user.click(within(m).getByRole('button', { name: 'Одобрить' }));
    expect(await screen.findByText('Заявка одобрена')).toBeInTheDocument();
    expect(seen.decisions).toHaveLength(1);
    expect(seen.decisions[0]?.body).toEqual({ comment: 'знаю его' });
    expect(seen.decisions[0]?.key).toBeTruthy();
    const d = await drawer();
    await within(d).findByText(/^Одобрена · Ольга В\./);
    expect(within(d).queryByRole('button', { name: 'Одобрить' })).toBeNull();
    expect(await screen.findByText('Очередь пуста')).toBeInTheDocument();
    await waitFor(() => expect(d.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(document.body);
  });

  test('одобрение без комментария — тело без comment', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Одобрить'));
    await user.click(
      within(await modal('Одобрить заявку')).getByRole('button', { name: 'Одобрить' }),
    );
    await screen.findByText('Заявка одобрена');
    expect(seen.decisions[0]?.body).toEqual({});
  });

  test('отказ other без комментария — ошибка поля до запроса; с комментарием — уходит', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Отклонить'));
    const m = await modal('Отклонить заявку');
    await pick(user, 'Причина', 'Другое');
    await user.type(within(m).getByRole('textbox', { name: 'Комментарий для игрока' }), '   ');
    await user.click(within(m).getByRole('button', { name: 'Отклонить' }));
    expect(await within(m).findByText('Заполните поле')).toBeInTheDocument();
    expect(seen.decisions).toEqual([]);

    await user.type(within(m).getByRole('textbox', { name: 'Комментарий для игрока' }), 'нет поля');
    await user.click(within(m).getByRole('button', { name: 'Отклонить' }));
    expect(await screen.findByText('Заявка отклонена')).toBeInTheDocument();
    expect(seen.decisions[0]?.body).toEqual({ rejection_code: 'other', comment: 'нет поля' });
  });

  test('409 application_closed — тост, карточка перечитана', async () => {
    staffServer();
    const { seen } = identityServer({
      answer: { approve: () => problem(409, 'identity.application_closed') },
    });
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Одобрить'));
    await user.click(
      within(await modal('Одобрить заявку')).getByRole('button', { name: 'Одобрить' }),
    );
    expect(
      await screen.findByText('Заявка уже решена или отозвана — карточка обновлена'),
    ).toBeInTheDocument();
    await waitFor(() => expect(seen.cards.length).toBeGreaterThanOrEqual(2));
    const d = await drawer();
    await within(d).findByText(/^Одобрена · Игорь Л\./);
    expect(screen.queryByRole('dialog', { name: 'Одобрить заявку' })).toBeNull();
    // кнопки решения сняты перечитыванием — фокус на панели, не на body (ревью T8 I2)
    await waitFor(() => expect(d.contains(document.activeElement)).toBe(true));
  });

  test('409 captain_ineligible — причины текстом в карточке, незнакомая — общим текстом', async () => {
    staffServer();
    identityServer({
      answer: {
        approve: () =>
          problem(409, 'identity.captain_ineligible', { reasons: ['too_young', 'from_future'] }),
      },
    });
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Одобрить'));
    await user.click(
      within(await modal('Одобрить заявку')).getByRole('button', { name: 'Одобрить' }),
    );
    const d = await drawer();
    expect(await within(d).findByText('возраст ниже порога страны')).toBeInTheDocument();
    expect(within(d).getByText('Условия капитанства сейчас не выполнены')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Одобрить заявку' })).toBeNull();
  });

  test('409 self_action — текст ошибки в окне', async () => {
    staffServer();
    identityServer({ answer: { reject: () => problem(409, 'identity.self_action') } });
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Отклонить'));
    const m = await modal('Отклонить заявку');
    await pick(user, 'Причина', 'Не удалось связаться');
    await user.click(within(m).getByRole('button', { name: 'Отклонить' }));
    expect(
      await within(m).findByText('Это ваша учётка игрока — решение примет другой сотрудник'),
    ).toBeInTheDocument();
  });

  test('404 при решении — «Заявка не найдена», панель закрыта', async () => {
    staffServer();
    identityServer({ answer: { approve: () => problem(404, 'identity.application_not_found') } });
    const app = renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    await user.click(await footerButton('Одобрить'));
    await user.click(
      within(await modal('Одобрить заявку')).getByRole('button', { name: 'Одобрить' }),
    );
    await waitFor(() => expect(app.at()).toBe('/captains'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByText('Заявка не найдена')).toBeInTheDocument();
  });

  test('карточки нет (404) — «Заявка не найдена» в панели', async () => {
    staffServer();
    identityServer({ applications: [] });
    renderApp(PATH, { me: meCaptains });
    const d = await screen.findByRole('dialog');
    expect(await within(d).findByText('Заявка не найдена')).toBeInTheDocument();
  });

  test('решённая заявка — итог вместо кнопок', async () => {
    staffServer();
    identityServer({
      applications: [
        captainApplication({
          status: 'rejected',
          decided_at: '2026-10-13T12:00:00Z',
          decided_by_label: 'Ольга В.',
          rejection_code: 'contact_unreachable',
          comment: 'не отвечает',
        }),
      ],
    });
    renderApp(PATH, { me: meCaptains });
    const d = await drawer();
    expect(await within(d).findByText('Причина: Не удалось связаться')).toBeInTheDocument();
    expect(within(d).queryByRole('button', { name: 'Одобрить' })).toBeNull();
    expect(within(d).queryByRole('button', { name: 'Отклонить' })).toBeNull();
  });

  test('без права decide — кнопок решения нет', async () => {
    staffServer();
    identityServer();
    renderApp(PATH, {
      me: without('identity.captain_applications.decide'),
    });
    const d = await drawer();
    await within(d).findByText('Условия сейчас');
    expect(within(d).queryByRole('button', { name: 'Одобрить' })).toBeNull();
  });
});

describe('контакты', () => {
  const openContacts = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.click(
      await within(await drawer()).findByRole('button', { name: 'Показать контакты' }),
    );
    return modal('Контакты заявителя');
  };
  const reasonBox = (m: HTMLElement) =>
    within(m).getByRole('textbox', { name: 'Причина просмотра' });

  test('причина 2 символа — ошибка поля, запроса нет; подсказка о ПД видна', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    expect(
      within(m).getByText('Например, номер обращения. Не указывайте персональные данные игрока'),
    ).toBeInTheDocument();
    await user.type(reasonBox(m), 'аб');
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    expect(await within(m).findByText('Слишком короткое значение')).toBeInTheDocument();
    expect(seen.contacts).toEqual([]);
  });

  test('управляющий символ — понятная ошибка, запроса нет', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    fireEvent.change(reasonBox(m), { target: { value: 'обра\u202Eщение 4812' } });
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    expect(
      await within(m).findByText('Уберите переводы строк и служебные символы'),
    ).toBeInTheDocument();
    expect(seen.contacts).toEqual([]);
  });

  test('заголовок — percent-encoding кириллицы, невидимые срезаны; фокус окна и online — без второго запроса', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    fireEvent.change(reasonBox(m), { target: { value: 'обраще\u200Bние 4812' } });
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    expect(await within(m).findByText(CONTACT.email)).toBeInTheDocument();
    expect(within(m).getByText('+79181234567')).toBeInTheDocument();
    // форма и «Показать» исчезли — фокус в окне, не на body (ревью T8 I2)
    await waitFor(() => expect(m.contains(document.activeElement)).toBe(true));
    expect(seen.contacts).toEqual([{ id: APP.id, reason: encodeURIComponent('обращение 4812') }]);
    act(() => {
      fireEvent.focus(window);
      window.dispatchEvent(new Event('visibilitychange'));
      // сеть пропала и вернулась (onlineManager перечитывает только на переходе offline → online)
      window.dispatchEvent(new Event('offline'));
      window.dispatchEvent(new Event('online'));
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(seen.contacts).toHaveLength(1);
    // ПД и причина — только в памяти диалога
    expect(storageDump()).not.toContain(CONTACT.email);
    expect(storageDump()).not.toContain('4812');
  });

  test('ошибка чтения — фокус окна и online не повторяют чтение', async () => {
    staffServer();
    const { seen } = identityServer({ contact: () => problem(500, 'internal') });
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    await user.type(reasonBox(m), 'обращение 4812');
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    await waitFor(() => expect(seen.contacts).toHaveLength(1));
    await within(m).findByRole('alert');
    act(() => {
      fireEvent.focus(window);
      window.dispatchEvent(new Event('visibilitychange'));
      // сеть пропала и вернулась (onlineManager перечитывает только на переходе offline → online)
      window.dispatchEvent(new Event('offline'));
      window.dispatchEvent(new Event('online'));
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(seen.contacts).toHaveLength(1);
  });

  test('после закрытия повторное открытие не показывает прежние данные', async () => {
    staffServer();
    const { seen } = identityServer();
    const app = renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    let m = await openContacts(user);
    await user.type(reasonBox(m), 'обращение 4812');
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    await within(m).findByText(CONTACT.email);
    await user.click(within(m).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Контакты заявителя' })).toBeNull(),
    );
    await new Promise((r) => setTimeout(r, 10));
    m = await openContacts(user);
    expect(within(m).queryByText(CONTACT.email)).toBeNull();
    expect(reasonBox(m)).toHaveValue('');
    expect(seen.contacts).toHaveLength(1);
    // контакты не пережили окно и в кэше запросов (R19)
    expect(app.queryClient.getQueryData(contactKey(APP.id))).toBeUndefined();
  });

  test('без права identity.pii.read — кнопки «Показать контакты» нет', async () => {
    staffServer();
    identityServer();
    renderApp(PATH, {
      me: without('identity.pii.read'),
    });
    const d = await drawer();
    await within(d).findByText('Условия сейчас');
    expect(within(d).queryByRole('button', { name: 'Показать контакты' })).toBeNull();
  });

  test('двойное «Показать», пока идёт чтение, — один запрос (одна запись в журнале)', async () => {
    staffServer();
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const { seen } = identityServer({ holdContact: () => gate });
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    await user.type(reasonBox(m), 'обращение 4812');
    await user.click(within(m).getByRole('button', { name: 'Показать' }));
    await waitFor(() => expect(seen.contacts).toHaveLength(1));
    // кнопка занята, повтор щелчком и Enter в поле не уходит
    const show = within(m).getByRole('button', { name: 'Показать' });
    expect(show).toHaveAttribute('aria-busy', 'true');
    await user.click(show);
    fireEvent.submit(reasonBox(m).closest('form') as HTMLFormElement);
    release();
    expect(await within(m).findByText(CONTACT.email)).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 50));
    expect(seen.contacts).toHaveLength(1);
  });

  test('введена причина — закрытие спрашивает «Уйти без сохранения?»', async () => {
    staffServer();
    identityServer();
    renderApp(PATH, { me: meCaptains });
    const user = userEvent.setup();
    const m = await openContacts(user);
    await user.type(reasonBox(m), 'обр');
    await user.click(within(m).getByRole('button', { name: 'Закрыть' }));
    expect(await screen.findByText('Уйти без сохранения?')).toBeInTheDocument();
  });

  test('модератор: контакты своего города — есть, чужого — нет', async () => {
    staffServer();
    identityServer({ applications: [APP, OTHER_CITY_APPLICATION] });
    renderApp(PATH, { me: meModCaptains });
    const d = await drawer();
    expect(await within(d).findByRole('button', { name: 'Показать контакты' })).toBeInTheDocument();
  });

  test('модератор: заявка чужого города — без «Показать контакты» и решений', async () => {
    staffServer();
    identityServer({ applications: [APP, OTHER_CITY_APPLICATION] });
    renderApp(`/captains?application=${OTHER_CITY_APPLICATION.id}`, { me: meModCaptains });
    const d = await screen.findByRole('dialog', { name: 'misha_gk' });
    await within(d).findByText('Условия сейчас');
    expect(within(d).queryByRole('button', { name: 'Показать контакты' })).toBeNull();
    expect(within(d).queryByRole('button', { name: 'Одобрить' })).toBeNull();
  });
});
