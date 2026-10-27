import { describe, expect, test } from 'vitest';
import type { Me } from '../../app/permissions';
import { CITY_OTHER, CITY_OWN, meAdmin, meModerator, meOwner } from '../../test/me';
import { account } from '../../test/staff';
import {
  grantableRoles,
  hasActions,
  roleChangePermission,
  staffActions,
  twoHandsRules,
} from './roles';

// Правила кнопок «Ролей и доступа» (спека §2.5–2.6, §9.3): право берётся из me.permissions, роль
// цели решает, КАКОЕ право спрашивать. Решает всегда сервер — здесь только «не показывать заведомо
// запрещённое» и «dual — на подтверждение».

describe('приглашение: роли, которые можно выдать', () => {
  test('владелец — все роли, админ — только модератор и поддержка', () => {
    expect(grantableRoles(meOwner)).toEqual(['owner', 'admin', 'moderator', 'support']);
    expect(grantableRoles(meAdmin)).toEqual(['moderator', 'support']);
  });

  test('без прав выдачи — ни одной роли (кнопки «Пригласить» нет)', () => {
    expect(grantableRoles(meModerator)).toEqual([]);
  });
});

describe('смена роли и городов (§2.6)', () => {
  const mod = (city_ids: string[]) => ({ role: 'moderator', city_ids });

  test('без изменений — права не нужно', () => {
    expect(roleChangePermission(mod([CITY_OWN]), mod([CITY_OWN]))).toBeNull();
    expect(
      roleChangePermission(mod([CITY_OWN, CITY_OTHER]), mod([CITY_OTHER, CITY_OWN])),
    ).toBeNull();
  });

  test('старая или новая роль owner/admin — grant_elevated', () => {
    expect(roleChangePermission({ role: 'admin', city_ids: [] }, mod([CITY_OWN]))).toBe(
      'staff.roles.grant_elevated',
    );
    expect(roleChangePermission(mod([CITY_OWN]), { role: 'owner', city_ids: [] })).toBe(
      'staff.roles.grant_elevated',
    );
  });

  test('роль меняется или добавлен город — grant', () => {
    expect(roleChangePermission(mod([CITY_OWN]), { role: 'support', city_ids: [] })).toBe(
      'staff.roles.grant',
    );
    expect(roleChangePermission(mod([CITY_OWN]), mod([CITY_OWN, CITY_OTHER]))).toBe(
      'staff.roles.grant',
    );
    // убрали один и добавили другой — это добавление
    expect(roleChangePermission(mod([CITY_OWN]), mod([CITY_OTHER]))).toBe('staff.roles.grant');
  });

  test('только снятие городов — revoke (сразу)', () => {
    expect(roleChangePermission(mod([CITY_OWN, CITY_OTHER]), mod([CITY_OWN]))).toBe(
      'staff.roles.revoke',
    );
  });

  test('незнакомая роль цели — как elevated (строже)', () => {
    expect(roleChangePermission({ role: 'future', city_ids: [] }, mod([CITY_OWN]))).toBe(
      'staff.roles.grant_elevated',
    );
  });
});

describe('действия над строкой сотрудника', () => {
  test('своя строка — никаких действий', () => {
    const self = account({ id: meOwner.id ?? '', role: 'owner' });
    const a = staffActions(meOwner, self);
    expect(a.self).toBe(true);
    expect(hasActions(a)).toBe(false);
  });

  test('владелец над модератором: снять — сразу, сброс — вдвоём, роль, сеансы', () => {
    const a = staffActions(meOwner, account({ role: 'moderator', city_ids: [CITY_OWN] }));
    expect(a.disable).toBe('allow');
    expect(a.reset).toBe('needs_approval');
    expect(a.sessions).toBe(true);
    expect(a.roleOptions).toEqual(['owner', 'admin', 'moderator', 'support']);
  });

  test('владелец над владельцем: снять и сбросить — вдвоём', () => {
    const a = staffActions(meOwner, account({ role: 'owner' }));
    expect(a.disable).toBe('needs_approval');
    expect(a.reset).toBe('needs_approval');
    expect(a.sessions).toBe(true);
  });

  test('владелец над админом: снять — сразу (U1)', () => {
    expect(staffActions(meOwner, account({ role: 'admin' })).disable).toBe('allow');
  });

  test('админ над владельцем и админом: ни роли, ни сброса, ни снятия, ни сеансов', () => {
    for (const role of ['owner', 'admin'] as const) {
      const a = staffActions(meAdmin, account({ role }));
      expect(a.disable, role).toBe('deny');
      expect(a.reset, role).toBe('deny');
      expect(a.sessions, role).toBe(false);
      expect(a.roleOptions, role).toEqual([]);
      expect(hasActions(a), role).toBe(false);
    }
  });

  test('админ над модератором: роли только moderator/support', () => {
    const a = staffActions(meAdmin, account({ role: 'moderator', city_ids: [CITY_OWN] }));
    expect(a.roleOptions).toEqual(['moderator', 'support']);
    expect(a.disable).toBe('allow');
    expect(a.reset).toBe('needs_approval');
    expect(a.sessions).toBe(true);
  });

  test('приглашение ещё не принято — переотправка; выключенная учётка — без действий', () => {
    const invited = staffActions(
      meAdmin,
      account({
        role: 'owner',
        status: 'invited',
        invite: { purpose: 'invite', expires_at: '2026-10-14T09:00:00Z', expired: false },
      }),
    );
    expect(invited.resend).toBe(true);
    expect(hasActions(invited)).toBe(true);

    const off = staffActions(meOwner, account({ role: 'support', status: 'disabled' }));
    expect(off.disable).toBe('deny');
    expect(off.reset).toBe('deny');
    expect(off.sessions).toBe(false);
    expect(off.roleOptions).toEqual([]);
  });

  test('незнакомая роль цели — ни снятия, ни сброса', () => {
    const a = staffActions(meOwner, account({ role: 'future' as never }));
    expect(a.disable).toBe('deny');
    expect(a.reset).toBe('deny');
  });

  test('модератор (без прав управления) — никаких действий', () => {
    const me: Me = meModerator;
    expect(hasActions(staffActions(me, account({ role: 'support' })))).toBe(false);
  });
});

describe('«Правило двух рук» — по матрице', () => {
  test('права с dual хотя бы у одной роли и роли, которым они вдвоём', () => {
    const rules = twoHandsRules({
      roles: ['owner', 'admin', 'moderator', 'support'],
      permissions: [
        {
          key: 'staff.roles.read',
          mutating: false,
          grants: { owner: 'allow', admin: 'allow', moderator: 'allow', support: 'allow' },
        },
        {
          key: 'staff.roles.grant',
          mutating: true,
          grants: { owner: 'dual', admin: 'dual', moderator: 'deny', support: 'deny' },
        },
        {
          key: 'staff.roles.revoke_owner',
          mutating: true,
          grants: { owner: 'dual', admin: 'deny', moderator: 'deny', support: 'deny' },
        },
      ],
    });
    expect(rules).toEqual([
      { key: 'staff.roles.grant', roles: ['owner', 'admin'] },
      { key: 'staff.roles.revoke_owner', roles: ['owner'] },
    ]);
  });
});
