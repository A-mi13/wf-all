import { screen } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { stubMedia } from '../../test/media';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { ActionGate } from './ActionGate';

// ActionGate (спека UI-фундамента §3.2): на карманном устройстве действие по праву из реестра —
// пометка, не кнопка и не в Tab-порядке; право не из реестра и компьютер — само действие.
const NOTICE = 'Выдача и снятие прав — только с компьютера';

const gate = (permission: string | readonly string[], compact?: boolean) => (
  <ActionGate permission={permission} compact={compact}>
    <Button>Выключить</Button>
  </ActionGate>
);

describe('ActionGate', () => {
  test('компьютер — действие', () => {
    stubMedia({ width: 1280, pointer: 'fine' });
    renderWithIntl(gate('staff.roles.revoke'));
    expect(screen.getByRole('button', { name: 'Выключить' })).toBeInTheDocument();
    expect(screen.queryByText(NOTICE)).toBeNull();
  });

  test('телефон и право из реестра — пометка-блок, не кнопка и не в Tab-порядке', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { user, container } = renderWithIntl(
      gate(['staff.sessions.revoke', 'staff.roles.revoke']),
    );
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText(NOTICE)).toBeInTheDocument();
    expect(container.querySelector('[tabindex], button, a, input')).toBeNull();
    await user.tab();
    expect(document.body).toHaveFocus();
  });

  test('compact — одна строка с иконкой', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { container } = renderWithIntl(gate('staff.access.reset', true));
    const notice = screen.getByText(NOTICE).closest('[data-desktop-only]');
    expect(notice).toHaveAttribute('data-desktop-only', 'compact');
    expect(container.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
  });

  test('телефон и право не из реестра (завершить сеансы) — действие', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithIntl(gate('staff.sessions.revoke'));
    expect(screen.getByRole('button', { name: 'Выключить' })).toBeInTheDocument();
  });
});
