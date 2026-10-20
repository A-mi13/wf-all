import { type BrowserContext, expect, type Page, test } from '@playwright/test';
import { origin, PAIRS } from './env';
import { t } from './keys';
import { mailIds } from './mailpit';
import {
  acceptAndSetup,
  api,
  bootstrap,
  loginFactor,
  loginPassword,
  logout,
  newStaff,
  receiveInvite,
  typeOtp,
} from './ui';

// Живой сценарий спеки сотрудников §11, п. 1–4 и 6 (п. 5 — план B) на настоящих admin-api, Postgres и
// Mailpit: bootstrap двух владельцев → принятие и 2FA → выход и вход → приглашение админа вдвоём →
// админ входит → журнал. Шаги зависят друг от друга (serial): упал один — остальные пропущены.

const pair = PAIRS.chromium;
const ownerA = newStaff('owner-a');
const ownerB = newStaff('owner-b');
const admin = newStaff('admin-1');
const support = newStaff('support-1');

type Approval = { id: string; status: string; action: string };
type Me = { id?: string; role?: string; stage: string; email?: string };
type AuditRecord = {
  action: string;
  actor_role: string;
  actor_label: string | null;
  actor_staff_id: string | null;
  object_id: string | null;
};

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

test.describe.configure({ mode: 'serial' });

let ctxA: BrowserContext;
let ctxB: BrowserContext;
let ctxAdmin: BrowserContext;
let pageA: Page;
let pageB: Page;
let pageAdmin: Page;
let approvalId = '';
let adminId = '';

test.beforeAll(async ({ browser }) => {
  // три сотрудника — три браузера (cookie сессии у каждого своя)
  const opts = { baseURL: origin(pair), locale: 'ru-RU', viewport: { width: 1280, height: 800 } };
  [ctxA, ctxB, ctxAdmin] = await Promise.all([
    browser.newContext(opts),
    browser.newContext(opts),
    browser.newContext(opts),
  ]);
  // те же пределы ожиданий, что use.actionTimeout / navigationTimeout у контекстов раннера
  for (const c of [ctxA, ctxB, ctxAdmin]) {
    c.setDefaultTimeout(20_000);
    c.setDefaultNavigationTimeout(30_000);
  }
  [pageA, pageB, pageAdmin] = await Promise.all([
    ctxA.newPage(),
    ctxB.newPage(),
    ctxAdmin.newPage(),
  ]);
});

test.afterAll(async () => {
  await Promise.all([ctxA, ctxB, ctxAdmin].map((c) => c?.close()));
});

test('1. bootstrap двух владельцев: код приглашения приходит письмом, не в вывод', async () => {
  for (const s of [ownerA, ownerB]) {
    const run = await bootstrap(pair, s);
    expect(run.code, run.stderr).toBe(0);
    const code = await receiveInvite(s);
    // без --print-code код — только в письме (Task 10c)
    expect(run.stdout).not.toContain(code);
    expect(run.stderr).not.toContain(code);
  }
});

test('2. владельцы принимают приглашение и настраивают 2FA', async () => {
  for (const [page, s] of [
    [pageA, ownerA],
    [pageB, ownerB],
  ] as const) {
    const codes = await acceptAndSetup(page, s);
    for (const c of codes) expect(c).toMatch(/^[a-z2-7]{5}-[a-z2-7]{5}$/);
    const me = await api<Me>(page, 'GET', '/v1/staff/me');
    expect(me.status).toBe(200);
    expect(me.body).toMatchObject({ stage: 'active', role: 'owner', email: s.email });
  }
});

test('3. выход и вход: код следующего шага TOTP, повтор принятого кода — отказ', async () => {
  await logout(pageA);
  await loginPassword(pageA, ownerA);
  await loginFactor(pageA, ownerA);

  // тот же код ещё в окне ±1 шаг, но шаг уже принят (last_used_step) — отказ, попытка засчитана
  await logout(pageA);
  await loginPassword(pageA, ownerA);
  const reused = pageA.waitForResponse((r) => r.url().endsWith('/v1/staff/auth/mfa'));
  await typeOtp(pageA, t('auth.mfa.code'), ownerA.device!.last()!);
  const res = await reused;
  expect(res.ok()).toBe(false);
  expect(await res.json()).toMatchObject({ code: 'staff.mfa_invalid', attempts_left: 4 });
  await expect(pageA.getByRole('alert')).toBeVisible();

  // код следующего шага — вход
  await loginFactor(pageA, ownerA);
  const me = await api<Me>(pageA, 'GET', '/v1/staff/me');
  expect(me.body).toMatchObject({ stage: 'active', email: ownerA.email });
});

