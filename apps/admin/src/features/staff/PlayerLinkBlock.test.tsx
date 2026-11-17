import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { identityServer, player, PLAYER_EMAIL } from '../../test/identity';
import { meOwner } from '../../test/me';
import { problem } from '../../test/problem';
import { account, staffServer } from '../../test/staff';

// Связь «сотрудник → учётка игрока» в карточке сотрудника (спека identity §7.4; план I-4, Task 9): только владелец
// (staff.player_links.manage); «Связать» — окно: почта и причина (поиск — тот же аудит ПД, что в «Игроках») →
// подтверждение «Связать с <ник>?» → PUT; ошибки — текстом в окне; «Снять» — ConfirmDialog с ником → DELETE.
// Почта и причина — только в памяти окна; фокус окна во время подтверждения второго поиска не делает.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const meLinks: Me = {
  ...meOwner,
  permissions: {
    ...meOwner.permissions,
    'staff.player_links.manage': 'allow',
    'identity.pii.read': 'allow',
  },
};

const MARIA = account();
const PLAYER = player();
const LINK = {
  user_id: PLAYER.id,
  nickname: 'kosta_10',
  linked_at: '2026-11-14T10:00:00Z',
  linked_by_label: 'Ольга В.',
};
const PATH = `/staff?account=${MARIA.id}`;

function setup(opts: { me?: Me; target?: typeof MARIA; linkAnswer?: () => Response | null } = {}) {
  const staff = staffServer({
    accounts: [opts.target ?? MARIA],
    nicknames: { [PLAYER.id]: PLAYER.nickname },
    linkAnswer: opts.linkAnswer,
  });
  const id = identityServer({ applications: [], players: { [PLAYER_EMAIL]: PLAYER } });
  renderApp(PATH, { me: opts.me ?? meLinks });
  return { staff: staff.seen, identity: id.seen, user: userEvent.setup() };
}

const panel = () => screen.findByRole('dialog', { name: 'Мария К.' });
const block = async () => within(await panel()).findByRole('region', { name: 'Учётка игрока' });

async function findInModal() {
  const m = await screen.findByRole('dialog', { name: 'Связь с учёткой игрока' });
  fireEvent.change(within(m).getByLabelText('Почта игрока'), { target: { value: PLAYER_EMAIL } });
  fireEvent.change(within(m).getByLabelText('Причина просмотра'), {
    target: { value: 'обращение 4812' },
  });
  fireEvent.click(within(m).getByRole('button', { name: 'Найти' }));
  return m;
}

test('не владелец (нет staff.player_links.manage) — блока нет', async () => {
  setup({ me: meOwner });
  const p = await panel();
  await within(p).findByText('maria.k@wf.example');
  expect(within(p).queryByRole('region', { name: 'Учётка игрока' })).toBeNull();
});

describe('связать', () => {
  test('поиск → «Связать с kosta_10?» → PUT; блок с ником; почта не в storage', async () => {
    const { staff, identity, user } = setup();
    const b = await block();
    expect(within(b).getByText('Не связан')).toBeInTheDocument();
    await user.click(within(b).getByRole('button', { name: 'Связать' }));
    const m = await findInModal();
    expect(
      await within(m).findByText('Связать Мария К. с учёткой игрока kosta_10?'),
    ).toBeInTheDocument();
    expect(identity.lookups).toEqual([
      {
        email: encodeURIComponent(PLAYER_EMAIL),
        reason: encodeURIComponent('обращение 4812'),
      },
    ]);
    // фокус окна и сеть во время подтверждения — второго поиска нет
    act(() => {
      fireEvent.focus(window);
      window.dispatchEvent(new Event('visibilitychange'));
      window.dispatchEvent(new Event('offline'));
      window.dispatchEvent(new Event('online'));
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(identity.lookups).toHaveLength(1);
    await user.click(within(m).getByRole('button', { name: 'Связать' }));
    expect(await within(b).findByText('kosta_10')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Связь с учёткой игрока' })).toBeNull();
    expect(staff.links).toEqual([
      { staffId: MARIA.id, userId: PLAYER.id, key: expect.any(String) },
    ]);
    expect(storageDump()).not.toContain(PLAYER_EMAIL);
    expect(storageDump()).not.toContain('4812');
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
  });

  test('409 player_link_taken — текст в окне, окно открыто', async () => {
    const { user } = setup({ linkAnswer: () => problem(409, 'staff.player_link_taken') });
    const b = await block();
    await user.click(within(b).getByRole('button', { name: 'Связать' }));
    const m = await findInModal();
    await within(m).findByText(/kosta_10\?/);
    await user.click(within(m).getByRole('button', { name: 'Связать' }));
    expect(
      await within(m).findByText('Эта учётка игрока уже связана с другим сотрудником'),
    ).toBeInTheDocument();
    expect(within(b).getByText('Не связан')).toBeInTheDocument();
  });

  test('игрок не найден — текст в форме окна, подтверждения нет', async () => {
    const { user } = setup();
    const b = await block();
    await user.click(within(b).getByRole('button', { name: 'Связать' }));
    const m = await screen.findByRole('dialog', { name: 'Связь с учёткой игрока' });
    fireEvent.change(within(m).getByLabelText('Почта игрока'), {
      target: { value: 'nobody@example.com' },
    });
    fireEvent.change(within(m).getByLabelText('Причина просмотра'), {
      target: { value: 'обращение 4812' },
    });
    fireEvent.click(within(m).getByRole('button', { name: 'Найти' }));
    expect(await within(m).findByText('Игрок с такой почтой не найден')).toBeInTheDocument();
    expect(within(m).queryByRole('button', { name: 'Связать' })).toBeNull();
  });
});

test('снять — ConfirmDialog с ником → DELETE, «Не связан»', async () => {
  const { staff, user } = setup({ target: account({ player_link: LINK }) });
  const b = await block();
  expect(within(b).getByText('kosta_10')).toBeInTheDocument();
  expect(within(b).getByText(/Ольга В\./)).toBeInTheDocument();
  await user.click(within(b).getByRole('button', { name: 'Снять связь' }));
  const d = await screen.findByRole('dialog', { name: 'Снять связь Мария К. с учёткой kosta_10?' });
  fireEvent.change(within(d).getByRole('textbox'), { target: { value: 'kosta_10' } });
  await user.click(within(d).getByRole('button', { name: 'Снять связь' }));
  expect(await within(b).findByText('Не связан')).toBeInTheDocument();
  expect(staff.unlinks).toEqual([{ staffId: MARIA.id, key: expect.any(String) }]);
  await waitFor(() => expect(document.activeElement).not.toBe(document.body));
});
