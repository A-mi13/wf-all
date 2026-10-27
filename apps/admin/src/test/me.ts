import type { components } from '../api/gen/admin';

type Me = components['schemas']['StaffMe'];

export const CITY_OWN = '3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47';
export const CITY_OTHER = '7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';

const session = { id: '0192f1d0-1b2c-7d3e-8f4a-5b6c7d8e9f01', expires_at: '2026-10-13T17:00:00Z' };

// Ответы GET /v1/staff/me по стадиям (R15, P20) — для моков тестов.
export const meMfaPending: Me = {
  stage: 'mfa_pending',
  reauth_required: false,
  city_ids: [],
  permissions: {},
  session,
};

export const meSetup: Me = {
  id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
  email: 'olga.v@wf.example',
  display_name: 'Ольга В.',
  locale: 'ru',
  role: 'moderator',
  city_ids: [CITY_OWN],
  stage: 'setup',
  reauth_required: false,
  permissions: {},
  session,
};

// Активный модератор: журнал — scoped, матрица ролей — allow, подтверждать — нечего.
export const meModerator: Me = {
  ...meSetup,
  stage: 'active',
  permissions: {
    'staff.roles.read': 'allow',
    'audit.log.read': 'scoped',
    'geo.cities.read': 'scoped',
    'geo.cities.rename': 'scoped',
  },
};

// Активный владелец (спека §2.5): выдача ролей и сброс доступа — вдвоём, снятие админа и
// модератора — сразу, снятие владельца — вдвоём.
export const meOwner: Me = {
  ...meSetup,
  role: 'owner',
  city_ids: [],
  stage: 'active',
  permissions: {
    'staff.roles.read': 'allow',
    'staff.accounts.read': 'allow',
    'staff.roles.grant_elevated': 'dual',
    'staff.roles.grant': 'dual',
    'staff.roles.revoke_owner': 'dual',
    'staff.roles.revoke_admin': 'allow',
    'staff.roles.revoke': 'allow',
    'staff.access.reset_elevated': 'dual',
    'staff.access.reset': 'dual',
    'staff.invites.resend': 'allow',
    'staff.sessions.revoke': 'allow',
    'audit.log.read': 'allow',
    'geo.cities.read': 'allow',
  },
};

// Активный админ (спека §2.5): владельцев и админов не назначает и не снимает, их доступ не
// сбрасывает, их сессии не видит.
export const meAdmin: Me = {
  ...meSetup,
  id: '0192f1c4-0000-7b21-9c5d-0000000000ad',
  email: 'igor.l@wf.example',
  display_name: 'Игорь Л.',
  role: 'admin',
  city_ids: [],
  stage: 'active',
  permissions: {
    'staff.roles.read': 'allow',
    'staff.accounts.read': 'allow',
    'staff.roles.grant': 'dual',
    'staff.roles.revoke': 'allow',
    'staff.access.reset': 'dual',
    'staff.invites.resend': 'allow',
    'staff.sessions.revoke': 'allow',
    'audit.log.read': 'allow',
    'geo.cities.read': 'allow',
  },
};
