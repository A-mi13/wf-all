import { type BrowserContext, expect, type Page, test } from '@playwright/test';
import { t } from './keys';
import { pairOf } from './env';
import { acceptAndSetup, api, bootstrap, newStaff, receiveInvite } from './ui';

// Живой сценарий спеки geo §11 п. 5 (план B, Task 23): импорт страны и активация города поверх настоящих admin-api,
// воркера, Postgres и HTTPS-фикстуры GeoNames (scripts/e2e-admin.sh, WORKER_GEONAMES_BASE_URL — фикстура, внешней сети
// нет) — и запись в журнале. Порядок «импорт → активация» (R16): активация требует места в источнике. Идёт в одном
// проекте (geoProject: prod — Chromium под настоящей CSP, без него — chromium): место активируется в общей базе один
// раз за прогон, база пересоздаётся каждым прогоном. Нарушения CSP собираются на всём пути и проверяются в конце.

const COUNTRY = 'RU';
// Михайловск Свердловской области (выдержка testdata). Тёзка из сида (Ставропольский, 493702) живёт под slug
// «mihaylovsk» (0010_seed) — первый кандидат domain.SlugCandidates «mikhaylovsk» (ascii GeoNames) свободен, суффикс
// региона не нужен (план ждал «mikhaylovsk-sverdlovsk-oblast» — расхождение в отчёте G10-E)
const PLACE = 526815;
const SLUG = 'mikhaylovsk';
const REGION = /Свердловская область/i;
// импорт выдержки идёт секунды; запас — на опрос строки задачи и подъём воркера под нагрузкой прогона
const IMPORT_TIMEOUT = 90_000;

const owner = newStaff('owner-geo');

type Job = { job_id: number; state: string; import_id: string | null };
type ImportDetail = {
  id: string;
  status: string;
  started_by: string | null;
  started_by_label: string | null;
  reconciled: { conflict: unknown[]; linked: { slug: string; geoname_id: number }[] } | null;
};
type City = { id: string; slug: string; status: string; geoname_id: number | null };
type AuditRecord = {
  action: string;
  actor_role: string;
  actor_label: string | null;
  actor_staff_id: string | null;
  object_id: string | null;
};

test.describe.configure({ mode: 'serial' });

let ctx: BrowserContext;
let page: Page;
let jobId = 0;
let cityId = '';
const violations: string[] = [];

// Нарушения CSP — до скриптов документа (как prod.spec): у chromium-проекта (dev Vite без CSP) список пуст всегда
async function collect(): Promise<void> {
  violations.push(
    ...(await page.evaluate(() => (window as unknown as { __wfCsp: string[] }).__wfCsp.splice(0))),
  );
}

test.beforeAll(async ({ browser }, info) => {
  ctx = await browser.newContext({
    baseURL: info.project.use.baseURL,
    locale: 'ru-RU',
    viewport: { width: 1280, height: 800 },
  });
  ctx.setDefaultTimeout(20_000);
  ctx.setDefaultNavigationTimeout(30_000);
  await ctx.addInitScript(() => {
    const w = window as unknown as { __wfCsp: string[] };
    w.__wfCsp = [];
    document.addEventListener('securitypolicyviolation', (e) => {
      w.__wfCsp.push(`${location.pathname}: ${e.violatedDirective} ${e.blockedURI || ''}`.trim());
    });
  });
  page = await ctx.newPage();
  page.on('console', (m) => {
    if (/Content Security Policy/i.test(m.text())) violations.push(`console: ${m.text()}`);
  });
});

test.afterAll(async () => {
  await ctx?.close();
});

test('1. владелец: приглашение, 2FA → стартовая «Главная»', async () => {
  const info = test.info();
  const run = await bootstrap(pairOf(info.project.name), owner);
  expect(run.code, run.stderr).toBe(0);
  await receiveInvite(owner);
  await acceptAndSetup(page, owner);
  expect(new URL(page.url()).pathname).toBe('/');
  await expect(page.getByRole('heading', { level: 1, name: t('nav.home') })).toBeVisible();
  await collect();
});

