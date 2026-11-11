import { screen, within } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { approvalsServer, INCOMING } from '../../test/approvals';
import { renderApp } from '../../test/app';
import { meOwner } from '../../test/me';
import { staffServer } from '../../test/staff';

// IL2 (G8 I1 — подтверждающий видит всё): язык новичка из payload приглашения — строкой «Язык» в «Что будет
// сделано»; незнакомое значение — как есть; запрос до IL2 (поля нет) — строки нет.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

async function facts(locale?: unknown) {
  staffServer({ accounts: [] });
  approvalsServer({
    incoming: [
      {
        ...INCOMING,
        payload: { ...INCOMING.payload, ...(locale === undefined ? {} : { locale }) },
      },
    ],
  });
  renderApp('/approvals', { me: meOwner });
  return within((await screen.findAllByRole('definition'))[0]!.closest('dl')!);
}

test('приглашение на EN — «Язык: English»', async () => {
  const dl = await facts('en');
  expect(dl.getByText('Язык')).toBeInTheDocument();
  expect(dl.getByText('English')).toBeInTheDocument();
});

test('незнакомый язык — как есть', async () => {
  const dl = await facts('de');
  expect(dl.getByText('Язык').nextElementSibling).toHaveTextContent('de');
});

test('запрос до IL2 — строки языка нет', async () => {
  const dl = await facts();
  expect(dl.getByText('Иван П.')).toBeInTheDocument();
  expect(dl.queryByText('Язык')).toBeNull();
});
