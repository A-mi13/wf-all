import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import {
  identityServer,
  mePlayers,
  player,
  PLAYER_EMAIL,
  SCHEDULED_RESET,
} from '../../test/identity';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { http, server } from '../../test/server';

// «Игроки» (спека identity §7.4 «Поддержка: сброс 2FA»; план I-4, Task 9, R19, R20): поиск по почте с причиной —
// заголовками в percent-encoding, только по явной отправке, без перезапросов по фокусу окна и сети (каждое чтение —
// запись в журнале ПД); почта, причина и результат — только в памяти экрана (ни адреса, ни storage); запрос сброса
// 2FA — 202 и пометка «на подтверждение»; отмена назначенного сброса — 200, карточка обновляется по ответу без
// повторного поиска (m7).

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

type Player = components['schemas']['IdentityPlayerLookup'];

const REASON = 'обращение 4812';

function setup(
  players: Record<string, Player> = { [PLAYER_EMAIL]: player() },
  history: 'memory' | 'browser' = 'memory',
) {
  sectionsServer();
  const id = identityServer({ applications: [], players });
  const app = renderApp('/players', { me: mePlayers, history });
  return { ...id, app, user: userEvent.setup() };
}

async function search(email = PLAYER_EMAIL, reason = REASON) {
  fireEvent.change(await screen.findByLabelText('Почта игрока'), { target: { value: email } });
  fireEvent.change(screen.getByLabelText('Причина просмотра'), { target: { value: reason } });
  fireEvent.click(screen.getByRole('button', { name: 'Найти' }));
}

const card = () => screen.findByRole('region', { name: 'kosta_10' });

describe('поиск', () => {
  test('заголовки в percent-encoding, найден — карточка: ник, город, статус, 2FA', async () => {
    const { seen } = setup();
    // почта — как введена (нормализует сервер); безопасный невидимый в причине срезан
    await search('kosta.f@example.com', 'обраще\u200Bние 4812');
    const c = await card();
    expect(seen.lookups).toEqual([
      { email: encodeURIComponent(PLAYER_EMAIL), reason: encodeURIComponent(REASON) },
    ]);
    expect(within(c).getByText('Активен')).toBeInTheDocument();
    // карточка появляется после ответа — живая строка объявляет находку (m5 ревью T9)
    expect(screen.getAllByRole('status').map((el) => el.textContent)).toContain(
      'Найден игрок kosta_10',
    );
    expect(within(c).getByText('2FA включена')).toBeInTheDocument();
    expect(await within(c).findByText(/Ставрополь/)).toBeInTheDocument();
    // подсказка о ПД у причины (R41)
    expect(
      screen.getByText('Например, номер обращения. Не указывайте персональные данные игрока'),
    ).toBeInTheDocument();
  });

  test('404 — «Игрок с такой почтой не найден» в форме, карточки нет', async () => {
    setup({});
    await search('nobody@example.com');
    expect(await screen.findByText('Игрок с такой почтой не найден')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'kosta_10' })).toBeNull();
  });

  test('причина короче 3 символов и с bidi-переопределением — ошибка поля, запроса нет', async () => {
    const { seen } = setup();
    await search(PLAYER_EMAIL, 'аб');
    expect(await screen.findByText('Слишком короткое значение')).toBeInTheDocument();
    // bidi-переопределение (Cf) — ошибка поля; перевод строки однострочное поле срезает само
    await search(PLAYER_EMAIL, 'обращение\u202E4812');
    expect(
      await screen.findByText('Уберите переводы строк и служебные символы'),
    ).toBeInTheDocument();
    expect(seen.lookups).toEqual([]);
  });

  test('незнакомый статус — общим текстом; без 2FA — кнопки сброса нет', async () => {
    setup({ [PLAYER_EMAIL]: player({ status: 'frozen_future', mfa_enabled: false }) });
    await search();
    const c = await card();
    expect(within(c).getByText('Другой статус')).toBeInTheDocument();
    expect(within(c).getByText('2FA не включена')).toBeInTheDocument();
    expect(within(c).queryByRole('button', { name: 'Запросить сброс 2FA' })).toBeNull();
  });

  test('найден → фокус окна, возврат во вкладку, сеть — второго поиска нет', async () => {
    const { seen } = setup();
    await search();
    await card();
    act(() => {
      fireEvent.focus(window);
      window.dispatchEvent(new Event('visibilitychange'));
      window.dispatchEvent(new Event('offline'));
      window.dispatchEvent(new Event('online'));
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(seen.lookups).toHaveLength(1);
  });

  test('почта и причина — не в адресе и не в storage; уход и возврат — пустая форма', async () => {
    // browser history: адрес окна виден целиком (search, hash), а не только запись роутера
    const { app, user } = setup(undefined, 'browser');
    await search();
    await card();
    expect(app.at()).toBe('/players');
    expect(window.location.href).not.toContain('kosta');
    expect(window.location.href).not.toContain('4812');
    expect(storageDump()).not.toContain(PLAYER_EMAIL);
    expect(storageDump()).not.toContain('4812');
    expect(storageDump()).not.toContain('kosta_10');
    await user.click(screen.getByRole('link', { name: 'Журнал действий' }));
    await waitFor(() => expect(app.at()).toBe('/audit'));
    // экран журнала, а не только адрес: пока ленивый кусок журнала грузится (под нагрузкой ./task ci 17.11), роутер держит
    // на экране прежнюю страницу «Игроков» — возврат на неё не перемонтирует форму, и тест видел старую почту
    await screen.findByRole('heading', { level: 1, name: 'Журнал действий' }, { timeout: 10_000 });
    await user.click(screen.getByRole('link', { name: 'Игроки' }));
    expect(await screen.findByLabelText('Почта игрока')).toHaveDisplayValue('');
    expect(screen.getByLabelText('Причина просмотра')).toHaveDisplayValue('');
    expect(screen.queryByRole('region', { name: 'kosta_10' })).toBeNull();
    // прежний ответ поиска в кэше не живёт (gcTime 0): у нового экрана — только пустой запрос без данных
    const cached = app.queryClient
      .getQueryCache()
      .findAll({ queryKey: ['identity', 'player-lookup'] });
    expect(cached.filter((q) => q.state.data !== undefined)).toEqual([]);
  }, 15_000);
});

