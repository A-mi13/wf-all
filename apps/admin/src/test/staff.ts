import type { components } from '../api/gen/admin';
import { CITY_OTHER, CITY_OWN } from './me';

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
