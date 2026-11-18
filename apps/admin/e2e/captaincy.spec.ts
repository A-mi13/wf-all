import {
  type APIRequestContext,
  type BrowserContext,
  expect,
  type Page,
  request,
  test,
} from '@playwright/test';
import { newPlayer, PublicApi } from './api';
import { pairOf, PUBLIC_API_URL } from './env';
import { t } from './keys';
import { waitForMail } from './mailpit';
import { acceptAndSetup, api as adminApi, bootstrap, newStaff, receiveInvite } from './ui';

// Живой сценарий капитанства (спека identity §13 «e2e», К1; план I-4, Task 11) поверх настоящих api и admin-api,
// собранных с тегом wf_e2e (тестовые порты условий — «выполнено»), воркера, Postgres и Mailpit:
//   игрок регистрируется через api и подаёт заявку → владелец в «Капитанах» (счётчик 1) открывает карточку (все
//   условия выполнены), смотрит контакты с причиной и одобряет → у игрока допуск в городе, в журнале — одобрение и
//   просмотр ПД; второй игрок — отказ «Другое» с комментарием → письмо → повторная подача — 409 application_cooldown;
//   «Игроки» — поиск по почте → карточка без 2FA (запроса сброса нет).
// Один проект (identityProject: prod — Chromium под настоящей CSP, без него chromium): заявки и счётчик — в общей базе.
// Нарушения CSP собираются на всём пути и проверяются в конце.

const CITY = 'stavropol';
const REASON = 'Обращение E2E-1';
const COMMENT = 'Расскажите подробнее о команде и поле';

const owner = newStaff('owner-cap');
const first = newPlayer('cap1');
const second = newPlayer('cap2');

type Card = {
  application: { id: string; status: string; nickname: string };
  checks: { check: string; state: string }[];
};
type AuditRecord = {
  action: string;
  actor_role: string;
  actor_label: string | null;
  object_id: string | null;
};

test.describe.configure({ mode: 'serial' });

let ctx: BrowserContext;
let page: Page;
let apiCtx: APIRequestContext;
let pub: PublicApi;
let cityId = '';
let firstApp = '';
let secondApp = '';
const violations: string[] = [];

// Текст ветки «one» ICU plural (счётчик 1): «{count, plural, one {# заявка ждёт решения} …}» → «1 заявка ждёт решения».
function pluralOne(icu: string): string {
  const m = /\bone \{([^}]*)\}/.exec(icu);
  if (!m?.[1]) throw new Error(`e2e: нет ветки one в «${icu}»`);
  return m[1].replace('#', '1');
}

async function collect(): Promise<void> {
  violations.push(
    ...(await page.evaluate(() => (window as unknown as { __wfCsp: string[] }).__wfCsp.splice(0))),
  );
}

// Карточка заявки из очереди «Ждут решения»: «Открыть» в строке игрока → панель (диалог с его ником).
async function openApplication(nickname: string) {
  await page.getByRole('link', { name: new RegExp(`^${t('nav.captains')}`) }).click();
  const table = page.getByRole('table', { name: t('captains.table.label') });
  await expect(table).toBeVisible();
  await table
    .getByRole('button', { name: t('captains.table.open_label').replace('{name}', nickname) })
    .click();
  const drawer = page.getByRole('dialog', { name: nickname });
  await expect(drawer).toBeVisible();
  return drawer;
}

test.beforeAll(async ({ browser }, info) => {
  apiCtx = await request.newContext({ baseURL: PUBLIC_API_URL });
  pub = new PublicApi(apiCtx);
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
  await apiCtx?.dispose();
});

test('1. игроки: регистрация двоих через api; первый может подать заявку (тестовые порты) и подаёт её', async () => {
  cityId = await pub.cityId(CITY);
  await pub.signup(first, cityId);
  await pub.signup(second, cityId);

  const before = await pub.captaincy(first);
  expect(before.status).toBe(200);
  expect(before.body.eligibility).toMatchObject({ city_id: cityId, eligible: true, reasons: [] });
  expect(before.body.permits).toEqual([]);

  const r = await pub.submitApplication(first, {
    proposed_format: '5×5 по вечерам',
    proposed_pitch: 'Стадион школы № 1',
    phone: '+79001234567',
  });
  expect(r.status, JSON.stringify(r.body)).toBe(201);
  expect(r.body.status).toBe('pending');
  firstApp = r.body.id;
});

test('2. владелец: приглашение, 2FA → стартовая', async () => {
  const run = await bootstrap(pairOf(test.info().project.name), owner);
  expect(run.code, run.stderr).toBe(0);
  await receiveInvite(owner);
  await acceptAndSetup(page, owner);
  await collect();
});

