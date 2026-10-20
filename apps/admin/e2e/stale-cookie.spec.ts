import { expect, test } from '@playwright/test';
import { cookieName, pairOf } from './env';
import { t } from './keys';
import { acceptAndSetup, api, bootstrap, loginFactor, logout, newStaff, receiveInvite } from './ui';

// Вход с протухшей cookie (⚠️ журнала исполнения, проверяется только в браузере): в браузере осталась
// cookie погашенной сессии — ответ входа несёт два Set-Cookie одного имени: стирание старой (конвейер) и
// новую (хендлер), именно в этом порядке. Браузер должен остаться с новой — иначе шаг 2 входа не
// найдёт сессию. Chromium — cookie __Host- + Secure (как в проде), WebKit — своя пара admin-api с
// ADMIN_COOKIE_SECURE=false (R6: WebKit не принимает Secure на http).
test('вход с протухшей cookie: стирание и новая cookie в одном ответе — сессия входа жива', async ({
  page,
  context,
}, testInfo) => {
  const pair = pairOf(testInfo.project.name);
  const name = cookieName(pair);
  const s = newStaff(`owner-cookie-${testInfo.project.name}`);

  const run = await bootstrap(pair, s);
  expect(run.code, run.stderr).toBe(0);
  await receiveInvite(s);
  await acceptAndSetup(page, s);

  const live = (await context.cookies()).find((c) => c.name === name);
  expect(live, `cookie ${name} после настройки 2FA`).toBeDefined();
  await logout(page);
  expect((await context.cookies()).find((c) => c.name === name)).toBeUndefined();

  // Чистый экран входа: запросы каркаса после выхода (счётчик подтверждений) уже не придут и не сотрут
  // подложенную cookie раньше входа
  const anonymous = page.waitForResponse((r) => r.url().endsWith('/v1/staff/me'));
  await page.goto('/login');
  expect((await anonymous).status()).toBe(401);
  await page.getByLabel(t('auth.login.email'), { exact: true }).fill(s.email);
  await page.getByLabel(t('auth.login.password'), { exact: true }).fill(s.password);

  // cookie погашенной (выход) сессии снова в браузере — и сразу вход
  await context.addCookies([live!]);
  const login = page.waitForResponse((r) => r.url().endsWith('/v1/staff/auth/login'));
  await page.getByRole('button', { name: t('auth.login.submit') }).click();
  const res = await login;
  expect(res.status()).toBe(200);
  const set = (await res.headersArray())
    .filter((h) => h.name.toLowerCase() === 'set-cookie')
    .map((h) => h.value);
  expect(set).toHaveLength(2);
  expect(set[0]).toMatch(new RegExp(`^${name}=;`));
  expect(set[0]).toMatch(/Max-Age=0/i);
  expect(set[1]).toMatch(new RegExp(`^${name}=[^;]+;`));

  // браузер остался с новой cookie: шаг 2 входа нашёл сессию
  await expect(page.getByRole('group', { name: t('auth.mfa.code'), exact: true })).toBeVisible();
  await loginFactor(page, s);
  const me = await api<{ stage: string; email?: string }>(page, 'GET', '/v1/staff/me');
  expect(me.status).toBe(200);
  expect(me.body).toMatchObject({ stage: 'active', email: s.email });
});
