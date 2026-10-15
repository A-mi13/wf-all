import { act, cleanup, screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { CITY_OTHER, meAdmin, meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { approval, staffServer, TEAM } from '../../test/staff';

// «Пригласить» (спека §9.4 п. 5, §9.3, §4.9): роли — только выдаваемые, города модератора — поиск
// на сервере и обязательны, 202 → «Отправлено на подтверждение», ошибки — по полям; черновик формы
// переживает перезагрузку и исчезает после выхода.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

type Sent = { body: unknown; key: string | null };

function inviteServer(answer: (body: unknown) => Response | null = () => null) {
  const sent: Sent[] = [];
  server.use(
    http.post('/v1/staff/invites', async ({ request, response }) => {
      const body = await request.json();
      sent.push({ body, key: request.headers.get('Idempotency-Key') });
      const r = answer(body);
      return r ? response.untyped(r) : response(202).json({ approval: approval() });
    }),
  );
  return sent;
}

async function openInvite(user: UserEvent) {
  await user.click(await screen.findByRole('button', { name: 'Пригласить сотрудника' }));
  return screen.getByRole('dialog', { name: 'Приглашение сотрудника' });
}

const roleOptions = (dialog: HTMLElement) =>
  within(within(dialog).getByLabelText('Роль'))
    .getAllByRole('option')
    .filter((o) => !(o as HTMLOptionElement).disabled)
    .map((o) => (o as HTMLOptionElement).value);

async function fill(user: UserEvent, dialog: HTMLElement, role: string) {
  await user.type(within(dialog).getByLabelText('Рабочая почта'), 'ivan.p@wf.example');
  await user.type(within(dialog).getByLabelText('Имя'), 'Иван П.');
  await user.selectOptions(within(dialog).getByLabelText('Роль'), role);
}

describe('форма приглашения', () => {
  test('админ не видит owner/admin, владелец видит все роли', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meAdmin });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    expect(roleOptions(dialog)).toEqual(['moderator', 'support']);
    cleanup();

    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner });
    const owner = await openInvite(userEvent.setup());
    expect(roleOptions(owner)).toEqual(['owner', 'admin', 'moderator', 'support']);
  });

  test('модератор без городов — ошибка поля на клиенте, запрос не уходит', async () => {
    staffServer({ accounts: TEAM });
    const sent = inviteServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'moderator');
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(dialog).findByText('Выберите хотя бы один вариант')).toBeInTheDocument();
    expect(sent).toHaveLength(0);
  });

  test('поиск городов уходит на сервер (q), выбранный город — в запросе', async () => {
    const { seen } = staffServer({ accounts: TEAM });
    const sent = inviteServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'moderator');
    await user.type(within(dialog).getByRole('searchbox', { name: /Города модератора/ }), 'Пят');
    await waitFor(() => expect(seen.cities.some((q) => q.get('q') === 'Пят')).toBe(true));
    // локально не фильтруем: варианты — ответ сервера на q
    await waitFor(() =>
      expect(within(dialog).queryByLabelText('Ставрополь, Ставропольский край')).toBeNull(),
    );
    await user.click(within(dialog).getByLabelText('Пятигорск, Ставропольский край'));
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(dialog).findByText('Отправлено на подтверждение');
    expect(sent.map((s) => s.body)).toEqual([
      {
        email: 'ivan.p@wf.example',
        display_name: 'Иван П.',
        role: 'moderator',
        city_ids: [CITY_OTHER],
      },
    ]);
  });

  test('202 — «Отправлено на подтверждение», ключ идемпотентности, черновик стёрт', async () => {
    staffServer({ accounts: TEAM });
    const sent = inviteServer();
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'support');
    expect(storageDump()).toContain('ivan.p@wf.example');
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(dialog).findByRole('status')).toHaveTextContent(
      'Отправлено на подтверждение',
    );
    // не модератор — поля городов в запросе нет
    expect(sent).toEqual([
      {
        body: { email: 'ivan.p@wf.example', display_name: 'Иван П.', role: 'support' },
        key: expect.any(String),
      },
    ]);
    expect(storageDump()).not.toContain('ivan.p@wf.example');
    // «Пригласить ещё» — пустая форма
    await user.click(within(dialog).getByRole('button', { name: 'Пригласить ещё' }));
    expect(within(dialog).getByLabelText('Рабочая почта')).toHaveValue('');
  });

  test('409 staff.email_taken — на поле почты; 422 city_not_found — на поле городов', async () => {
    staffServer({ accounts: TEAM });
    let n = 0;
    inviteServer(() =>
      ++n === 1 ? problem(409, 'staff.email_taken') : problem(422, 'staff.city_not_found'),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'moderator');
    await user.click(await within(dialog).findByLabelText('Ставрополь, Ставропольский край'));
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    const email = within(dialog).getByLabelText('Рабочая почта');
    await waitFor(() =>
      expect(email).toHaveAccessibleDescription('Сотрудник с такой почтой уже есть'),
    );
    expect(email).toHaveFocus();

    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(dialog).findByText('Город не найден')).toBeInTheDocument();
  });

  test('validation.failed — по полям', async () => {
    staffServer({ accounts: TEAM });
    inviteServer(() =>
      problem(400, 'validation.failed', { errors: [{ field: 'body.email', code: 'format' }] }),
    );
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'support');
    await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Рабочая почта')).toHaveAccessibleDescription(
        'Неверный формат',
      ),
    );
  });
});

