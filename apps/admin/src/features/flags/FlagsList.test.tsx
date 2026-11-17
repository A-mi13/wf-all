import { screen, within } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { AUTH_FLAGS, flagsServer } from '../../test/flags';
import { flag, meDirectoriesAdmin } from '../../test/geo';
import { stubMedia } from '../../test/media';
import { sectionsServer } from '../../test/sections';

// Список флагов на телефоне (план I-4, R3): флаг только «везде» не раскрывается городами — кнопки «Города» нет,
// переключатель «Везде» есть; правила requires — подписью, как в матрице.

afterEach(() => sessionStorage.clear());

test('global_only — без раскрытия городов; requires — подпись', async () => {
  stubMedia({ width: 390, pointer: 'coarse' });
  sectionsServer();
  flagsServer([flag(), ...AUTH_FLAGS]);
  renderApp('/flags', { me: meDirectoriesAdmin });
  const google = await screen.findByRole('listitem', { name: 'Регистрация через Google' });
  expect(
    within(google).getByRole('switch', { name: 'Регистрация через Google: везде' }),
  ).toBeEnabled();
  expect(
    within(google).queryByRole('button', { name: 'Города: Регистрация через Google' }),
  ).toBeNull();
  expect(within(google).getByText('Требует «Регистрация через Apple»')).toBeInTheDocument();
  expect(within(google).getByText('Только везде')).toBeInTheDocument();
  const subs = screen.getByRole('listitem', { name: 'Подписки' });
  expect(within(subs).getByRole('button', { name: 'Города: Подписки' })).toBeInTheDocument();
});