test('3. «Капитаны»: счётчик 1 → карточка, все условия выполнены → контакты с причиной → «Одобрить»', async () => {
  // счётчик очереди — в имени ссылки меню
  await expect(
    page.getByRole('link', {
      name: `${t('nav.captains')} ${pluralOne(t('nav.counts.captains'))}`,
    }),
  ).toBeVisible();

  const drawer = await openApplication(first.nickname);
  // условия — тестовые порты сборки wf_e2e: все «выполнено», «проверить нельзя» и «не выполнено» нет
  await expect(drawer.getByText(t('captains.check_state.ok'), { exact: true })).toHaveCount(4);
  await expect(
    drawer.getByText(t('captains.check_state.unavailable'), { exact: true }),
  ).toHaveCount(0);
  await expect(drawer.getByText(t('captains.check_state.failed'), { exact: true })).toHaveCount(0);
  const card = await adminApi<Card>(page, 'GET', `/v1/identity/captain-applications/${firstApp}`);
  expect(card.status).toBe(200);
  expect(card.body.checks.map((c) => c.state)).toEqual(['ok', 'ok', 'ok', 'ok']);

  // контакты — только с причиной; почта игрока на экране
  await drawer.getByRole('button', { name: t('captains.contact.show'), exact: true }).click();
  const contact = page.getByRole('dialog', { name: t('captains.contact.title') });
  await contact.getByLabel(t('captains.contact.reason'), { exact: true }).fill(REASON);
  await contact.getByRole('button', { name: t('captains.contact.submit'), exact: true }).click();
  await expect(contact.getByText(first.email, { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(contact).toHaveCount(0);

  const approveBtn = drawer.getByRole('button', {
    name: t('captains.approve.submit'),
    exact: true,
  });
  await expect(approveBtn).toBeEnabled();
  await approveBtn.click();
  const approve = page.getByRole('dialog', { name: t('captains.approve.title') });
  const decided = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' &&
      new URL(r.url()).pathname === `/v1/identity/captain-applications/${firstApp}/approve`,
  );
  await approve.getByRole('button', { name: t('captains.approve.submit'), exact: true }).click();
  expect((await decided).status()).toBe(200);
  await expect(page.getByText(t('captains.toasts.approved'), { exact: true })).toBeVisible();
  await expect(drawer.getByText(t('captains.result.approved')).first()).toBeVisible();
  await collect();
});

test('4. игрок: допуск в городе; журнал — одобрение и просмотр контактов владельцем', async () => {
  const c = await pub.captaincy(first);
  expect(c.status).toBe(200);
  expect(c.body.permits.map((p) => p.city_id)).toEqual([cityId]);
  expect(c.body.open_application).toBeNull();

  await page.goto('/audit?action=identity.captain_application_approved');
  await expect(
    page
      .getByRole('row')
      .filter({
        has: page.getByText(t('audit.actions.identity.captain_application_approved'), {
          exact: true,
        }),
      })
      .filter({ hasText: owner.name }),
  ).toHaveCount(1);
  await collect();

  const list = async (prefix: string) => {
    const r = await adminApi<{ items: AuditRecord[] }>(
      page,
      'GET',
      `/v1/audit?action_prefix=${prefix}`,
    );
    expect(r.status).toBe(200);
    return r.body.items;
  };
  const approved = await list('identity.captain_application_approved');
  expect(approved).toHaveLength(1);
  expect(approved[0]).toMatchObject({
    actor_role: 'owner',
    actor_label: owner.name,
    object_id: firstApp,
  });
  const reads = await list('identity.pii_read');
  expect(reads).toHaveLength(1);
  expect(reads[0]).toMatchObject({
    actor_role: 'owner',
    actor_label: owner.name,
    object_id: first.id,
  });
  // почты в журнале нет
  expect(JSON.stringify(reads)).not.toContain(first.email);
});

test('5. второй игрок: «Отклонить» — «Другое» с комментарием → письмо → повторная подача — 409 application_cooldown', async () => {
  const r = await pub.submitApplication(second, { note: 'Хочу собрать команду' });
  expect(r.status, JSON.stringify(r.body)).toBe(201);
  secondApp = r.body.id;

  await page.goto('/');
  const drawer = await openApplication(second.nickname);
  await drawer.getByRole('button', { name: t('captains.reject.submit'), exact: true }).click();
  const reject = page.getByRole('dialog', { name: t('captains.reject.title') });
  await reject.getByRole('combobox', { name: t('captains.reject.code'), exact: true }).click();
  await page.getByRole('option', { name: t('captains.rejection.other'), exact: true }).click();
  await reject.getByLabel(t('captains.reject.comment'), { exact: true }).fill(COMMENT);
  const decided = page.waitForResponse(
    (res) =>
      res.request().method() === 'POST' &&
      new URL(res.url()).pathname === `/v1/identity/captain-applications/${secondApp}/reject`,
  );
  await reject.getByRole('button', { name: t('captains.reject.submit'), exact: true }).click();
  expect((await decided).status()).toBe(200);
  await expect(page.getByText(t('captains.toasts.rejected'), { exact: true })).toBeVisible();
  // панель решённой заявки закрывается Esc (ввода в ней нет)
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
  await collect();

  // письмо решения (identity.mail.captain_application_rejected) — с комментарием модератора
  const mail = await waitForMail(second.email, { match: /отклонена/ });
  expect(mail.subject).toBe('Заявка в капитаны отклонена');
  expect(mail.text).toContain(COMMENT);

  const again = await pub.submitApplication(second, { note: 'Ещё раз' });
  expect(again.status).toBe(409);
  expect(again.body.code).toBe('identity.application_cooldown');
  expect(Date.parse(again.body.available_at ?? '')).toBeGreaterThan(Date.now());
});

test('6. «Игроки»: поиск по почте с причиной → карточка, 2FA не включена — запроса сброса нет', async () => {
  await page.getByRole('link', { name: t('nav.players'), exact: true }).click();
  await page.getByLabel(t('players.lookup.email'), { exact: true }).fill(second.email);
  await page.getByLabel(t('players.lookup.reason'), { exact: true }).fill(REASON);
  await page.getByRole('button', { name: t('players.lookup.submit'), exact: true }).click();
  await expect(page.getByRole('heading', { level: 2, name: second.nickname })).toBeVisible();
  await expect(page.getByText(t('players.card.mfa_off'), { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: t('players.reset.request') })).toHaveCount(0);
  await collect();

  // на всём пути — ни одного нарушения CSP (prod: настоящий staticCSP admin-api)
  expect(violations).toEqual([]);
});