test('2. «Импорт GeoNames»: запуск RU → задача выполнена воркером → журнал «завершён», кто запустил — владелец, конфликтов нет', async () => {
  await page.getByRole('link', { name: t('nav.imports'), exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: t('nav.imports') })).toBeVisible();

  // журнал пуст: «Запустить импорт» — и в шапке, и в пустом состоянии; первая — шапка
  await page
    .getByRole('button', { name: t('geo.imports.start'), exact: true })
    .first()
    .click();
  const dialog = page.getByRole('dialog', { name: t('geo.imports.dialog.title') });
  await expect(dialog).toBeVisible();
  // страна — combobox + option (пузырь Popover под настоящей CSP в prod)
  await dialog
    .getByRole('combobox', { name: t('geo.imports.dialog.country'), exact: true })
    .click();
  await page.getByRole('option', { name: new RegExp(`\\(${COUNTRY}\\)$`) }).click();
  const started = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/v1/geo/imports',
  );
  await dialog.getByRole('button', { name: t('geo.imports.dialog.submit'), exact: true }).click();
  const res = await started;
  expect(res.status()).toBe(202);
  jobId = ((await res.json()) as { job_id: number }).job_id;
  expect(jobId).toBeGreaterThan(0);
  await expect(dialog).toHaveCount(0);
  // «в очереди»: задачу воркер берёт за миллисекунды — чип queued не наблюдаем надёжно; постановку подтверждают тост
  // и строка задачи, дальше — опрос до конечного состояния
  await expect(
    page.getByText(t('geo.imports.started').replace('{country}', COUNTRY)),
  ).toBeVisible();
  const job = page.getByRole('region', { name: t('geo.imports.job.label') });
  await expect(job).toBeVisible();
  await expect(job.getByText(t('geo.imports.job.states.completed'), { exact: true })).toBeVisible({
    timeout: IMPORT_TIMEOUT,
  });

  // строка журнала: статус «завершён», «кто запустил» — владелец (из аудита постановки, R3)
  const row = page
    .getByRole('table', { name: t('geo.imports.label') })
    .getByRole('row')
    .filter({ hasText: owner.name });
  await expect(row).toHaveCount(1);
  await expect(row.getByText(t('geo.imports.statuses.succeeded'), { exact: true })).toBeVisible();
  await expect(row).toContainText(COUNTRY);

  // отчёт сверки: конфликтов нет
  await job.getByRole('button', { name: t('geo.imports.job.open_report'), exact: true }).click();
  const report = page.getByRole('region', { name: t('geo.imports.report.label') });
  await expect(report).toBeVisible();
  await expect(
    report.getByText(
      `${t('geo.imports.report.groups.conflict')}: ${t('geo.imports.report.empty_group')}`,
      { exact: true },
    ),
  ).toBeVisible();
  await collect();

  // те же факты в API: задача → строка журнала
  const j = await api<Job>(page, 'GET', `/v1/geo/imports/jobs/${jobId}`);
  expect(j.status).toBe(200);
  expect(j.body).toMatchObject({ job_id: jobId, state: 'completed' });
  expect(j.body.import_id).toBeTruthy();
  const imp = await api<ImportDetail>(page, 'GET', `/v1/geo/imports/${j.body.import_id}`);
  expect(imp.status).toBe(200);
  expect(imp.body).toMatchObject({ status: 'succeeded', started_by_label: owner.name });
  expect(imp.body.reconciled?.conflict).toEqual([]);
});

test('3. «Города и запуск»: активировать Михайловск (526815, Свердловская область) пилотом → страница города', async () => {
  await page.getByRole('link', { name: t('nav.cities'), exact: true }).click();
  await page.getByRole('button', { name: t('geo.activate.open'), exact: true }).click();
  const drawer = page.getByRole('dialog', { name: t('geo.activate.title') });
  await expect(drawer).toBeVisible();
  await drawer.getByRole('searchbox').fill('Михайловск');
  const places = drawer.getByRole('listbox', { name: t('geo.activate.places_label') });
  const place = places.getByRole('option').filter({ hasText: REGION });
  await expect(place).toHaveCount(1);
  await place.click();
  await drawer.getByRole('button', { name: new RegExp(`^${t('geo.activate.choose')}`) }).click();

  // статус — combobox + option
  await drawer.getByRole('combobox', { name: t('geo.activate.status'), exact: true }).click();
  await page.getByRole('option', { name: t('geo.status.pilot'), exact: true }).click();
  const created = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/v1/geo/cities',
  );
  await drawer.getByRole('button', { name: t('geo.activate.submit'), exact: true }).click();
  const res = await created;
  expect(res.status()).toBe(200);
  cityId = ((await res.json()) as City).id;
  await page.waitForURL(`**/geo/cities/${cityId}`);

  const facts = page.getByRole('list', { name: t('geo.city.facts.label') });
  await expect(facts).toContainText(String(PLACE));
  await expect(facts).toContainText(SLUG);
  await expect(facts).toContainText(REGION);
  await collect();

  const city = await api<City>(page, 'GET', `/v1/geo/cities/${cityId}`);
  expect(city.status).toBe(200);
  expect(city.body).toMatchObject({ id: cityId, slug: SLUG, status: 'pilot', geoname_id: PLACE });
});

test('4. «Журнал»: постановка импорта — владелец, импорт — система, активация — владелец', async () => {
  const rows = (label: string) =>
    page.getByRole('row').filter({ has: page.getByText(label, { exact: true }) });

  await page.goto('/audit?action=geo.import_requested');
  await expect(
    rows(t('audit.actions.geo.import_requested')).filter({ hasText: owner.name }),
  ).toHaveCount(1);
  await page.goto('/audit?action=geo.import');
  await expect(
    rows(t('audit.actions.geo.import')).filter({ hasText: t('audit.actor.system') }),
  ).toHaveCount(1);
  await page.goto('/audit?action=geo.city_activated');
  await expect(
    rows(t('audit.actions.geo.city_activated')).filter({ hasText: owner.name }),
  ).toHaveCount(1);
  await collect();

  const list = async (query: string) => {
    const r = await api<{ items: AuditRecord[] }>(page, 'GET', `/v1/audit?${query}`);
    expect(r.status).toBe(200);
    return r.body.items;
  };
  const requested = await list('action_prefix=geo.import_requested');
  expect(requested).toHaveLength(1);
  expect(requested[0]).toMatchObject({ actor_role: 'owner', actor_label: owner.name });
  const imported = await list('action_prefix=geo.import');
  expect(imported).toHaveLength(1);
  expect(imported[0]).toMatchObject({
    action: 'geo.import',
    actor_role: 'system',
    actor_staff_id: null,
  });
  const activated = await list('action_prefix=geo.city_activated');
  expect(activated).toHaveLength(1);
  expect(activated[0]).toMatchObject({
    actor_role: 'owner',
    actor_label: owner.name,
    object_id: cityId,
  });

  // на всём пути — ни одного нарушения CSP (prod: настоящий staticCSP admin-api)
  expect(violations).toEqual([]);
});
