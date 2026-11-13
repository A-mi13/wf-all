import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { renderApp } from '../../test/app';
import { cityDetails, geoImport, importsServer, meDirectoriesAdmin } from '../../test/geo';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { http, server } from '../../test/server';

type Reconcile = NonNullable<components['schemas']['GeoImportReport']['reconciled']>;

// Отчёт сверки (план B, Task 20): группы — связанные, неоднозначные, конфликтные (кандидаты и «Привязать» —
// linkGeoCity выбранного geoname_id, после успеха «привязан»), не найденные, missing, пропущено без пояса. Раскрытие —
// ?import=<id>, другой — switch, повторный щелчок сворачивает.

afterEach(() => sessionStorage.clear());

const IMP = geoImport({ id: '0192f1f5-0000-7c3d-8e4f-0000000000aa' });
const OTHER = geoImport({ id: '0192f1f5-0000-7c3d-8e4f-0000000000bb', country_code: 'KZ' });
const CITY = '7a1d5e9c-2b4f-4c8a-b3e6-1f0d9c8b7a65';
const REPORT: Reconcile = {
  linked: [
    { city_id: '3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47', slug: 'stavropol', geoname_id: 487846 },
  ],
  ambiguous: [
    {
      city_id: CITY,
      slug: 'mihaylovsk',
      candidates: [
        {
          geoname_id: 526815,
          name: 'Михайловск',
          region_name: 'Ставропольский край',
          population: 9000,
        },
        {
          geoname_id: 526816,
          name: 'Михайловск',
          region_name: 'Свердловская область',
          population: 12000,
        },
      ],
    },
  ],
  not_found: [{ city_id: '9c3b1a7e-4d2f-4e8a-b5c6-7d8e9f0a1b2c', slug: 'zarechny-3' }],
  conflict: [],
  missing: [524901],
  skipped_timezone: 3,
};

function setup(link: (body: unknown) => Response | undefined = () => undefined) {
  sectionsServer();
  const srv = importsServer({ imports: [IMP, OTHER], reports: { [IMP.id]: REPORT } });
  const linked: { path: string; body: unknown; key: string | null }[] = [];
  server.use(
    http.post('/v1/geo/cities/{cityId}/link', async ({ request, params, response }) => {
      const body: unknown = await request.json();
      linked.push({
        path: `/v1/geo/cities/${params.cityId}/link`,
        body,
        key: request.headers.get('Idempotency-Key'),
      });
      const own = link(body);
      if (own) return response.untyped(own);
      return response(200).json(
        cityDetails({ id: CITY, geoname_id: (body as { geoname_id: number }).geoname_id }),
      );
    }),
  );
  const app = renderApp(`/geo/imports?import=${IMP.id}`, { me: meDirectoriesAdmin });
  return { srv, app, linked, user: userEvent.setup() };
}

const report = () => screen.findByRole('region', { name: 'Сверка городов' });

test('группы: связанные, неоднозначные с кандидатами, не найденные, missing, пропущено', async () => {
  setup();
  const r = await report();
  expect(await within(r).findByText('Привязаны автоматически · 1')).toBeInTheDocument();
  expect(within(r).getByText('stavropol → GeoNames 487846')).toBeInTheDocument();
  expect(within(r).getByText('Несколько подходящих мест · 1')).toBeInTheDocument();
  expect(within(r).getByText('Михайловск · Свердловская область')).toBeInTheDocument();
  expect(within(r).getByText(/жителей: 12\s000/)).toBeInTheDocument();
  expect(within(r).getByText('Конфликт — проверить вручную: Нет')).toBeInTheDocument();
  expect(within(r).getByRole('button', { name: 'Открыть город zarechny-3' })).toBeInTheDocument();
  expect(within(r).getByText('524901')).toBeInTheDocument();
  expect(within(r).getByText('Пропущено мест без часового пояса: 3')).toBeInTheDocument();
});