test('4. A приглашает админа (202) → B подтверждает во «Входящих» без окна → письмо админу', async () => {
  await pageA.goto('/staff');
  await pageA.getByRole('button', { name: t('staff.invite.open') }).click();
  const drawer = pageA.getByRole('dialog');
  await drawer.getByLabel(t('staff.invite.email'), { exact: true }).fill(admin.email);
  await drawer.getByLabel(t('staff.invite.name'), { exact: true }).fill(admin.name);
  await drawer
    .getByLabel(t('staff.invite.role'), { exact: true })
    .selectOption({ label: t('roles.admin') });
  const created = pageA.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().endsWith('/v1/staff/invites'),
  );
  await drawer.getByRole('button', { name: t('staff.invite.submit') }).click();
  const res = await created;
  expect(res.status()).toBe(202);
  const approval = ((await res.json()) as { approval: Approval }).approval;
  expect(approval).toMatchObject({ status: 'pending', action: 'staff.invite' });
  approvalId = approval.id;
  // до подтверждения писем приглашённому нет; сразу после 202 это ещё ничего не доказывает (фон River мог
  // не успеть) — доказывает «письмо одно» после принятия ниже (ревью Task 16, M1)
  expect((await mailIds(admin.email)).size).toBe(0);

  await pageB.goto('/approvals');
  // имя вкладки — «Входящие N»: число ожидающих в самом имени
  const incoming = pageB.getByRole('tab', {
    name: new RegExp(`^${escape(t('approvals.tabs.incoming'))}\\s*1$`),
  });
  await expect(incoming).toHaveAttribute('aria-selected', 'true');
  const row = pageB.getByRole('row').filter({ hasText: admin.email });
  const approved = pageB.waitForResponse(
    (r) =>
      r.request().method() === 'POST' && r.url().endsWith(`/v1/approvals/${approvalId}/approve`),
  );
  await row.getByRole('button', { name: t('approvals.approve') }).click();
  // «Подтвердить» уходит сразу: ответ пришёл, окна не было
  const decided = await approved;
  await expect(pageB.getByRole('dialog')).toHaveCount(0);
  expect(decided.status()).toBe(200);
  expect(await decided.json()).toMatchObject({ id: approvalId, status: 'executed' });
  await expect(pageB.getByText(t('approvals.done.executed'), { exact: true })).toBeVisible();

  await receiveInvite(admin);
  // правило двух рук: письмо с кодом — одно, от подтверждения; приглашение на 202 его не слало (оно
  // ушло бы в ту же очередь раньше и к этому моменту уже лежало бы в Mailpit)
  expect((await mailIds(admin.email)).size).toBe(1);
});

test('5. админ принимает приглашение, настраивает 2FA и видит свой раздел', async () => {
  await acceptAndSetup(pageAdmin, admin);
  const me = await api<Me>(pageAdmin, 'GET', '/v1/staff/me');
  expect(me.body).toMatchObject({ stage: 'active', role: 'admin', email: admin.email });
  adminId = me.body.id ?? '';
  expect(adminId).not.toBe('');

  // стартовая — первый разрешённый раздел меню (журнал)
  await expect(pageAdmin).toHaveURL(/\/audit$/);
  await expect(pageAdmin.getByRole('heading', { level: 1, name: t('nav.audit') })).toBeVisible();

  // права админа: выдаёт только модератора и поддержку — владельца и админа в списке нет
  await pageAdmin.goto('/staff');
  await pageAdmin.getByRole('button', { name: t('staff.invite.open') }).click();
  const options = await pageAdmin
    .getByRole('dialog')
    .getByLabel(t('staff.invite.role'), { exact: true })
    .locator('option')
    .allTextContents();
  expect(options).toContain(t('roles.support'));
  expect(options).not.toContain(t('roles.admin'));
  expect(options).not.toContain(t('roles.owner'));
  await pageAdmin.keyboard.press('Escape');
  await expect(pageAdmin.getByRole('dialog')).toHaveCount(0);
});

