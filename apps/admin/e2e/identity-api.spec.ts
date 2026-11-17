import { type APIRequestContext, expect, request, test } from '@playwright/test';
import { type AuthResult, newPlayer, PublicApi } from './api';
import { PUBLIC_API_URL } from './env';

// Живой сценарий ядра identity (спека identity §13 «e2e», план I-4, Task 11) поверх настоящих api (тег wf_e2e),
// воркера, Postgres и Mailpit: регистрация с кодом из письма → обновление токенов → выход → вход → удаление аккаунта
// (свежий вход есть сразу после входа) → вход отвечает deletion_pending → отмена удаления → снова authenticated.
// Браузер не нужен; один проект (identityProject), игрок — свой на прогон.

test.describe.configure({ mode: 'serial' });

let ctx: APIRequestContext;
let api: PublicApi;
let cityId = '';
const player = newPlayer('core');

test.beforeAll(async () => {
  ctx = await request.newContext({ baseURL: PUBLIC_API_URL });
  api = new PublicApi(ctx);
  cityId = await api.cityId('stavropol');
});

test.afterAll(async () => {
  await ctx?.dispose();
});

test('1. регистрация: код из письма → 201, сессия и новый игрок', async () => {
  const r = await api.signup(player, cityId);
  expect(r.status).toBe('authenticated');
  expect(r.user).toMatchObject({ nickname: player.nickname, new_user: true });
  expect(r.tokens?.access_token).toBeTruthy();
  const me = await api.me(player);
  expect(me.status).toBe(200);
  expect(me.body).toMatchObject({ id: player.id, status: 'active' });
});

test('2. обновление токенов → новая пара; выход → 204, refresh после выхода — 401 refresh_invalid', async () => {
  const before = player.tokens;
  const r = await api.refresh(player);
  expect(r.status).toBe(200);
  expect(r.body.refresh_token).not.toBe(before?.refresh_token);
  expect(r.body.session_id).toBe(before?.session_id);

  const out = await api.logout(player);
  expect(out.status).toBe(204);
  const again = await api.refresh(player);
  expect(again.status).toBe(401);
  expect(again.body.code).toBe('identity.refresh_invalid');
});

test('3. вход → удаление аккаунта → вход: deletion_pending → отмена удаления → authenticated', async () => {
  const login = await api.login(player);
  expect(login.status).toBe(200);
  expect(login.body.status).toBe('authenticated');

  const del = await api.deleteAccount(player);
  expect(del.status, JSON.stringify(del.body)).toBe(200);
  expect(Date.parse(del.body.purge_at)).toBeGreaterThan(Date.now());

  const pending = await api.login(player);
  expect(pending.status).toBe(200);
  const body: AuthResult = pending.body;
  expect(body.status).toBe('deletion_pending');
  expect(body.tokens).toBeUndefined();
  expect(Date.parse(body.deletion?.purge_at ?? '')).toBe(Date.parse(del.body.purge_at));

  const restored = await api.cancelDeletion(player, body.deletion?.restore_token ?? '');
  expect(restored.status, JSON.stringify(restored.body)).toBe(200);
  expect(restored.body.status).toBe('authenticated');
  const me = await api.me(player);
  expect(me.status).toBe(200);
  expect(me.body.status).toBe('active');
});
