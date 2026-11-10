import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../test/app';
import { type MediaDevice, stubMedia } from '../test/media';
import { meOwner } from '../test/me';
import { staffServer, TEAM } from '../test/staff';
import { DESKTOP_ONLY_PERMISSIONS, isDesktopOnly, isDesktopOnlyDecision } from './desktopOnly';
import { reauthBus } from './reauth';

// «Только с компьютера» — действие и устройство, а не раздел и ширина (спека UI-фундамента §3.2):
// на карманном устройстве (палец и экран уже compact) действия, которые выдают или снимают права,
// заменены пометкой; компьютер при любом масштабе (640 px + мышь) действий не теряет.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const NOTICE = 'Выдача и снятие прав — только с компьютера';

describe('реестр', () => {
  test('ровно 7 прав §3.2', () => {
    expect([...DESKTOP_ONLY_PERMISSIONS].sort()).toEqual([
      'staff.access.reset',
      'staff.access.reset_elevated',
      'staff.roles.grant',
      'staff.roles.grant_elevated',
      'staff.roles.revoke',
      'staff.roles.revoke_admin',
      'staff.roles.revoke_owner',
    ]);
  });

  test('isDesktopOnly — хоть одно право в реестре', () => {
    expect(isDesktopOnly('staff.roles.grant')).toBe(true);
    expect(isDesktopOnly('staff.sessions.revoke')).toBe(false);
    expect(isDesktopOnly('staff.invites.resend')).toBe(false);
    expect(isDesktopOnly(['staff.sessions.revoke', 'staff.roles.revoke_owner'])).toBe(true);
    expect(isDesktopOnly([])).toBe(false);
  });

  test('решения подтверждений — любой staff.*, включая незнакомое будущее; geo.* — нет', () => {
    expect(isDesktopOnlyDecision('staff.role_change')).toBe(true);
    expect(isDesktopOnlyDecision('staff.future_action')).toBe(true);
    expect(isDesktopOnlyDecision('geo.country_rules')).toBe(false);
    expect(isDesktopOnlyDecision('staffing.x')).toBe(false);
  });
});

async function openStaff(device: MediaDevice) {
  stubMedia(device);
  staffServer({ accounts: TEAM });
  renderApp('/staff', { me: meOwner });
  await screen.findByRole('heading', { level: 1, name: 'Роли и доступ' });
  const table = await screen.findByRole('table', { name: 'Сотрудники' });
  // цель — админ: сеансы видны владельцу только при grant_elevated (roles.ts)
  await userEvent
    .setup()
    .click(await within(table).findByRole('button', { name: 'Управлять: Игорь Л.' }));
  const panel = screen.getByRole('dialog', { name: 'Игорь Л.' });
  await within(panel).findByRole('region', { name: 'Сеансы' });
  return panel;
}

describe('карманное устройство: 390 px + палец', () => {
  test('нет «Пригласить», «Сохранить роль», «Сбросить доступ», «Выключить»; есть пометка и «Завершить все сеансы» у цели-админа', async () => {
    const panel = await openStaff({ width: 390, pointer: 'coarse' });
    expect(screen.queryByRole('button', { name: 'Пригласить сотрудника' })).toBeNull();
    expect(
      within(panel).queryByRole('button', { name: /^(Сохранить|Отправить на подтверждение)$/ }),
    ).toBeNull();
    expect(within(panel).queryByRole('button', { name: 'Запросить сброс доступа' })).toBeNull();
    expect(within(panel).queryByRole('button', { name: 'Выключить' })).toBeNull();
    expect(within(panel).getAllByText(NOTICE).length).toBeGreaterThanOrEqual(3);
    expect(screen.getAllByText(NOTICE).length).toBeGreaterThan(
      within(panel).getAllByText(NOTICE).length,
    );
    expect(within(panel).getByRole('button', { name: 'Завершить все сеансы' })).toBeInTheDocument();
  });
});

describe.each([
  ['640 px + мышь (масштаб на ноутбуке)', { width: 640, pointer: 'fine' }],
  ['1280 px + мышь', { width: 1280, pointer: 'fine' }],
] as const)('компьютер: %s', (_, device) => {
  test('все кнопки есть, пометки нет', async () => {
    const panel = await openStaff(device);
    expect(screen.getByRole('button', { name: 'Пригласить сотрудника' })).toBeInTheDocument();
    expect(
      within(panel).getByRole('button', { name: /^(Сохранить|Отправить на подтверждение)$/ }),
    ).toBeInTheDocument();
    expect(
      within(panel).getByRole('button', { name: 'Запросить сброс доступа' }),
    ).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Выключить' })).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Завершить все сеансы' })).toBeInTheDocument();
    expect(screen.queryByText(NOTICE)).toBeNull();
  });
});
