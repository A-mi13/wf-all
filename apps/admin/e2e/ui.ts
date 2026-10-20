import { execFile } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { expect, type Page } from '@playwright/test';
import { ADMIN_API_BIN, adminApiEnv, type Pair } from './env';
import { t } from './keys';
import { inviteCode, waitForMail } from './mailpit';
import { base32Decode, TotpDevice } from './totp';

// Шаги живого e2e на экранах админки: элементы — по ролям и подписям из локалей (keys.ts), как их
// видит сотрудник и читалка экрана.

export type Staff = {
  email: string;
  name: string;
  password: string;
  inviteCode?: string;
  device?: TotpDevice;
};

// Сотрудник прогона: почта в зарезервированном домене .test, пароль — случайный на прогон (база
// пересоздаётся, в репо паролей нет).
export function newStaff(slug: string): Staff {
  return {
    email: `${slug}@wf-e2e.test`,
    name: `E2E ${slug}`,
    password: `e2e-${randomBytes(15).toString('base64url')}`,
  };
}

export type Run = { code: number; stdout: string; stderr: string };

// admin-api staff bootstrap — подкоманда оператора (Task 10c) с окружением пары; без --print-code:
// код приглашения приходит только письмом.
export function bootstrap(pair: Pair, s: Staff): Promise<Run> {
  return new Promise((resolve) => {
    execFile(
      ADMIN_API_BIN,
      ['staff', 'bootstrap', '--email', s.email, '--name', s.name],
      { env: adminApiEnv(pair), timeout: 60_000 },
      (err, stdout, stderr) => {
        const code = err ? (typeof err.code === 'number' ? err.code : 1) : 0;
        resolve({ code, stdout, stderr });
      },
    );
  });
}

// Код приглашения из письма (ждёт фон admin-api).
export async function receiveInvite(s: Staff, seen?: ReadonlySet<string>): Promise<string> {
  const mail = await waitForMail(s.email, { match: /\b[a-z2-7]{5}-[a-z2-7]{5}\b/, seen });
  s.inviteCode = inviteCode(mail.text);
  return s.inviteCode;
}

const field = (page: Page, label: string) => page.getByLabel(label, { exact: true });

// Шесть ячеек OtpInput (role="group" с aria-label): ввод по цифре, автопереход и автоотправка.
export async function typeOtp(page: Page, label: string, code: string): Promise<void> {
  const group = page.getByRole('group', { name: label, exact: true });
  await group.getByRole('textbox').first().click();
  await page.keyboard.type(code);
}

// /accept → /setup-2fa (экран сам выпускает секрет; секрет — из ответа staffMfaSetup, как его
// показывает QR) → код «телефона» → 10 резервных кодов → «Я сохранил коды» → «Готово».
export async function acceptAndSetup(page: Page, s: Staff): Promise<string[]> {
  if (!s.inviteCode) throw new Error(`e2e: нет кода приглашения ${s.email}`);
  await page.goto('/accept');
  await field(page, t('auth.accept.email')).fill(s.email);
  await field(page, t('auth.accept.password')).fill(s.password);
  await field(page, t('auth.accept.passwordRepeat')).fill(s.password);
  await field(page, t('auth.accept.code')).fill(s.inviteCode);
  const setup = page.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().endsWith('/v1/staff/me/mfa/setup'),
  );
  await page.getByRole('button', { name: t('auth.accept.submit') }).click();
  await page.waitForURL('**/setup-2fa');
  const res = await setup;
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { secret: string };
  s.device = new TotpDevice(base32Decode(body.secret));

  await typeOtp(page, t('auth.setup.code'), await s.device.next());
  const codes = page.getByRole('list', { name: t('auth.setup.codes_title') }).getByRole('listitem');
  await expect(codes).toHaveCount(10);
  const list = await codes.allTextContents();
  await page.getByRole('checkbox', { name: t('auth.setup.saved') }).check();
  await page.getByRole('button', { name: t('auth.setup.finish') }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/setup-2fa'));
  return list;
}

// Шаг 1 входа: почта и пароль → второй фактор (группа ячеек).
export async function loginPassword(page: Page, s: Staff): Promise<void> {
  await field(page, t('auth.login.email')).fill(s.email);
  await field(page, t('auth.login.password')).fill(s.password);
  await page.getByRole('button', { name: t('auth.login.submit') }).click();
  await expect(page.getByRole('group', { name: t('auth.mfa.code'), exact: true })).toBeVisible();
}

// Шаг 2: код следующего шага TOTP (повтор кода сервер отвергает — last_used_step) → вход.
export async function loginFactor(page: Page, s: Staff): Promise<void> {
  if (!s.device) throw new Error(`e2e: у ${s.email} не настроен второй фактор`);
  await typeOtp(page, t('auth.mfa.code'), await s.device.next());
  await page.waitForURL((u) => u.pathname !== '/login');
}

export async function logout(page: Page): Promise<void> {
  await page.getByRole('button', { name: t('nav.logout') }).click();
  await page.waitForURL('**/login');
}

export type ApiResult<T = unknown> = { status: number; body: T };

// Запрос к admin-api из страницы (тот же origin, cookie сессии браузера, Origin ставит браузер);
// мутации — с X-WF-Admin и ключом идемпотентности, как у клиента админки.
export async function api<T = unknown>(
  page: Page,
  method: 'GET' | 'POST',
  path: string,
  body?: unknown,
): Promise<ApiResult<T>> {
  const headers: Record<string, string> =
    method === 'GET' ? {} : { 'X-WF-Admin': '1', 'Idempotency-Key': randomUUID() };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  return page.evaluate(
    async ({ method, path, headers, body }) => {
      const r = await fetch(path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
      });
      const text = await r.text();
      return { status: r.status, body: text ? JSON.parse(text) : null };
    },
    { method, path, headers, body },
  );
}
