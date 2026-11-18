import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp } from '../../test/app';
import { captainApplication, identityServer, meModCaptains as meReader } from '../../test/identity';
import { meOwner } from '../../test/me';
import { http, server } from '../../test/server';

// Счётчик «Капитаны» в меню (план I-4, Task 8): ожидающие заявки (status=pending&limit=100) фоном
// (X-WF-Background: 1), «99+» — 100 строк или next_cursor; без права — ни пункта, ни запроса.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

// «Мой доступ» — экран без заявок: запросы очереди идут только от счётчика.
function meServer() {
  server.use(
    http.get('/v1/staff/me/mfa', ({ response }) =>
      response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 8 }),
    ),
    http.get('/v1/staff/me/sessions', ({ response }) => response(200).json({ items: [] })),
    http.get('/v1/geo/cities', ({ response }) =>
      response(200).json({ items: [], next_cursor: null }),
    ),
  );
}

const many = (n: number) =>
  Array.from({ length: n }, (_, i) =>
    captainApplication({ id: `0193a1c9-7f80-7d91-8a1b-${String(i + 100).padStart(12, '0')}` }),
  );
const counter = () => screen.getByRole('link', { name: /^Капитаны/ });

describe('счётчик «Капитаны»', () => {
  test('число у пункта; запрос — pending, limit=100, фоном', async () => {
    meServer();
    const { seen } = identityServer({ applications: many(3) });
    renderApp('/me', { me: meReader });
    await waitFor(() => expect(counter()).toHaveAccessibleName('Капитаны 3 заявки ждут решения'));
    expect(seen.lists[0]?.get('status')).toBe('pending');
    expect(seen.lists[0]?.get('limit')).toBe('100');
    expect(seen.background).toEqual(['1']);
  });

  test('100 строк — «99+»', async () => {
    meServer();
    identityServer({ applications: many(120), pageSize: 100 });
    renderApp('/me', { me: meReader });
    await waitFor(() => expect(within(counter()).getByText('99+')).toBeInTheDocument());
  });

  test('меньше 100 строк, но есть next_cursor — «99+»', async () => {
    meServer();
    identityServer({ applications: many(30), pageSize: 20 });
    renderApp('/me', { me: meReader });
    await waitFor(() => expect(within(counter()).getByText('99+')).toBeInTheDocument());
  });

  test('без права — ни пункта, ни запроса', async () => {
    meServer();
    const { seen } = identityServer({ applications: many(3) });
    renderApp('/me', { me: meOwner });
    await screen.findByRole('heading', { level: 1, name: 'Мой доступ' });
    expect(screen.queryByRole('link', { name: /^Капитаны/ })).toBeNull();
    expect(seen.lists).toEqual([]);
  });
});
