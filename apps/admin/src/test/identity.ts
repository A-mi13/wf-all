import type { components } from '../api/gen/admin';
import type { Me } from '../app/permissions';
import { CITY_OTHER, CITY_OWN, meModerator, meOwner } from './me';
import { problem } from './problem';
import { http, server } from './server';
import { approval } from './staff';

// Сервер identity для моков — по схемам контракта admin.d.ts (план I-4, Task 8): очередь заявок в капитаны
// (status — по умолчанию pending, city_id, курсор — позиция строки, как keyset сервера), карточка, контакты
// заявителя (заголовок X-WF-Access-Reason — в seen как пришёл, в percent-encoding), одобрение и отказ
// (решённая заявка — 409 identity.application_closed, как на сервере), поиск игрока по почте (по умолчанию —
// 404 identity.user_not_found) и статистика способов входа (до I-3 — нули, R1). Состояние меняется, как на
// сервере: решение переводит заявку из pending, очередь его больше не отдаёт. answer — свой ответ вместо
// решения сервера (Response — ошибка); ошибка решения состояние не меняет, кроме application_closed (record
// — записать итог в обход ответа: «сервер решил, ответ потерян»).

type Application = components['schemas']['IdentityCaptainApplication'];
type Card = components['schemas']['IdentityCaptainApplicationCard'];
type Contact = components['schemas']['IdentityApplicantContact'];
type Check = Card['checks'][number];
type Player = components['schemas']['IdentityPlayerLookup'];
type Usage = components['schemas']['IdentityAuthMethodsUsage'];

// Владелец с правами раздела «Капитаны» (каталог: read, decide, pii — allow) и модератор (scoped — свои города, docs/05).
export const meCaptains: Me = {
  ...meOwner,
  permissions: {
    ...meOwner.permissions,
    'identity.captain_applications.read': 'allow',
    'identity.captain_applications.decide': 'allow',
    'identity.pii.read': 'allow',
  },
};

export const meModCaptains: Me = {
  ...meModerator,
  permissions: {
    ...meModerator.permissions,
    'identity.captain_applications.read': 'scoped',
    'identity.captain_applications.decide': 'scoped',
    'identity.pii.read': 'scoped',
  },
};

export const DECIDED_AT = '2026-10-13T12:00:00Z';
export const DECIDER = 'Ольга В.';

export function captainApplication(over: Partial<Application> = {}): Application {
  return {
    id: '0193a1c9-7f80-7d91-8a1b-000000000001',
    user_id: '0193a1c1-5d2e-7a10-8b3c-000000000001',
    nickname: 'kosta_10',
    city_id: CITY_OWN,
    age_years: 28,
    account_age_days: 256,
    status: 'pending',
    proposed_format: '6×6 по средам',
    proposed_pitch: 'Динамо, искусственное поле',
    play_frequency: 'раз в неделю',
    contact_link: 'https://t.me/kosta_10',
    note: 'Собираю ребят с работы\nтретий год',
    has_phone: true,
    created_at: '2026-10-12T09:00:00Z',
    decided_at: null,
    decided_by_label: null,
    rejection_code: null,
    comment: null,
    ...over,
  };
}

// Заявка Пятигорска (чужой город модератора с CITY_OWN).
export const OTHER_CITY_APPLICATION = captainApplication({
  id: '0193a1c9-7f80-7d91-8a1b-000000000002',
  user_id: '0193a1c1-5d2e-7a10-8b3c-000000000002',
  nickname: 'misha_gk',
  city_id: CITY_OTHER,
  age_years: 19,
  account_age_days: 12,
  has_phone: false,
  created_at: '2026-10-12T10:00:00Z',
});

export const ALL_OK: Check[] = [
  { check: 'age', state: 'ok' },
  { check: 'restrictions', state: 'ok' },
  { check: 'profile', state: 'ok' },
  { check: 'captaincy', state: 'ok' },
];

// Как в проде до модулей players/teams: профиль и капитанство проверить нельзя (К1).
export const UNAVAILABLE: Check[] = [
  { check: 'age', state: 'ok' },
  { check: 'restrictions', state: 'ok' },
  { check: 'profile', state: 'unavailable' },
  { check: 'captaincy', state: 'unavailable' },
];

export const CONTACT: Contact = { email: 'kosta.f@example.com', phone: '+79181234567' };

// Поддержка «Игроки» (план I-4, Task 9): владелец — ПД и сброс 2FA (каталог: identity.mfa.reset — dual, docs/05).
export const mePlayers: Me = {
  ...meCaptains,
  permissions: { ...meCaptains.permissions, 'identity.mfa.reset': 'dual' },
};

