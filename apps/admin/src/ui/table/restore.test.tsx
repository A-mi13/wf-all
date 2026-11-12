import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { geoServer } from '../../test/geo';
import { meOwner } from '../../test/me';
import { forgetTablePositions, rememberedRow, restoreKeyOf } from './restore';

// Возврат на ту же позицию на всём приложении (спека UI-фундамента §4.5, план UF2b.4): из карточки
// «Назад» (браузерная или общий возврат каркаса) — фокус на элементе, с которого ушли.

afterEach(() => {
  sessionStorage.clear();
  forgetTablePositions();
});

const openLink = async (name: string) =>
  within(await screen.findByRole('article', { name })).getByRole('link', {
    name: `Открыть: ${name}`,
  });

describe('возврат из карточки', () => {
  test('браузерная «Назад» — фокус на «Открыть» карточки, с которой ушли', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meOwner });
    await userEvent.setup().click(await openLink('Пятигорск'));
    await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    await app.back();
    await waitFor(() => expect(document.activeElement).toHaveAccessibleName('Открыть: Пятигорск'));
    expect(app.at()).toBe('/geo/cities?country=RU');
  });

  test('общий возврат каркаса («Назад: Города и запуск») — тоже фокус на карточке', async () => {
    geoServer();
    renderApp('/geo/cities?country=RU', { me: meOwner });
    const user = userEvent.setup();
    await user.click(await openLink('Пятигорск'));
    await user.click(await screen.findByRole('link', { name: 'Назад: Города и запуск' }));
    await waitFor(() => expect(document.activeElement).toHaveAccessibleName('Открыть: Пятигорск'));
  });

  test('другой сотрудник (каркас key={me.id}) — память строк стёрта', async () => {
    geoServer();
    const app = renderApp('/geo/cities?country=RU', { me: meOwner });
    await userEvent.setup().click(await openLink('Пятигорск'));
    await screen.findByRole('link', { name: 'Назад: Города и запуск' });
    const key = restoreKeyOf({ pathname: '/geo/cities', search: { country: 'RU' } });
    expect(rememberedRow(key)).toBeDefined();
    app.me.current = { ...meOwner, id: '0192f1c4-9999-7b21-9c5d-000000000099' };
    await app.refetchMe();
    await waitFor(() => expect(rememberedRow(key)).toBeUndefined());
  });

  test('роутер включает scrollRestoration (m7): история окна — manual', async () => {
    geoServer();
    window.history.scrollRestoration = 'auto';
    renderApp('/geo/cities', { me: meOwner });
    await screen.findByRole('heading', { level: 1, name: 'Города и запуск' });
    expect(window.history.scrollRestoration).toBe('manual');
  });
});
