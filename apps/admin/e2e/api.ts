import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { type APIRequestContext, expect } from '@playwright/test';
import { mailIds, waitForMail } from './mailpit';

// Клиент публичного API живого e2e (план I-4, Task 11): игрок как мобильное приложение — device в теле входа и
// регистрации (иначе 422 identity.device_required), Bearer-токен, ключ идемпотентности на мутациях с замком.
// Риск входа и регистрации может потребовать антибот — 403 humancheck.required: клиент решает задачу PoW (ALTCHA) и
// повторяет запрос с решением в X-WF-Humancheck, а не ослабляет политику окружением. Пути и тела — контракт
// contracts/openapi/public (identity, geo).

export type Device = {
  platform: 'android';
  install_id: string;
  model: string;
  os_version: string;
  app_version: string;
};

export type Player = {
  email: string;
  password: string;
  nickname: string;
  device: Device;
  id?: string;
  tokens?: TokenPair;
};

export type TokenPair = {
  access_token: string;
  refresh_token: string;
  session_id: string;
  access_expires_at: string;
  refresh_idle_expires_at: string;
};

export type AuthResult = {
  status: string;
  tokens?: TokenPair;
  user?: { id: string; nickname: string; new_user: boolean };
  deletion?: { restore_token: string; purge_at: string };
};

export type Problem = {
  code: string;
  status: number;
  reasons?: string[];
  available_at?: string;
  challenge?: Challenge;
};

type Challenge = {
  algorithm: string;
  challenge: string;
  maxNumber: number;
  salt: string;
  signature: string;
};

export type Res<T = unknown> = { status: number; body: T };

// Игрок прогона: почта в зарезервированном домене .test, ник 3–20 символов с буквой, пароль и устройство — случайные
// на прогон (база пересоздаётся, в репо паролей нет). Устройство одно на игрока: вход с нового устройства — риск.
export function newPlayer(slug: string): Player {
  const tag = randomBytes(3).toString('hex');
  return {
    email: `${slug}-${tag}@wf-e2e.test`,
    password: `e2e-${randomBytes(15).toString('base64url')}`,
    nickname: `${slug}_${tag}`,
    device: {
      platform: 'android',
      install_id: randomUUID(),
      model: 'e2e',
      os_version: '15',
      app_version: '1.0.0',
    },
  };
}

// Решение задачи антибота (контракт: число от 0 до maxNumber, при котором SHA-256(salt + число) в hex равен
// challenge; ответ — base64 от JSON с полями algorithm, challenge, number, salt, signature).
export function solveHumancheck(c: Challenge): string {
  for (let n = 0; n <= c.maxNumber; n++) {
    if (
      createHash('sha256')
        .update(c.salt + n)
        .digest('hex') === c.challenge
    ) {
      const payload = {
        algorithm: c.algorithm,
        challenge: c.challenge,
        number: n,
        salt: c.salt,
        signature: c.signature,
      };
      return Buffer.from(JSON.stringify(payload)).toString('base64');
    }
  }
  throw new Error('e2e: задача антибота не решается в пределах maxNumber');
}

// Код регистрации в письме: 6 цифр (identity.mail.signup_code).
const SIX_DIGITS = /\b(\d{6})\b/;

export class PublicApi {
  constructor(private readonly ctx: APIRequestContext) {}

  // Запрос; мутация с замком (token) — с новым Idempotency-Key. humancheck.required — решить задачу и повторить раз.
  async call<T = unknown>(
    method: 'GET' | 'POST',
    path: string,
    opts: { body?: unknown; token?: string; human?: string } = {},
  ): Promise<Res<T>> {
    const headers: Record<string, string> = {};
    if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
    if (opts.token && method === 'POST') headers['Idempotency-Key'] = randomUUID();
    if (opts.human) headers['X-WF-Humancheck'] = opts.human;
    const r = await this.ctx.fetch(path, { method, headers, data: opts.body });
    const text = await r.text();
    const body = (text ? JSON.parse(text) : null) as T;
    const p = body as Problem | null;
    if (r.status() === 403 && p?.code === 'humancheck.required' && p.challenge && !opts.human) {
      return this.call<T>(method, path, { ...opts, human: solveHumancheck(p.challenge) });
    }
    return { status: r.status(), body };
  }

