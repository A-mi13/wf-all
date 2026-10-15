import type { components } from '../api/gen/admin';
import { CITY_OTHER, CITY_OWN, meOwner } from './me';
import { problem } from './problem';
import { http, server } from './server';

// Данные «Ролей и доступа» и «Моего доступа» для моков — по схемам контракта admin.d.ts (урок 12b:
// мок не отвечает тем, чего сервер не скажет).

type StaffAccount = components['schemas']['StaffAccount'];
type StaffRoles = components['schemas']['StaffRoles'];
type StaffSession = components['schemas']['StaffSession'];
type GeoCity = components['schemas']['GeoCity'];
type Approval = components['schemas']['Approval'];

// Сотрудник списка; по умолчанию — активный модератор Ставрополя.
export function account(over: Partial<StaffAccount> = {}): StaffAccount {
  return {
    id: '0192f1c4-1111-7b21-9c5d-000000000001',
    email: 'maria.k@wf.example',
    display_name: 'Мария К.',
    locale: 'ru',
    status: 'active',
    role: 'moderator',
    city_ids: [CITY_OWN],
    created_at: '2026-10-01T09:00:00Z',
    last_login_at: '2026-10-13T09:15:00Z',
    locked_until: null,
    version: 3,
    invite: null,
    ...over,
  };
}

// Команда для списка: своя строка владельца (meOwner), модератор, админ, приглашённые (живое и
// просроченное приглашение), выключенная учётка.
export const ME_ROW = account({
  id: meOwner.id ?? '',
  email: meOwner.email ?? '',
  display_name: 'Ольга В.',
  role: 'owner',
  city_ids: [],
});
export const TEAM = [
  ME_ROW,
  account(),
  account({
    id: '0192f1c4-1111-7b21-9c5d-000000000002',
    email: 'igor.l@wf.example',
    display_name: 'Игорь Л.',
    role: 'admin',
    city_ids: [],
  }),
  account({
    id: '0192f1c4-1111-7b21-9c5d-000000000003',
    email: 'nastya.d@wf.example',
    display_name: 'Настя Д.',
    role: 'support',
    city_ids: [],
    status: 'invited',
    last_login_at: null,
    invite: { purpose: 'invite', expires_at: '2026-10-14T09:00:00Z', expired: false },
  }),
  account({
    id: '0192f1c4-1111-7b21-9c5d-000000000004',
    email: 'petr.s@wf.example',
    display_name: 'Пётр С.',
    role: 'support',
    city_ids: [],
    status: 'invited',
    last_login_at: null,
    invite: { purpose: 'access_reset', expires_at: '2026-10-12T09:00:00Z', expired: true },
  }),
  account({
    id: '0192f1c4-1111-7b21-9c5d-000000000005',
    email: 'old@wf.example',
    display_name: 'Олег Б.',
    role: 'support',
    city_ids: [],
    status: 'disabled',
  }),
];

// Матрица ролей по спеке §2.5 (часть строк).
export const rolesMatrix: StaffRoles = {
  roles: ['owner', 'admin', 'moderator', 'support'],
  permissions: [
    {
      key: 'staff.roles.read',
      mutating: false,
      grants: { owner: 'allow', admin: 'allow', moderator: 'allow', support: 'allow' },
    },
    {
      key: 'staff.roles.grant_elevated',
      mutating: true,
      grants: { owner: 'dual', admin: 'deny', moderator: 'deny', support: 'deny' },
    },
    {
      key: 'staff.roles.grant',
      mutating: true,
      grants: { owner: 'dual', admin: 'dual', moderator: 'deny', support: 'deny' },
    },
    {
      key: 'staff.roles.revoke_admin',
      mutating: true,
      grants: { owner: 'allow', admin: 'deny', moderator: 'deny', support: 'deny' },
    },
    {
      key: 'audit.log.read',
      mutating: false,
      grants: { owner: 'allow', admin: 'allow', moderator: 'scoped', support: 'allow' },
    },
  ],
};