// Игрок, найденный по почте: 2FA включена, сброса нет (почта поиска — PLAYER_EMAIL).
export const PLAYER_EMAIL = 'kosta.f@example.com';
export function player(over: Partial<Player> = {}): Player {
  return {
    id: '0193a1c1-5d2e-7a10-8b3c-000000000001',
    nickname: 'kosta_10',
    city_id: CITY_OWN,
    status: 'active',
    created_at: '2026-03-02T18:20:00Z',
    mfa_enabled: true,
    mfa_reset: null,
    ...over,
  };
}
export const SCHEDULED_RESET = {
  id: '0193a1ca-8091-7ea1-9b2c-3d4e5f6a7b89',
  execute_at: '2026-11-20T09:00:00Z',
};

// Статистика способов входа как на стенде с привязками (до I-3 — нули, R1).
export const USAGE: Usage = {
  items: [
    { method: 'apple', only_method: 12, linked: 840 },
    { method: 'google', only_method: 5, linked: 310 },
    { method: 'passkeys', only_method: 0, linked: 95 },
  ],
};

type Decision = 'approve' | 'reject';

export function identityServer(
  opts: {
    applications?: Application[];
    pageSize?: number;
    checks?: Check[];
    history?: Application[];
    linkedAccounts?: number;
    contact?: Contact | (() => Response);
    answer?: Partial<Record<Decision, (a: Application, body: unknown) => Response | null>>;
    // ворота карточки: ответ уходит, когда промис разрешится
    holdCard?: () => Promise<void> | undefined;
    // ворота контактов: запрос записан в seen сразу, ответ уходит, когда промис разрешится
    holdContact?: () => Promise<void> | undefined;
    // поиск игрока по почте: игроки по почте (нижний регистр); нет — 404 identity.user_not_found
    players?: Record<string, Player>;
    // ворота поиска: запрос записан в seen сразу, ответ уходит, когда промис разрешится
    holdLookup?: () => Promise<void> | undefined;
    // свой ответ на запрос или отмену сброса 2FA (Response — ошибка); null — как сервер
    resetAnswer?: (userId: string) => Response | null;
    cancelAnswer?: (resetId: string) => Response | null;
    // статистика способов входа; функция — свой ответ (ошибка)
    usage?: Usage | (() => Response);
  } = {},
) {
  const state = { applications: opts.applications ?? [captainApplication()] };
  const seen = {
    lists: [] as URLSearchParams[],
    background: [] as (string | null)[],
    cards: [] as string[],
    contacts: [] as { id: string; reason: string | null }[],
    decisions: [] as { kind: Decision; id: string; body: unknown; key: string | null }[],
    lookups: [] as { email: string | null; reason: string | null }[],
    resets: [] as { userId: string; body: unknown; key: string | null }[],
    cancels: [] as { resetId: string; key: string | null }[],
    usage: 0,
  };
  const players = opts.players ?? {};
  const size = opts.pageSize ?? 20;
  const order = new Map<string, number>();
  const pos = (a: Application) => {
    if (!order.has(a.id)) order.set(a.id, order.size);
    return order.get(a.id) ?? 0;
  };
  state.applications.forEach(pos);
  const find = (id: string) => state.applications.find((a) => a.id === id);
  const record = (out: Application) => {
    state.applications = state.applications.map((a) => (a.id === out.id ? out : a));
  };
  const notFound = () => problem(404, 'identity.application_not_found');

  const decide = async (kind: Decision, id: string, request: Request) => {
    const body: unknown = await request.json().catch(() => null);
    seen.decisions.push({ kind, id, body, key: request.headers.get('Idempotency-Key') });
    const target = find(id);
    if (!target) return notFound();
    const r = opts.answer?.[kind]?.(target, body);
    if (r) {
      const code = (await r
        .clone()
        .json()
        .catch(() => null)) as { code?: unknown } | null;
      // заявку решил другой сотрудник — на сервере она уже не ожидает
      if (code?.code === 'identity.application_closed' && target.status === 'pending')
        record({
          ...target,
          status: 'approved',
          decided_at: DECIDED_AT,
          decided_by_label: 'Игорь Л.',
        });
      return r;
    }
    if (target.status !== 'pending') return problem(409, 'identity.application_closed');
    const b = (body ?? {}) as { comment?: string; rejection_code?: Application['rejection_code'] };
    const out: Application = {
      ...target,
      status: kind === 'approve' ? 'approved' : 'rejected',
      decided_at: DECIDED_AT,
      decided_by_label: DECIDER,
      rejection_code: kind === 'reject' ? (b.rejection_code ?? null) : null,
      comment: b.comment ?? null,
    };
    record(out);
    return out;
  };

  server.use(
    http.get('/v1/identity/captain-applications', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.lists.push(q);
      seen.background.push(request.headers.get('X-WF-Background'));
      const status = q.get('status') ?? 'pending';
      const city = q.get('city_id');
      const from = Number(q.get('cursor') ?? 0);
      const rows = state.applications.filter(
        (a) => a.status === status && (!city || a.city_id === city) && pos(a) >= from,
      );
      const limit = Math.min(Number(q.get('limit') ?? size), size);
      const items = rows.slice(0, limit);
      const last = items.at(-1);
      const next = last && items.length < rows.length ? String(pos(last) + 1) : null;
      return response(200).json({ items, next_cursor: next });
    }),
    http.get('/v1/identity/captain-applications/{applicationId}', async ({ params, response }) => {
      seen.cards.push(params.applicationId);
      await opts.holdCard?.();
      const a = find(params.applicationId);
      if (!a) return response.untyped(notFound());
      return response(200).json({
        application: a,
        applicant: {
          user_id: a.user_id,
          nickname: a.nickname,
          city_id: a.city_id,
          age_years: a.age_years,
          account_age_days: a.account_age_days,
        },
        checks: opts.checks ?? ALL_OK,
        history: opts.history ?? [],
        linked_accounts: opts.linkedAccounts ?? 0,
      });
    }),
    http.get(
      '/v1/identity/captain-applications/{applicationId}/contact',
      async ({ params, request, response }) => {
        seen.contacts.push({
          id: params.applicationId,
          reason: request.headers.get('X-WF-Access-Reason'),
        });
        await opts.holdContact?.();
        if (!find(params.applicationId)) return response.untyped(notFound());
        const c = opts.contact ?? CONTACT;
        return typeof c === 'function' ? response.untyped(c()) : response(200).json(c);
      },
    ),
    http.post(
      '/v1/identity/captain-applications/{applicationId}/approve',
      async ({ params, request, response }) => {
        const r = await decide('approve', params.applicationId, request);
        return r instanceof Response ? response.untyped(r) : response(200).json(r);
      },
    ),
    http.post(
      '/v1/identity/captain-applications/{applicationId}/reject',
      async ({ params, request, response }) => {
        const r = await decide('reject', params.applicationId, request);
        return r instanceof Response ? response.untyped(r) : response(200).json(r);
      },
    ),
    http.get('/v1/identity/users/lookup', async ({ request, response }) => {
      const email = request.headers.get('X-WF-Lookup-Email');
      seen.lookups.push({ email, reason: request.headers.get('X-WF-Access-Reason') });
      await opts.holdLookup?.();
      const found = email === null ? undefined : players[decodeURIComponent(email).toLowerCase()];
      return found
        ? response(200).json(found)
        : response.untyped(problem(404, 'identity.user_not_found'));
    }),
    http.post('/v1/identity/users/{userId}/mfa-reset', async ({ params, request, response }) => {
      const body: unknown = await request.json().catch(() => null);
      seen.resets.push({
        userId: params.userId,
        body,
        key: request.headers.get('Idempotency-Key'),
      });
      const own = opts.resetAnswer?.(params.userId);
      if (own) return response.untyped(own);
      return response(202).json({
        approval: approval({
          action: 'identity.mfa_reset',
          permission: 'identity.mfa.reset',
          target_id: params.userId,
        }),
      });
    }),
    http.post('/v1/identity/mfa-resets/{resetId}/cancel', ({ params, request, response }) => {
      seen.cancels.push({ resetId: params.resetId, key: request.headers.get('Idempotency-Key') });
      const own = opts.cancelAnswer?.(params.resetId);
      if (own) return response.untyped(own);
      return response(200).json({ id: params.resetId, cancelled_at: '2026-11-15T10:00:00Z' });
    }),
    http.get('/v1/identity/auth-methods/usage', ({ response }) => {
      seen.usage++;
      const u = opts.usage ?? {
        items: [
          { method: 'apple', only_method: 0, linked: 0 },
          { method: 'google', only_method: 0, linked: 0 },
          { method: 'passkeys', only_method: 0, linked: 0 },
        ],
      };
      return typeof u === 'function' ? response.untyped(u()) : response(200).json(u);
    }),
  );
  return { state, seen, record };
}