  async cityId(slug: string): Promise<string> {
    const r = await this.call<{ id: string }>('GET', `/v1/cities/by-slug/${slug}`);
    expect(r.status, `город ${slug}`).toBe(200);
    return r.body.id;
  }

  // Регистрация по почте: документы → startSignup → код из письма (его шлёт воркер прогона) → confirmSignup (201).
  async signup(p: Player, cityId: string): Promise<AuthResult> {
    const docs = await this.call<{ items: { document: string; version: string }[] }>(
      'GET',
      '/v1/legal/documents',
    );
    expect(docs.status).toBe(200);
    const seen = await mailIds(p.email);
    const start = await this.call<{ code_id: string }>('POST', '/v1/auth/signup', {
      body: {
        email: p.email,
        password: p.password,
        nickname: p.nickname,
        birth_date: '1995-05-17',
        city_id: cityId,
        accepted_documents: docs.body.items.map((d) => ({
          document: d.document,
          version: d.version,
        })),
        locale: 'ru',
        device: p.device,
      },
    });
    expect(start.status, JSON.stringify(start.body)).toBe(202);
    const mail = await waitForMail(p.email, { match: SIX_DIGITS, seen });
    const code = SIX_DIGITS.exec(mail.text)?.[1];
    if (!code) throw new Error(`e2e: в письме ${p.email} нет кода`);
    const done = await this.call<AuthResult>('POST', '/v1/auth/signup/confirm', {
      body: { code_id: start.body.code_id, code, device: p.device },
    });
    expect(done.status, JSON.stringify(done.body)).toBe(201);
    this.remember(p, done.body);
    return done.body;
  }

  async login(p: Player): Promise<Res<AuthResult>> {
    const r = await this.call<AuthResult>('POST', '/v1/auth/login', {
      body: { email: p.email, password: p.password, device: p.device },
    });
    if (r.status === 200) this.remember(p, r.body);
    return r;
  }

  async refresh(p: Player): Promise<Res<TokenPair & Problem>> {
    const r = await this.call<TokenPair & Problem>('POST', '/v1/auth/refresh', {
      body: { refresh_token: p.tokens?.refresh_token, install_id: p.device.install_id },
    });
    if (r.status === 200) p.tokens = r.body;
    return r;
  }

  logout(p: Player): Promise<Res> {
    return this.call('POST', '/v1/auth/logout', { token: access(p) });
  }

  deleteAccount(p: Player): Promise<Res<{ purge_at: string }>> {
    return this.call('POST', '/v1/me/delete', {
      token: access(p),
      body: { confirm_nickname: p.nickname },
    });
  }

  async cancelDeletion(p: Player, restoreToken: string): Promise<Res<AuthResult>> {
    const r = await this.call<AuthResult>('POST', '/v1/auth/deletion/cancel', {
      body: { restore_token: restoreToken, device: p.device },
    });
    if (r.status === 200) this.remember(p, r.body);
    return r;
  }

  me(p: Player): Promise<Res<{ id: string; status: string; nickname: string }>> {
    return this.call('GET', '/v1/me', { token: access(p) });
  }

  captaincy(p: Player): Promise<
    Res<{
      permits: { city_id: string; probationary: boolean }[];
      eligibility: { city_id: string; eligible: boolean; reasons: string[] };
      open_application: { id: string } | null;
    }>
  > {
    return this.call('GET', '/v1/me/captaincy', { token: access(p) });
  }

  // 201 — заявка (status — её статус), иначе Problem (code, available_at)
  submitApplication(
    p: Player,
    body: Record<string, string>,
  ): Promise<Res<{ id: string; status: string; code?: string; available_at?: string }>> {
    return this.call('POST', '/v1/captain-applications', { token: access(p), body });
  }

  private remember(p: Player, r: AuthResult) {
    if (r.tokens) p.tokens = r.tokens;
    if (r.user) p.id = r.user.id;
  }
}

function access(p: Player): string {
  if (!p.tokens) throw new Error(`e2e: у ${p.nickname} нет сессии`);
  return p.tokens.access_token;
}