describe('черновик приглашения', () => {
  test('пережил перезагрузку: почта, имя, роль и города вернулись', async () => {
    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner });
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'moderator');
    await user.click(await within(dialog).findByLabelText('Пятигорск, Ставропольский край'));
    cleanup(); // «перезагрузка»: sessionStorage остаётся

    staffServer({ accounts: TEAM });
    renderApp('/staff', { me: meOwner });
    const again = await openInvite(userEvent.setup());
    expect(within(again).getByLabelText('Рабочая почта')).toHaveValue('ivan.p@wf.example');
    expect(within(again).getByLabelText('Имя')).toHaveValue('Иван П.');
    expect(within(again).getByLabelText('Роль')).toHaveValue('moderator');
    // подпись выбранного города — по ids, хотя поиск его не показывал
    expect(
      await within(again).findByRole('button', { name: 'Убрать «Пятигорск, Ставропольский край»' }),
    ).toBeInTheDocument();
  });

  // Журнал Task 13: форма, пишущая черновик при размонтировании, перезаписала бы его после
  // clearDrafts() выхода. Выход при открытой форме — «Выйти» в окне разблокировки (20 минут без
  // действий): форма размонтируется уже ПОСЛЕ стирания черновиков.
  test('выход при открытой форме → черновика нет ни в storage, ни в форме следующего сотрудника', async () => {
    staffServer({ accounts: TEAM });
    const app = renderApp('/staff', { me: meOwner });
    server.use(
      http.post('/v1/staff/auth/logout', ({ response }) => {
        app.me.current = null;
        return response(204).empty();
      }),
    );
    const user = userEvent.setup();
    const dialog = await openInvite(user);
    await fill(user, dialog, 'support');
    expect(storageDump()).toContain('ivan.p@wf.example');
    // 20 минут без действий: окно разблокировки поверх открытой формы
    act(() => void reauthBus.request().catch(() => {}));
    const reauth = await screen.findByRole('dialog', { name: 'Подтвердите вход' });
    expect(screen.getByRole('dialog', { name: 'Приглашение сотрудника' })).toBeInTheDocument();
    await user.click(within(reauth).getByRole('button', { name: 'Выйти' }));
    await screen.findByRole('heading', { level: 1, name: 'Вход администратора' });
    await waitFor(() => expect(storageDump()).not.toContain('ivan.p@wf.example'));
    expect(storageDump()).not.toContain('Иван П.');

    // следующий сотрудник за этим компьютером
    app.me.current = meAdmin;
    await app.refetchMe();
    await user.click(await screen.findByRole('link', { name: 'Роли и доступ' }));
    const next = await openInvite(user);
    expect(within(next).getByLabelText('Рабочая почта')).toHaveValue('');
    expect(within(next).getByLabelText('Имя')).toHaveValue('');
  });
});

test('StrictMode: черновик пишется, приглашение уходит один раз', async () => {
  staffServer({ accounts: TEAM });
  const sent = inviteServer();
  renderApp('/staff', { me: meOwner, strict: true });
  const user = userEvent.setup();
  const dialog = await openInvite(user);
  await fill(user, dialog, 'support');
  expect(storageDump()).toContain('ivan.p@wf.example');
  await user.click(within(dialog).getByRole('button', { name: 'Отправить на подтверждение' }));
  await within(dialog).findByText('Отправлено на подтверждение');
  expect(sent).toHaveLength(1);
});