export function session(over: Partial<StaffSession> = {}): StaffSession {
  return {
    id: '0192f1d0-1b2c-7d3e-8f4a-5b6c7d8e9f01',
    stage: 'active',
    current: true,
    created_at: '2026-10-13T09:00:00Z',
    last_seen_at: '2026-10-13T10:41:00Z',
    expires_at: '2026-10-13T17:00:00Z',
    ip: '203.0.113.0/24',
    user_agent:
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36',
    ...over,
  };
}

export function city(over: Partial<GeoCity> = {}): GeoCity {
  return {
    id: CITY_OWN,
    slug: 'stavropol',
    name: 'Ставрополь',
    status: 'live',
    country_code: 'RU',
    region_name: 'Ставропольский край',
    ...over,
  };
}

export const CITIES: GeoCity[] = [
  city(),
  city({ id: CITY_OTHER, slug: 'pyatigorsk', name: 'Пятигорск' }),
];

// Ответ 202 — запрос на подтверждение.
export function approval(over: Partial<Approval> = {}): Approval {
  return {
    id: '0192f1e2-4c5d-7e6f-a081-92b3c4d5e6f7',
    action: 'staff.invite',
    permission: 'staff.roles.grant',
    status: 'pending',
    payload: { v: 1 },
    target_id: null,
    scope_city_id: null,
    requested_by: { id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d', label: 'Ольга В.' },
    requested_at: '2026-10-13T09:15:00Z',
    expires_at: '2026-10-14T09:15:00Z',
    decided_by: null,
    decided_at: null,
    comment: null,
    error: null,
    ...over,
  };
}

// Сервер «Ролей и доступа»: матрица, список (фильтры q/status/role, курсор по страницам), карточка,
// города (q — начало названия, ids — подписи). seen — что спрашивал экран.
export function staffServer(
  opts: { accounts?: StaffAccount[]; pageSize?: number; roles?: StaffRoles } = {},
) {
  const state = { accounts: opts.accounts ?? [account()] };
  const seen = {
    list: [] as URLSearchParams[],
    cities: [] as URLSearchParams[],
    roles: 0,
    cards: [] as string[],
  };
  const size = opts.pageSize ?? 50;
  server.use(
    http.get('/v1/staff/roles', ({ response }) => {
      seen.roles++;
      return response(200).json(opts.roles ?? rolesMatrix);
    }),
    http.get('/v1/staff/accounts', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.list.push(q);
      const text = (q.get('q') ?? '').toLocaleLowerCase();
      const rows = state.accounts.filter(
        (a) =>
          (!q.get('status') || a.status === q.get('status')) &&
          (!q.get('role') || a.role === q.get('role')) &&
          (!text ||
            a.display_name.toLocaleLowerCase().startsWith(text) ||
            a.email.startsWith(text)),
      );
      const from = Number(q.get('cursor') ?? 0);
      const items = rows.slice(from, from + size);
      const next = from + size < rows.length ? String(from + size) : null;
      return response(200).json({ items, next_cursor: next });
    }),
    http.get('/v1/staff/accounts/{staffId}', ({ params, response }) => {
      seen.cards.push(params.staffId);
      const a = state.accounts.find((x) => x.id === params.staffId);
      return a ? response(200).json(a) : response.untyped(problem(404, 'staff.not_found'));
    }),
    http.get('/v1/geo/cities', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.cities.push(q);
      const ids = q.getAll('ids');
      const text = (q.get('q') ?? '').toLocaleLowerCase();
      const items = CITIES.filter(
        (c) =>
          (ids.length === 0 || ids.includes(c.id)) &&
          (!text || c.name.toLocaleLowerCase().startsWith(text)),
      );
      return response(200).json({ items, next_cursor: null });
    }),
  );
  return { state, seen };
}
