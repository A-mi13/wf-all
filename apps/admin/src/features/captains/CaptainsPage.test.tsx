import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp } from '../../test/app';
import {
  captainApplication,
  DECIDER,
  identityServer,
  meCaptains,
  meModCaptains,
  OTHER_CITY_APPLICATION,
} from '../../test/identity';
import { CITY_OTHER, CITY_OWN, meOwner } from '../../test/me';
import { openOptions, pick } from '../../test/pick';
import { staffServer } from '../../test/staff';

// «Капитаны» (план I-4, Task 8; спека identity §7.4): очередь заявок — DataTable с ником, городом, возрастом,
// стажем и датой; статус и город — search-параметры; модератору — только свои города в фильтре; без права —
// ForbiddenState каркаса.

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
});

const page = () => screen.findByRole('heading', { level: 1, name: 'Заявки капитанов' });
const table = () => screen.findByRole('table', { name: 'Заявки в капитаны' });
const rows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1);
const lists = (seen: { lists: URLSearchParams[]; background: (string | null)[] }) =>
  seen.lists.filter((_, i) => seen.background[i] === null);

describe('очередь', () => {
  test('строки из мока: ник, город, возраст, стаж; запрос — pending', async () => {
    staffServer();
    const { seen } = identityServer({
      applications: [captainApplication(), OTHER_CITY_APPLICATION],
    });
    renderApp('/captains', { me: meCaptains });
    await page();
    const t = await table();
    await waitFor(() => expect(rows(t)).toHaveLength(2));
    const first = rows(t)[0] as HTMLElement;
    expect(first).toHaveTextContent('kosta_10');
    await waitFor(() => expect(first).toHaveTextContent('Ставрополь'));
    expect(first).toHaveTextContent('28 лет');
    expect(first).toHaveTextContent('256 дней');
    expect(rows(t)[1]).toHaveTextContent('19 лет');
    expect(rows(t)[1]).toHaveTextContent('12 дней');
    for (const h of ['Игрок', 'Город', 'Возраст', 'Стаж', 'Подана'])
      expect(within(t).getByRole('columnheader', { name: h })).toBeInTheDocument();
    expect(lists(seen)[0]?.get('status')).toBe('pending');
  });

  test('чип статуса — в адресе и в запросе; решённая — итог и кто решил', async () => {
    staffServer();
    const { seen } = identityServer({
      applications: [
        captainApplication(),
        captainApplication({
          id: '0193a1c9-7f80-7d91-8a1b-000000000009',
          nickname: 'old_rejected',
          status: 'rejected',
          decided_at: '2026-10-13T12:00:00Z',
          decided_by_label: DECIDER,
          rejection_code: 'insufficient_info',
        }),
      ],
    });
    const app = renderApp('/captains', { me: meCaptains });
    await page();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Отклонённые' }));
    await waitFor(() => expect(app.at()).toBe('/captains?status=rejected'));
    const t = await table();
    await waitFor(() => expect(rows(t)[0]).toHaveTextContent('old_rejected'));
    expect(rows(t)[0]).toHaveTextContent('Отклонена');
    expect(rows(t)[0]).toHaveTextContent(DECIDER);
    expect(lists(seen).at(-1)?.get('status')).toBe('rejected');

    // назад к ожидающим — параметр снят
    await user.click(screen.getByRole('button', { name: 'Ждут решения' }));
    await waitFor(() => expect(app.at()).toBe('/captains'));
  });

  test('незнакомый статус из адреса — ожидающие', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp('/captains?status=expired', { me: meCaptains });
    await table();
    await waitFor(() => expect(lists(seen)[0]?.get('status')).toBe('pending'));
  });

  test('«Показать ещё» — следующая страница по курсору', async () => {
    staffServer();
    identityServer({ applications: [captainApplication(), OTHER_CITY_APPLICATION], pageSize: 1 });
    renderApp('/captains', { me: meCaptains });
    const t = await table();
    await waitFor(() => expect(rows(t)).toHaveLength(1));
    await userEvent.setup().click(screen.getByRole('button', { name: 'Показать ещё' }));
    await waitFor(() => expect(rows(t)).toHaveLength(2));
    expect(rows(t)[1]).toHaveTextContent('misha_gk');
  });

  test('пусто — «Очередь пуста»', async () => {
    staffServer();
    identityServer({ applications: [] });
    renderApp('/captains', { me: meCaptains });
    expect(await screen.findByText('Очередь пуста')).toBeInTheDocument();
  });

  test('город — фильтр в адресе и в запросе', async () => {
    staffServer();
    const { seen } = identityServer({
      applications: [captainApplication(), OTHER_CITY_APPLICATION],
    });
    const app = renderApp('/captains', { me: meCaptains });
    await table();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Ещё фильтры' }));
    await pick(user, 'Город', /^Пятигорск/);
    await waitFor(() => expect(app.at()).toBe(`/captains?city=${CITY_OTHER}`));
    await waitFor(() => expect(lists(seen).at(-1)?.get('city_id')).toBe(CITY_OTHER));
    const t = await table();
    await waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(rows(t)[0]).toHaveTextContent('misha_gk');
  });

  test('модератор — в фильтре города только его города', async () => {
    staffServer();
    identityServer();
    renderApp('/captains', { me: meModCaptains });
    await table();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Ещё фильтры' }));
    await waitFor(async () =>
      expect((await openOptions(user, 'Город')).map((o) => o.value)).toEqual(['', CITY_OWN]),
    );
  });

  test('без права — «Нет доступа к разделу», запросов очереди нет', async () => {
    staffServer();
    const { seen } = identityServer();
    renderApp('/captains', { me: meOwner });
    expect(await screen.findByText('Нет доступа к разделу')).toBeInTheDocument();
    expect(seen.lists).toEqual([]);
  });
});