test('«Привязать» шлёт linkGeoCity с выбранным geoname_id, строка — «привязан», другие кнопки сняты', async () => {
  const { linked, user } = setup();
  const r = await report();
  const buttons = await within(r).findAllByRole('button', {
    name: 'Привязать mihaylovsk к месту Михайловск',
  });
  expect(buttons).toHaveLength(2);
  await user.click(buttons[0]!);
  await waitFor(() => expect(linked).toHaveLength(1));
  expect(linked[0]?.path).toBe(`/v1/geo/cities/${CITY}/link`);
  expect(linked[0]?.body).toEqual({ geoname_id: 526815 });
  expect(await within(r).findByText('привязан')).toBeInTheDocument();
  expect(within(r).queryByRole('button', { name: /^Привязать/ })).toBeNull();
});

test('второй кандидат — его geoname_id', async () => {
  const { linked, user } = setup();
  const r = await report();
  const buttons = await within(r).findAllByRole('button', { name: /^Привязать mihaylovsk/ });
  await user.click(buttons[1]!);
  await waitFor(() => expect(linked).toHaveLength(1));
  expect(linked[0]?.body).toEqual({ geoname_id: 526816 });
  expect(linked[0]?.key).toMatch(/^[0-9a-f-]{36}$/);
  const marked = (await within(r).findByText('привязан')).closest('li');
  expect(marked).toHaveTextContent('Свердловская область');
  // ревью m-4 (WCAG 2.4.3): «Привязать» исчезли — фокус на городе, не на body
  await waitFor(() =>
    expect(document.activeElement).toBe(
      within(r).getByRole('button', { name: 'Открыть город mihaylovsk' }),
    ),
  );
});

// Ревью m-5: отчёт — панелью над журналом во всю ширину, не в последнем столбце; виден и по ссылке на импорт,
// которого нет в отфильтрованном журнале (m-2: «Открыть отчёт» задачи другой страны не молчит).
test('отчёт — вне таблицы журнала; ?country= другой страны — отчёт всё равно виден', async () => {
  sectionsServer();
  importsServer({ imports: [IMP, OTHER], reports: { [IMP.id]: REPORT } });
  renderApp(`/geo/imports?country=KZ&import=${IMP.id}`, { me: meDirectoriesAdmin });
  const r = await report();
  expect(await within(r).findByText('Привязаны автоматически · 1')).toBeInTheDocument();
  const table = await screen.findByRole('table', { name: 'Журнал импортов' });
  expect(table.contains(r)).toBe(false);
  expect(within(r).getByRole('heading', { name: 'Отчёт импорта RU' })).toBeInTheDocument();
});

test('409 geo.city_already_linked — текст под городом, кнопки остаются', async () => {
  const { user } = setup(() => problem(409, 'geo.city_already_linked'));
  const r = await report();
  const buttons = await within(r).findAllByRole('button', { name: /^Привязать mihaylovsk/ });
  await user.click(buttons[0]!);
  expect(await within(r).findByRole('alert')).toBeInTheDocument();
  expect(within(r).queryByText('привязан')).toBeNull();
});

test('отчёта нет (импорт упал до сверки) — текст и причина', async () => {
  sectionsServer();
  const failed = geoImport({ id: IMP.id, status: 'failed', error: 'download timeout' });
  importsServer({ imports: [failed] });
  renderApp(`/geo/imports?import=${IMP.id}`, { me: meDirectoriesAdmin });
  const r = await report();
  expect(
    await within(r).findByText('Отчёта сверки нет: импорт идёт или упал до сверки.'),
  ).toBeInTheDocument();
  expect(within(r).getByText('Причина: download timeout')).toBeInTheDocument();
});

test('раскрытие — ?import=, другой — switch (replace), повторный щелчок сворачивает', async () => {
  const { app, user } = setup();
  await report();
  const toggles = await screen.findAllByRole('button', { name: /^Отчёт:/ });
  expect(toggles[0]).toHaveAttribute('aria-expanded', 'true');
  await user.click(screen.getByRole('button', { name: 'Отчёт: KZ' }));
  await waitFor(() => expect(app.at()).toBe(`/geo/imports?import=${OTHER.id}`));
  await user.click(screen.getByRole('button', { name: 'Отчёт: KZ' }));
  await waitFor(() => expect(app.at()).toBe('/geo/imports'));
});