describe('сброс 2FA', () => {
  test('запрос — причина и обращение → 202, пометка «на подтверждение», кнопки больше нет', async () => {
    const { seen, user } = setup();
    await search();
    const c = await card();
    await user.click(within(c).getByRole('button', { name: 'Запросить сброс 2FA' }));
    const d = await screen.findByRole('dialog', { name: 'Сброс 2FA: kosta_10' });
    fireEvent.change(within(d).getByLabelText('Почему и как проверена личность'), {
      target: { value: '  Потерял телефон, проверен по документам  ' },
    });
    fireEvent.change(within(d).getByLabelText('Номер обращения'), {
      target: { value: 'SUP-4812' },
    });
    await user.click(within(d).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(c).findByText('Отправлено на подтверждение')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Сброс 2FA: kosta_10' })).toBeNull();
    expect(seen.resets).toHaveLength(1);
    expect(seen.resets[0]?.userId).toBe(player().id);
    expect(seen.resets[0]?.key).toBeTruthy();
    expect(seen.resets[0]?.body).toEqual({
      reason: 'Потерял телефон, проверен по документам',
      ticket: 'SUP-4812',
    });
    expect(within(c).queryByRole('button', { name: 'Запросить сброс 2FA' })).toBeNull();
    // фокус не на body
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
  });

  test('причина короче 3 — ошибка поля, без обращения — без поля ticket; 409 — текст в окне', async () => {
    const { seen, user } = (() => {
      sectionsServer();
      const id = identityServer({
        applications: [],
        players: { [PLAYER_EMAIL]: player() },
        resetAnswer: () => problem(409, 'identity.mfa_reset_pending'),
      });
      renderApp('/players', { me: mePlayers });
      return { ...id, user: userEvent.setup() };
    })();
    await search();
    const c = await card();
    await user.click(within(c).getByRole('button', { name: 'Запросить сброс 2FA' }));
    const d = await screen.findByRole('dialog', { name: 'Сброс 2FA: kosta_10' });
    const reason = within(d).getByLabelText('Почему и как проверена личность');
    fireEvent.change(reason, { target: { value: 'аб' } });
    await user.click(within(d).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(d).findByText('Слишком короткое значение')).toBeInTheDocument();
    expect(seen.resets).toEqual([]);
    fireEvent.change(reason, { target: { value: 'Потерял телефон' } });
    await user.click(within(d).getByRole('button', { name: 'Отправить на подтверждение' }));
    expect(await within(d).findByText('Сброс 2FA этому игроку уже назначен')).toBeInTheDocument();
    expect(seen.resets[0]?.body).toEqual({ reason: 'Потерял телефон' });
  });

  test('отмена назначенного — тост, сброса в карточке нет, поиск не повторён (m7)', async () => {
    const { seen, user } = setup({ [PLAYER_EMAIL]: player({ mfa_reset: SCHEDULED_RESET }) });
    await search();
    const c = await card();
    expect(within(c).getByText(/Сброс назначен на/)).toBeInTheDocument();
    expect(within(c).queryByRole('button', { name: 'Запросить сброс 2FA' })).toBeNull();
    await user.click(within(c).getByRole('button', { name: 'Отменить сброс' }));
    const d = await screen.findByRole('dialog', { name: 'Отменить сброс 2FA kosta_10?' });
    fireEvent.change(within(d).getByRole('textbox'), { target: { value: 'kosta_10' } });
    await user.click(within(d).getByRole('button', { name: 'Отменить сброс' }));
    expect(await screen.findByText('Сброс 2FA отменён')).toBeInTheDocument();
    await waitFor(() => expect(within(c).queryByText(/Сброс назначен на/)).toBeNull());
    expect(within(c).getByRole('button', { name: 'Запросить сброс 2FA' })).toBeInTheDocument();
    expect(seen.cancels).toHaveLength(1);
    expect(seen.cancels[0]?.resetId).toBe(SCHEDULED_RESET.id);
    expect(seen.lookups).toHaveLength(1);
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
  });

  test('исход запроса неизвестен (500) — счётчик подтверждений всё равно перечитан', async () => {
    sectionsServer();
    identityServer({
      applications: [],
      players: { [PLAYER_EMAIL]: player() },
      resetAnswer: () => problem(500, 'internal'),
    });
    let approvals = 0;
    server.use(
      http.get('/v1/approvals', () => {
        approvals++;
        return undefined;
      }),
    );
    renderApp('/players', { me: mePlayers });
    const user = userEvent.setup();
    await search();
    const c = await card();
    await user.click(within(c).getByRole('button', { name: 'Запросить сброс 2FA' }));
    const d = await screen.findByRole('dialog', { name: 'Сброс 2FA: kosta_10' });
    fireEvent.change(within(d).getByLabelText('Почему и как проверена личность'), {
      target: { value: 'Потерял телефон' },
    });
    await waitFor(() => expect(approvals).toBeGreaterThan(0));
    const before = approvals;
    await user.click(within(d).getByRole('button', { name: 'Отправить на подтверждение' }));
    await within(d).findByRole('alert');
    await waitFor(() => expect(approvals).toBeGreaterThan(before));
  });

  test('без identity.mfa.reset — ни запроса, ни отмены', async () => {
    sectionsServer();
    identityServer({
      applications: [],
      players: { [PLAYER_EMAIL]: player({ mfa_reset: SCHEDULED_RESET }) },
    });
    const permissions = { ...mePlayers.permissions };
    delete permissions['identity.mfa.reset'];
    renderApp('/players', { me: { ...mePlayers, permissions } });
    await search();
    const c = await card();
    expect(within(c).queryByRole('button', { name: 'Отменить сброс' })).toBeNull();
    expect(within(c).queryByRole('button', { name: 'Запросить сброс 2FA' })).toBeNull();
  });
});
