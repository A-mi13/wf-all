import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { approvalsServer, IGOR } from '../../test/approvals';
import { COUNTRIES, cityEditServer, geoServer, meGeoAdmin } from '../../test/geo';
import { CITY_OWN, meOwner } from '../../test/me';
import { pick } from '../../test/pick';
import { approval, staffServer } from '../../test/staff';
import { geoKeys } from './queries';

// G9-C, I-1 (ревью G8:124): список стран (open_cities — pilot и live) устаревает после смены статуса города и после
// исполненного в «Подтверждениях» запроса geo.* — его перечитывают, а не показывают прежнее число. Активация — в
// ActivateDrawer.test.tsx («I-1: успешная активация…»).

afterEach(() => sessionStorage.clear());

const countriesQuery = (app: ReturnType<typeof renderApp>) =>
  app.queryClient.getQueryCache().find({ queryKey: geoKeys.countries('ru'), exact: true });

describe('I-1: кэш стран', () => {
  test('смена статуса города — список стран помечен устаревшим', async () => {
    cityEditServer();
    const app = renderApp(`/geo/cities/${CITY_OWN}`, { me: meGeoAdmin });
    await screen.findByRole('heading', { level: 1, name: 'Ставрополь' });
    app.queryClient.setQueryData(geoKeys.countries('ru'), { items: COUNTRIES });
    expect(countriesQuery(app)?.state.isInvalidated).toBe(false);
    const card = screen.getByRole('region', { name: 'Статус' });
    const user = userEvent.setup();
    await pick(user, within(card).getByRole('combobox', { name: 'Статус города' }), 'pilot');
    await user.click(within(card).getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Статус сохранён');
    await waitFor(() => expect(countriesQuery(app)?.state.isInvalidated).toBe(true));
  });

  test('одобрение geo.country_rules с итогом executed — «Справочники» перечитаны', async () => {
    staffServer();
    geoServer();
    const rules = approval({
      id: '0192f1e2-4c5d-7e6f-a081-0000000000c7',
      action: 'geo.country_rules',
      permission: 'geo.countries.rules',
      payload: {
        v: 1,
        code: 'RU',
        version: 3,
        patch: { currency: 'KZT' },
        from: { currency: 'RUB' },
        to: { currency: 'KZT' },
      },
      requested_by: IGOR,
    });
    approvalsServer({ incoming: [rules] });
    const me = {
      ...meOwner,
      permissions: { ...meOwner.permissions, 'geo.countries.rules': 'dual' as const },
    };
    const app = renderApp('/approvals', { me });
    const table = await screen.findByRole('table', { name: 'Входящие запросы' });
    const [row] = within(table).getAllByRole('row').slice(1);
    app.queryClient.setQueryData(geoKeys.countries('ru'), { items: COUNTRIES });
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    await waitFor(() => expect(countriesQuery(app)?.state.isInvalidated).toBe(true));
  });

  test('одобрение staff.* — «Справочники» не трогаются', async () => {
    staffServer();
    approvalsServer();
    const app = renderApp('/approvals', { me: meOwner });
    const table = await screen.findByRole('table', { name: 'Входящие запросы' });
    const [row] = within(table).getAllByRole('row').slice(1);
    app.queryClient.setQueryData(geoKeys.countries('ru'), { items: COUNTRIES });
    await userEvent.setup().click(within(row!).getByRole('button', { name: 'Подтвердить' }));
    await screen.findByText('Подтверждено и выполнено.');
    expect(countriesQuery(app)?.state.isInvalidated).toBe(false);
  });
});