test('6. «Отклонить» — окно; строка исчезла при перечитывании — фокус не на body', async () => {
  const req = await api<{ approval: Approval }>(pageA, 'POST', '/v1/staff/invites', {
    email: support.email,
    display_name: support.name,
    role: 'support',
  });
  expect(req.status).toBe(202);
  const id = req.body.approval.id;

  await pageB.goto('/approvals');
  const row = pageB.getByRole('row').filter({ hasText: support.email });
  await row.getByRole('button', { name: t('approvals.reject') }).click();
  const dialog = pageB.getByRole('dialog', { name: t('approvals.reject_dialog.title') });
  await expect(dialog).toBeVisible();

  // пока окно открыто, инициатор отменяет запрос
  const cancelled = await api<Approval>(pageA, 'POST', `/v1/approvals/${id}/cancel`);
  expect(cancelled.status).toBe(200);
  expect(cancelled.body.status).toBe('cancelled');

  await dialog
    .getByLabel(t('approvals.reject_dialog.comment'), { exact: true })
    .fill('e2e: запрос уже отменён');
  const rejected = pageB.waitForResponse((r) => r.url().endsWith(`/v1/approvals/${id}/reject`));
  await dialog.getByRole('button', { name: t('approvals.reject_dialog.submit') }).click();
  const res = await rejected;
  expect(res.status()).toBe(409);
  expect(((await res.json()) as { code: string }).code).toMatch(/^platform\.approval_/);

  // окно закрыто, список перечитан — строки нет; фокус — на списке, не на body
  await expect(dialog).toHaveCount(0);
  await expect(row).toHaveCount(0);
  await expect
    .poll(() =>
      pageB.evaluate(
        () => document.activeElement !== null && document.activeElement !== document.body,
      ),
    )
    .toBe(true);
});

test('7. журнал: bootstrap от оператора, запрос и решение по approval.id, принятие приглашения', async () => {
  const rows = (label: string) =>
    pageA.getByRole('row').filter({ has: pageA.getByText(label, { exact: true }) });

  // экран «Журнал действий»
  await pageA.goto('/audit?action=staff.bootstrapped');
  const boot = rows(t('audit.actions.staff.bootstrapped'));
  await expect(boot.first()).toBeVisible();
  expect(await boot.count()).toBeGreaterThanOrEqual(2);
  await expect(boot.filter({ hasText: t('audit.actor.operator') })).toHaveCount(await boot.count());

  await pageA.goto(`/audit?object_id=${approvalId}`);
  await expect(rows(t('audit.actions.platform.approval_requested'))).toHaveCount(1);
  await expect(rows(t('audit.actions.platform.approval_executed'))).toHaveCount(1);

  await pageA.goto(`/audit?object_id=${adminId}`);
  await expect(rows(t('audit.actions.staff.invite_accepted'))).toHaveCount(1);

  // те же записи в listAudit
  const list = async (query: string) => {
    const r = await api<{ items: AuditRecord[] }>(pageA, 'GET', `/v1/audit?${query}`);
    expect(r.status).toBe(200);
    return r.body.items;
  };
  const bootstrapped = await list('action_prefix=staff.bootstrapped');
  expect(bootstrapped.length).toBeGreaterThanOrEqual(2);
  for (const r of bootstrapped) {
    expect(r).toMatchObject({
      actor_role: 'operator',
      actor_label: 'operator',
      actor_staff_id: null,
    });
  }

  const byApproval = await list(`object_id=${approvalId}`);
  expect(byApproval.map((r) => r.action).sort()).toEqual([
    'platform.approval_executed',
    'platform.approval_requested',
  ]);
  const requested = byApproval.find((r) => r.action === 'platform.approval_requested');
  const executed = byApproval.find((r) => r.action === 'platform.approval_executed');
  expect(requested).toMatchObject({
    actor_role: 'owner',
    actor_label: ownerA.name,
    object_id: approvalId,
  });
  expect(executed).toMatchObject({
    actor_role: 'owner',
    actor_label: ownerB.name,
    object_id: approvalId,
  });

  const accepted = await list(`object_id=${adminId}&action_prefix=staff.invite_accepted`);
  expect(accepted).toHaveLength(1);
  expect(accepted[0]).toMatchObject({ action: 'staff.invite_accepted', object_id: adminId });
});
