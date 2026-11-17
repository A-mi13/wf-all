import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { AUTH_FLAGS, flagsServer } from '../../test/flags';
import { flag, meDirectoriesAdmin } from '../../test/geo';
import { identityServer, USAGE } from '../../test/identity';
import { stubMedia } from '../../test/media';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';

// Аварийный выключатель входа (спека identity §4.3, §7.5; план I-4, Task 9): выключение auth_*_login — диалог с
// числом «входят только так» из getAuthMethodsUsage; ошибка статистики — «Повторить» без числа, выключить можно
// (авария важнее); включение — без диалога. Отказ в диалоге — ни одного PATCH.

afterEach(() => sessionStorage.clear());

const sw = (name: string) => screen.findByRole('switch', { name });
const TITLE = 'Выключить «Вход через Apple» везде?';

function setup(usage: Parameters<typeof identityServer>[0] = { usage: USAGE }, flags = AUTH_FLAGS) {
  sectionsServer();
  const id = identityServer(usage);
  const srv = flagsServer(flags);
  renderApp('/flags', { me: meDirectoriesAdmin });
  return { ...srv, seen: id.seen, user: userEvent.setup() };
}

describe('выключение аварийного флага', () => {
  test('число из статистики, «Выключить» — PATCH; до подтверждения запроса нет', async () => {
    const { calls, seen, user } = setup();
    const all = await sw('Вход через Apple: везде');
    await user.click(all);
    const dialog = await screen.findByRole('dialog', { name: TITLE });
    expect(
      await within(dialog).findByText(
        'Только этим способом входят 12 игроков — им останется «Забыли пароль?»',
      ),
    ).toBeInTheDocument();
    expect(seen.usage).toBe(1);
    expect(calls).toHaveLength(0);
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(all.getAttribute('aria-checked')).toBe('false'));
    expect(calls).toHaveLength(1);
    expect(calls[0]?.body).toEqual({ version: 1, enabled_globally: false });
    expect(screen.queryByRole('dialog', { name: TITLE })).toBeNull();
  });

  test('«Отменить» — без запроса, флаг включён', async () => {
    const { calls, user } = setup();
    const all = await sw('Вход через Apple: везде');
    await user.click(all);
    const dialog = await screen.findByRole('dialog', { name: TITLE });
    await user.click(within(dialog).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: TITLE })).toBeNull());
    expect(calls).toHaveLength(0);
    expect(all.getAttribute('aria-checked')).toBe('true');
  });

  test('ошибка статистики — «Повторить» без числа, выключить можно', async () => {
    const { calls, seen, user } = setup({ usage: () => problem(503, 'internal') });
    const all = await sw('Вход через Apple: везде');
    await user.click(all);
    const dialog = await screen.findByRole('dialog', { name: TITLE });
    const retry = await within(dialog).findByRole('button', { name: 'Повторить' });
    expect(within(dialog).queryByText(/Только этим способом/)).toBeNull();
    await user.click(retry);
    await waitFor(() => expect(seen.usage).toBe(2));
    const off = within(dialog).getByRole('button', { name: 'Выключить' });
    expect(off).toBeEnabled();
    await user.click(off);
    await waitFor(() => expect(calls).toHaveLength(1));
  });

  test('палец — тот же диалог вместо шторки, один', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { calls, user } = setup();
    const item = await screen.findByRole('listitem', { name: 'Вход через Apple' });
    await user.click(within(item).getByRole('switch', { name: 'Вход через Apple: везде' }));
    const dialog = await screen.findByRole('dialog', { name: TITLE });
    expect(await within(dialog).findByText(/Только этим способом входят 12/)).toBeInTheDocument();
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
    await waitFor(() => expect(calls).toHaveLength(1));
  });
});

test('включение аварийного флага и выключение обычного — без диалога', async () => {
  const { calls, seen, user } = setup({ usage: USAGE }, [
    flag({
      key: 'auth_google_login',
      rules: { global_only: true, requires: null },
    }),
    flag({ key: 'predictions', enabled_globally: true }),
  ]);
  await user.click(await sw('Вход через Google: везде'));
  await waitFor(() => expect(calls).toHaveLength(1));
  await user.click(await sw('Прогнозы: везде'));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(seen.usage).toBe(0);
});
